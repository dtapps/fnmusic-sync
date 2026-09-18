// Package safego 提供统一的 goroutine 启动入口：入口即带 recover。
//
// goroutine 内未捕获的 panic 不仅会拖垮整个进程，还会让该 goroutine 消失——
// goroutine 总数不升反降，依赖数量阈值的监视器抓不到现场。
// 因此所有后台 goroutine 都应经 Go 启动：panic 时以 Error 级别打印
// recover 到的值和全进程 goroutine 栈快照，进程继续存活。
package safego

import (
	"log/slog"
	"runtime"
)

// Go 以后台协程运行 fn。logger 为 nil 时回退 slog.Default()。
// name 仅用于日志定位（建议 "包名.函数名"）。
func Go(logger *slog.Logger, name string, fn func()) {
	if logger == nil {
		logger = slog.Default()
	}

	go func() {
		defer func() {
			r := recover()
			if r == nil {
				return
			}

			buf := make([]byte, 1<<20)
			n := runtime.Stack(buf, true)
			logger.Error("goroutine panic 已捕获，进程继续运行",
				"goroutine", name,
				"panic", r,
				"全量栈", string(buf[:n]),
			)
		}()

		fn()
	}()
}
