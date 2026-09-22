//go:build !fpk

package main

import (
	"log/slog"
)

// webUIStopper 是 Web UI 服务的停止接口。
type webUIStopper interface {
	Stop()
}

// startWebUI 在传统安装模式下不启动 Web UI（没有统一网关）。
func startWebUI(logger *slog.Logger, _ any, _ any, _ any) (webUIStopper, error) {
	return nil, nil
}
