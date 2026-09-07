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
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/natefinch/lumberjack.v2"
)

// defaultLogDir 默认日志目录，与主日志一致。
const defaultLogDir = "/var/log/fnmusic-sync"

// Logger 请求日志写入器（异步排队、完整记录）。
// 格式与 proxy.CaptureLogger 一致：请求+响应成对成块、敏感头打码、body 截断。
//
// 仅在日志级别为 debug 时才记录请求/响应（与 capture.log 条件一致），
// 通过 levelVar 实时判断，支持配置热更新后自动生效。
type Logger struct {
	rotator  *lumberjack.Logger
	ch       chan []byte
	stop     chan struct{}
	wg       sync.WaitGroup
	levelVar *slog.LevelVar
}

// New 创建请求日志写入器。
//   - dir 日志目录，为空回退到默认目录；off/none/- 禁用文件日志返回 nil。
//   - filename 日志文件名（如 feiniu.log、lastfm.log、listenbrainz.log）。
//   - maxSize/maxBackups/maxAge/compress 复用主日志的轮转配置。
//   - levelVar 主日志的级别变量，用于实时判断是否 debug 级别。
//     nil 时始终记录（不按级别过滤）。
//
// dir 不可写时返回 nil，调用方判断 nil 跳过日志。
func New(dir, filename string, maxSize, maxBackups, maxAge int, compress bool, levelVar *slog.LevelVar) *Logger {
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
		ch:       make(chan []byte, 4096),
		stop:     make(chan struct{}),
		levelVar: levelVar,
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
// 仅在 debug 级别时才创建（与 capture.log 条件一致），其他级别返回 nil。
// logger 不可用时也返回 nil，调用方可直接忽略后续 Finish。
func (l *Logger) Begin(method, path, query string, header http.Header, body []byte) *Entry {
	if l == nil || l.rotator == nil {
		return nil
	}
	// 与 capture.log 一致：仅 debug 级别时才记录请求/响应。
	if l.levelVar != nil && l.levelVar.Level() > slog.LevelDebug {
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
		fmt.Fprintf(&buf, "查询: %s\n", e.query)
	}
	writeHeaders(&buf, "请求头", e.reqHeader)

	if len(e.reqBody) > 0 {
		limit := min(len(e.reqBody), 65536)
		fmt.Fprintf(&buf, "请求体 (%d bytes):\n", len(e.reqBody))
		buf.Write(e.reqBody[:limit])
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
		fmt.Fprintf(&buf, "响应体 (%d bytes):\n", len(respBody))
		buf.Write(respBody[:limit])
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
