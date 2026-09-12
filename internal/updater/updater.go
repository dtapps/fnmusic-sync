// Package updater 提供统一的升级公共逻辑：获取最新版本号、拼接下载地址、下载文件。
//
// 不同安装方式（deb 包管理器安装、fpk 通过 appcenter-cli 安装）
// 只需各自实现安装步骤，版本检查和下载逻辑统一调用本包。
package updater

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"cnb.cool/dtapp/fnmusic-sync/internal/buildinfo"
)

// 仓库地址与 API 端点常量。
const (
	RepoBaseCNB        = "https://cnb.cool/dtapp/fnmusic-sync"
	RepoBaseGitHub     = "https://github.com/dtapps/fnmusic-sync"
	cnbRepoPath        = "dtapp/fnmusic-sync"
	githubRepoPath     = "dtapps/fnmusic-sync"
	cnbReleaseListURL  = "https://api.cnb.cool/" + cnbRepoPath + "/-/releases?page=1&page_size=20"
	ghReleaseLatestURL = "https://api.github.com/repos/" + githubRepoPath + "/releases/latest"
)

// cnbReleaseItem CNB API releases 列表项。
type cnbReleaseItem struct {
	TagName     string `json:"tag_name"`
	Name        string `json:"name"`
	Prerelease  bool   `json:"prerelease"`
	Draft       bool   `json:"draft"`
	PublishedAt string `json:"published_at"`
	IsLatest    bool   `json:"is_latest"`
}

// RepoBase 根据构建来源返回对应的仓库 Web 地址。
func RepoBase() string {
	switch buildinfo.RepoSource {
	case "github":
		return RepoBaseGitHub
	default:
		return RepoBaseCNB
	}
}

// FetchLatestTag 从对应平台获取最新 release tag。
//
// CNB 使用 API 接口（需要 Bearer Token 鉴权），
// GitHub 使用 API 接口（Token 可选，为空时受速率限制）。
//
// CNB Token 为空时返回错误，提示用户手动下载。
func FetchLatestTag(client *http.Client) (string, error) {
	if buildinfo.IsGitHub() {
		return fetchLatestTagGitHub(client)
	}
	return fetchLatestTagCNB(client)
}

// fetchLatestTagGitHub 从 GitHub API 获取最新 release tag。
func fetchLatestTagGitHub(client *http.Client) (string, error) {
	req, err := http.NewRequest("GET", ghReleaseLatestURL, nil)
	if err != nil {
		return "", fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if buildinfo.GithubToken != "" {
		req.Header.Set("Authorization", "Bearer "+buildinfo.GithubToken)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("无法连接到 GitHub: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("GitHub API 返回 %d: %s", resp.StatusCode, string(body))
	}

	var rel struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return "", fmt.Errorf("解析 GitHub API 响应失败: %w", err)
	}
	if rel.TagName == "" {
		return "", fmt.Errorf("无法从 GitHub API 解析最新版本号")
	}
	return rel.TagName, nil
}

// fetchLatestTagCNB 从 CNB API 获取最新 release tag。
func fetchLatestTagCNB(client *http.Client) (string, error) {
	if buildinfo.CnbToken == "" {
		return "", fmt.Errorf("CNB 访问令牌未配置，暂时无法自动升级，请前往 %s 手动下载", RepoBaseCNB)
	}

	req, err := http.NewRequest("GET", cnbReleaseListURL, nil)
	if err != nil {
		return "", fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+buildinfo.CnbToken)

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("无法连接到 CNB: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return "", fmt.Errorf("CNB 访问令牌无效或已过期，请前往 %s 手动下载", RepoBaseCNB)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("CNB API 返回 %d: %s", resp.StatusCode, string(body))
	}

	var releases []cnbReleaseItem
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return "", fmt.Errorf("解析 CNB API 响应失败: %w", err)
	}
	if len(releases) == 0 {
		return "", fmt.Errorf("CNB 上暂无任何发布版本")
	}

	// 优先返回 is_latest 的，否则取第一个
	for _, r := range releases {
		if r.IsLatest && !r.Draft {
			return r.TagName, nil
		}
	}
	return releases[0].TagName, nil
}

// BuildDownloadURL 根据构建来源、版本 tag 和文件名拼接下载地址。
//
// GitHub: https://github.com/{repo}/releases/download/{tag}/{file}
// CNB:    https://cnb.cool/{repo}/-/releases/download/{tag}/{file}
func BuildDownloadURL(tag, fileName string) string {
	base := RepoBase()
	if buildinfo.IsGitHub() {
		return fmt.Sprintf("%s/releases/download/%s/%s", base, tag, fileName)
	}
	return fmt.Sprintf("%s/-/releases/download/%s/%s", base, tag, fileName)
}

// DownloadFile 下载指定 URL 的文件到临时目录，返回本地文件路径。
// 调用方负责在安装完成后清理临时文件（defer os.Remove）。
func DownloadFile(client *http.Client, url, fileName string) (string, error) {
	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("下载失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载失败: 服务器返回 %d (请检查 Release 附件是否存在: %s)", resp.StatusCode, url)
	}

	tmpFile := filepath.Join(os.TempDir(), fileName)
	f, err := os.OpenFile(tmpFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return "", fmt.Errorf("创建临时文件失败: %w", err)
	}
	if _, err = io.Copy(f, resp.Body); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpFile)
		return "", fmt.Errorf("写入临时文件失败: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmpFile)
		return "", fmt.Errorf("关闭临时文件失败: %w", err)
	}

	return tmpFile, nil
}

// HasUpdate 判断是否有新版本。
// dev 视为未发布，强制更新；其余情况比较 tag 是否不同。
func HasUpdate(latestTag string) bool {
	return latestTag != buildinfo.Version || buildinfo.Version == "dev"
}

// DefaultHTTPClient 返回升级用的默认 HTTP 客户端（30s 超时）。
func DefaultHTTPClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Second}
}
