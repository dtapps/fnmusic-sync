#!/bin/sh
# fnmusic-sync 安装 / 升级 / 卸载脚本（必须以 root 运行）
#
# 用法：
#   sudo ./install.sh                       安装（或覆盖安装）最新版
#   sudo ./install.sh upgrade               升级二进制，保留配置
#   sudo ./install.sh upgrade --version v1.2.3   升级到指定版本
#   sudo ./install.sh uninstall             卸载二进制与服务（保留配置）
#   sudo ./install.sh uninstall --purge     连配置一起删除
#
# 可选参数：
#   --dir <目录>          二进制安装目录，默认 /usr/local/bin
#   --config-dir <目录>   配置目录，默认 /etc/fnmusic-sync
#   --log-dir <目录>      日志目录，默认 /var/log/fnmusic-sync
#   --state-dir <目录>    状态目录，默认 /var/lib/fnmusic-sync
#   --file <文件>         使用本地 tar.gz 或二进制，不联网下载
#   --no-service          不安装系统服务
#   -h, --help            查看帮助
#
# 环境变量：
#   FNMUSIC_REPO   覆盖仓库地址（默认 https://cnb.cool/dtapp/fnmusic-sync）

set -eu

REPO="${FNMUSIC_REPO:-https://cnb.cool/dtapp/fnmusic-sync}"
BINARY="fnmusic-sync"
INSTALL_DIR="/usr/local/bin"
CONFIG_DIR="/etc/fnmusic-sync"
LOG_DIR="/var/log/fnmusic-sync"
STATE_DIR="/var/lib/fnmusic-sync"
VERSION="latest"
ACTION="install"
LOCAL_FILE=""
NO_SERVICE=0
PURGE=0

usage() {
    sed -n '2,21p' "$0" | sed 's/^# \{0,1\}//'
}

log()  { printf '%s\n' "$*" >&2; }
info() { printf 'ℹ️  %s\n' "$*" >&2; }
ok()   { printf '✅ %s\n' "$*" >&2; }
warn() { printf '⚠️  %s\n' "$*" >&2; }
die()  { printf '❌ %s\n' "$*" >&2; exit 1; }

require_root() {
    [ "$(id -u)" -eq 0 ] || die "需要 root 权限，请改用：sudo sh $0 $ORIG_ARGS"
}

need_cmd() {
    command -v "$1" >/dev/null 2>&1 || die "缺少命令: $1"
}

# 平台探测：输出 "<os>-<arch>"，与 Release 附件名 fnmusic-sync-<os>-<arch>.tar.gz 对应
detect_platform() {
    os=$(uname -s | tr 'A-Z' 'a-z')
    case "$os" in
        linux)  os="linux" ;;
        darwin) os="darwin" ;;
        *)      die "不支持的操作系统: $os" ;;
    esac

    arch=$(uname -m)
    case "$arch" in
        x86_64|amd64) arch="amd64" ;;
        aarch64|arm64) arch="arm64" ;;
        armv7l|armv6l|arm) arch="arm" ;;
        i386|i686) arch="386" ;;
        mips) arch="mips" ;;
        mipsel) arch="mipsle" ;;
        *) die "不支持的 CPU 架构: $arch" ;;
    esac

    printf '%s-%s' "$os" "$arch"
}

# 从仓库 releases 页面解析最新 tag
latest_version() {
    curl -fsSL --connect-timeout 10 --max-time 30 "${REPO}/-/releases" 2>/dev/null \
        | grep -o 'releases/tag/v[0-9][0-9]*\.[0-9][0-9]*\.[0-9][0-9]*' \
        | head -n 1 | sed 's|releases/tag/||' || true
}

installed_version() {
    if [ -x "${INSTALL_DIR}/${BINARY}" ]; then
        "${INSTALL_DIR}/${BINARY}" version 2>/dev/null | head -n 1 | awk '{print $2}' || true
    fi
}

# 下载并解压 release 包，输出解压出的二进制路径
fetch_binary() {
    tmp=$(mktemp -d)

    if [ -n "$LOCAL_FILE" ]; then
        case "$LOCAL_FILE" in
            *.tar.gz|*.tgz)
                tar -xzf "$LOCAL_FILE" -C "$tmp"
                ;;
            *)
                cp "$LOCAL_FILE" "${tmp}/${BINARY}"
                chmod +x "${tmp}/${BINARY}"
                ;;
        esac
    else
        platform=$(detect_platform)

        if [ "$VERSION" = "latest" ]; then
            VERSION=$(latest_version)
            [ -n "$VERSION" ] || die "无法解析最新版本号，请用 --version vX.Y.Z 指定"
        fi

        url="${REPO}/-/releases/download/${VERSION}/${BINARY}-${platform}.tar.gz"
        info "下载 ${url}"
        curl -fsSL --connect-timeout 10 --max-time 300 -o "${tmp}/pkg.tar.gz" "$url" || die "下载失败：${url}"
        tar -xzf "${tmp}/pkg.tar.gz" -C "$tmp"
    fi

    bin=$(find "$tmp" -type f -name "${BINARY}*" | head -n 1)
    [ -n "$bin" ] || { rm -rf "$tmp"; die "安装包中未找到 ${BINARY} 二进制"; }

    chmod +x "$bin"
    printf '%s' "$bin"
}

# 目录：配置 /etc/fnmusic-sync、日志 /var/log/fnmusic-sync、状态 /var/lib/fnmusic-sync
prepare_dirs() {
    for d in "$CONFIG_DIR" "$LOG_DIR" "$STATE_DIR" "$INSTALL_DIR"; do
        mkdir -p "$d"
    done
}

# 配置文件：已存在则原样保留（升级绝不覆盖手填的 token）。
# 不从仓库下载任何文件——默认配置由程序首次运行时自己生成（SaveDefault）。
prepare_config() {
    cfg="${CONFIG_DIR}/config.yaml"

    if [ -f "$cfg" ]; then
        info "保留现有配置：${cfg}"
        return 0
    fi

    info "首次运行 ${BINARY} 会自动在 ${cfg} 生成默认配置（含 lastfm / listenbrainz 段）"
}

# 系统服务交给 Go 库（kardianos/service）：fnmusic-sync service install
install_service() {
    [ "$NO_SERVICE" -eq 1 ] && return 0

    bin="${INSTALL_DIR}/${BINARY}"

    if [ -f "/etc/systemd/system/${BINARY}.service" ]; then
        info "systemd 服务已存在，保持原样"
        if systemctl is-active --quiet "$BINARY" 2>/dev/null; then
            "$bin" service restart >/dev/null 2>&1 || true
            ok "服务已重启"
        fi

        return 0
    fi

    if "$bin" service install; then
        ok "已安装系统服务（${BINARY}.service）"
    else
        warn "服务安装失败，可手动执行：${bin} service install"
    fi
}

do_install() {
    require_root "$@"
    need_cmd tar
    [ -n "$LOCAL_FILE" ] || need_cmd curl

    before=$(installed_version || true)
    bin=$(fetch_binary)
    tmp_dir=$(dirname "$bin")

    prepare_dirs
    install -m 0755 "$bin" "${INSTALL_DIR}/${BINARY}"
    rm -rf "$tmp_dir"

    prepare_config
    install_service

    after=$(installed_version || true)
    log ""
    ok "安装完成：${INSTALL_DIR}/${BINARY} (${after:-未知版本})"

    if [ "$ACTION" = "upgrade" ] && [ -n "$before" ]; then
        info "升级前版本：${before}（配置已保留）"
    fi

    log ""
    log "配置  ${CONFIG_DIR}/config.yaml"
    log "状态  ${STATE_DIR}/state.yaml"
    log "日志  ${LOG_DIR}/${BINARY}.log"

    if [ "$NO_SERVICE" -eq 1 ]; then
        log ""
        log "前台运行：${BINARY} --config ${CONFIG_DIR}/config.yaml --state ${STATE_DIR}/state.yaml"
    elif command -v systemctl >/dev/null 2>&1; then
        log ""
        log "启动并设置开机自启：systemctl enable --now ${BINARY}"
    fi

    log ""
    log "查看版本：${BINARY} version"
    log "体检：    ${BINARY} --check"
}

do_uninstall() {
    require_root "$@"

    bin="${INSTALL_DIR}/${BINARY}"

    if [ -x "$bin" ]; then
        "$bin" service stop >/dev/null 2>&1 || true
        "$bin" service uninstall >/dev/null 2>&1 || true
    fi

    rm -f "/etc/systemd/system/${BINARY}.service"
    command -v systemctl >/dev/null 2>&1 && systemctl daemon-reload 2>/dev/null || true

    rm -f "$bin"
    ok "已卸载 ${bin}"

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
            install|upgrade|uninstall) ACTION="$1" ;;
            --version|-v)
                shift
                [ $# -gt 0 ] || die "--version 需要指定版本号，如 v0.0.1"
                VERSION="$1"
                ;;
            --dir)
                shift
                [ $# -gt 0 ] || die "--dir 需要指定目录"
                INSTALL_DIR="$1"
                ;;
            --config-dir)
                shift
                [ $# -gt 0 ] || die "--config-dir 需要指定目录"
                CONFIG_DIR="$1"
                ;;
            --log-dir)
                shift
                [ $# -gt 0 ] || die "--log-dir 需要指定目录"
                LOG_DIR="$1"
                ;;
            --state-dir)
                shift
                [ $# -gt 0 ] || die "--state-dir 需要指定目录"
                STATE_DIR="$1"
                ;;
            --file)
                shift
                [ $# -gt 0 ] || die "--file 需要指定文件"
                LOCAL_FILE="$1"
                ;;
            --no-service) NO_SERVICE=1 ;;
            --purge) PURGE=1 ;;
            -h|--help) usage; exit 0 ;;
            *) die "未知参数: $1（用 -h 查看用法）" ;;
        esac
        shift
    done

    case "$ACTION" in
        install|upgrade) do_install "$@" ;;
        uninstall) do_uninstall "$@" ;;
    esac
}

main "$@"
