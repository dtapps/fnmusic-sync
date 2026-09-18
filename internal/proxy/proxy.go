package proxy

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"cnb.cool/dtapp/fnmusic-sync/internal/db"
	"cnb.cool/dtapp/fnmusic-sync/internal/playback"
	"cnb.cool/dtapp/fnmusic-sync/internal/strutil"
)

const (
	// maxInspectBody 抓取请求体用于分析的最大字节数。
	maxInspectBody = 1 << 20
	// maxLogBody 单条日志打印的最大 body 长度。
	// 抓包阶段要能看到完整的曲目元数据，给得大一些。
	maxLogBody = 16384
)

// 敏感请求头，日志中一律打码，避免 token / session 泄漏。
var sensitiveHeaders = []string{
	"Authorization",
	"Proxy-Authorization",
	"Cookie",
	"Set-Cookie",
	"X-Auth-Token",
	"X-Session",
}

// ── 飞牛音乐 API 路径（集中定义，便于排查与维护）────────────────────
// 路径匹配统一用 strings.Contains（上游 URL.Path 无论是否带前缀都能命中）。
const (
	// pathUserMe：被动识别用户（客户端启动/刷新时调用，响应含用户名）。
	// 用不含版本前缀的 /user/me 做子串匹配，无论上游路径是否带 /music/api/v1 前缀都能命中
	//（与此前一致：strings.Contains(path, "/user/me")），避免严格前缀在路径变化时静默失效。
	pathUserMe = "/user/me"
	// pathUserAuthLogin / pathUserPasswordLogin：登录与换 token 接口。
	// 二者响应体结构一致（见 playback.ParseLoginToken）：含新签发的 userToken + 用户名。
	// 此前只处理了 password-login，漏掉了客户端实际调用的 auth-login，导致新登录客户端无法登记。
	// 同样用不含前缀的子串匹配，兼容路径前缀变化。
	pathUserAuthLogin     = "/user/auth-login"
	pathUserPasswordLogin = "/user/password-login"

	// pathTrackStream / pathTrackPlayURL：音频取流（播放）请求。
	pathTrackStream  = "/track/stream"
	pathTrackPlayURL = "/track/play-url"
	// pathTrackMetadata：曲目元数据（标题/艺人/专辑/时长），供 scrobble 使用。
	pathTrackMetadata = "/track/metadata"
	// pathTrackHLS：HLS 播放（/track/hls/<guid>/...），含 .m3u8 播单与 .m4s 分片。
	pathTrackHLS = "/track/hls/"
	// pathEventReport：播放事件上报（track_play），请求侧据此开启播放会话。
	pathEventReport = "/event/report"
)

// isUserMePath 是否为被动识别路径（/user/me）。
func isUserMePath(path string) bool { return strings.Contains(path, pathUserMe) }

// isUserLoginPath 是否为登录/换 token 路径（auth-login 或 password-login 任一）。
func isUserLoginPath(path string) bool {
	return strings.Contains(path, pathUserAuthLogin) ||
		strings.Contains(path, pathUserPasswordLogin)
}

// isPlaybackPath 判断是否为音频取流（播放）请求。
func isPlaybackPath(path string) bool {
	return strings.Contains(path, pathTrackStream) ||
		strings.Contains(path, pathTrackPlayURL)
}

// isTrackMetadataPath 是否为曲目元数据请求（/track/metadata）。
func isTrackMetadataPath(path string) bool { return strings.Contains(path, pathTrackMetadata) }

// isHLSPath 是否为 HLS 播放请求（/track/hls/<guid>/...）。
func isHLSPath(path string) bool { return strings.Contains(path, pathTrackHLS) }

// isEventReportPath 是否为播放事件上报请求（/event/report，track_play）。
func isEventReportPath(path string) bool { return strings.Contains(path, pathEventReport) }

type Config struct {
	ListenSocket   string
	UpstreamSocket string

	// SocketMode 代理 socket 权限，0 表示沿用官方 socket 权限。
	SocketMode os.FileMode

	// Store 用户状态持久化（是否启用各平台、推送次数等）。
	Store *playback.UserStore

	// ProviderStatus 返回某用户是否启用了 Last.fm / ListenBrainz（依据配置）。
	// 用于把"是否启用各平台"写入该用户的状态文件。
	ProviderStatus func(username string) (lastfm, listenBrainz bool)

	// LogDir 日志目录，抓包日志写入此目录。
	LogDir string
	// LogMaxSize 抓包日志单文件大小上限(MB)，复用主日志配置。
	LogMaxSize int
	// LogMaxBackups 抓包日志保留份数，复用主日志配置。
	LogMaxBackups int
	// LogMaxAge 抓包日志保留天数，复用主日志配置。
	LogMaxAge int
	// LogCompress 抓包日志是否压缩，复用主日志配置。
	LogCompress bool
	// CaptureEnabled 控制抓包日志（capture.log）的开关，
	// 由 --debug flag 控制。
	CaptureEnabled *atomic.Bool
}

type Proxy struct {
	cfg     Config
	manager *playback.Manager
	logger  *slog.Logger

	transport *http.Transport
	upstream  *http.Client
	reverse   *httputil.ReverseProxy

	userCache *playback.UserCache
	detector  *playback.Detector
	capture   *CaptureLogger

	// seen 按 token 节流地异步刷新 users.last_seen_at（活跃心跳），不阻塞请求。
	seen *seenToucher

	server *http.Server
}

func New(
	cfg Config,
	manager *playback.Manager,
	logger *slog.Logger,
) *Proxy {
	p := &Proxy{
		cfg:     cfg,
		manager: manager,
		logger:  logger,
		seen:    newSeenToucher(cfg.Store),
	}

	p.transport = &http.Transport{
		DialContext: func(
			ctx context.Context,
			_ string,
			_ string,
		) (net.Conn, error) {
			dialer := &net.Dialer{Timeout: 5 * time.Second}

			return dialer.DialContext(ctx, "unix", cfg.UpstreamSocket)
		},
		// 关键：禁用 keep-alive，每个请求独立 dial、响应后立即关闭。
		//
		// 上游是本地 unix socket，dial 开销极小；而"复用连接池"会引入两类致命问题：
		//   1. 上游重启后池里残留指向旧进程的死连接；
		//   2. 空闲连接的关闭由 Transport 在【持有 idleMu】的情况下执行
		//      （closeConnIfStillIdle / CloseIdleConnections → pc.close）。
		//      一旦 conn.Close() 阻塞（上游假死 / 连接处于不可中断状态），
		//      idleMu 就被长期占住，所有请求都会卡在 queueForIdleConn → idleMu.Lock，
		//      整个代理彻底无响应（官方 Music 打不开）。
		// 禁用连接池后，上述路径全部消失，代理不会再被上游拖死。
		DisableKeepAlives:     true,
		ExpectContinueTimeout: time.Second,
		// 兜底：若上游建连成功但迟迟不回响应头（卡死/假死），
		// 限制等待响应头的时长，避免请求无期限挂起。仅作用于响应头阶段，
		// 不影响音频流等持续 body 的正常传输。
		ResponseHeaderTimeout: 30 * time.Second,
		// 不禁用压缩：客户端自带 Accept-Encoding 时原样透传，
		// 由 nginx / 客户端自己解压，保证对 trim-music 完全透明。
	}

	p.upstream = &http.Client{Transport: p.transport}

	p.userCache = playback.NewUserCache(
		logger,
		p.transport,
		p.cfg.Store,
		p.cfg.ProviderStatus,
	)

	p.capture = NewCaptureLogger(cfg.LogDir, cfg.LogMaxSize, cfg.LogMaxBackups, cfg.LogMaxAge, cfg.LogCompress, cfg.CaptureEnabled)

	// 播放识别 → scrobble 推送。
	p.detector = playback.NewDetector(manager, logger)

	p.reverse = &httputil.ReverseProxy{
		Rewrite:        p.rewrite,
		Transport:      p.transport,
		ErrorHandler:   p.errorHandler,
		ModifyResponse: p.modifyResponse,
		// -1：写入后立即 flush，保证音频流 / SSE 不被缓冲。
		FlushInterval: -1,
	}

	return p
}

// UserCache 返回代理内部的用户缓存，供其他服务（如歌单同步）使用。
func (p *Proxy) UserCache() *playback.UserCache {
	return p.userCache
}

// Close 释放代理资源（抓包日志文件等）。
func (p *Proxy) Close() {
	if p.capture != nil {
		p.capture.Close()
	}

	if p.detector != nil {
		p.detector.Stop()
	}
}

// Start 在指定的 listener 上启动代理，ctx 取消时优雅退出。
func (p *Proxy) Start(ctx context.Context, listener net.Listener) error {
	p.server = &http.Server{
		Handler: p,
		// 音频流可能持续很久，不设置 ReadTimeout / WriteTimeout。
		ReadHeaderTimeout: 30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		<-ctx.Done()

		p.logger.Info("收到退出信号，正在停止代理")

		shutdownCtx, cancel := context.WithTimeout(
			context.WithoutCancel(ctx),
			10*time.Second,
		)
		defer cancel()

		_ = p.server.Shutdown(shutdownCtx)
	}()

	err := p.server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		p.logger.Info("代理已停止")

		return nil
	}

	return err
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == HealthzPath {
		p.serveHealth(w, r)

		return
	}

	start := time.Now()

	// 提取 per-user 标识，并（必要时）异步探测用户名。
	userKey, userSource := extractUserKey(r)
	if userKey != "" {
		name := p.userCache.Resolve(userKey, userSource, r.Header)
		r = r.WithContext(
			context.WithValue(
				r.Context(),
				userCtxKey{},
				userCtx{Key: userKey, Name: name, Source: userSource},
			),
		)
		// 已知用户（已解析出用户名）的任意请求都算一次活跃心跳；
		// 异步、按 token 节流，绝不阻塞请求，也不影响同账号其它 token 行。
		if name != "" {
			p.seen.touch(strutil.FirstN8(userKey))
		}
	}

	// 抓包日志已启用时，原始请求（含 Cookie 头）已在 capture.log 完整记录，主日志不再重复打用户识别诊断。
	if p.capture == nil {
		p.logger.Debug("用户识别诊断",
			"含music-token", hasCookie(r, "music-token"),
			"含fnos-token", hasCookie(r, "fnos-token"),
			"userKey来源", userSource,
			"userKey前8位", strutil.FirstN8(userKey),
		)
	}

	body := p.bufferBody(r)
	stream := isPlaybackPath(r.URL.Path)

	writer := &countWriter{ResponseWriter: w}

	// debug + capture 启用时，创建一条抓包记录（请求部分先暂存，响应返回时合并写出，
	// 保证请求/响应成对成块，并发下不交错）。连接断开等未走到响应的情况由 defer 兜底写出。
	if p.capture != nil && !stream {
		if entry := p.capture.Begin(r.Method, r.URL.Path, r.URL.RawQuery, r.Header, body); entry != nil {
			r = r.WithContext(context.WithValue(r.Context(), captureEntryKey{}, entry))
			defer entry.Finish(0, nil, nil, 0) // 兜底：响应未正常返回时仍写出请求部分
		}
	}

	// 取流是推算真实播放时长的唯一线索，但每次播放会频繁触发，
	// 默认打 DEBUG（--debug 可见），避免正常使用时刷屏。
	if stream {
		uc := userFromCtx(r.Context())
		p.logger.Debug("开始取流",
			"用户", userLabel(uc),
			"歌曲", r.URL.Query().Get("guid"),
			"路径", r.URL.Path,
			"范围", r.Header.Get("Range"),
		)
	}

	p.inspect(r, body)

	// 播放事件（track_play）→ 开启播放会话。
	if isEventReportPath(r.URL.Path) {
		uc := userFromCtx(r.Context())
		// 调试用：直接确认 event/report 被解析成了哪个用户（空串=不推送）。
		p.logger.Info("播放事件(event/report)用户识别",
			"用户名", uc.Name,
			"来源", uc.Source,
			"userKey前8位", strutil.FirstN8(uc.Key),
			"是否推送", uc.Name != "",
		)
		p.detector.HandleEventReport(
			uc.Name,
			body,
		)
	}

	p.reverse.ServeHTTP(writer, r)

	p.logDone(r, writer, time.Since(start), stream)
}

// captureEntryKey 用于在请求 context 中传递抓包记录，使响应返回时能与请求合并成一条。
type captureEntryKey struct{}

func captureEntryFromCtx(ctx context.Context) *captureEntry {
	if e, ok := ctx.Value(captureEntryKey{}).(*captureEntry); ok {
		return e
	}

	return nil
}

func (p *Proxy) logDone(
	r *http.Request,
	w *countWriter,
	cost time.Duration,
	stream bool,
) {
	uc := userFromCtx(r.Context())

	status := w.status
	if status == 0 {
		// 还没写响应头连接就断了。
		status = http.StatusOK
	}

	attrs := []any{
		"用户", userLabel(uc),
		"方法", r.Method,
		"路径", r.URL.Path,
		"查询", r.URL.RawQuery,
		"状态码", status,
		"传输字节", w.written,
		"耗时", cost.String(),
	}

	if guid := r.URL.Query().Get("guid"); guid != "" {
		attrs = append(attrs, "歌曲", guid)
	}

	// 客户端主动断开（切歌、拖进度条、关页面）。
	if err := r.Context().Err(); err != nil {
		attrs = append(attrs, "中断原因", err.Error())

		if stream {
			p.logger.Warn("取流被中断", attrs...)

			return
		}

		p.logger.Debug("请求被中断", attrs...)

		return
	}

	if stream {
		p.logger.Debug("取流结束", attrs...)

		return
	}

	// 响应详情已完整写入 capture.log（debug + capture 启用时），主日志不再重复刷屏。
	if p.capture == nil {
		p.logger.Debug("响应完成", attrs...)
	}
}

// director 只改写转发目标，其他一律保持原样。
//
// 注意：不能动 outreq.Host。nginx 的 proxy_pass 指向 unix socket 时
// 默认下发 Host: unix，trim-music 可能依赖它，必须原样透传。
func (p *Proxy) rewrite(r *httputil.ProxyRequest) {
	r.Out.URL.Scheme = "http"
	r.Out.URL.Host = "trim-music"
}

// bufferBody 复制请求体用于分析，同时保证 upstream 仍能读到完整 body。
//
// 只读取前 maxInspectBody 字节，剩余部分通过 MultiReader 继续透传，
// 不会把大 body（上传、长表单）全量读进内存。
func (p *Proxy) bufferBody(r *http.Request) []byte {
	if r.Body == nil {
		return nil
	}

	var buf bytes.Buffer

	if _, err := io.Copy(&buf, io.LimitReader(r.Body, maxInspectBody)); err != nil {
		p.logger.Warn(
			"读取请求体失败",
			"路径", r.URL.Path,
			"错误", err,
		)
	}

	r.Body = io.NopCloser(
		io.MultiReader(bytes.NewReader(buf.Bytes()), r.Body),
	)

	return buf.Bytes()
}

// inspect 识别播放相关请求。
//
// 当前只做记录：stream 请求不等于用户真的播放了这么久，
// 在没有抓到真实的播放/暂停/进度 API 之前，不做任何 scrobble。
func (p *Proxy) inspect(r *http.Request, body []byte) {
	if !isPlaybackPath(r.URL.Path) {
		return
	}

	// 与「开始取流」重复，这里降到 Debug，避免一条请求打两条 INFO。
	p.logger.Debug(
		"疑似播放取流",
		"方法", r.Method,
		"路径", r.URL.Path,
		"查询", r.URL.RawQuery,
		"请求体", truncate(string(body), maxLogBody),
	)
}

func (p *Proxy) modifyResponse(resp *http.Response) error {
	// 始终拦截 /user/me 以识别用户名，不依赖 debug 开关。
	// 仅使用 music-token 识别用户，忽略 fnos-token。
	if isUserMePath(resp.Request.URL.Path) {
		if uc := userFromCtx(resp.Request.Context()); uc.Key != "" && uc.Source == "music-token" {
			if body, err := drainJSONBody(resp, 1<<16); err == nil {
				p.userCache.Update(uc.Key, body)
			}
		}
	}

	// 缓存曲目元数据（标题/艺人/专辑/时长），供后续 scrobble 使用。
	if isTrackMetadataPath(resp.Request.URL.Path) {
		guid := resp.Request.URL.Query().Get("guid")
		if guid != "" {
			body, err := drainJSONBody(resp, maxInspectBody)
			if err != nil {
				p.logger.Warn("读取曲目元数据失败",
					"歌曲", guid,
					"错误", err,
				)
			} else {
				p.detector.HandleMetadata(guid, body)
			}
		}
	}

	// 取流进度 → 推算播放位置，达标则 scrobble（与 debug 无关，始终执行）。
	if isPlaybackPath(resp.Request.URL.Path) {
		p.detector.HandleStreamProgress(
			userFromCtx(resp.Request.Context()).Name,
			resp.Request.URL.Query().Get("guid"),
			resp.Header.Get("Content-Range"),
		)
	}

	// HLS 播放（/track/hls/<guid>/...）：此前完全没纳入进度识别，客户端切到 HLS 后
	// 就没有任何进度信号，表现为“还在播放却不推送”。这里解析播单拿分片时长、
	// 按分片序号推算播放位置（详见 playback.Detector 的 HLS 说明）。
	if isHLSPath(resp.Request.URL.Path) {
		switch {
		case strings.HasSuffix(resp.Request.URL.Path, ".m3u8"):
			if b, rerr := drainJSONBody(resp, 1<<20); rerr == nil {
				p.detector.HandleHLSPlaylist(resp.Request.URL.Path, b)
			}
		case strings.HasSuffix(resp.Request.URL.Path, ".m4s"):
			p.detector.HandleHLSSegment(
				userFromCtx(resp.Request.Context()).Name,
				resp.Request.URL.Path,
			)
		}
	}

	// 被动识别用户：客户端（网页/移动）自己会调 /user/me，
	// 复用它"自身带着正确凭证"的那次响应解析用户名，比主动探测稳得多
	// （主动探测拿的是任意触发请求的头，很可能不含 music-token 而 401）。
	// 与 debug 无关，始终执行——这是功能本身，不是日志开销。
	if resp.StatusCode == http.StatusOK && resp.Request != nil &&
		isUserMePath(resp.Request.URL.Path) {
		uc := userFromCtx(resp.Request.Context())
		key := uc.Key
		if key == "" {
			key, _ = extractUserKey(resp.Request)
		}
		// 仅使用 music-token 识别用户。
		if key != "" && uc.Source == "music-token" {
			if b, rerr := drainJSONBody(resp, 1<<16); rerr == nil {
				if name := p.userCache.Update(key, b); name != "" {
					p.logger.Debug(
						"被动识别用户(复用客户端 /user/me )",
						"用户标识", strutil.FirstN8(key),
						"用户名", name,
					)
					p.recordUserIdentity(key, name, resp.Request)
				}
			}
		}
	}

	// 客户端 token 过期后会重新登录/换 token：实际调用的是 /user/auth-login
	//（也可能走 /user/password-login，二者响应结构一致），从响应里解析出新 userToken + 用户名。
	// 主动登记：客户端一旦切换到新 token，scrobble / 同步立即生效，
	// 不再依赖异步探测（探测头常被签名机制挡成 401），
	// 也不会因旧 token 失效而丢掉这一期间的播放记录。
	// 这正是"检查新获取的 token"这一环——此前只处理了"过期 token"（InvalidateToken）。
	if isUserLoginPath(resp.Request.URL.Path) {
		if b, rerr := drainJSONBody(resp, 1<<16); rerr == nil {
			if tok, name := playback.ParseLoginToken(b); tok != "" && name != "" {
				p.userCache.Register(tok, name)
				p.recordUserIdentity(tok, name, resp.Request)

				// 这次登录用的是旧（已过期）token，现在已轮换，把它从缓存剔除，
				// 避免旧 token 残留在活跃列表里反复打上游。
				if uc := userFromCtx(resp.Request.Context()); uc.Source == "music-token" && uc.Key != "" && uc.Key != tok {
					p.userCache.InvalidateToken(uc.Key)
				}

				p.logger.Info(
					"从登录响应登记新 token",
					"用户标识", strutil.FirstN8(tok),
					"用户名", name,
				)
			}
		}
	}

	// 仅开启请求日志时才读取/搬运响应 body：否则每个响应都多一次 I/O，非 debug 下毫无必要。
	// capture 可用时，完整响应写入 capture.log，主日志不再打印响应体。
	if p.capture != nil && p.capture.enabled != nil && p.capture.enabled.Load() {
		ct := resp.Header.Get("Content-Type")
		if !isPlaybackPath(resp.Request.URL.Path) &&
			(strings.Contains(ct, "application/json") || strings.Contains(ct, "text/")) {
			body, err := io.ReadAll(io.LimitReader(resp.Body, maxInspectBody))
			if err != nil {
				p.logger.Warn("读取响应体失败",
					"路径", resp.Request.URL.Path,
					"错误", err,
				)
			} else {
				resp.Body = io.NopCloser(
					io.MultiReader(bytes.NewReader(body), resp.Body),
				)

				if entry := captureEntryFromCtx(resp.Request.Context()); entry != nil {
					// 请求 + 响应合并为一条记录一次性写出（capture.log），主日志不再重复。
					entry.Finish(resp.StatusCode, resp.Header, body, resp.ContentLength)
				}
			}
		}
	}

	return nil
}

// recordUserIdentity 把识别到的音乐用户持久化到 users 表（UA + token 前缀 + 时间）。
// 平台绑定（is_admin 等）由 Web UI 打开时经 BindUserPlatform 补全，此处只落音乐侧身份。
func (p *Proxy) recordUserIdentity(token, username string, r *http.Request) {
	if p.cfg.Store == nil || username == "" {
		return
	}
	ua := ""
	if r != nil {
		ua = r.UserAgent()
	}
	sys, client := db.ParseUserAgent(ua)
	_ = p.cfg.Store.UpsertUser(db.UserUpsert{
		Username:    username,
		TokenPrefix: strutil.FirstN8(token),
		UARaw:       ua,
		UASystem:    sys,
		UAClient:    client,
	})
}

// drainJSONBody 读取（必要时先解压）响应体并解析为明文 JSON 字节，同时把解压后的
// 内容重新塞回 resp.Body，并移除 Content-Encoding / Content-Length，确保下游客户端
// 拿到的是已解码内容（否则客户端会再解一次压缩而失败）。
//
// 背景：代理 transport 透传客户端 Accept-Encoding，上游对 /user/me、
// /user/password-login 等 JSON 响应做 gzip 压缩；此前直接当 JSON 解析导致静默失败、
// 用户名与新 token 永远解析不出、用户无法被记录。
func drainJSONBody(resp *http.Response, max int64) ([]byte, error) {
	r := io.LimitReader(resp.Body, max)
	if enc := strings.TrimSpace(strings.ToLower(resp.Header.Get("Content-Encoding"))); enc != "" {
		switch enc {
		case "gzip":
			gz, err := gzip.NewReader(r)
			if err != nil {
				return nil, err
			}
			defer gz.Close()
			r = gz
		case "deflate":
			// 标准里 Content-Encoding: deflate 实为 zlib 包装。
			zr, err := zlib.NewReader(r)
			if err != nil {
				return nil, err
			}
			defer zr.Close()
			r = zr
			// br(brotli) 标准库无解码器，交给后续 JSON 解析（失败即降级）。
		}
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	if resp.Header.Get("Content-Encoding") != "" {
		resp.Header.Del("Content-Encoding")
		resp.Header.Del("Content-Length")
	}
	resp.Body = io.NopCloser(bytes.NewReader(b))
	return b, nil
}

// seenToucher 按 token 前缀节流地异步刷新 users.last_seen_at（活跃心跳）。
// 每个 token 在 interval 内最多写一次库，且写入在独立 goroutine 完成，
// 因此绝不阻塞请求处理；sqlite 单连接下也能扛住高并发取流请求。
type seenToucher struct {
	mu       sync.Mutex
	last     map[string]time.Time
	interval time.Duration
	store    *playback.UserStore
}

func newSeenToucher(store *playback.UserStore) *seenToucher {
	return &seenToucher{
		last:     make(map[string]time.Time),
		interval: 2 * time.Minute,
		store:    store,
	}
}

// touch 刷新指定 token 前缀的活跃时间。throttle 失败/缺失均静默返回。
func (t *seenToucher) touch(tokenPrefix string) {
	if t == nil || t.store == nil || tokenPrefix == "" {
		return
	}
	now := time.Now()
	t.mu.Lock()
	if lt, ok := t.last[tokenPrefix]; ok && now.Sub(lt) < t.interval {
		t.mu.Unlock()
		return
	}
	t.last[tokenPrefix] = now
	if len(t.last) > 2000 { // 长运行内存保护：超阈值整体重建。
		t.last = make(map[string]time.Time)
		t.last[tokenPrefix] = now
	}
	t.mu.Unlock()

	go func() {
		_ = t.store.TouchUserSeenByToken(tokenPrefix)
	}()
}

func (p *Proxy) errorHandler(
	w http.ResponseWriter,
	r *http.Request,
	err error,
) {
	// 客户端主动断开（切歌、拖动进度条）不算错误。
	if errors.Is(err, context.Canceled) ||
		errors.Is(err, net.ErrClosed) {
		p.logger.Debug("连接已断开",
			"路径", r.URL.Path,
			"查询", r.URL.RawQuery,
			"原因", err,
		)

		return
	}

	p.logger.Error("上游请求失败",
		"方法", r.Method,
		"路径", r.URL.Path,
		"查询", r.URL.RawQuery,
		"上游", p.cfg.UpstreamSocket,
		"错误", err,
	)

	http.Error(w, "upstream unavailable", http.StatusBadGateway)
}

// countWriter 统计响应状态码与实际写入字节数，用于取流时长推算。
type countWriter struct {
	http.ResponseWriter

	status  int
	written int64
}

func (c *countWriter) WriteHeader(code int) {
	if c.status == 0 {
		c.status = code
	}

	c.ResponseWriter.WriteHeader(code)
}

func (c *countWriter) Write(b []byte) (int, error) {
	if c.status == 0 {
		c.status = http.StatusOK
	}

	n, err := c.ResponseWriter.Write(b)
	c.written += int64(n)

	return n, err
}

func (c *countWriter) Flush() {
	if f, ok := c.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap 让 http.ResponseController 能找到底层的 Flusher / Hijacker，
// 否则流式响应和 WebSocket 会失效。
func (c *countWriter) Unwrap() http.ResponseWriter {
	return c.ResponseWriter
}

func isSensitiveHeader(key string) bool {
	for _, name := range sensitiveHeaders {
		if strings.EqualFold(key, name) {
			return true
		}
	}

	return false
}

type userCtxKey struct{}

type userCtx struct {
	Key    string
	Name   string
	Source string // "music-token" 或 "fnos-token"
}

// extractUserKey 从 Cookie 提取 per-user 会话标识：
// 优先 music-token（音乐服务会话），回退 fnos-token（全系统统一会话）。
// 返回标识值与来源名，便于诊断。
func extractUserKey(r *http.Request) (key, source string) {
	// 优先 music-token cookie（官方 App 走这个）。
	if c, err := r.Cookie("music-token"); err == nil && c.Value != "" {
		return c.Value, "music-token"
	}

	// 网页端 / 部分客户端把 music-token 放在 Authorization: Bearer <token>
	// 或 X-Auth-Token 头里。这些同样承载【音乐服务】会话，应视同 music-token 参与识别，
	// 否则这类客户端永远取不到 token、用户表不记录（之前只读了 music-token cookie）。
	if v := r.Header.Get("Authorization"); strings.HasPrefix(v, "Bearer ") {
		if tk := strings.TrimSpace(strings.TrimPrefix(v, "Bearer ")); tk != "" {
			return tk, "music-token"
		}
	}
	if v := r.Header.Get("X-Auth-Token"); v != "" {
		return v, "music-token"
	}

	if c, err := r.Cookie("fnos-token"); err == nil && c.Value != "" {
		return c.Value, "fnos-token"
	}

	return "", ""
}

// hasCookie 仅判断请求是否携带某名称的 cookie（不打值，避免泄露）。
func hasCookie(r *http.Request, name string) bool {
	_, err := r.Cookie(name)

	return err == nil
}

func userFromCtx(ctx context.Context) userCtx {
	if v, ok := ctx.Value(userCtxKey{}).(userCtx); ok {
		return v
	}

	return userCtx{}
}

// userLabel 日志里展示"用户名"，无用户名时仅展示标识前 8 位，都没有则"未知"。
func userLabel(uc userCtx) string {
	if uc.Name != "" {
		return uc.Name
	}

	if uc.Key != "" {
		k := uc.Key
		if len(k) > 8 {
			k = k[:8]
		}

		return "匿名:" + k
	}

	return "未知"
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}

	return fmt.Sprintf("%s...(%d bytes)", s[:max], len(s))
}
