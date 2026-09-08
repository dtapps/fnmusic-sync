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

// UserAgent 返回上报给第三方服务的 User-Agent（含版本号）。
func UserAgent() string {
	return "cnb.cool/dtapp/fnmusic-sync/" + Version
}

// DefaultListenSocket 飞牛音乐监听 Unix Socket 路径（固定值，不可自定义）。
// nginx ──► /var/run/trim_music.socket ──► Go Proxy
const DefaultListenSocket = "/var/run/trim_music.socket"

// DefaultUpstreamSocket 飞牛音乐上游 Unix Socket 路径（固定值，不可自定义）。
// Go Proxy ──► /var/run/trim_music_upstream.socket ──► trim-music
const DefaultUpstreamSocket = "/var/run/trim_music_upstream.socket"
