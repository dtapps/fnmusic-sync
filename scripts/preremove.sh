#!/bin/sh
# __BINARY_NAME__ 卸载前脚本 (preremove)
# 由 nfpm 在 deb/rpm/apk 卸载前自动执行
# 注：本脚本中的 __BINARY_NAME__ 由 Makefile 打包时替换为真实二进制名
set -e

BINARY="/usr/bin/__BINARY_NAME__"

# 卸载前若程序仍在运行，尝试停止（本工具为交互式 TUI，通常无后台进程）
if command -v pidof >/dev/null 2>&1; then
    pids=$(pidof "$BINARY" 2>/dev/null || true)
    if [ -n "$pids" ]; then
        kill "$pids" 2>/dev/null || true
    fi
fi

exit 0
