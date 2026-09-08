package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"cnb.cool/dtapp/fnmusic-sync/internal/config"
	"gopkg.in/natefinch/lumberjack.v2"
)

// newLogger 构造进程唯一的 logger：同时输出到控制台(stderr)与日志文件。
// 返回 logger、级别变量（供配置热更新）与关闭函数。
func newLogger(lg config.LoggingConfig) (*slog.Logger, *slog.LevelVar, func()) {
	levelVar := levelOf()

	logger, _, closeLog := setupLogger(rt.LogDir, lg, levelVar)

	return logger, levelVar, closeLog
}

// levelOf 起始日志级别：等配置加载后由 parseLevel 决定。
func levelOf() *slog.LevelVar {
	levelVar := &slog.LevelVar{}
	levelVar.Set(slog.LevelInfo)

	return levelVar
}

// parseLevel 计算日志级别：由配置文件 logging.level 决定。
// 请求日志（capture/feiniu/lastfm/listenbrainz）的开关由独立的 enabled 标志控制，
// 与 slog 级别解耦。
func parseLevel(cfg *config.Config) slog.Level {
	switch strings.ToLower(cfg.Logging.Level) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// 日志目录与文件名：固定值，不在配置文件里暴露。
// 目录与 scripts/install.sh、scripts/postinstall.sh 创建的目录保持一致：
// 日志 /var/log/fnmusic-sync，状态 /var/lib/fnmusic-sync，配置 /etc/fnmusic-sync。
// fpk 模式下使用 rt.LogDir（TRIM_PKGVAR/logs）。
const (
	defaultLogFile = "fnmusic-sync.log"
)

// setupLogger 构造 logger：同一条日志同时写控制台(stderr)与日志文件。
// 文件写入走 lumberjack：按大小自动切割、历史文件 gzip 压缩、按份数与天数清理过期。
// 日志目录建不出来（非 root 运行、目录不可写等）时降级为仅控制台并告警，不阻断启动。
// 返回 logger、日志文件实际路径（未开启文件日志时为空）、关闭文件句柄的函数。
func setupLogger(
	dir string,
	lg config.LoggingConfig,
	levelVar *slog.LevelVar,
) (*slog.Logger, string, func()) {
	opts := &slog.HandlerOptions{Level: levelVar}
	console := slog.NewTextHandler(os.Stderr, opts)
	closeFn := func() {}

	// 显式关闭文件日志：off / none / -
	if logDisabled(dir) {
		return slog.New(console), "", closeFn
	}

	if dir == "" {
		dir = rt.LogDir
		if dir == "" {
			dir = "/var/log/fnmusic-sync"
		}
	}

	path := filepath.Join(dir, defaultLogFile)

	if err := os.MkdirAll(dir, 0o755); err != nil {
		l := slog.New(console)
		l.Warn("日志目录创建失败，仅输出到控制台", "目录", dir, "错误", err)

		return l, "", closeFn
	}

	// 轮转参数来自配置文件 logging.*（0 表示对应策略关闭）。
	rotator := &lumberjack.Logger{
		Filename:   path,
		MaxSize:    lg.MaxSize,
		MaxBackups: lg.MaxBackups,
		MaxAge:     lg.MaxAge,
		Compress:   lg.Compress,
		LocalTime:  true,
	}

	closeFn = func() { _ = rotator.Close() }

	l := slog.New(newMultiHandler(console, slog.NewTextHandler(rotator, opts)))
	l.Info("日志文件已开启",
		"路径", path,
		"切割MB", lg.MaxSize,
		"保留份数", lg.MaxBackups,
		"过期天", lg.MaxAge,
		"压缩", lg.Compress,
	)

	return l, path, closeFn
}

// logDisabled 判断日志目录是否被显式关闭。
func logDisabled(dir string) bool {
	switch strings.ToLower(strings.TrimSpace(dir)) {
	case "off", "none", "-":
		return true
	default:
		return false
	}
}

// multiHandler 把一条日志分发给多个 slog.Handler（控制台 + 文件）。
// 任一 handler 失败不影响其它 handler，避免“日志文件写不了把控制台日志也搞丢”。
type multiHandler struct {
	handlers []slog.Handler
}

func newMultiHandler(handlers ...slog.Handler) *multiHandler {
	return &multiHandler{handlers: handlers}
}

func (h *multiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, x := range h.handlers {
		if x.Enabled(ctx, level) {
			return true
		}
	}

	return false
}

func (h *multiHandler) Handle(ctx context.Context, r slog.Record) error {
	var errs []error

	for _, x := range h.handlers {
		if !x.Enabled(ctx, r.Level) {
			continue
		}

		if err := x.Handle(ctx, r); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

func (h *multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}

	hs := make([]slog.Handler, 0, len(h.handlers))
	for _, x := range h.handlers {
		hs = append(hs, x.WithAttrs(attrs))
	}

	return newMultiHandler(hs...)
}

func (h *multiHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}

	hs := make([]slog.Handler, 0, len(h.handlers))
	for _, x := range h.handlers {
		hs = append(hs, x.WithGroup(name))
	}

	return newMultiHandler(hs...)
}
