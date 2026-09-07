package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"cnb.cool/dtapp/fnmusic-sync/internal/config"
	"cnb.cool/dtapp/fnmusic-sync/internal/playback"
	"cnb.cool/dtapp/fnmusic-sync/internal/proxy"
	"cnb.cool/dtapp/fnmusic-sync/internal/scrobbler"
)

const (
	defaultListenSocket   = "/var/run/trim_music.socket"
	defaultUpstreamSocket = "/var/run/trim_music_upstream.socket"
	defaultUpstreamWait   = 30 * time.Second
	defaultConfigPath     = "/etc/fnmusic-sync/config.yaml"
	defaultStatePath      = "/var/lib/fnmusic-sync/state.yaml"
)

// 版本/构建信息：由 Makefile 通过 -ldflags 注入；
// 默认值用于本地直接 `go run` / `go build`（此时 self-upgrade 会判定为 dev）。
var (
	Version    = "dev"
	GitCommit  = "unknown"
	BuildTime  = "unknown"
	BinaryName = "fnmusic-sync"
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
	// self-upgrade 由安装脚本/用户手动触发，用于升级自身二进制。
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "self-upgrade":
			handleSelfUpgrade()

			return
		case "version", "version-info", "-v", "--version":
			printVersion()

			return
		case "service":
			handleService(os.Args[2:])

			return
		}
	}

	opts := options{
		wait: defaultUpstreamWait,
	}

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

		fmt.Fprintf(out, "用法：\n  %s [子命令] [参数]\n\n子命令：\n", BinaryName)
		fmt.Fprintf(out, "  version         打印版本、Git 提交、构建时间与平台信息\n")
		fmt.Fprintf(out, "  self-upgrade    升级自身二进制到仓库最新版本（写安装目录通常需要 sudo）\n")
		fmt.Fprintf(out, "  service         安装/卸载/启停系统服务（install|uninstall|start|stop|restart|status，需 root）\n\n参数：\n")

		flag.PrintDefaults()
	}

	flag.Parse()

	// 日志目录固定为 /var/log/fnmusic-sync（仅 --log-dir 可临时覆盖），
	// 文件名固定 fnmusic-sync.log；配置里只放级别与轮转策略。
	// logger 必须先于 run 构造（run 内所有日志都用它），故此处预读一次配置；
	// 读失败就用默认值，等 run 里正式加载时统一报错。
	logCfg := config.DefaultLogging()

	if pre, perr := config.Load(opts.config); perr == nil {
		logCfg = pre.Logging
	}

	// 单一 logger：同时输出到控制台(stderr)与日志文件，级别由 levelVar 控制（支持运行时热更新）。
	levelVar := &slog.LevelVar{}
	if opts.debug {
		levelVar.Set(slog.LevelDebug)
	} else {
		levelVar.Set(slog.LevelInfo)
	}

	logger, _, closeLog := setupLogger(opts.logDir, logCfg, levelVar)
	defer closeLog()

	if err := run(opts, logger, levelVar); err != nil {
		logger.Error("启动失败", "错误", err)

		closeLog()

		os.Exit(1)
	}
}

// printVersion 打印版本与构建信息（version 子命令）。
// 版本/提交/构建时间由 Makefile 通过 -ldflags 注入，本地直接 go build 时为 dev。
func printVersion() {
	fmt.Printf("%s %s\n", BinaryName, Version)
	fmt.Printf("  版本      %s\n", Version)
	fmt.Printf("  Git提交   %s\n", GitCommit)
	fmt.Printf("  构建时间  %s\n", BuildTime)
	fmt.Printf("  Go版本    %s\n", runtime.Version())
	fmt.Printf("  平台      %s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Printf("  配置      %s\n", defaultConfigPath)
	fmt.Printf("  状态      %s\n", defaultStatePath)
	fmt.Printf("  日志      %s\n", filepath.Join(defaultLogDir, defaultLogFile))

	if Version == "dev" {
		fmt.Println("  说明      本地构建（未注入发布版本号）")
	}
}

func run(opts options, logger *slog.Logger, levelVar *slog.LevelVar) error {
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

	// 日志级别由 main 传入的 levelVar 控制；applyConfig 在启动时及配置热更新时
	// 调用 levelVar.Set(parseLevel(...)) 细化级别（命令行 --debug 优先，其次 logging.level）。

	// socket 路径优先级：命令行 --listen/--upstream > 配置文件 > 内置默认。
	listen := resolveSocket(opts.listen, appCfg.Server.ListenSocket, defaultListenSocket)
	upstream := resolveSocket(opts.upstream, appCfg.Server.UpstreamSocket, defaultUpstreamSocket)
	wait := opts.wait
	if d, perr := time.ParseDuration(appCfg.Server.UpstreamWait); perr == nil {
		wait = d
	}

	if opts.check {
		cfg := proxy.Config{
			ListenSocket:   listen,
			UpstreamSocket: upstream,
			SocketMode:     0,
		}

		p := proxy.New(cfg, playback.NewManager(map[string][]scrobbler.Scrobbler{}, logger), logger)

		return check(context.Background(), opts, p, logger)
	}

	store := playback.NewUserStore(opts.state, logger)

	// 每个用户各自的 Last.fm / ListenBrainz 凭证 → 各自的推送平台实例。
	providerStatus := func(username string) (bool, bool) {
		u, ok := appCfg.Users[username]
		if !ok {
			return false, false
		}

		return u.LastFM.Enabled &&
				u.LastFM.APIKey != "" &&
				u.LastFM.APISecret != "" &&
				u.LastFM.SessionKey != "",
			u.ListenBrainz.Enabled && u.ListenBrainz.Token != ""
	}

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
		ProviderStatus: providerStatus,
	}

	// applyConfig 统一处理：重建各用户推送平台、更新日志级别、打印用户列表。
	// 启动与配置热更新都走它，保证两处行为一致。
	applyConfig := func(c *config.Config) {
		appCfg = c

		manager.UpdateProviders(buildUserProviders(c, logger))
		manager.SetScrobbleThreshold(c.Playback.ScrobbleThreshold)
		levelVar.Set(parseLevel(c, opts.debug))

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

	// Last.fm 授权辅助：对"已启用且填了 api_key/api_secret 但缺 session_key"的
	// 用户，打印授权链接到日志并后台异步轮询；用户在浏览器点同意后，程序自动把
	// session_key 写回配置文件（config.Watch 会热更新使其立即生效）。
	for name, u := range appCfg.Users {
		lfm := u.LastFM
		if lfm.Enabled && lfm.APIKey != "" && lfm.APISecret != "" && lfm.SessionKey == "" {
			go startLastFMAuth(name, lfm.APIKey, lfm.APISecret, opts.config, logger)
		}
	}

	takeover := proxy.NewTakeover(
		cfg.ListenSocket,
		cfg.UpstreamSocket,
		logger,
	)

	p := proxy.New(cfg, manager, logger)

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

	// 接管：把官方 trim-music 的 socket 改名到 upstream 路径，腾出原路径。
	// 必须在 Listen 之前执行，否则原路径仍是官方后端，Listen 会拒绝绑定。
	if err := takeover.Prepare(); err != nil {
		return err
	}

	// 在原路径监听，权限沿用官方 socket（至少 0666，保证 www-data 可连）
	listener, err := takeover.Listen(cfg.SocketMode)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer stop()

	// 官方后端健康检查，未就绪就回滚，绝不留下 502 状态
	if err := p.WaitUpstream(ctx, wait); err != nil {
		return fmt.Errorf("上游不可用 (%s): %w", cfg.UpstreamSocket, err)
	}

	logger.Info(
		"上游已就绪",
		"上游", cfg.UpstreamSocket,
	)

	// 启动代理
	return p.Start(ctx, listener)
}

// check 只做体检：socket 归属、权限、upstream 连通性。
func check(
	ctx context.Context,
	opts options,
	p *proxy.Proxy,
	logger *slog.Logger,
) error {
	listenKind := proxy.ProbeSocket(opts.listen)
	upstreamKind := proxy.ProbeSocket(opts.upstream)

	fmt.Printf("监听   %-45s 归属=%s\n", opts.listen, listenKind)
	fmt.Printf("上游   %-45s 归属=%s\n", opts.upstream, upstreamKind)

	for _, path := range []string{opts.listen, opts.upstream} {
		mode, uid, gid, ok := proxy.SocketMode(path)
		if !ok {
			fmt.Printf("权限   %-45s (不存在)\n", path)

			continue
		}

		fmt.Printf("权限   %-45s %#o 属主=%s:%s\n",
			path, mode&os.ModePerm, num(uid), num(gid))

		if mode&0o007 == 0 {
			fmt.Printf("警告   %-45s 权限不允许 other 访问，"+
				"nginx(www-data) 将无法连接，会表现为 502\n", path)
		}
	}

	// 体检时官方后端可能在 listen 路径（未接管）也可能在 upstream（已接管），
	// 探测它实际所在的那个 socket。
	trimPath := opts.upstream

	switch {
	case upstreamKind == proxy.SocketTrim:
		trimPath = opts.upstream
	case listenKind == proxy.SocketTrim:
		trimPath = opts.listen
		fmt.Println("提示：尚未接管，飞牛音乐当前为官方直连")
	}

	if err := p.CheckSocket(ctx, trimPath); err != nil {
		fmt.Printf("官方后端连通性(%s)：失败 (%v)\n", trimPath, err)

		return fmt.Errorf("上游检查失败")
	}

	fmt.Printf("官方后端连通性(%s)：正常\n", trimPath)

	if upstreamKind != proxy.SocketTrim && listenKind != proxy.SocketTrim {
		fmt.Println("警告：两个 socket 上都没有探测到官方 trim-music")
	}

	return nil
}

// startLastFMAuth 对单个"缺 session_key"的 Last.fm 用户执行授权辅助流程：
// 申请临时 token → 打印授权链接到日志 → 异步轮询 60s，用户点同意后把
// session_key 自动写回配置文件（config.Watch 热更新会立即使其生效）。
func startLastFMAuth(
	username, apiKey, apiSecret, configPath string,
	logger *slog.Logger,
) {
	client := scrobbler.NewLastFM(apiKey, apiSecret, "")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	token, err := client.RequestToken(ctx)
	if err != nil {
		logger.Warn("Last.fm 申请授权 token 失败", "用户", username, "错误", err)
		return
	}

	logger.Info("Last.fm 需要授权，请在浏览器打开以下链接并点击同意",
		"用户", username, "授权链接", client.AuthURL(token))

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Warn("Last.fm 授权轮询超时（60秒），重启程序可重试",
				"用户", username, "授权链接", client.AuthURL(token))
			return
		case <-ticker.C:
			sk, name, err := client.Session(ctx, token)
			if err != nil {
				logger.Warn("Last.fm 换取 session_key 失败", "用户", username, "错误", err)
				continue
			}
			if sk == "" {
				// 用户尚未在浏览器点同意，继续等待。
				continue
			}
			if err := config.SetLastFMSessionKey(configPath, username, sk); err != nil {
				logger.Warn("Last.fm session_key 写入配置失败", "用户", username, "错误", err)
				return
			}
			logger.Info("Last.fm 授权成功，session_key 已写入配置并自动生效",
				"用户", username, "Last.fm账号", name)
			return
		}
	}
}

// buildUserProviders 按配置文件中的 users 段，为每个用户创建各自的推送平台实例。
// 多用户隔离：不同用户用各自的 Last.fm / ListenBrainz 凭证，互不干扰。
func buildUserProviders(
	cfg *config.Config,
	logger *slog.Logger,
) map[string][]scrobbler.Scrobbler {
	result := map[string][]scrobbler.Scrobbler{}

	for name, u := range cfg.Users {
		var providers []scrobbler.Scrobbler

		lfm := u.LastFM
		if lfm.Enabled && lfm.APIKey != "" && lfm.APISecret != "" && lfm.SessionKey != "" {
			providers = append(
				providers,
				scrobbler.NewLastFM(lfm.APIKey, lfm.APISecret, lfm.SessionKey),
			)
		} else if lfm.Enabled {
			logger.Warn("用户 Last.fm 已启用但配置不完整，已跳过",
				"用户", name,
				"已设api_key", lfm.APIKey != "",
				"已设api_secret", lfm.APISecret != "",
				"已设session_key", lfm.SessionKey != "")
		}

		lb := u.ListenBrainz
		if lb.Enabled && lb.Token != "" {
			providers = append(
				providers,
				scrobbler.NewListenBrainz(lb.Token, lb.Username),
			)
		}

		if len(providers) > 0 {
			result[name] = providers
		}
	}

	return result
}

// resolveSocket 解析 socket 路径：命令行优先，其次配置文件，最后内置默认。
func resolveSocket(flagVal, cfgVal, fallback string) string {
	if flagVal != "" {
		return flagVal
	}
	if cfgVal != "" {
		return cfgVal
	}
	return fallback
}

// parseLevel 计算日志级别：命令行 --debug 优先，其次配置文件 logging.level。
func parseLevel(cfg *config.Config, debug bool) slog.Level {
	if debug {
		return slog.LevelDebug
	}

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

// socketModeFromConfig 从配置文件的 server.socket_mode 取 socket 权限。
// 为 0 时由 takeover 的 effectiveMode 自动推断（继承官方 socket 权限）。
func socketModeFromConfig(cfg *config.Config) os.FileMode {
	return os.FileMode(cfg.Server.SocketMode) & os.ModePerm
}

func num(value int) string {
	if value < 0 {
		return "-"
	}

	return strconv.Itoa(value)
}
