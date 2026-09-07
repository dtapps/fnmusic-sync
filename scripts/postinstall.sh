#!/bin/sh
# __BINARY_NAME__ 安装后脚本 (postinstall)
# 由 nfpm 在 deb/rpm/apk 安装后自动执行
# 注：本脚本中的 __BINARY_NAME__ 由 Makefile 打包时替换为真实二进制名
set -e

BINARY="/usr/bin/__BINARY_NAME__"
LOG_DIR="/var/log/__BINARY_NAME__"
DAT_DIR="/var/lib/__BINARY_NAME__"

# 创建日志与数据目录（若包未通过 contents 创建）
for d in "$LOG_DIR" "$DAT_DIR"; do
    if [ ! -d "$d" ]; then
        mkdir -p "$d" 2>/dev/null || true
    fi
done

# 同步二进制能力（部分包管理器不会自动 chmod，确保可执行）
if [ -f "$BINARY" ]; then
    chmod 0755 "$BINARY" 2>/dev/null || true
fi

cat <<'EOF'
__BINARY_NAME__ 安装完成！

常用命令：
  __BINARY_NAME__                启动代理（接管 trim-music 的 unix socket）
  __BINARY_NAME__ --help         查看全部命令行参数
  __BINARY_NAME__ --config <文件> --state <文件>
                              指定配置文件 / 状态文件路径
  __BINARY_NAME__ --check        仅检查 socket 与 upstream 状态，不启动代理
  sudo __BINARY_NAME__ self-upgrade
                              升级到 CNB 仓库最新版本（写 /usr/bin 需 sudo）

示例：
  sudo __BINARY_NAME__ self-upgrade
  __BINARY_NAME__ --config /etc/fnmusic-sync/config.yaml --state /etc/fnmusic-sync/state.yaml
EOF

exit 0
