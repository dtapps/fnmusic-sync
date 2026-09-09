#!/bin/sh
# __BINARY_NAME__ 安装后脚本 (postinstall)
# 由 nfpm 在 deb/rpm/apk 安装后自动执行
# 注：本脚本中的 __BINARY_NAME__ 由 Makefile 打包时替换为真实二进制名
set -e

BINARY="/usr/bin/__BINARY_NAME__"
ETC_DIR="/etc/__BINARY_NAME__"
LOG_DIR="/var/log/__BINARY_NAME__"
DAT_DIR="/var/lib/__BINARY_NAME__"
NAME="__BINARY_NAME__"

# 创建配置 / 日志 / 状态目录（若包未通过 contents 创建）
for d in "$ETC_DIR" "$LOG_DIR" "$DAT_DIR"; do
  if [ ! -d "$d" ]; then
    mkdir -p "$d" 2>/dev/null || true
  fi
done

# 确保二进制可执行
if [ -f "$BINARY" ]; then
  chmod 0755 "$BINARY" 2>/dev/null || true
fi

# 安装 systemd 服务并设置开机自启（若 systemd 可用）
if command -v systemctl >/dev/null 2>&1 && [ -x "$BINARY" ]; then
  # 注册 systemd 服务（kardianos/service）
  "$BINARY" service install >/dev/null 2>&1 || true
  # 设置开机自启并立即启动
  systemctl enable --now "$NAME" >/dev/null 2>&1 || true
fi

cat <<EOF
${NAME} 安装完成！

常用命令：
  ${NAME}                                    启动代理（接管 trim-music 的 unix socket）
  ${NAME} --help                             查看全部命令行参数
  ${NAME} --check                            仅检查 socket 与 upstream 状态，不启动代理
  ${NAME} version                            查看版本、Git 提交与构建时间
  sudo ${NAME} self-upgrade                  升级到仓库最新版本（需 sudo）

服务管理：
  systemctl status ${NAME}                   查看运行状态
  systemctl restart ${NAME}                  重启服务
  systemctl stop ${NAME}                      停止服务
  sudo ${NAME} service debug                 开启 debug 模式（请求/响应详细日志）
  sudo ${NAME} service nodebug               关闭 debug 模式

卸载（自动停止并移除服务）：
  sudo apt remove ${NAME}          # deb 系（Debian/Ubuntu）
  sudo rpm -e ${NAME}              # rpm 系（Fedora/RHEL/openSUSE）
  sudo apk del ${NAME}             # apk 系（Alpine）
  # 连配置一起删除：sudo apt purge ${NAME} / sudo apk del --purge ${NAME}

配置文件由首次启动自动生成（${ETC_DIR}/config.yaml），修改后自动热加载。
EOF

exit 0
