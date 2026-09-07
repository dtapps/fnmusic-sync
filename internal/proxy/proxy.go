package proxy

import (
	"bytes"
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
	"time"

	"cnb.cool/dtapp/fnmusic-sync/internal/playback"
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
		MaxIdleConns:          64,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       90 * time.Second,
		ExpectContinueTimeout: time.Second,
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

	// 播放识别 → scrobble 推送。
	p.detector = playback.NewDetector(manager, logger)

	p.reverse = &httputil.ReverseProxy{
		Director:       p.director,
		Transport:      p.transport,
		ErrorHandler:   p.errorHandler,
		ModifyResponse: p.modifyResponse,
		// -1：写入后立即 flush，保证音频流 / SSE 不被缓冲。
		FlushInterval: -1,
	}

	return p
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
			context.Background(),
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
				userCtx{Key: userKey, Name: name},
			),
		)
	}

	p.logger.Debug("用户识别诊断",
		"含music-token", hasCookie(r, "music-token"),
		"含fnos-token", hasCookie(r, "fnos-token"),
		"userKey来源", userSource,
		"userKey前8位", firstN(userKey, 8),
	)

	body := p.bufferBody(r)
	stream := isPlaybackPath(r.URL.Path)

	writer := &countWriter{ResponseWriter: w}

	p.logger.Debug("收到请求",
		"方法", r.Method,
		"主机", r.Host,
		"路径", r.URL.Path,
		"查询", r.URL.RawQuery,
		"请求头", redactHeaders(r.Header),
		"请求体", truncate(string(body), maxLogBody),
		"声明长度", r.ContentLength,
	)

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
	if strings.Contains(r.URL.Path, "/event/report") {
		p.detector.HandleEventReport(
			userFromCtx(r.Context()).Name,
			body,
		)
	}

	p.reverse.ServeHTTP(writer, r)

	p.logDone(r, writer, time.Since(start), stream)
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

	p.logger.Debug("响应完成", attrs...)
}

// director 只改写转发目标，其他一律保持原样。
//
// 注意：不能动 outreq.Host。nginx 的 proxy_pass 指向 unix socket 时
// 默认下发 Host: unix，trim-music 可能依赖它，必须原样透传。
func (p *Proxy) director(req *http.Request) {
	req.URL.Scheme = "http"
	req.URL.Host = "trim-music"
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
	if strings.Contains(resp.Request.URL.Path, "/user/me") {
		if uc := userFromCtx(resp.Request.Context()); uc.Key != "" {
			body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
			if err == nil {
				resp.Body = io.NopCloser(bytes.NewReader(body))
				p.userCache.Update(uc.Key, body)
			}
		}
	}

	// 缓存曲目元数据（标题/艺人/专辑/时长），供后续 scrobble 使用。
	if strings.Contains(resp.Request.URL.Path, "/track/metadata") {
		guid := resp.Request.URL.Query().Get("guid")
		if guid != "" {
			body, err := io.ReadAll(io.LimitReader(resp.Body, maxInspectBody))
			if err != nil {
				p.logger.Warn("读取曲目元数据失败",
					"歌曲", guid,
					"错误", err,
				)
			} else {
				// 读完必须塞回去，否则客户端拿不到响应内容。
				resp.Body = io.NopCloser(
					io.MultiReader(bytes.NewReader(body), resp.Body),
				)

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

	// 被动识别用户：客户端（网页/移动）自己会调 /user/me，
	// 复用它"自身带着正确凭证"的那次响应解析用户名，比主动探测稳得多
	// （主动探测拿的是任意触发请求的头，很可能不含 music-token 而 401）。
	// 与 debug 无关，始终执行——这是功能本身，不是日志开销。
	if resp.StatusCode == http.StatusOK && resp.Request != nil &&
		resp.Request.URL.Path == p.userCache.MEPath() {
		key := userFromCtx(resp.Request.Context()).Key
		if key == "" {
			key, _ = extractUserKey(resp.Request)
		}
		if key != "" {
			if b, rerr := io.ReadAll(io.LimitReader(resp.Body, 1<<16)); rerr == nil {
				// 读完必须塞回，否则客户端拿不到原响应。
				resp.Body = io.NopCloser(io.MultiReader(bytes.NewReader(b), resp.Body))
				if name := p.userCache.Update(key, b); name != "" {
					p.logger.Debug(
						"被动识别用户(复用客户端 /user/me )",
						"用户标识", firstN(key, 8),
						"用户名", name,
					)
				}
			}
		}
	}

	attrs := []any{
		"状态码", resp.StatusCode,
		"路径", resp.Request.URL.Path,
		"查询", resp.Request.URL.RawQuery,
		"内容类型", resp.Header.Get("Content-Type"),
		"响应长度", resp.ContentLength,
		"响应头", redactHeaders(resp.Header),
	}

	// 仅 debug 时才读取/搬运响应 body：否则每个响应都多一次 I/O，非 debug 下毫无必要。
	// 注意这里用 Enabled 只是为了"跳过昂贵的 body 读取"，Debug 日志本身仍由级别过滤。
	if p.logger.Enabled(resp.Request.Context(), slog.LevelDebug) {
		// 仅对 JSON / 文本类响应打印 body，避免图片、音频等二进制把日志撑爆；
		// 音频流（playback path）本就不读 body。非文本响应只记类型与长度。
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
				// 读完必须塞回去，否则客户端拿不到响应内容。
				resp.Body = io.NopCloser(
					io.MultiReader(bytes.NewReader(body), resp.Body),
				)

				attrs = append(attrs, "响应体", truncate(string(body), maxLogBody))
			}
		}
	}

	p.logger.Debug("上游响应", attrs...)

	return nil
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

// isPlaybackPath 判断是否为音频取流请求。
func isPlaybackPath(path string) bool {
	return strings.Contains(path, "/track/stream") ||
		strings.Contains(path, "/track/play-url")
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

func redactHeaders(src http.Header) http.Header {
	dst := make(http.Header, len(src))

	for key, values := range src {
		if isSensitiveHeader(key) {
			dst[key] = []string{"<已脱敏>"}

			continue
		}

		dst[key] = values
	}

	return dst
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
	Key  string
	Name string
}

// extractUserKey 从 Cookie 提取 per-user 会话标识：
// 优先 music-token（音乐服务会话），回退 fnos-token（全系统统一会话）。
// 返回标识值与来源名，便于诊断。
func extractUserKey(r *http.Request) (key, source string) {
	if c, err := r.Cookie("music-token"); err == nil && c.Value != "" {
		return c.Value, "music-token"
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

func firstN(s string, n int) string {
	if len(s) <= n {
		return s
	}

	return s[:n]
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
