package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// RuntimeMode 表示应用的运行模式。
type RuntimeMode int

const (
	// ModeTraditional 传统安装模式（deb/脚本安装到 /usr/bin）。
	ModeTraditional RuntimeMode = iota
	// ModeFPK 飞牛 fnOS fpk 安装模式（通过应用中心安装到 /var/apps/fnmusic-sync）。
	ModeFPK
)

// 传统模式下的固定路径（不可自定义）。
const (
	traditionalConfigPath  = "/etc/fnmusic-sync/config.yaml"
	traditionalStatePath   = "/var/lib/fnmusic-sync/state.yaml"
	traditionalLogDir      = "/var/log/fnmusic-sync"
	traditionalRunLockPath = "/run/fnmusic-sync/run.lock"
)

// runtimeInfo 保存运行模式相关的路径信息。
type runtimeInfo struct {
	Mode        RuntimeMode
	ConfigPath  string
	StatePath   string
	LogDir      string
	RunLockPath string
}

// detectRuntime 检测当前运行模式并返回对应的路径信息。
//
// fpk 安装模式下，飞牛 fnOS 会设置 TRIM_APPDEST、TRIM_PKGETC、TRIM_PKGVAR 等环境变量，
// 此时使用这些变量对应的路径；否则使用传统模式的固定路径。
func detectRuntime() runtimeInfo {
	appDest := os.Getenv("TRIM_APPDEST")
	if appDest != "" {
		// fpk 模式
		pkgVar := os.Getenv("TRIM_PKGVAR")
		if pkgVar == "" {
			pkgVar = filepath.Join(appDest, "..", "var")
		}
		pkgEtc := os.Getenv("TRIM_PKGETC")
		if pkgEtc == "" {
			pkgEtc = filepath.Join(appDest, "..", "etc")
		}

		return runtimeInfo{
			Mode:        ModeFPK,
			ConfigPath:  filepath.Join(pkgEtc, "config.yaml"),
			StatePath:   filepath.Join(pkgVar, "state.yaml"),
			LogDir:      filepath.Join(pkgVar, "logs"),
			RunLockPath: filepath.Join(pkgVar, "run.lock"),
		}
	}

	// 传统模式
	return runtimeInfo{
		Mode:        ModeTraditional,
		ConfigPath:  traditionalConfigPath,
		StatePath:   traditionalStatePath,
		LogDir:      traditionalLogDir,
		RunLockPath: traditionalRunLockPath,
	}
}

// modeName 返回运行模式的人类可读名称。
func (m RuntimeMode) modeName() string {
	switch m {
	case ModeFPK:
		return "fpk（飞牛应用中心）"
	default:
		return "传统安装"
	}
}

// String 用于日志输出。
func (r runtimeInfo) String() string {
	return fmt.Sprintf("模式=%s 配置=%s 状态=%s 日志=%s",
		r.Mode.modeName(), r.ConfigPath, r.StatePath, r.LogDir)
}
