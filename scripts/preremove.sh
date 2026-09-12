#!/bin/sh
# __BINARY_NAME__ 卸载前脚本 (preremove)
# 由 nfpm 在 deb 卸载前自动执行
# 注：本脚本中的 __BINARY_NAME__ 由 Makefile 打包时替换为真实二进制名
set -e

BINARY="/usr/bin/__BINARY_NAME__"
NAME="__BINARY_NAME__"

# 卸载前停止并移除 systemd 服务（若已安装）
if command -v systemctl >/dev/null 2>&1; then
  systemctl stop "$NAME" >/dev/null 2>&1 || true
  systemctl disable "$NAME" >/dev/null 2>&1 || true
fi

if [ -x "$BINARY" ]; then
  "$BINARY" service stop >/dev/null 2>&1 || true
  "$BINARY" service uninstall >/dev/null 2>&1 || true
fi

# 清理 systemd unit 文件并重新加载
rm -f "/etc/systemd/system/${NAME}.service" 2>/dev/null || true
command -v systemctl >/dev/null 2>&1 && systemctl daemon-reload 2>/dev/null || true

# 兜底：杀掉残留进程
if command -v pidof >/dev/null 2>&1; then
  pids=$(pidof "$BINARY" 2>/dev/null || true)
  if [ -n "$pids" ]; then
    kill "$pids" 2>/dev/null || true
  fi
fi

exit 0
