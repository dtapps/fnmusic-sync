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
	"strings"
	"time"

	"cnb.cool/dtapp/fnmusic-sync/internal/buildinfo"
)

// 仓库地址与 API 端点常量。
const (
	RepoBaseCNB    = "https://cnb.cool/dtapp/fnmusic-sync"
	RepoBaseGitHub = "https://github.com/dtapps/fnmusic-sync"
	RepoBaseGitLab = "https://gitlab.com/dtapps/fnmusic-sync"

	cnbRepoPath    = "dtapp/fnmusic-sync"
	githubRepoPath = "dtapps/fnmusic-sync"

	cnbReleaseListURL = "https://api.cnb.cool/" + cnbRepoPath + "/-/releases?page=1&page_size=20"
	// 不用 /releases/latest：它虽然天然排除 prerelease，但只在「平台标记」正确时成立。
	// 拿列表才能同时看到 prerelease 与 draft 两个字段，也才能做「字段 + tag 后缀」的并集判断。
	ghReleaseListURL = "https://api.github.com/repos/" + githubRepoPath + "/releases?per_page=20"

	// GitLab 的 API 项目标识必须 URL 编码（斜杠 → %2F），否则嵌套命名空间会被当成两段路径。
	// 这里写字面量而不是 url.PathEscape(...)，因为常量表达式里不能调函数。
	gitlabProjectAPI = "dtapps%2Ffnmusic-sync"
	// GitLab 没有「最新 release」的 API（permalink/latest 只保证 Web 可用），只能列出来取第一条。
	// 按 released_at 倒序 + 过滤 upcoming_release，即「最近一次真正发布」。
	gitlabReleaseListURL = "https://gitlab.com/api/v4/projects/" + gitlabProjectAPI +
		"/releases?per_page=20&order_by=released_at&sort=desc"

	// 下载中转服务：在原始下载链接前拼接，GET 计数并跳转源站。
	// 三个来源（cnb / github / gitlab）统一走 dl-stats.dtapp.net —— 它是
	// 「<中转域名>/<原始完整 URL>」的透传，不挑源站，GitLab 的 Release 直链实测能正常
	// 转发（302 到通用包仓库）。曾经按来源拆成 .top / .net 两个域，没必要，已合并。
	relayBase = "https://dl-stats.dtapp.net"
)

// noRelayFiles 这些发布附件不走下载中转，直接连接源站：
// SHA256SUMS / GIT_COMMIT / BUILD_TIME 为元数据文件，无需计入中转统计。
var noRelayFiles = map[string]bool{
	"SHA256SUMS": true,
	"GIT_COMMIT": true,
	"BUILD_TIME": true,
}

// cnbReleaseItem CNB API releases 列表项。
type cnbReleaseItem struct {
	TagName     string `json:"tag_name"`
	Name        string `json:"name"`
	Prerelease  bool   `json:"prerelease"`
	Draft       bool   `json:"draft"`
	PublishedAt string `json:"published_at"`
	IsLatest    bool   `json:"is_latest"`
}

// gitlabReleaseItem GitLab API releases 列表项（只取用得到的字段）。
// GitLab 的 Release 没有「预发布」标记，唯一的排除项是 upcoming_release（计划未来发布的占位）。
type gitlabReleaseItem struct {
	TagName         string `json:"tag_name"`
	Name            string `json:"name"`
	ReleasedAt      string `json:"released_at"`
	UpcomingRelease bool   `json:"upcoming_release"`
}

// RepoBase 根据构建来源返回对应的仓库 Web 地址。
func RepoBase() string {
	switch buildinfo.RepoSource {
	case "github":
		return RepoBaseGitHub
	case "gitlab":
		return RepoBaseGitLab
	default:
		return RepoBaseCNB
	}
}

// FetchLatestTag 从对应平台获取最新 release tag。
//
// CNB 使用 API 接口（需要 Bearer Token 鉴权），
// GitHub 使用 API 接口（Token 可选，为空时受速率限制），
// GitLab 使用 API 接口（公开项目可匿名读，私有项目需要 Token）。
//
// CNB Token 为空时返回错误，提示用户手动下载。
func FetchLatestTag(client *http.Client) (string, error) {
	switch {
	case buildinfo.IsGitHub():
		return fetchLatestTagGitHub(client)
	case buildinfo.IsGitLab():
		return fetchLatestTagGitLab(client)
	default:
		return fetchLatestTagCNB(client)
	}
}

// releaseCandidate 三个平台的 release 列表项归一化后的形态，供 selectTag 统一挑选。
type releaseCandidate struct {
	Tag        string
	Prerelease bool // 平台自带的预发布标记（GitHub / CNB 有；GitLab 没有，永远是 false）
	Draft      bool
	Upcoming   bool // GitLab 的 upcoming_release：计划发布，还没真发
}

// selectTag 从「按发布时间倒序」的候选里挑一个 tag。
//
// 预发布判定是**并集**：平台字段标了 或者 tag 带 semver 后缀，任一成立就算预发布。
// 这就是「后缀约定 + prerelease 字段」的兼容点——三个平台只要有一个信号到位就不会漏。
// 稳定版客户端只拿正式版；预发布客户端（当前版本自己是 rc 或 dev）可以继续吃预发布。
// 全是预发布时退回最新一条，避免「只有 rc 的项目永远无法自动升级」。
func selectTag(cands []releaseCandidate, allowPre bool) string {
	var fallback string

	for _, c := range cands {
		if c.Tag == "" || c.Draft || c.Upcoming {
			continue
		}
		if fallback == "" {
			fallback = c.Tag
		}
		if allowPre || (!c.Prerelease && !isPrereleaseTag(c.Tag)) {
			return c.Tag
		}
	}
	return fallback
}

// fetchLatestTagGitHub 从 GitHub API 获取最新 release tag。
func fetchLatestTagGitHub(client *http.Client) (string, error) {
	req, err := http.NewRequest("GET", ghReleaseListURL, nil)
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

	var releases []struct {
		TagName    string `json:"tag_name"`
		Prerelease bool   `json:"prerelease"`
		Draft      bool   `json:"draft"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return "", fmt.Errorf("解析 GitHub API 响应失败: %w", err)
	}

	cands := make([]releaseCandidate, 0, len(releases))
	for _, r := range releases {
		cands = append(cands, releaseCandidate{Tag: r.TagName, Prerelease: r.Prerelease, Draft: r.Draft})
	}

	tag := selectTag(cands, allowPrerelease())
	if tag == "" {
		return "", fmt.Errorf("GitHub 上暂无任何发布版本")
	}
	return tag, nil
}

// isPrereleaseTag 判断 tag 是不是预发布，依据是 semver 的 `-<prerelease>` 段：
// v1.2.3-rc.1 / v1.2.3-beta / 1.2.4-pre 都算，v1.2.3 不算。
//
// 为什么靠 tag 而不是平台字段：GitHub 的 releases API 自带 prerelease 标记、
// releases/latest 会直接排除它们；CNB 有 prerelease 字段；而 GitLab 的 Release
// **没有任何预发布概念**（只有一个「还没正式发布的 upcoming_release」），
// 所以唯一能跨平台统一的信号就是 tag 命名。
func isPrereleaseTag(tag string) bool {
	core := strings.TrimPrefix(strings.TrimSpace(tag), "v")
	i := strings.Index(core, "-")
	if i <= 0 || i == len(core)-1 {
		return false
	}
	return isNumericDotted(core[:i])
}

// isNumericDotted 判断 s 是否为 x.y.z 形态（至少两段、每段纯数字）。
func isNumericDotted(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) < 2 {
		return false
	}
	for _, p := range parts {
		if p == "" {
			return false
		}
		for _, r := range p {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}

// allowPrerelease 当前二进制自己就是预发布（版本号带 -rc 之类）或本地 dev 构建时，
// 才允许把预发布当成可升级目标——用稳定版的人不该被推到 rc 上。
func allowPrerelease() bool {
	return buildinfo.Version == "dev" || isPrereleaseTag(buildinfo.Version)
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

	// is_latest 是平台给的权威标记：把带它的排到最前，交给 selectTag 优先挑，
	// 其余保持列表原顺序（API 已按发布时间倒序）。
	cands := make([]releaseCandidate, 0, len(releases))
	for _, r := range releases {
		if r.IsLatest {
			cands = append(cands, releaseCandidate{Tag: r.TagName, Prerelease: r.Prerelease, Draft: r.Draft})
		}
	}
	for _, r := range releases {
		if !r.IsLatest {
			cands = append(cands, releaseCandidate{Tag: r.TagName, Prerelease: r.Prerelease, Draft: r.Draft})
		}
	}

	tag := selectTag(cands, allowPrerelease())
	if tag == "" {
		return "", fmt.Errorf("CNB 上暂无任何发布版本")
	}
	return tag, nil
}

// fetchLatestTagGitLab 从 GitLab API 获取最新 release tag。
//
// 公开项目匿名可读，私有项目需要 GitlabToken（read_api）；没有 Token 又遇到私有项目时，
// GitLab 对未认证请求返回 404 而不是 401，所以两种状态码都按「没权限/不存在」提示。
func fetchLatestTagGitLab(client *http.Client) (string, error) {
	req, err := http.NewRequest("GET", gitlabReleaseListURL, nil)
	if err != nil {
		return "", fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if buildinfo.GitlabToken != "" {
		req.Header.Set("PRIVATE-TOKEN", buildinfo.GitlabToken)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("无法连接到 GitLab: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden ||
		resp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("GitLab 项目不可读（私有项目需要 GitlabToken，read_api 权限），请前往 %s 手动下载", RepoBaseGitLab)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("GitLab API 返回 %d: %s", resp.StatusCode, string(body))
	}

	var releases []gitlabReleaseItem
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return "", fmt.Errorf("解析 GitLab API 响应失败: %w", err)
	}

	// 列表已按 released_at 倒序。GitLab 没有 prerelease 字段，
	// 预发布只能靠 tag 后缀识别（见 selectTag 的并集判断）。
	cands := make([]releaseCandidate, 0, len(releases))
	for _, r := range releases {
		cands = append(cands, releaseCandidate{Tag: r.TagName, Upcoming: r.UpcomingRelease})
	}

	tag := selectTag(cands, allowPrerelease())
	if tag == "" {
		return "", fmt.Errorf("GitLab 上暂无任何发布版本")
	}
	return tag, nil
}

// BuildDownloadURL 根据构建来源、版本 tag 和文件名拼接下载地址。
//
// GitHub: https://github.com/{repo}/releases/download/{tag}/{file}
// CNB:    https://cnb.cool/{repo}/-/releases/download/{tag}/{file}
// GitLab: https://gitlab.com/{repo}/-/releases/{tag}/downloads/{file}
//
// GitLab 走 Release 的 direct asset 链接（建 Release 时 CI 给每个文件挂了
// direct_asset_path），它会 302 到通用包仓库；而 Web 侧的 /-/packages/generic/...
// 对匿名用户是跳登录页，不能用于自动升级。
func BuildDownloadURL(tag, fileName string) string {
	base := RepoBase()
	switch {
	case buildinfo.IsGitLab():
		return fmt.Sprintf("%s/-/releases/%s/downloads/%s", base, tag, fileName)
	case buildinfo.IsGitHub():
		return fmt.Sprintf("%s/releases/download/%s/%s", base, tag, fileName)
	default:
		return fmt.Sprintf("%s/-/releases/download/%s/%s", base, tag, fileName)
	}
}

// relayDownloadURL 在原始下载链接前拼接下载中转服务地址。
// 格式：<中转域名>/<原始完整 URL>，例如
//
//	https://dl-stats.dtapp.net/https://github.com/owner/repo/releases/download/v1.0.0/file.zip
//
// 不分来源，一律 dl-stats.dtapp.net。
func relayDownloadURL(original string) string {
	return relayBase + "/" + original
}

// DownloadFile 下载指定 URL 的文件到临时目录，返回本地文件路径。
// 优先走下载中转服务（dl-stats）；若中转下载失败（网络错误或源站返回非 200），
// 立即回退到未拼接前缀的原始地址重新下载。调用方负责清理临时文件。
// 注意：SHA256SUMS / GIT_COMMIT / BUILD_TIME 三类元数据文件不走中转，直连源站，
// 否则中转的下载计数会被元数据请求污染。
func DownloadFile(client *http.Client, url, fileName string) (string, error) {
	if noRelayFiles[fileName] {
		// 元数据文件直连源站，不走中转、也不回退
		return downloadTo(client, url, fileName)
	}
	if tmp, err := downloadTo(client, relayDownloadURL(url), fileName); err == nil {
		return tmp, nil
	}
	// 中转失败，回退到原始地址
	return downloadTo(client, url, fileName)
}

// downloadTo 实际执行一次下载（不含中转回退）；失败时返回错误。
func downloadTo(client *http.Client, url, fileName string) (string, error) {
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
