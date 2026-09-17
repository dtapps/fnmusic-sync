package main

import (
	"fmt"
	"path/filepath"
	"runtime"
	"time"

	"cnb.cool/dtapp/fnmusic-sync/internal/buildinfo"
	"cnb.cool/dtapp/fnmusic-sync/internal/strutil"
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
// 标签列按显示宽度对齐（中文字符占 2 列），值列左对齐。
func printVersion() {
	// 标签 → 值
	type kv struct{ label, value string }

	rows := []kv{
		{"版本", buildinfo.Version},
		{"Git提交", buildinfo.GitCommit},
	}

	// 构建时间行需要特殊处理（可能附"（本地 …）"）
	buildTimeVal := buildinfo.BuildTime
	if local := localBuildTime(); local != buildinfo.BuildTime {
		buildTimeVal = buildinfo.BuildTime + "（本地 " + local + "）"
	}
	rows = append(rows, kv{"构建时间", buildTimeVal})

	rows = append(rows,
		kv{"Go版本", runtime.Version()},
		kv{"平台", runtime.GOOS + "/" + runtime.GOARCH},
		kv{"运行模式", rt.Mode.modeName()},
		kv{"监听Socket", buildinfo.DefaultListenSocket},
		kv{"上游Socket", buildinfo.DefaultUpstreamSocket},
		kv{"配置", rt.ConfigPath},
		kv{"数据库", rt.StatePath},
		kv{"日志", filepath.Join(rt.LogDir, defaultLogFile)},
	)

	if buildinfo.Version == "dev" {
		rows = append(rows, kv{"说明", "本地构建（未注入发布版本号）"})
	}

	// 计算最长标签的显示宽度，值列对齐到该位置
	maxLabelWidth := 0
	for _, r := range rows {
		if w := strutil.DisplayWidth(r.label); w > maxLabelWidth {
			maxLabelWidth = w
		}
	}

	// 输出
	fmt.Printf("%s %s\n", buildinfo.BinaryName, buildinfo.Version)
	for _, r := range rows {
		padded := strutil.PadRight(r.label, maxLabelWidth)
		fmt.Printf("  %s  %s\n", padded, r.value)
	}
}
