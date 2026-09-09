#!/bin/sh
# fnmusic-sync 安装 / 升级 / 卸载脚本（必须以 root 运行）
#
# 一键安装（推荐）：
#   curl -fsSL https://cnb.cool/dtapp/fnmusic-sync/-/git/raw/main/scripts/install.sh | sudo sh
#
# 其他用法：
#   sudo sh install.sh install              安装最新版（默认动作，可省略）
#   sudo sh install.sh upgrade              升级（下载系统安装包，保留配置，自动重启服务）
#   sudo sh install.sh upgrade --version v1.2.3   升级到指定版本
#   sudo sh install.sh uninstall            卸载二进制与服务（保留配置）
#   sudo sh install.sh uninstall --purge    连配置一起删除
#
# 可选参数：
#   --version vX.Y.Z       指定版本（默认 latest）
#   --purge                卸载时连配置、日志、状态一起删除
#   -h, --help             查看帮助
#
# 环境变量：
#   FNMUSIC_REPO   覆盖仓库地址（默认 https://cnb.cool/dtapp/fnmusic-sync）

set -eu

REPO="${FNMUSIC_REPO:-https://cnb.cool/dtapp/fnmusic-sync}"
BINARY="fnmusic-sync"
CONFIG_DIR="/etc/${BINARY}"
LOG_DIR="/var/log/${BINARY}"
STATE_DIR="/var/lib/${BINARY}"
VERSION="latest"
ACTION="install"
PURGE=0

usage() {
  sed -n '2,16p' "$0" | sed 's/^# \{0,1\}//'
}

log() { printf '%s\n' "$*" >&2; }
info() { printf 'ℹ️  %s\n' "$*" >&2; }
ok() { printf '✅  %s\n' "$*" >&2; }
warn() { printf '⚠️  %s\n' "$*" >&2; }
die() {
  printf '❌  %s\n' "$*" >&2
  exit 1
}

require_root() {
  [ "$(id -u)" -eq 0 ] || die "需要 root 权限，请改用：sudo sh $0 $ORIG_ARGS"
}

# 检测是否已通过 fpk 方式安装（互斥逻辑）
check_fpk_conflict() {
  if [ -d "/var/apps/${BINARY}" ]; then
    die "检测到已通过 fpk 方式安装 ${BINARY} (/var/apps/${BINARY})。\nfpk 安装与传统安装方式互斥，请先在飞牛 fnOS 应用中心卸载 fpk 版本。"
  fi

  if [ -n "${TRIM_APPDEST:-}" ]; then
    die "检测到当前处于 fpk 安装环境 (TRIM_APPDEST=${TRIM_APPDEST})。\nfpk 安装与传统安装方式互斥，请勿在 fpk 环境内执行传统安装脚本。"
  fi
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "缺少命令: $1"
}

# 从仓库 releases 页面解析最新 tag
latest_version() {
  curl -fsSL --connect-timeout 10 --max-time 30 "${REPO}/-/releases" 2>/dev/null \
    | grep -o 'releases/tag/v[0-9][0-9]*\.[0-9][0-9]*\.[0-9][0-9]*' \
    | head -n 1 | sed 's|releases/tag/||' || true
}

installed_version() {
  bin=""
  # 优先 /usr/bin（包管理器安装路径），再 fallback /usr/local/bin
  for p in /usr/bin/${BINARY} /usr/local/bin/${BINARY}; do
    if [ -x "$p" ]; then
      bin="$p"
      break
    fi
  done
  if [ -n "$bin" ]; then
    "$bin" version 2>/dev/null | head -n 1 | awk '{print $2}' || true
  fi
}

# 检测系统包管理器，输出: "deb amd64" / "rpm x86_64" / "apk x86_64"
detect_package_info() {
  arch=$(uname -m)
  case "$arch" in
    x86_64 | amd64)
      pkg_arch="amd64"
      rpm_arch="x86_64"
      ;;
    aarch64 | arm64)
      pkg_arch="arm64"
      rpm_arch="aarch64"
      ;;
    *) die "不支持的 CPU 架构: $arch（当前仅支持 amd64/arm64）" ;;
  esac

  if command -v dpkg >/dev/null 2>&1; then
    printf "deb %s" "$pkg_arch"
  elif command -v rpm >/dev/null 2>&1; then
    printf "rpm %s" "$rpm_arch"
  elif command -v apk >/dev/null 2>&1; then
    printf "apk %s" "$rpm_arch"
  else
    echo ""
  fi
}

# 拼接安装包文件名
package_filename() {
  pkg_type="$1"
  pkg_arch="$2"
  case "$pkg_type" in
    deb) printf "%s_%s.deb" "$BINARY" "$pkg_arch" ;;
    rpm) printf "%s.%s.rpm" "$BINARY" "$pkg_arch" ;;
    apk) printf "%s.%s.apk" "$BINARY" "$pkg_arch" ;;
  esac
}

# 调用包管理器安装指定包文件
install_package() {
  pkg_type="$1"
  pkg_file="$2"
  case "$pkg_type" in
    deb) dpkg -i "$pkg_file" ;;
    rpm) rpm -Uvh --force "$pkg_file" ;;
    apk) apk add --allow-untrusted --force-overwrite "$pkg_file" ;;
  esac
}

# 下载系统安装包并用包管理器安装
download_and_install() {
  need_cmd curl

  pkg_info=$(detect_package_info)
  if [ -z "$pkg_info" ]; then
    die "未检测到支持的包管理器（dpkg/rpm/apk），无法自动安装。"
  fi

  pkg_type=$(echo "$pkg_info" | awk '{print $1}')
  pkg_arch=$(echo "$pkg_info" | awk '{print $2}')

  if [ "$VERSION" = "latest" ]; then
    VERSION=$(latest_version)
    [ -n "$VERSION" ] || die "无法解析最新版本号，请用 --version vX.Y.Z 指定"
  fi

  fileName=$(package_filename "$pkg_type" "$pkg_arch")
  url="${REPO}/-/releases/download/${VERSION}/${fileName}"

  log ""
  info "检测到包管理器: ${pkg_type} (${pkg_arch})"
  info "下载 ${url}"

  tmp=$(mktemp -d)
  pkgFile="${tmp}/${fileName}"
  curl -fsSL --connect-timeout 10 --max-time 300 -o "$pkgFile" "$url" || {
    rm -rf "$tmp"
    die "下载失败：${url}"
  }

  info "安装 ${pkgFile}"
  install_package "$pkg_type" "$pkgFile" || {
    rm -rf "$tmp"
    die "包管理器安装失败"
  }
  rm -rf "$tmp"
}

do_install() {
  require_root "$@"
  check_fpk_conflict

  before=$(installed_version || true)

  # 升级前先停掉正在运行的服务/进程，避免旧版本驻留
  if [ -n "$before" ]; then
    if command -v systemctl >/dev/null 2>&1; then
      if systemctl is-active "$BINARY" >/dev/null 2>&1; then
        info "停止运行中的服务以准备升级..."
        systemctl stop "$BINARY" 2>/dev/null || true
      fi
    fi
    # 兜底：杀掉残留进程
    if command -v pidof >/dev/null 2>&1; then
      pids=$(pidof "$BINARY" 2>/dev/null || true)
      if [ -n "$pids" ]; then
        kill $pids 2>/dev/null || true
        sleep 1
      fi
    fi
  fi

  download_and_install

  # 清理 /usr/local/bin 可能残留的旧版二进制（手动安装遗留）
  if [ -f "/usr/local/bin/${BINARY}" ] && [ -f "/usr/bin/${BINARY}" ]; then
    rm -f "/usr/local/bin/${BINARY}"
  fi

  after=$(installed_version || true)
  log ""
  ok "安装完成：${BINARY} (${after:-未知版本})"

  if [ -n "$before" ] && [ "$before" != "$after" ]; then
    info "升级前版本：${before}（配置已保留）"
  fi

  # 升级后重启服务使新版本生效（postinstall 已自动注册并启动服务）
  if [ -n "$before" ] && [ "$before" != "$after" ]; then
    if command -v systemctl >/dev/null 2>&1; then
      if systemctl is-enabled "$BINARY" >/dev/null 2>&1; then
        info "重启服务以加载新版本..."
        systemctl restart "$BINARY" 2>/dev/null || true
      fi
    fi
  fi

  log ""
  log "配置  ${CONFIG_DIR}/config.yaml"
  log "状态  ${STATE_DIR}/state.yaml"
  log "日志  ${LOG_DIR}/${BINARY}.log"

  if command -v systemctl >/dev/null 2>&1; then
    if systemctl is-active "$BINARY" >/dev/null 2>&1; then
      log ""
      ok "服务已启动并设为开机自启"
    else
      log ""
      warn "服务未运行，手动启动：systemctl enable --now ${BINARY}"
    fi
  else
    log ""
    log "前台运行：${BINARY}（路径由运行模式自动决定）"
  fi

  log ""
  log "服务管理：systemctl status | restart | stop ${BINARY}"
  log "查看版本：${BINARY} version"
  log "体检：    ${BINARY} --check"
  log "升级：    sudo ${BINARY} self-upgrade"
  log "卸载：    sudo apt remove ${BINARY} / sudo rpm -e ${BINARY} / sudo apk del ${BINARY}"
}

do_uninstall() {
  require_root "$@"
  check_fpk_conflict

  bin=""
  for p in /usr/bin/${BINARY} /usr/local/bin/${BINARY}; do
    if [ -x "$p" ]; then
      bin="$p"
      break
    fi
  done

  if [ -n "$bin" ]; then
    "$bin" service stop >/dev/null 2>&1 || true
    "$bin" service uninstall >/dev/null 2>&1 || true
  fi

  rm -f "/etc/systemd/system/${BINARY}.service"
  command -v systemctl >/dev/null 2>&1 && systemctl daemon-reload 2>/dev/null || true

  # 用包管理器卸载
  if command -v dpkg >/dev/null 2>&1; then
    dpkg -r "$BINARY" 2>/dev/null || true
  elif command -v rpm >/dev/null 2>&1; then
    rpm -e "$BINARY" 2>/dev/null || true
  elif command -v apk >/dev/null 2>&1; then
    apk del "$BINARY" 2>/dev/null || true
  fi

  # 确保二进制被删除
  rm -f /usr/local/bin/${BINARY} /usr/bin/${BINARY}
  ok "已卸载 ${BINARY}"

  if [ "$PURGE" -eq 1 ]; then
    rm -rf "$CONFIG_DIR" "$LOG_DIR" "$STATE_DIR"
    ok "已删除配置、日志与状态目录"
  else
    info "配置已保留：${CONFIG_DIR}（彻底删除：rm -rf ${CONFIG_DIR} ${LOG_DIR} ${STATE_DIR}）"
  fi
}

main() {
  ORIG_ARGS="$*"

  while [ $# -gt 0 ]; do
    case "$1" in
      install) ACTION="install" ;;
      upgrade) ACTION="upgrade" ;;
      uninstall) ACTION="uninstall" ;;
      --version | -v)
        shift
        [ $# -gt 0 ] || die "--version 需要指定版本号，如 v0.0.1"
        VERSION="$1"
        ;;
      --purge) PURGE=1 ;;
      -h | --help)
        usage
        exit 0
        ;;
      *) die "未知参数: $1（用 -h 查看用法）" ;;
    esac
    shift
  done

  case "$ACTION" in
    install | upgrade) do_install "$@" ;;
    uninstall) do_uninstall "$@" ;;
  esac
}

main "$@"
