package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"cnb.cool/dtapp/fnmusic-sync/internal/buildinfo"
	"cnb.cool/dtapp/fnmusic-sync/internal/config"
	"cnb.cool/dtapp/fnmusic-sync/internal/playback"
	"cnb.cool/dtapp/fnmusic-sync/internal/playlist"
	"cnb.cool/dtapp/fnmusic-sync/internal/proxy"
	"cnb.cool/dtapp/fnmusic-sync/internal/reqlog"
	"cnb.cool/dtapp/fnmusic-sync/internal/scrobbler"
)

const (
	// defaultUpstreamWait 等待上游 socket 就绪的超时时间。
	defaultUpstreamWait = 30 * time.Second
)

// isDebugEnv 判断 debug 环境变量（fpk 安装向导字段 wizard_debug 会以同名环境变量传入）。
// 接受 1/true/on/yes（大小写不敏感）；未设置或为空视为关闭。
func isDebugEnv() bool {
	switch strings.ToLower(os.Getenv("wizard_debug")) {
	case "1", "true", "on", "yes":
		return true
	default:
		return false
	}
}

// rt 在 main 启动时一次性检测，后续所有函数共用。
var rt = detectRuntime()

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		os.Exit(1)
	}
}

// runProxy 是根命令的代理模式入口（不带子命令直接运行）。
// 构造 logger 并启动代理服务。
func runProxy() error {
	opts := proxyFlags

	// logger 必须先于 run 构造（run 内所有日志都用它），故此处预读一次配置：
	// 日志目录固定、文件名固定，配置里只有级别与轮转策略（见 logging.go）。
	logCfg := config.DefaultLogging()

	if pre, perr := config.Load(rt.ConfigPath); perr == nil {
		logCfg = pre.Logging
	}

	logger, levelVar, closeLog := newLogger(logCfg)
	defer closeLog()

	return run(opts.check, opts.debug, opts.wait.Duration, logger, levelVar)
}

// run 启动代理服务的完整流程。
// debug 参数控制请求日志（capture/feiniu/lastfm/listenbrainz）的开关，
// 不影响 slog 主日志级别（由配置文件 logging.level 控制）。
func run(doCheck, debug bool, wait time.Duration, logger *slog.Logger, levelVar *slog.LevelVar) error {
	// 启动先打一行版本信息，日志里就能直接看出跑的是哪个包、什么时候编的。
	logger.Info("版本信息",
		"版本", buildinfo.Version,
		"Git提交", buildinfo.GitCommit,
		"构建时间", buildinfo.BuildTime,
		"构建时间(本地)", localBuildTime(),
		"Go版本", runtime.Version(),
		"平台", runtime.GOOS+"/"+runtime.GOARCH,
		"运行模式", rt.Mode.modeName(),
	)

	// 首次启动若没有配置文件：自动生成一份带示例的默认配置，便于直接编辑；
	// 生成失败（如权限不足）则降级为纯代理模式并告警，不退出。
	if _, statErr := os.Stat(rt.ConfigPath); statErr != nil {
		if werr := config.SaveDefault(rt.ConfigPath); werr != nil {
			logger.Warn("未找到配置文件且无法自动创建，使用内置默认配置（纯代理模式，不推送 scrobble）",
				"路径", rt.ConfigPath, "错误", werr, "示例", "configs/config.example.yaml")
		} else {
			logger.Info("已生成默认配置文件，编辑后程序会自动热加载",
				"路径", rt.ConfigPath, "示例", "configs/config.example.yaml")
		}
	}

	appCfg, err := config.Load(rt.ConfigPath)
	if err != nil {
		return err
	}

	// socket 路径固定，不可自定义。
	listen := buildinfo.DefaultListenSocket
	upstream := buildinfo.DefaultUpstreamSocket
	if d, perr := time.ParseDuration(appCfg.Server.UpstreamWait); perr == nil {
		wait = d
	}

	// 日志目录由运行模式决定，不可自定义。
	logDir := rt.LogDir
	if logDisabled(logDir) {
		logDir = ""
	}

	// 抓包/请求日志开关：由 --debug flag 或环境变量 wizard_debug=1/true 控制。
	// 二者等效（service debug 命令即加 --debug）；fpk 安装向导的字段 wizard_debug 会作为同名环境变量传入。
	captureEnabled := atomic.Bool{}
	captureEnabled.Store(debug || isDebugEnv())

	if doCheck {
		cfg := proxy.Config{
			ListenSocket:   listen,
			UpstreamSocket: upstream,
			SocketMode:     0,
			LogDir:         logDir,
			LogMaxSize:     appCfg.Logging.MaxSize,
			LogMaxBackups:  appCfg.Logging.MaxBackups,
			LogMaxAge:      appCfg.Logging.MaxAge,
			LogCompress:    appCfg.Logging.Compress,
			CaptureEnabled: &captureEnabled,
		}

		p := proxy.New(cfg, playback.NewManager(map[string][]scrobbler.Scrobbler{}, logger), logger)

		return check(context.Background(), listen, upstream, p, logger)
	}

	store := playback.NewUserStore(rt.StatePath, logger)

	manager := playback.NewManager(nil, logger)
	manager.SetScrobbledHook(func(username, provider string) {
		if username == "" {
			return
		}

		store.RecordScrobble(username, provider)
	})

	cfg := proxy.Config{
		ListenSocket:   listen,
		UpstreamSocket: upstream,
		SocketMode:     socketModeFromConfig(appCfg),
		Store:          store,
		ProviderStatus: providerStatusOf(appCfg),
		LogDir:         logDir,
		LogMaxSize:     appCfg.Logging.MaxSize,
		LogMaxBackups:  appCfg.Logging.MaxBackups,
		LogMaxAge:      appCfg.Logging.MaxAge,
		LogCompress:    appCfg.Logging.Compress,
		CaptureEnabled: &captureEnabled,
	}

	// 创建三个客户端各自的请求日志记录器，格式与 capture.log 一致，仅文件名不同。
	// 复用主日志的轮转配置（大小/份数/天数/压缩）。
	feiniuReqLog := reqlog.New(logDir, "feiniu.log", appCfg.Logging.MaxSize, appCfg.Logging.MaxBackups, appCfg.Logging.MaxAge, appCfg.Logging.Compress, &captureEnabled)
	lfReqLog := reqlog.New(logDir, "lastfm.log", appCfg.Logging.MaxSize, appCfg.Logging.MaxBackups, appCfg.Logging.MaxAge, appCfg.Logging.Compress, &captureEnabled)
	lbReqLog := reqlog.New(logDir, "listenbrainz.log", appCfg.Logging.MaxSize, appCfg.Logging.MaxBackups, appCfg.Logging.MaxAge, appCfg.Logging.Compress, &captureEnabled)
	defer feiniuReqLog.Close()
	defer lfReqLog.Close()
	defer lbReqLog.Close()

	// 创建代理（内部包含 UserCache）
	p := proxy.New(cfg, manager, logger)

	// 创建歌单同步服务（使用代理的 UserCache 获取活跃用户）
	playlistSync := playlist.NewSyncService(appCfg, logger, p.UserCache(), feiniuReqLog, lfReqLog, lbReqLog)

	// applyConfig 统一处理：重建各用户推送平台、更新日志级别、打印用户列表。
	// 启动与配置热更新都走它，保证两处行为一致。
	applyConfig := func(c *config.Config) {
		appCfg = c

		manager.UpdateProviders(buildUserProviders(c, logger, lfReqLog, lbReqLog))
		manager.SetScrobbleThreshold(c.Playback.ScrobbleThreshold)
		levelVar.Set(parseLevel(c))
		captureEnabled.Store(debug || isDebugEnv())
		playlistSync.UpdateConfig(c)

		for name, u := range c.Users {
			logger.Info("用户配置",
				"用户", name,
				"Last.fm", u.LastFM.Enabled,
				"ListenBrainz", u.ListenBrainz.Enabled,
			)
		}
	}

	applyConfig(appCfg)

	// 监听配置文件变化，热更新（修改 users 凭证 / 日志级别无需重启）。
	if err := config.Watch(
		rt.ConfigPath,
		func(c *config.Config) {
			logger.Info("配置热更新")
			applyConfig(c)
		},
		func(e error) {
			logger.Warn("配置热更新失败，沿用旧配置", "错误", e)
		},
	); err != nil {
		logger.Warn("配置监听启动失败，将不会热更新", "错误", err)
	}

	// Last.fm 授权辅助：对"已启用且填了 api_key/api_secret 但缺 session_key"的用户，
	// 打印授权链接并后台轮询，用户点同意后自动写回配置（见 auth.go）。
	for name, u := range appCfg.Users {
		lfm := u.LastFM
		if lfm.Enabled && lfm.APIKey != "" && lfm.APISecret != "" && lfm.SessionKey == "" {
			go startLastFMAuth(name, lfm.APIKey, lfm.APISecret, rt.ConfigPath, logger, lfReqLog)
		}
	}

	// ListenBrainz 用户名自动补全：对"已启用且填了 token 但缺 username"的用户，
	// 调用 /1/validate-token 获取用户名并写回配置（歌单同步需要 username）。
	for name, u := range appCfg.Users {
		lb := u.ListenBrainz
		if lb.Enabled && lb.Token != "" && lb.Username == "" {
			go startListenBrainzUsernameLookup(name, lb.Token, rt.ConfigPath, logger, lbReqLog)
		}
	}

	// 信号处理放最前：等待重试 / 停机都能及时响应 SIGTERM。
	ctx, stop := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer stop()

	// 单实例锁：避免重复启动互相破坏 socket（见 socket.go 的接管逻辑）。
	// 已存在实例则直接拒绝；锁目录无权限（如本地非 root 调试）则降级告警放行。
	lock, occupied, lockErr := acquireRunLock()
	if occupied {
		return fmt.Errorf("已有实例在运行，拒绝重复启动（lock=%s）。请先停止现有实例：%s service stop",
			rt.RunLockPath, buildinfo.BinaryName)
	}
	if lockErr != nil {
		logger.Warn("无法获取运行锁，跳过单实例保护（可能权限不足）", "错误", lockErr)
	} else {
		defer lock.Close()
	}

	takeover := proxy.NewTakeover(cfg.ListenSocket, cfg.UpstreamSocket, logger)

	logger.Info("启动配置",
		"监听", cfg.ListenSocket,
		"上游", cfg.UpstreamSocket,
		"调试", debug,
		"配置", rt.ConfigPath,
		"状态文件", rt.StatePath,
		"日志目录", rt.LogDir,
		"进程号", os.Getpid(),
	)

	// 只要进入接管态，任何退出路径都必须把官方 socket 还回去，
	// 否则 trim-music 会一直留在 upstream 路径上，nginx 直接 502。
	defer takeover.Restore()

	// 退出时把用户统计（推送次数等）写回状态文件，下次启动可继续累加。
	defer store.Save()

	// 飞牛音乐可能尚未启动：持续等待其 socket 出现，进程保持存活，
	// 避免秒退触发 systemd Restart=always 的重启风暴（start-limit）。
	if err := waitForTakeover(ctx, takeover, logger); err != nil {
		return err
	}

	// 在原路径监听，权限沿用官方 socket（至少 0666，保证 www-data 可连）
	listener, err := takeover.Listen(cfg.SocketMode)
	if err != nil {
		return err
	}

	// 官方后端健康检查，未就绪就回滚，绝不留下 502 状态
	if err := p.WaitUpstream(ctx, wait); err != nil {
		return fmt.Errorf("上游不可用 (%s): %w", cfg.UpstreamSocket, err)
	}

	logger.Info("上游已就绪", "上游", cfg.UpstreamSocket)

	// 启动歌单同步服务
	playlistSync.Start()
	defer playlistSync.Stop()

	// fpk 模式：启动 Web UI（统一网关监听 app.sock）
	webUI, webUIErr := startWebUI(logger)
	if webUIErr != nil {
		logger.Warn("Web UI 启动失败（不影响代理功能）", "错误", webUIErr)
	} else if webUI != nil {
		defer webUI.Stop()
	}

	defer p.Close()

	// 启动代理
	return p.Start(ctx, listener)
}

// acquireRunLock 用 flock 保证全局只有一个代理实例在运行，避免重复启动互相破坏 socket。
// 调用方需保持返回的 *os.File 打开（defer Close 释放锁）；进程退出后锁自动释放。
//   - occupied=true：锁已被其它实例持有，应视为"已在运行"拒绝启动；
//   - err!=nil 且 occupied=false：通常是锁目录无权限（如本地非 root 调试），降级放行。
func acquireRunLock() (lock *os.File, occupied bool, err error) {
	dir := filepath.Dir(rt.RunLockPath)
	if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
		return nil, false, fmt.Errorf("创建锁目录失败 %s: %w", dir, mkErr)
	}

	f, openErr := os.OpenFile(rt.RunLockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if openErr != nil {
		return nil, false, fmt.Errorf("打开锁文件失败 %s: %w", rt.RunLockPath, openErr)
	}

	if flockErr := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); flockErr != nil {
		_ = f.Close()

		if errors.Is(flockErr, syscall.EWOULDBLOCK) {
			return nil, true, fmt.Errorf("锁已被其它实例持有 %s", rt.RunLockPath)
		}

		return nil, false, fmt.Errorf("加锁失败 %s: %w", rt.RunLockPath, flockErr)
	}

	// 记录当前 PID，便于排查（仅信息用途，真正互斥靠 flock）。
	_, _ = f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)

	return f, false, nil
}

// waitForTakeover 反复尝试 socket 接管，直到成功或被信号取消。
// 飞牛音乐可能晚于本服务启动，此时持续等待能让进程保持存活，
// 避免立即退出触发 systemd Restart=always 的重启风暴（start-limit-hit）。
func waitForTakeover(ctx context.Context, t *proxy.Takeover, logger *slog.Logger) error {
	const interval = 3 * time.Second

	for attempt := 1; ; attempt++ {
		if err := t.Prepare(); err == nil {
			return nil
		} else {
			logger.Warn("等待飞牛音乐 socket 可用，稍后重试",
				"尝试", attempt, "间隔", interval.String(), "错误", err)
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("等待飞牛音乐 socket 时被信号中断: %w", ctx.Err())
		case <-time.After(interval):
		}
	}
}
