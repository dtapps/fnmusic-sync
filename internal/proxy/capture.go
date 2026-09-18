// Package proxy
// capture.go 实现 API 抓包日志：debug 开启时，将每个请求/响应的完整信息
// 写入独立日志文件（capture.log），与主日志分离、独立轮转，方便排查接口。
//
// 写入采用异步排队：handler 把组装好的日志条目投递到 channel，由单个后台
// goroutine 顺序写入 lumberjack。运行中完整记录每一条请求（不丢、不限频），
// 避免高并发下写盘阻塞请求处理。
package proxy

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"cnb.cool/dtapp/fnmusic-sync/internal/reqlog"
	"gopkg.in/natefinch/lumberjack.v2"
)

// captureLogFile 抓包日志文件名（与主日志同目录）。
const captureLogFile = "capture.log"

// defaultCaptureLogDir 抓包日志默认目录，与主日志一致（setupLogger 的默认目录）。
const defaultCaptureLogDir = "/var/log/fnmusic-sync"

// CaptureLogger 请求抓包日志写入器（异步排队、完整记录）。
// 通过 enabled 标志控制是否记录，与 slog 级别解耦。
type CaptureLogger struct {
	rotator *lumberjack.Logger
	ch      chan []byte
	stop    chan struct{}
	wg      sync.WaitGroup
	enabled *atomic.Bool
}

// NewCaptureLogger 创建抓包日志写入器。
// dir 为空回退到默认目录（与 setupLogger 一致）；off/none/- 禁用文件日志返回 nil。
// dir 不可写时返回 nil，调用方判断 nil 跳过抓包。
// enabled 控制是否记录的原子标志，由外部（--debug 或配置热更新）设置。
func NewCaptureLogger(dir string, maxSize, maxBackups, maxAge int, compress bool, enabled *atomic.Bool) *CaptureLogger {
	switch strings.ToLower(strings.TrimSpace(dir)) {
	case "off", "none", "-":
		return nil
	case "":
		dir = defaultCaptureLogDir
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil
	}

	path := filepath.Join(dir, captureLogFile)

	c := &CaptureLogger{
		rotator: &lumberjack.Logger{
			Filename:   path,
			MaxSize:    maxSize,
			MaxBackups: maxBackups,
			MaxAge:     maxAge,
			Compress:   compress,
			LocalTime:  true,
		},
		// 缓冲较大，正常情况发送方不阻塞；worker 消费极快。
		ch:      make(chan []byte, 4096),
		stop:    make(chan struct{}),
		enabled: enabled,
	}

	// 立即投递启动标记，便于确认抓包日志已就绪（异步写出，不阻塞请求处理）。
	c.ch <- []byte("抓包日志已启用（异步排队写入，完整记录每个请求/响应）\n")

	c.wg.Add(1)
	go c.worker()

	return c
}

// worker 单 goroutine 顺序消费日志条目并写入 lumberjack，天然无并发竞争。
func (c *CaptureLogger) worker() {
	defer c.wg.Done()
	for {
		select {
		case b := <-c.ch:
			_, _ = c.rotator.Write(b)
		case <-c.stop:
			// 关闭：把缓冲里剩余的条目写完再退出。
			for {
				select {
				case b := <-c.ch:
					_, _ = c.rotator.Write(b)
				default:
					return
				}
			}
		}
	}
}

// Close 关闭抓包日志：发 stop 信号让 worker 写完剩余条目后退出，再关闭底层文件。
// 注意用 stop 而非 close(ch)，避免关闭瞬间仍有发送方访问已关闭的 channel 而 panic。
func (c *CaptureLogger) Close() {
	if c == nil || c.rotator == nil {
		return
	}
	close(c.stop)
	c.wg.Wait()
	_ = c.rotator.Close()
}

// captureEntry 一条完整的请求/响应抓包记录：从请求进入时暂存请求部分，
// 响应返回时再合并响应部分、一次性写出。这样每条请求严格成对成块，
// 并发下不会与其他请求的请求/响应交错，日志始终可读。
type captureEntry struct {
	c *CaptureLogger

	method, path, query string
	reqHeader           http.Header
	reqBody             []byte

	finished bool
}

// Begin 在请求进入时创建一条抓包记录（仅暂存请求部分，不写出）。
// 仅在 enabled 标志为 true 时才创建，其他情况返回 nil。
// capture 不可用时返回 nil，调用方可直接忽略后续 Finish。
func (c *CaptureLogger) Begin(method, path, query string, header http.Header, body []byte) *captureEntry {
	if c == nil || c.rotator == nil {
		return nil
	}
	if c.enabled != nil && !c.enabled.Load() {
		return nil
	}
	return &captureEntry{c: c, method: method, path: path, query: query, reqHeader: header, reqBody: body}
}

// Finish 追加响应信息并将整条记录（请求 + 响应）一次性写出。
// 幂等：仅首次调用生效，后续调用（如 defer 兜底）直接忽略。
func (e *captureEntry) Finish(status int, header http.Header, respBody []byte, contentLength int64) {
	if e == nil || e.c == nil || e.finished {
		return
	}
	e.finished = true

	var buf bytes.Buffer
	ts := time.Now().Format("2006-01-02 15:04:05.000")
	fmt.Fprintf(&buf, "\n========== %s 请求 %s %s ==========\n", ts, e.method, e.path)
	if e.query != "" {
		fmt.Fprintf(&buf, "查询: %s\n", reqlog.RedactQuery(e.query))
	}
	writeHeaders(&buf, "请求头", e.reqHeader)

	if len(e.reqBody) > 0 {
		limit := min(len(e.reqBody), 65536)
		fmt.Fprintf(&buf, "请求体 (%d bytes):\n", len(e.reqBody))
		buf.Write(reqlog.RedactBody(e.reqHeader.Get("Content-Type"), e.reqBody[:limit]))
		if len(e.reqBody) > limit {
			fmt.Fprintf(&buf, "\n... (截断，共 %d bytes)\n", len(e.reqBody))
		} else {
			fmt.Fprintf(&buf, "\n")
		}
	}

	// 响应部分与请求同一块，天然配对，不再有独立响应条目。
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
		buf.Write(reqlog.RedactBody(header.Get("Content-Type"), respBody[:limit]))
		if len(respBody) > limit {
			fmt.Fprintf(&buf, "\n... (截断，共 %d bytes)\n", len(respBody))
		} else {
			fmt.Fprintf(&buf, "\n")
		}
	}
	fmt.Fprintf(&buf, "================================\n\n")

	e.c.enqueue(buf.Bytes())
}

// enqueue 阻塞排队写入：运行中所有请求完整入队（不丢）；stop 触发后（关闭中）丢弃。
func (c *CaptureLogger) enqueue(b []byte) {
	cp := make([]byte, len(b))
	copy(cp, b)
	select {
	case c.ch <- cp:
	case <-c.stop:
	}
}

// writeHeaders 把请求/响应头写入 buf，敏感头打码，避免 token / session 泄漏到日志。
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
