// Package buildinfo 提供统一的构建信息。
//
// 版本信息由 Makefile 通过 -ldflags -X 注入：
//   - Version   版本号（git describe --tags）
//   - GitCommit  Git 提交哈希
//   - BuildTime  构建时间
//   - BinaryName 二进制文件名
//
// 使用方式：
//
//	import "cnb.cool/dtapp/fnmusic-sync/internal/buildinfo"
//
//	fmt.Println(buildinfo.Version)
//	fmt.Println(buildinfo.UserAgent())
package buildinfo

// Version 版本号，由 Makefile 通过 -ldflags 注入，默认值 dev 用于本地未注入时。
var Version = "dev"

// GitCommit Git 提交哈希，由 Makefile 通过 -ldflags 注入。
var GitCommit = "unknown"

// BuildTime 构建时间，由 Makefile 通过 -ldflags 注入。
var BuildTime = "unknown"

// BinaryName 二进制文件名，由 Makefile 通过 -ldflags 注入。
var BinaryName = "fnmusic-sync"

// RepoSource 构建来源，标识当前二进制由哪个平台构建发布，由 Makefile 通过 -ldflags 注入。
// 可选值: "cnb", "github"，默认空字符串表示未知来源。
var RepoSource = ""

// CnbToken CNB 访问令牌，由 Makefile 通过 -ldflags 注入。
// CNB API 需要鉴权才能访问 release 列表接口，为空时升级接口将提示无法自动升级。
var CnbToken = ""

// GithubToken GitHub 访问令牌，由 Makefile 通过 -ldflags 注入。
// GitHub API 在无 Token 时也可使用（有速率限制），因此可以为空。
var GithubToken = ""

// UserAgent 返回上报给第三方服务的 User-Agent（含版本号）。
func UserAgent() string {
	return "cnb.cool/dtapp/fnmusic-sync/" + Version
}

// IsCNB 返回当前二进制是否由 CNB 平台构建发布。
func IsCNB() bool {
	return RepoSource == "cnb"
}

// IsGitHub 返回当前二进制是否由 GitHub 平台构建发布。
func IsGitHub() bool {
	return RepoSource == "github"
}

// DefaultListenSocket 飞牛音乐监听 Unix Socket 路径（固定值，不可自定义）。
// nginx ──► /var/run/trim_music.socket ──► Go Proxy
const DefaultListenSocket = "/var/run/trim_music.socket"

// DefaultUpstreamSocket 飞牛音乐上游 Unix Socket 路径（固定值，不可自定义）。
// Go Proxy ──► /var/run/trim_music_upstream.socket ──► trim-music
const DefaultUpstreamSocket = "/var/run/trim_music_upstream.socket"
