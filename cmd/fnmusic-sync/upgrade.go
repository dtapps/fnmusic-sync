package main

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// repoBase 是当前项目在 CNB 上的仓库地址（与 Makefile 的 PKG 对应），
// 升级时从这里抓取 releases 并下载对应平台的二进制。
const repoBase = "https://cnb.cool/dtapp/fnmusic-sync"

// handleSelfUpgrade 检查并升级当前二进制到 CNB 仓库的最新 release。
func handleSelfUpgrade() {
	arch := runtime.GOARCH
	osName := runtime.GOOS

	fmt.Printf("🔄 检查 %s 自身更新 (当前版本: %s, 平台: %s/%s)...\n", BinaryName, Version, osName, arch)

	client := &http.Client{Timeout: 30 * time.Second}

	// 1. 抓取最新 tag
	resp, err := client.Get(repoBase + "/-/releases")
	if err != nil {
		fmt.Printf("❌ 无法连接到 CNB: %v\n", err)
		return
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	re := regexp.MustCompile(`releases/tag/(v\d+\.\d+\.\d+)`)
	match := re.FindStringSubmatch(string(body))
	if len(match) < 2 {
		fmt.Println("❌ 无法从 CNB 页面解析最新版本号，请检查仓库是否公开。")
		return
	}
	latestTag := match[1]

	// 2. 版本比对（dev 视为未发布，强制更新）
	if latestTag == Version && Version != "dev" {
		fmt.Printf("✅ 已是最新版本 (%s)，无需升级。\n", Version)
		return
	}

	// 3. 拼接下载地址（Release 上传的是 .tar.gz 压缩包，供自升级下载）
	fileName := fmt.Sprintf("%s-%s-%s.tar.gz", BinaryName, osName, arch)
	downloadURL := fmt.Sprintf("%s/-/releases/download/%s/%s", repoBase, latestTag, fileName)

	fmt.Printf("🚀 发现新版本: %s\n📥 正在拉取: %s\n", latestTag, downloadURL)

	if err := doReplace(client, downloadURL); err != nil {
		fmt.Printf("❌ 替换失败: %v\n", err)
		if strings.Contains(err.Error(), "permission denied") {
			fmt.Println("💡 提示：请尝试使用 'sudo ./" + BinaryName + " self-upgrade' 运行")
		}
		return
	}

	fmt.Printf("✨ 升级成功！请重新运行以使用新版本 %s。\n", latestTag)
}

// doReplace 下载 url（.tar.gz）到临时目录并解压出二进制，原子替换当前可执行文件。
func doReplace(client *http.Client, url string) error {
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		return fmt.Errorf("服务器返回错误状态码: %d (请检查该平台的 Release 附件是否存在: %s)", resp.StatusCode, url)
	}

	exePath, err := os.Executable()
	if err != nil {
		return err
	}

	// 先下载到临时文件（.tar.gz）
	tmpGz := exePath + ".tar.gz.new"
	gzf, err := os.OpenFile(tmpGz, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	if _, err = io.Copy(gzf, resp.Body); err != nil {
		_ = gzf.Close()
		_ = os.Remove(tmpGz)
		return err
	}
	if err := gzf.Close(); err != nil {
		_ = os.Remove(tmpGz)
		return err
	}
	defer func() { _ = os.Remove(tmpGz) }()

	// 解压 tar.gz，取出其中的二进制（成员名前缀为 BinaryName）
	binData, err := extractBinaryFromTarGz(tmpGz)
	if err != nil {
		return err
	}

	// 在同目录下创建临时文件（解决跨分区 rename 失败问题）
	tmpPath := exePath + ".new"
	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	if _, err = f.Write(binData); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}

	// 原子替换
	if err := os.Rename(tmpPath, exePath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

// extractBinaryFromTarGz 从 tar.gz 中读取二进制内容（按 BinaryName 前缀匹配）。
func extractBinaryFromTarGz(gzPath string) ([]byte, error) {
	f, err := os.Open(gzPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	gzr, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("解压失败: %w", err)
	}
	defer func() { _ = gzr.Close() }()

	tr := tar.NewReader(gzr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("读取压缩包失败: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		// tar 包内的二进制名为 <BinaryName>-linux-<arch>（带平台后缀），
		// 而运行时 exePath 的 base 是安装后的 <BinaryName>，二者不一致。
		// 因此按 BinaryName 前缀匹配包内普通文件，兼容带目录前缀的情况。
		base := filepath.Base(hdr.Name)
		if !strings.HasPrefix(base, BinaryName) {
			continue
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("读取二进制失败: %w", err)
		}
		return data, nil
	}
	return nil, fmt.Errorf("压缩包中未找到 %s 二进制", BinaryName)
}
