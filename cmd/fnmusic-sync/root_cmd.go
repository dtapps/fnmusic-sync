package main

import (
	"time"

	"cnb.cool/dtapp/fnmusic-sync/internal/buildinfo"
	"github.com/spf13/cobra"
)

// rootCmd 是程序入口的根命令。
// 不带子命令直接运行时进入代理模式（run 函数）；
// 子命令（version、self-upgrade、service）在各自文件中通过 init 注册，
// fpk 构建中 self-upgrade 和 service 不编译，自然不会出现。
var rootCmd = &cobra.Command{
	Use:   buildinfo.BinaryName,
	Short: "fnOS 飞牛音乐 Last.fm / ListenBrainz scrobble 代理",
	Long:  "fnOS 飞牛音乐 Last.fm / ListenBrainz scrobble 代理\n\n直接运行（不带子命令）启动代理服务。",
	// SilenceErrors / SilenceUsage：错误由 main 统一处理，
	// 避免 cobra 在非零退出时多打一行 Usage。
	SilenceErrors: true,
	SilenceUsage:  true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runProxy()
	},
}

// proxyFlags 是代理模式需要的命令行参数。
var proxyFlags struct {
	check bool
	debug bool
	wait  durationFlag
}

// init 注册根命令的 flag。子命令在各自文件的 init() 中 AddCommand。
func init() {
	proxyFlags.wait = durationFlag{defaultUpstreamWait}

	rootCmd.PersistentFlags().BoolVar(&proxyFlags.check, "check", false,
		"只检查 socket 与 upstream 状态，不启动代理")
	rootCmd.PersistentFlags().BoolVar(&proxyFlags.debug, "debug", false,
		"开启请求/响应详细日志（等同于 logging.level: debug）")
	rootCmd.PersistentFlags().Var(&proxyFlags.wait, "upstream-wait",
		"启动时等待官方后端就绪的超时时间")
}

// durationFlag 包装 time.Duration 以实现 pflag.Value 接口。
type durationFlag struct {
	time.Duration
}

func (d *durationFlag) Set(s string) error {
	v, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	d.Duration = v
	return nil
}

func (d *durationFlag) Type() string { return "duration" }
