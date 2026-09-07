package main

import (
	"fmt"
	"path/filepath"
	"runtime"
	"time"

	"cnb.cool/dtapp/fnmusic-sync/internal/buildinfo"
)

// 构建时间的解析格式（全部用 time 包自带常量拼接，不写魔法字符串），
// 首选 Makefile 注入的 `date -u '+%Y-%m-%d %H:%M:%S UTC'`。
var buildTimeLayouts = []string{
	time.DateTime + " MST", // 2026-09-07 08:04:45 UTC（Makefile 注入）
	time.RFC3339,           // 2026-09-07T08:04:45Z
	time.DateTime,          // 2026-09-07 08:04:45（无时区，按 UTC 解析）
	time.ANSIC,             // Mon Jan  2 15:04:05 2006
	time.UnixDate,          // Mon Jan  2 15:04:05 MST 2006
	time.RFC1123,           // Mon, 02 Jan 2006 15:04:05 MST
}

// localBuildTimeFormat 本地时间展示格式：2026-09-07 16:04:45 +0800 CST
const localBuildTimeFormat = time.DateTime + " -0700 MST"

// localBuildTime 把注入的构建时间（Makefile 写入的是 UTC）转换成本地时区展示；
// 解析失败时原样返回，不影响启动。
func localBuildTime() string {
	if buildinfo.BuildTime == "" || buildinfo.BuildTime == "unknown" {
		return buildinfo.BuildTime
	}

	for _, layout := range buildTimeLayouts {
		if t, perr := time.Parse(layout, buildinfo.BuildTime); perr == nil {
			return t.Local().Format(localBuildTimeFormat)
		}
	}

	return buildinfo.BuildTime
}

// printVersion 打印版本与构建信息（version 子命令）。
// 版本/提交/构建时间由 Makefile 通过 -ldflags 注入，本地直接 go build 时为 dev。
func printVersion() {
	fmt.Printf("%s %s\n", buildinfo.BinaryName, buildinfo.Version)
	fmt.Printf("  版本      %s\n", buildinfo.Version)
	fmt.Printf("  Git提交   %s\n", buildinfo.GitCommit)
	fmt.Printf("  构建时间  %s", buildinfo.BuildTime)

	if local := localBuildTime(); local != buildinfo.BuildTime {
		fmt.Printf("（本地 %s）", local)
	}

	fmt.Println()

	fmt.Printf("  Go版本    %s\n", runtime.Version())
	fmt.Printf("  平台      %s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Printf("  配置      %s\n", defaultConfigPath)
	fmt.Printf("  状态      %s\n", defaultStatePath)
	fmt.Printf("  日志      %s\n", filepath.Join(defaultLogDir, defaultLogFile))

	if buildinfo.Version == "dev" {
		fmt.Println("  说明      本地构建（未注入发布版本号）")
	}
}
