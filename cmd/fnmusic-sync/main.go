package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
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
	defaultListenSocket   = "/var/run/trim_music.socket"
	defaultUpstreamSocket = "/var/run/trim_music_upstream.socket"
	defaultUpstreamWait   = 30 * time.Second
	defaultConfigPath     = "/etc/fnmusic-sync/config.yaml"
	defaultStatePath      = "/var/lib/fnmusic-sync/state.yaml"
	defaultRunLockPath    = "/run/fnmusic-sync/run.lock"
)

type options struct {
	check    bool
	debug    bool
	listen   string
	upstream string
	wait     time.Duration
	config   string
	state    string
	logDir   string
}

func main() {
	// 子命令：固定位置参数，不参与 flag 解析。
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "self-upgrade", "upgrade", "-u", "--self-upgrade":
			handleSelfUpgrade()

			return
		case "version", "version-info", "-v", "--version":
			printVersion()

			return
		case "service", "svc", "-s", "--service":
			handleService(os.Args[2:])

			return
		}
	}

	opts := options{
		wait: defaultUpstreamWait,
	}

	defineFlags(&opts)
	flag.Parse()

	// logger 必须先于 run 构造（run 内所有日志都用它），故此处预读一次配置：
	// 日志目录固定、文件名固定，配置里只有级别与轮转策略（见 logging.go）。
	logCfg := config.DefaultLogging()

	if pre, perr := config.Load(opts.config); perr == nil {
		logCfg = pre.Logging
	}

	logger, levelVar, closeLog := newLogger(&opts, logCfg)
	defer closeLog()

	if err := run(opts, logger, levelVar); err != nil {
		logger.Error("启动失败", "错误", err)

		closeLog()

		os.Exit(1)
	}
}

// defineFlags 注册命令行参数；--help 里一并列出子命令。
func defineFlags(opts *options) {
	flag.BoolVar(&opts.check, "check", false,
		"只检查 socket 与 upstream 状态，不启动代理")
	flag.BoolVar(&opts.debug, "debug", opts.debug,
		"打印每个请求/响应的详细信息")
	flag.StringVar(&opts.listen, "listen", opts.listen,
		"代理监听的 unix socket（覆盖配置文件 server.listen_socket）")
	flag.StringVar(&opts.upstream, "upstream", opts.upstream,
		"官方 trim-music 的 unix socket（覆盖配置文件 server.upstream_socket）")
	flag.DurationVar(&opts.wait, "upstream-wait", opts.wait,
		"启动时等待官方后端就绪的超时时间")
	flag.StringVar(&opts.config, "config",
		defaultConfigPath,
		"配置文件路径（yaml）")
	flag.StringVar(&opts.state, "state",
		defaultStatePath,
		"用户状态文件路径（程序维护，记录各用户推送统计）")
	flag.StringVar(&opts.logDir, "log-dir", opts.logDir,
		"日志保存目录（默认 "+defaultLogDir+"，off 表示只输出控制台，不写文件）")

	flag.Usage = func() {
		out := flag.CommandLine.Output()

		fmt.Fprintf(out, "用法：\n  %s [子命令] [参数]\n\n子命令：\n", buildinfo.BinaryName)
		fmt.Fprint(out, "  version (-v | --version)       打印版本、Git 提交、构建时间与平台信息\n")
		fmt.Fprint(out, "  self-upgrade (-u | upgrade)    升级自身二进制到仓库最新版本（写安装目录通常需要 sudo）\n")
		fmt.Fprint(out, "  service (-s | svc)             安装/卸载/启停系统服务（install|uninstall|start|stop|restart|status，需 root）\n\n参数：\n")

		flag.PrintDefaults()
	}
}

func run(opts options, logger *slog.Logger, levelVar *slog.LevelVar) error {
	// 启动先打一行版本信息，日志里就能直接看出跑的是哪个包、什么时候编的。
	logger.Info("版本信息",
		"版本", buildinfo.Version,
		"Git提交", buildinfo.GitCommit,
		"构建时间", buildinfo.BuildTime,
		"构建时间(本地)", localBuildTime(),
		"Go版本", runtime.Version(),
		"平台", runtime.GOOS+"/"+runtime.GOARCH,
	)

	// 首次启动若没有配置文件：自动生成一份带示例的默认配置，便于直接编辑；
	// 生成失败（如权限不足）则降级为纯代理模式并告警，不退出。
	if opts.config != "" {
		if _, statErr := os.Stat(opts.config); statErr != nil {
			if werr := config.SaveDefault(opts.config); werr != nil {
				logger.Warn("未找到配置文件且无法自动创建，使用内置默认配置（纯代理模式，不推送 scrobble）",
					"路径", opts.config, "错误", werr, "示例", "configs/config.example.yaml")
			} else {
				logger.Info("已生成默认配置文件，编辑后程序会自动热加载",
					"路径", opts.config, "示例", "configs/config.example.yaml")
			}
		}
	}

	appCfg, err := config.Load(opts.config)
	if err != nil {
		return err
	}

	// socket 路径优先级：命令行 --listen/--upstream > 配置文件 > 内置默认。
	listen := resolveSocket(opts.listen, appCfg.Server.ListenSocket, defaultListenSocket)
	upstream := resolveSocket(opts.upstream, appCfg.Server.UpstreamSocket, defaultUpstreamSocket)
	wait := opts.wait
	if d, perr := time.ParseDuration(appCfg.Server.UpstreamWait); perr == nil {
		wait = d
	}

	// 解析实际日志目录，供主日志与抓包日志共用：
	// 空值回退到默认目录（与 setupLogger 一致）；off/none/- 禁用文件日志，抓包也不写。
	logDir := opts.logDir
	if logDir == "" {
		logDir = defaultLogDir
	}
	if logDisabled(logDir) {
		logDir = ""
	}

	if opts.check {
		cfg := proxy.Config{
			ListenSocket:   listen,
			UpstreamSocket: upstream,
			SocketMode:     0,
			LogDir:         logDir,
			LogMaxSize:     appCfg.Logging.MaxSize,
			LogMaxBackups:  appCfg.Logging.MaxBackups,
			LogMaxAge:      appCfg.Logging.MaxAge,
			LogCompress:    appCfg.Logging.Compress,
		}

		p := proxy.New(cfg, playback.NewManager(map[string][]scrobbler.Scrobbler{}, logger), logger)

		return check(context.Background(), listen, upstream, p, logger)
	}

	store := playback.NewUserStore(opts.state, logger)

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
	}

	// 创建三个客户端各自的请求日志记录器，格式与 capture.log 一致，仅文件名不同。
	// 复用主日志的轮转配置（大小/份数/天数/压缩）。
	feiniuReqLog := reqlog.New(logDir, "feiniu.log", appCfg.Logging.MaxSize, appCfg.Logging.MaxBackups, appCfg.Logging.MaxAge, appCfg.Logging.Compress, levelVar)
	lfReqLog := reqlog.New(logDir, "lastfm.log", appCfg.Logging.MaxSize, appCfg.Logging.MaxBackups, appCfg.Logging.MaxAge, appCfg.Logging.Compress, levelVar)
	lbReqLog := reqlog.New(logDir, "listenbrainz.log", appCfg.Logging.MaxSize, appCfg.Logging.MaxBackups, appCfg.Logging.MaxAge, appCfg.Logging.Compress, levelVar)
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
		levelVar.Set(parseLevel(c, opts.debug))
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
	if opts.config != "" {
		if err := config.Watch(
			opts.config,
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
	}

	// Last.fm 授权辅助：对"已启用且填了 api_key/api_secret 但缺 session_key"的用户，
	// 打印授权链接并后台轮询，用户点同意后自动写回配置（见 auth.go）。
	for name, u := range appCfg.Users {
		lfm := u.LastFM
		if lfm.Enabled && lfm.APIKey != "" && lfm.APISecret != "" && lfm.SessionKey == "" {
			go startLastFMAuth(name, lfm.APIKey, lfm.APISecret, opts.config, logger, lfReqLog)
		}
	}

	// ListenBrainz 用户名自动补全：对"已启用且填了 token 但缺 username"的用户，
	// 调用 /1/validate-token 获取用户名并写回配置（歌单同步需要 username）。
	for name, u := range appCfg.Users {
		lb := u.ListenBrainz
		if lb.Enabled && lb.Token != "" && lb.Username == "" {
			go startListenBrainzUsernameLookup(name, lb.Token, opts.config, logger, lbReqLog)
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
			defaultRunLockPath, buildinfo.BinaryName)
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
		"调试", opts.debug,
		"配置", opts.config,
		"状态文件", opts.state,
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

	defer p.Close()

	// 启动代理
	return p.Start(ctx, listener)
}

// acquireRunLock 用 flock 保证全局只有一个代理实例在运行，避免重复启动互相破坏 socket。
// 调用方需保持返回的 *os.File 打开（defer Close 释放锁）；进程退出后锁自动释放。
//   - occupied=true：锁已被其它实例持有，应视为"已在运行"拒绝启动；
//   - err!=nil 且 occupied=false：通常是锁目录无权限（如本地非 root 调试），降级放行。
func acquireRunLock() (lock *os.File, occupied bool, err error) {
	dir := filepath.Dir(defaultRunLockPath)
	if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
		return nil, false, fmt.Errorf("创建锁目录失败 %s: %w", dir, mkErr)
	}

	f, openErr := os.OpenFile(defaultRunLockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if openErr != nil {
		return nil, false, fmt.Errorf("打开锁文件失败 %s: %w", defaultRunLockPath, openErr)
	}

	if flockErr := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); flockErr != nil {
		_ = f.Close()

		if errors.Is(flockErr, syscall.EWOULDBLOCK) {
			return nil, true, fmt.Errorf("锁已被其它实例持有 %s", defaultRunLockPath)
		}

		return nil, false, fmt.Errorf("加锁失败 %s: %w", defaultRunLockPath, flockErr)
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
