//go:build fpk

package main

import (
	"log/slog"
	"os"
	"path/filepath"

	"cnb.cool/dtapp/fnmusic-sync/internal/db"
	"cnb.cool/dtapp/fnmusic-sync/internal/webui"
)

// webUIStopper 是 Web UI 服务的停止接口。
type webUIStopper interface {
	Stop()
}

// startWebUI 在 fpk 模式下启动统一网关 Web UI 服务。
// 监听 ${TRIM_APPDEST}/app.sock，飞牛 fnOS 网关会把 /app/fnmusic-sync 路径的请求转发过来。
// provider 提供当前通过 token 识别的活跃用户列表（供 Web UI 展示）。
// dbStore 为持久化层（用户列表 / 运行状态），可为 nil。
func startWebUI(logger *slog.Logger, provider webui.ActiveUserProvider, dbStore *db.Store) (webUIStopper, error) {
	appDest := os.Getenv("TRIM_APPDEST")
	if appDest == "" {
		appDest = filepath.Join("/var/apps", "fnmusic-sync", "target")
	}

	socketPath := filepath.Join(appDest, "app.sock")

	server := webui.NewServer(rt.ConfigPath, rt.LogDir, logger, provider, dbStore)
	if err := server.Start(socketPath); err != nil {
		return nil, err
	}

	return server, nil
}
