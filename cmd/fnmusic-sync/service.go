package main

import (
	"fmt"
	"os"

	"github.com/kardianos/service"
)

// systemdScript 自定义 unit 模板（覆盖库默认模板）：
//   - Restart=always + RestartSec=5（库默认模板把 RestartSec 写死成 120）；
//   - 不配置 StandardOutput/StandardError 重定向，日志由程序自己写
//     /var/log/fnmusic-sync/fnmusic-sync.log（同时输出控制台）。
const systemdScript = `[Unit]
Description={{Description}}
ConditionFileIsExecutable={{Path | cmdEscape}}
{{range Dependencies}}{{.}}
{{end}}
[Service]
Type=simple
ExecStart={{Path | cmdEscape}}{{range Arguments}} {{. | cmd}}{{end}}
{{if WorkingDirectory}}WorkingDirectory={{WorkingDirectory | cmdEscape}}
{{end}}{{if UserName}}User={{UserName}}
{{end}}{{if Restart}}Restart={{Restart}}
{{end}}RestartSec=5
KillSignal=SIGTERM
{{range EnvVars}}{{.}}
{{end}}
[Install]
WantedBy=multi-user.target
`

// serviceProgram 满足 service.Interface。
// 本程序由 systemd 直接前台运行（Type=simple），不需要 service.Run 托管，
// 这里只提供空实现供 service.New 使用，服务启停全部走 systemctl。
type serviceProgram struct{}

func (p *serviceProgram) Start(s service.Service) error { return nil }

func (p *serviceProgram) Stop(s service.Service) error { return nil }

// newService 构造服务描述：名称、说明、启动参数（配置与状态文件路径）。
func newService() (service.Service, error) {
	cfg := &service.Config{
		Name:        BinaryName,
		DisplayName: BinaryName,
		Description: "fnOS 飞牛音乐 Last.fm / ListenBrainz scrobble 代理",
		Arguments: []string{
			"--config", defaultConfigPath,
			"--state", defaultStatePath,
		},
		Dependencies: []string{"After=network.target"},
		Option: service.KeyValue{
			"SystemdScript": systemdScript,
			"Restart":       "always",
		},
	}

	return service.New(&serviceProgram{}, cfg)
}

// handleService 处理 `fnmusic-sync service <动作>` 子命令。
// 安装/卸载/启停需要 root（写 /etc/systemd/system 与 systemctl）。
func handleService(args []string) {
	if len(args) == 0 {
		printServiceUsage()

		os.Exit(1)
	}

	s, err := newService()
	if err != nil {
		fmt.Printf("❌ 创建服务失败: %v\n", err)

		os.Exit(1)
	}

	switch args[0] {
	case "install":
		err = s.Install()
		if err == nil {
			fmt.Printf("✅ 服务已安装：%s\n", s.Platform())
			fmt.Println("   启动并设为开机自启：systemctl enable --now " + BinaryName)
		}
	case "uninstall", "remove":
		err = s.Uninstall()
		if err == nil {
			fmt.Println("✅ 服务已卸载")
		}
	case "start":
		err = s.Start()
		if err == nil {
			fmt.Println("✅ 服务已启动")
		}
	case "stop":
		err = s.Stop()
		if err == nil {
			fmt.Println("✅ 服务已停止")
		}
	case "restart":
		err = s.Restart()
		if err == nil {
			fmt.Println("✅ 服务已重启")
		}
	case "status":
		st, serr := s.Status()
		if serr != nil {
			fmt.Printf("❌ 查询状态失败: %v\n", serr)

			os.Exit(1)
		}

		switch st {
		case service.StatusRunning:
			fmt.Println("运行中")
		case service.StatusStopped:
			fmt.Println("已停止")
		default:
			fmt.Println("未知状态")
		}

		return
	default:
		printServiceUsage()

		os.Exit(1)
	}

	if err != nil {
		fmt.Printf("❌ %s 失败: %v\n", args[0], err)

		os.Exit(1)
	}
}

func printServiceUsage() {
	fmt.Printf("用法：%s service <动作>\n\n", BinaryName)
	fmt.Println("  install     安装系统服务（systemd，需 root）")
	fmt.Println("  uninstall   卸载系统服务（需 root）")
	fmt.Println("  start       启动服务")
	fmt.Println("  stop        停止服务")
	fmt.Println("  restart     重启服务")
	fmt.Println("  status      查看运行状态")
}
