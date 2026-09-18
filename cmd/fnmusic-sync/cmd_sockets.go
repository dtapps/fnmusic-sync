package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"cnb.cool/dtapp/fnmusic-sync/internal/proxy"
)

// socketsCmd 打印两个固定 socket（监听 / 上游）的详细信息，
// 并给出"是否正常"的简单判断，便于快速排查连接问题。
var socketsCmd = &cobra.Command{
	Use:   "sockets",
	Short: "查看两个 socket 的状态并判断是否正常",
	RunE: func(cmd *cobra.Command, args []string) error {
		ov := proxy.ProbeSockets()

		printSocket("监听", ov.Listen)
		printSocket("上游", ov.Upstream)

		fmt.Println()
		if ov.AllHealthy {
			fmt.Println("结论：✅ 正常（监听可达、飞牛音乐在线）")
		} else {
			fmt.Printf("结论：❌ 异常 —— %s\n", ov.Message)
		}

		return nil
	},
}

func printSocket(label string, s proxy.SocketStatus) {
	status := "✅ 正常"
	if !s.Healthy {
		status = "❌ 异常"
	}

	fmt.Printf("\n%s socket (%s)  %s\n", label, s.Path, status)
	fmt.Printf("  存在       %v\n", s.Exists)
	fmt.Printf("  类型       %s\n", socketType(s))
	fmt.Printf("  权限       %s\n", s.Perm)
	fmt.Printf("  属主       %d:%d\n", s.UID, s.GID)
	fmt.Printf("  归属       %s\n", kindLabel(s.Kind))
	fmt.Printf("  可连接     %v\n", s.Connectable)
	if s.Detail != "" {
		fmt.Printf("  说明       %s\n", s.Detail)
	}
}

func socketType(s proxy.SocketStatus) string {
	if !s.Exists {
		return "不存在"
	}
	if !s.IsSocket {
		return "非 socket 文件"
	}

	return "unix socket"
}

func kindLabel(kind string) string {
	switch kind {
	case string(proxy.SocketTrim):
		return "飞牛音乐(官方)"
	case string(proxy.SocketProxy):
		return "本代理"
	case string(proxy.SocketStale):
		return "未知进程(可连接但无法识别)"
	case string(proxy.SocketNone):
		return "无监听"
	default:
		return kind
	}
}

func init() {
	rootCmd.AddCommand(socketsCmd)
}
