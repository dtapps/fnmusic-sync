//go:build !fpk

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"cnb.cool/dtapp/fnmusic-sync/internal/buildinfo"
	"github.com/kardianos/service"
	"github.com/spf13/cobra"
)

// serviceProgram 满足 service.Interface。
// 本程序由 systemd 直接前台运行（Type=simple），不需要 service.Run 托管，
// 这里只提供空实现供 service.New 使用，服务启停全部走 systemctl。
type serviceProgram struct{}

func (p *serviceProgram) Start(s service.Service) error { return nil }
func (p *serviceProgram) Stop(s service.Service) error  { return nil }

// newService 构造服务描述：名称、说明。
// 配置/状态/日志路径由运行模式自动决定，不需要传命令行参数。
// systemd 参数通过 Option 配置，无需自定义模板。
func newService() (service.Service, error) {
	cfg := &service.Config{
		Name:         buildinfo.BinaryName,
		DisplayName:  buildinfo.BinaryName,
		Description:  "fnOS 飞牛音乐 Last.fm / ListenBrainz scrobble 代理",
		Dependencies: []string{"After=network.target"},
		Option: service.KeyValue{
			"Restart":    "always",
			"RestartSec": "5",
			"KillSignal": "SIGTERM",
		},
	}

	return service.New(&serviceProgram{}, cfg)
}

// serviceCmd 是 `fnmusic-sync service` 子命令，管理 systemd 服务。
// 安装/卸载/启停需要 root（写 /etc/systemd/system 与 systemctl）。
var serviceCmd = &cobra.Command{
	Use:   "service",
	Short: "安装/卸载/启停系统服务（systemd，需 root）",
}

var serviceInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "安装系统服务（systemd，需 root）",
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := newService()
		if err != nil {
			return fmt.Errorf("创建服务失败: %w", err)
		}
		if err := s.Install(); err != nil {
			return fmt.Errorf("安装失败: %w", err)
		}
		fmt.Printf("✅ 服务已安装：%s\n", s.Platform())
		fmt.Println("   启动并设为开机自启：systemctl enable --now " + buildinfo.BinaryName)
		return nil
	},
}

var serviceUninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "卸载系统服务（需 root）",
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := newService()
		if err != nil {
			return fmt.Errorf("创建服务失败: %w", err)
		}
		if err := s.Uninstall(); err != nil {
			return fmt.Errorf("卸载失败: %w", err)
		}
		fmt.Println("✅ 服务已卸载")
		return nil
	},
}

var serviceStartCmd = &cobra.Command{
	Use:   "start",
	Short: "启动服务",
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := newService()
		if err != nil {
			return fmt.Errorf("创建服务失败: %w", err)
		}
		if err := s.Start(); err != nil {
			return fmt.Errorf("启动失败: %w", err)
		}
		fmt.Println("✅ 服务已启动")
		return nil
	},
}

var serviceStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "停止服务",
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := newService()
		if err != nil {
			return fmt.Errorf("创建服务失败: %w", err)
		}
		if err := s.Stop(); err != nil {
			return fmt.Errorf("停止失败: %w", err)
		}
		fmt.Println("✅ 服务已停止")
		return nil
	},
}

var serviceRestartCmd = &cobra.Command{
	Use:   "restart",
	Short: "重启服务",
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := newService()
		if err != nil {
			return fmt.Errorf("创建服务失败: %w", err)
		}
		if err := s.Restart(); err != nil {
			return fmt.Errorf("重启失败: %w", err)
		}
		fmt.Println("✅ 服务已重启")
		return nil
	},
}

var serviceStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "查看运行状态",
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := newService()
		if err != nil {
			return fmt.Errorf("创建服务失败: %w", err)
		}
		st, err := s.Status()
		if err != nil {
			return fmt.Errorf("查询状态失败: %w", err)
		}
		switch st {
		case service.StatusRunning:
			fmt.Println("运行中")
		case service.StatusStopped:
			fmt.Println("已停止")
		default:
			fmt.Println("未知状态")
		}
		return nil
	},
}

var serviceDebugCmd = &cobra.Command{
	Use:   "debug",
	Short: "开启 debug 模式并重启服务（等同 --debug）",
	RunE: func(cmd *cobra.Command, args []string) error {
		return toggleDebug(true)
	},
}

var serviceNoDebugCmd = &cobra.Command{
	Use:   "nodebug",
	Short: "关闭 debug 模式并重启服务",
	RunE: func(cmd *cobra.Command, args []string) error {
		return toggleDebug(false)
	},
}

// unitFilePath 返回 systemd unit 文件的绝对路径。
func unitFilePath() string {
	return filepath.Join("/etc/systemd/system", buildinfo.BinaryName+".service")
}

// toggleDebug 修改 systemd unit 文件中 ExecStart 行，加上或去掉 --debug 参数，
// 然后 daemon-reload + restart 使改动生效。
// 不依赖 kardianos/service 库，直接做文件级操作，逻辑简单可靠。
func toggleDebug(enable bool) error {
	path := unitFilePath()

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("读取 unit 文件失败 %s: %w（请先执行 %s service install）", path, err, buildinfo.BinaryName)
	}

	lines := strings.Split(string(data), "\n")
	found := false
	for i, line := range lines {
		if !strings.HasPrefix(line, "ExecStart=") {
			continue
		}
		found = true
		if enable {
			if strings.Contains(line, " --debug") {
				fmt.Println("debug 模式已开启，无需重复设置")
				return nil
			}
			lines[i] = line + " --debug"
		} else {
			lines[i] = strings.ReplaceAll(line, " --debug", "")
			if !strings.Contains(line, " --debug") {
				fmt.Println("debug 模式未开启，无需关闭")
				return nil
			}
		}
		break
	}

	if !found {
		return fmt.Errorf("unit 文件中未找到 ExecStart 行: %s", path)
	}

	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0644); err != nil {
		return fmt.Errorf("写入 unit 文件失败: %w", err)
	}

	// daemon-reload + restart
	if err := exec.Command("systemctl", "daemon-reload").Run(); err != nil {
		return fmt.Errorf("daemon-reload 失败: %w", err)
	}
	if err := exec.Command("systemctl", "restart", buildinfo.BinaryName).Run(); err != nil {
		return fmt.Errorf("重启服务失败: %w", err)
	}

	if enable {
		fmt.Println("✅ debug 模式已开启，服务已重启")
		fmt.Println("   查看详细日志：journalctl -u " + buildinfo.BinaryName + " -f")
	} else {
		fmt.Println("✅ debug 模式已关闭，服务已重启")
	}
	return nil
}

func init() {
	serviceCmd.AddCommand(
		serviceInstallCmd,
		serviceUninstallCmd,
		serviceStartCmd,
		serviceStopCmd,
		serviceRestartCmd,
		serviceStatusCmd,
		serviceDebugCmd,
		serviceNoDebugCmd,
	)
	rootCmd.AddCommand(serviceCmd)
}
