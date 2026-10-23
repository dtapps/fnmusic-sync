package safego

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuffer 是可并发安全的 bytes.Buffer（slog handler 写、测试读）。
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

func newTestLogger() (*slog.Logger, *syncBuffer) {
	buf := &syncBuffer{}
	handler := slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})

	return slog.New(handler), buf
}

// waitFor 轮询等待条件成立，避免用固定 sleep 引入偶发失败。
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) bool {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}

		time.Sleep(5 * time.Millisecond)
	}

	return cond()
}

func TestGoRunsFunction(t *testing.T) {
	logger, _ := newTestLogger()

	done := make(chan struct{})

	Go(logger, "test.normal", func() {
		close(done)
	})

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("fn 未被调用")
	}
}

func TestGoRecoversFromPanic(t *testing.T) {
	logger, buf := newTestLogger()

	done := make(chan struct{})

	Go(logger, "test.panic", func() {
		defer close(done)

		panic("boom")
	})

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("panic 后 fn 未正常退出（可能整个测试进程已崩）")
	}

	// 日志里必须留下 recover 记录：goroutine 名、panic 值、全量栈
	ok := waitFor(t, time.Second, func() bool {
		return strings.Contains(buf.String(), "goroutine panic 已捕获")
	})
	if !ok {
		t.Fatalf("未记录 panic 日志，实际输出: %s", buf.String())
	}

	var entry map[string]any
	for line := range strings.SplitSeq(strings.TrimSpace(buf.String()), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err == nil && m["msg"] == "goroutine panic 已捕获，进程继续运行" {
			entry = m

			break
		}
	}

	if entry == nil {
		t.Fatalf("未找到结构化的 panic 日志条目: %s", buf.String())
	}

	if entry["goroutine"] != "test.panic" {
		t.Errorf("日志 goroutine 字段 = %v, want test.panic", entry["goroutine"])
	}

	if entry["panic"] != "boom" {
		t.Errorf("日志 panic 字段 = %v, want boom", entry["panic"])
	}

	// 全量栈快照（runtime.Stack(all=true)）必须非空，否则无法定位现场
	if stack, _ := entry["全量栈"].(string); !strings.Contains(stack, "goroutine ") {
		t.Errorf("全量栈内容异常: %v", entry["全量栈"])
	}
}

func TestGoNilLoggerFallsBackToDefault(t *testing.T) {
	// logger 为 nil 时回退 slog.Default()，不应 panic。
	done := make(chan struct{})

	Go(nil, "test.nil-logger", func() {
		close(done)
	})

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("nil logger 场景 fn 未被调用")
	}
}

func TestGoPanicDoesNotKillSiblingGoroutines(t *testing.T) {
	logger, _ := newTestLogger()

	var wg sync.WaitGroup

	const n = 5

	panics := make(chan struct{}, n)

	for range n {
		wg.Add(1)

		Go(logger, "test.parallel-panic", func() {
			defer wg.Done()
			defer func() { panics <- struct{}{} }()

			panic("parallel boom")
		})
	}

	wg.Wait()
	close(panics)

	count := 0
	for range panics {
		count++
	}

	if count != n {
		t.Errorf("捕获到 %d 个 panic，want %d", count, n)
	}
}
