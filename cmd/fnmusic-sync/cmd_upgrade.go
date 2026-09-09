//go:build !fpk

package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"cnb.cool/dtapp/fnmusic-sync/internal/buildinfo"
	"cnb.cool/dtapp/fnmusic-sync/internal/updater"
	"github.com/spf13/cobra"
)

// pkgManager 表示检测到的系统包管理器。
type pkgManager struct {
	name string // dpkg / rpm / apk
	arch string // 包管理器对应的架构名（amd64→amd64/x86_64, arm64→arm64/aarch64）
	ext  string // .deb / .rpm / .apk
}

// detectPackageManager 检测当前系统的包管理器，返回 nil 表示不支持。
// 优先级：dpkg > rpm > apk（三者互斥，同时存在的极端情况按常见发行版习惯处理）。
func detectPackageManager() *pkgManager {
	arch := runtime.GOARCH

	// dpkg（Debian/Ubuntu）
	if _, err := exec.LookPath("dpkg"); err == nil {
		pa := "amd64"
		if arch == "arm64" {
			pa = "arm64"
		}
		return &pkgManager{name: "dpkg", arch: pa, ext: ".deb"}
	}

	// rpm（Fedora/RHEL/openSUSE）
	if _, err := exec.LookPath("rpm"); err == nil {
		pa := "x86_64"
		if arch == "arm64" {
			pa = "aarch64"
		}
		return &pkgManager{name: "rpm", arch: pa, ext: ".rpm"}
	}

	// apk（Alpine）
	if _, err := exec.LookPath("apk"); err == nil {
		pa := "x86_64"
		if arch == "arm64" {
			pa = "aarch64"
		}
		return &pkgManager{name: "apk", arch: pa, ext: ".apk"}
	}

	return nil
}

// packageFileName 根据包管理器类型和架构拼接安装包文件名。
// deb: fnmusic-sync_{arch}.deb
// rpm: fnmusic-sync.{arch}.rpm
// apk: fnmusic-sync.{arch}.apk
func (pm *pkgManager) packageFileName() string {
	switch pm.name {
	case "dpkg":
		return fmt.Sprintf("%s_%s%s", buildinfo.BinaryName, pm.arch, pm.ext)
	default: // rpm / apk
		return fmt.Sprintf("%s.%s%s", buildinfo.BinaryName, pm.arch, pm.ext)
	}
}

// installCommand 返回安装指定包文件所需的命令和参数。
func (pm *pkgManager) installCommand(pkgPath string) (string, []string) {
	switch pm.name {
	case "dpkg":
		return "dpkg", []string{"-i", pkgPath}
	case "rpm":
		return "rpm", []string{"-Uvh", "--force", pkgPath}
	case "apk":
		return "apk", []string{"add", "--allow-untrusted", "--force-overwrite", pkgPath}
	}
	return "", nil
}

// upgradeCmd 是 `fnmusic-sync self-upgrade` 子命令。
// 下载对应平台的系统安装包（deb/rpm/apk）并调用包管理器安装（需 sudo）。
var upgradeCmd = &cobra.Command{
	Use:   "self-upgrade",
	Short: "下载并安装最新版本系统安装包（deb/rpm/apk，需 sudo）",
	RunE: func(cmd *cobra.Command, args []string) error {
		return doSelfUpgrade()
	},
}

func init() {
	rootCmd.AddCommand(upgradeCmd)
}

// doSelfUpgrade 检查并升级当前二进制到最新 release。
// 根据构建来源（CNB / GitHub）自动选择对应的下载源。
func doSelfUpgrade() error {
	arch := runtime.GOARCH
	osName := runtime.GOOS

	fmt.Printf("🔄 检查 %s 自身更新 (当前版本: %s, 平台: %s/%s, 来源: %s)...\n", buildinfo.BinaryName, buildinfo.Version, osName, arch, buildinfo.RepoSource)

	// 检测系统包管理器
	pm := detectPackageManager()
	if pm == nil {
		fmt.Println("❌ 未检测到支持的包管理器（dpkg/rpm/apk），无法自动升级。")
		fmt.Println("   请手动下载安装包进行升级。")
		return nil
	}

	client := updater.DefaultHTTPClient()

	// 1. 获取最新版本号
	latestTag, err := updater.FetchLatestTag(client)
	if err != nil {
		fmt.Printf("❌ %s\n", err.Error())
		return nil
	}

	// 2. 版本比对（dev 视为未发布，强制更新）
	if !updater.HasUpdate(latestTag) {
		fmt.Printf("✅ 已是最新版本 (%s)，无需升级。\n", buildinfo.Version)
		return nil
	}

	// 3. 拼接下载地址
	fileName := pm.packageFileName()
	downloadURL := updater.BuildDownloadURL(latestTag, fileName)

	fmt.Printf("🚀 发现新版本: %s\n📥 正在拉取: %s\n", latestTag, downloadURL)

	// 4. 下载安装包
	tmpFile, err := updater.DownloadFile(client, downloadURL, buildinfo.BinaryName+pm.ext)
	if err != nil {
		return fmt.Errorf("升级失败: %w", err)
	}
	defer func() { _ = os.Remove(tmpFile) }()

	// 5. 调用系统包管理器安装（deb/rpm/apk 专属逻辑）
	cmdName, cmdArgs := pm.installCommand(tmpFile)
	cmd := exec.Command(cmdName, cmdArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if strings.Contains(err.Error(), "permission denied") || strings.Contains(err.Error(), "Operation not permitted") {
			fmt.Println("💡 提示：请尝试使用 'sudo " + buildinfo.BinaryName + " self-upgrade' 运行")
		}
		return fmt.Errorf("升级失败: %w", err)
	}

	// 安装包已安装。如果服务正在运行，异步重启使新版本生效。
	// 当前进程就是 systemd 管理的服务进程，systemctl restart 会先发 SIGTERM
	// 杀掉当前进程，所以用 nohup + 子 shell 异步执行，让命令在当前进程
	// 退出后继续运行，完成 stop→start 的完整流程。
	restartCmd := exec.Command("sh", "-c", fmt.Sprintf("nohup systemctl restart %s >/dev/null 2>&1 &", buildinfo.BinaryName))
	if err := restartCmd.Start(); err != nil {
		fmt.Printf("✨ 升级成功！请手动重启服务：%s service restart\n", buildinfo.BinaryName)
		return nil
	}
	_ = restartCmd.Process.Release()

	fmt.Printf("✨ 升级成功！服务正在重启，即将运行新版本 %s。\n", latestTag)
	return nil
}
