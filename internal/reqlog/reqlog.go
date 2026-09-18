// Package reqlog 提供客户端请求日志记录器。
//
// 功能与 proxy.CaptureLogger 完全一致（异步排队、Begin/Finish 模式、
// 敏感头打码、body 截断），但独立实现、不依赖 proxy 包，
// 供 feiniu / lastfm / listenbrainz 三个客户端包复用。
//
// 每个客户端各自指定日志文件名（feiniu.log / lastfm.log / listenbrainz.log），
// 写入同一日志目录，格式统一，方便排查接口问题。
package reqlog

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gopkg.in/natefinch/lumberjack.v2"
)

// defaultLogDir 默认日志目录，与主日志一致。
const defaultLogDir = "/var/log/fnmusic-sync"

// Logger 请求日志写入器（异步排队、完整记录）。
// 格式与 proxy.CaptureLogger 一致：请求+响应成对成块、敏感头打码、body 截断。
//
// 通过 enabled 标志控制是否记录，与 slog 级别解耦：
// 由 --debug flag 开启。
type Logger struct {
	rotator *lumberjack.Logger
	ch      chan []byte
	stop    chan struct{}
	wg      sync.WaitGroup
	enabled *atomic.Bool
}

// New 创建请求日志写入器。
//   - dir 日志目录，为空回退到默认目录；off/none/- 禁用文件日志返回 nil。
//   - filename 日志文件名（如 feiniu.log、lastfm.log、listenbrainz.log）。
//   - maxSize/maxBackups/maxAge/compress 复用主日志的轮转配置。
//   - enabled 控制是否记录的原子标志，由外部（--debug 或配置热更新）设置。
//     为 nil 时始终记录。
//
// dir 不可写时返回 nil，调用方判断 nil 跳过日志。
func New(dir, filename string, maxSize, maxBackups, maxAge int, compress bool, enabled *atomic.Bool) *Logger {
	switch strings.ToLower(strings.TrimSpace(dir)) {
	case "off", "none", "-":
		return nil
	case "":
		dir = defaultLogDir
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil
	}

	path := filepath.Join(dir, filename)

	l := &Logger{
		rotator: &lumberjack.Logger{
			Filename:   path,
			MaxSize:    maxSize,
			MaxBackups: maxBackups,
			MaxAge:     maxAge,
			Compress:   compress,
			LocalTime:  true,
		},
		ch:      make(chan []byte, 4096),
		stop:    make(chan struct{}),
		enabled: enabled,
	}

	// 立即投递启动标记，便于确认日志已就绪。
	l.ch <- []byte("请求日志已启用（异步排队写入，完整记录每个请求/响应）\n")

	l.wg.Add(1)
	go l.worker()

	return l
}

// worker 单 goroutine 顺序消费日志条目并写入 lumberjack，天然无并发竞争。
func (l *Logger) worker() {
	defer l.wg.Done()
	for {
		select {
		case b := <-l.ch:
			_, _ = l.rotator.Write(b)
		case <-l.stop:
			for {
				select {
				case b := <-l.ch:
					_, _ = l.rotator.Write(b)
				default:
					return
				}
			}
		}
	}
}

// Close 关闭日志：发 stop 信号让 worker 写完剩余条目后退出，再关闭底层文件。
func (l *Logger) Close() {
	if l == nil || l.rotator == nil {
		return
	}
	close(l.stop)
	l.wg.Wait()
	_ = l.rotator.Close()
}

// Entry 一条完整的请求/响应记录：从请求进入时暂存请求部分，
// 响应返回时再合并响应部分、一次性写出。保证请求/响应成对成块，
// 并发下不会交错，日志始终可读。
type Entry struct {
	l *Logger

	method, path, query string
	reqHeader           http.Header
	reqBody             []byte

	finished bool
}

// Begin 在请求进入时创建一条记录（仅暂存请求部分，不写出）。
// 仅在 enabled 标志为 true 时才创建，其他情况返回 nil。
// logger 不可用时也返回 nil，调用方可直接忽略后续 Finish。
func (l *Logger) Begin(method, path, query string, header http.Header, body []byte) *Entry {
	if l == nil || l.rotator == nil {
		return nil
	}
	if l.enabled != nil && !l.enabled.Load() {
		return nil
	}
	return &Entry{l: l, method: method, path: path, query: query, reqHeader: header, reqBody: body}
}

// Finish 追加响应信息并将整条记录（请求 + 响应）一次性写出。
// 幂等：仅首次调用生效，后续调用（如 defer 兜底）直接忽略。
func (e *Entry) Finish(status int, header http.Header, respBody []byte, contentLength int64) {
	if e == nil || e.l == nil || e.finished {
		return
	}
	e.finished = true

	var buf bytes.Buffer
	ts := time.Now().Format("2006-01-02 15:04:05.000")
	fmt.Fprintf(&buf, "\n========== %s 请求 %s %s ==========\n", ts, e.method, e.path)
	if e.query != "" {
		fmt.Fprintf(&buf, "查询: %s\n", RedactQuery(e.query))
	}
	writeHeaders(&buf, "请求头", e.reqHeader)

	if len(e.reqBody) > 0 {
		limit := min(len(e.reqBody), 65536)
		redacted := RedactBody(e.reqHeader.Get("Content-Type"), e.reqBody[:limit])
		fmt.Fprintf(&buf, "请求体 (%d bytes):\n", len(e.reqBody))
		buf.Write(redacted)
		if len(e.reqBody) > limit {
			fmt.Fprintf(&buf, "\n... (截断，共 %d bytes)\n", len(e.reqBody))
		} else {
			fmt.Fprintf(&buf, "\n")
		}
	}

	// 响应部分与请求同一块，天然配对。
	fmt.Fprintf(&buf, "---------- 响应 ----------\n")
	if status > 0 {
		fmt.Fprintf(&buf, "%s 响应 %s 状态码: %d\n", ts, e.path, status)
	} else {
		fmt.Fprintf(&buf, "%s 响应 %s 状态码: (无响应/连接断开)\n", ts, e.path)
	}
	if contentLength > 0 {
		fmt.Fprintf(&buf, "响应长度: %d\n", contentLength)
	}
	if ct := header.Get("Content-Type"); ct != "" {
		fmt.Fprintf(&buf, "内容类型: %s\n", ct)
	}
	writeHeaders(&buf, "响应头", header)

	if len(respBody) > 0 {
		limit := min(len(respBody), 65536)
		redacted := RedactBody(header.Get("Content-Type"), respBody[:limit])
		fmt.Fprintf(&buf, "响应体 (%d bytes):\n", len(respBody))
		buf.Write(redacted)
		if len(respBody) > limit {
			fmt.Fprintf(&buf, "\n... (截断，共 %d bytes)\n", len(respBody))
		} else {
			fmt.Fprintf(&buf, "\n")
		}
	}
	fmt.Fprintf(&buf, "================================\n\n")

	e.l.enqueue(buf.Bytes())
}

// enqueue 阻塞排队写入：运行中所有请求完整入队（不丢）；stop 触发后丢弃。
func (l *Logger) enqueue(b []byte) {
	cp := make([]byte, len(b))
	copy(cp, b)
	select {
	case l.ch <- cp:
	case <-l.stop:
	}
}

// sensitiveHeaders 敏感请求头，日志中一律打码，避免 token / session 泄漏。
var sensitiveHeaders = []string{
	"Authorization",
	"Proxy-Authorization",
	"Cookie",
	"Set-Cookie",
	"X-Auth-Token",
	"X-Session",
}

// isSensitiveHeader 判断请求头是否敏感（大小写不敏感）。
func isSensitiveHeader(key string) bool {
	for _, name := range sensitiveHeaders {
		if strings.EqualFold(key, name) {
			return true
		}
	}
	return false
}

// writeHeaders 把请求/响应头写入 buf，敏感头打码。
func writeHeaders(buf *bytes.Buffer, title string, headers http.Header) {
	fmt.Fprintf(buf, "%s:\n", title)
	for k, vs := range headers {
		val := strings.Join(vs, ", ")
		if isSensitiveHeader(k) {
			val = "***"
		}
		fmt.Fprintf(buf, "  %s: %s\n", k, val)
	}
}

// sensitiveParams 需要打码的请求/响应参数名（大小写不敏感）。
// 涵盖 Last.fm（api_key / sk / api_sig）、ListenBrainz（token）及通用凭据字段，
// 避免 key / 会话密钥等敏感信息以明文落盘到 lastfm.log / listenbrainz.log / feiniu.log。
var sensitiveParams = map[string]bool{
	"api_key": true, "apikey": true,
	"api_secret": true, "apisecret": true,
	"secret":     true,
	"sk":         true,
	"sessionkey": true, "session_key": true, "session": true,
	"password": true, "passwd": true, "pass": true, "pwd": true,
	"token": true, "auth_token": true, "access_token": true, "refresh_token": true,
	"api_sig": true, "signature": true, "sig": true,
	"key": true,
}

// redactQuery 将查询串中敏感参数值打码；解析失败则原样返回，不影响正常日志。
func RedactQuery(raw string) string {
	if raw == "" {
		return raw
	}
	vals, err := url.ParseQuery(raw)
	if err != nil {
		return raw
	}
	for k := range vals {
		if sensitiveParams[strings.ToLower(k)] {
			vals[k] = []string{"***"}
		}
	}
	return vals.Encode()
}

// redactBody 按内容类型对 body 打码：
//   - application/x-www-form-urlencoded：解析后掩敏感字段
//     （Last.fm 的请求体即此格式，含 api_key / sk / api_sig）；
//   - 其它（JSON / XML 等）：用正则尽力掩敏感键的值
//     （如 ListenBrainz 响应、Last.fm 的 <key> 会话密钥）。
func RedactBody(contentType string, body []byte) []byte {
	if len(body) == 0 {
		return body
	}
	if strings.Contains(strings.ToLower(contentType), "x-www-form-urlencoded") {
		if vals, err := url.ParseQuery(string(body)); err == nil {
			for k := range vals {
				if sensitiveParams[strings.ToLower(k)] {
					vals[k] = []string{"***"}
				}
			}
			return []byte(vals.Encode())
		}
	}
	return []byte(redactSensitiveText(string(body)))
}

// reJSONSecret / reXMLSecret 用于非表单 body 的尽力打码。
// 注意：Go 的 regexp 基于 RE2，不支持反向引用（如 \1），故 XML 匹配对开/闭标签
// 使用独立的选择分支（实际数据中两者一致）；仅用于打码值，类型不匹配也不会误伤。
var (
	reJSONSecret = regexp.MustCompile(`(?i)("(?:api_key|apikey|api_secret|apisecret|secret|sk|sessionkey|session_key|session|password|passwd|pass|pwd|token|auth_token|access_token|refresh_token|api_sig|signature|sig|key)"\s*:\s*")([^"]*)(")`)
	reXMLSecret  = regexp.MustCompile(`(?i)<(key|session|token|secret|api_key|apikey|password)[^>]*>([^<]*)</(?:key|session|token|secret|api_key|apikey|password)>`)
)

func redactSensitiveText(s string) string {
	s = reJSONSecret.ReplaceAllString(s, `$1***$3`)
	s = reXMLSecret.ReplaceAllString(s, `$1***$3`)
	return s
}
