#!/bin/sh
# shellcheck disable=SC3043  # local 不是 POSIX 标准，但 dash / ash / bash 都支持（仓库内 scripts/ci/*.sh 同样用法）
#
# 发布附件「说明」文案生成器（跨仓库通用，复制本文件到任意仓库的 scripts/ci/ 即可）。
#
# 两种用法：
#   1) 命令行：sh scripts/ci/describe-artifact.sh <文件名> [文件名 ...]
#      每行打印「<文件名> | <说明>」，便于人工核对即将写进表格的文案。
#
#   2) 被 source（推荐，接进 render-release-body.sh）：
#        . scripts/ci/describe-artifact.sh
#        DESC="$(describe_artifact "$fname")"       # 直接传文件名
#        # 也兼容旧接口：先解析再无参调用
#        parse_artifact_name "$fname"; DESC="$(describe_artifact)"
#
# 文案集中在下方 DESC_* 变量。各仓库可在 source 之前用同名环境变量覆盖任意一条，
# 无需改动本文件（见下方「文案表」注释）。
#
# 自检：sh scripts/ci/describe-artifact.sh --self-test
#
# ⚠️ 本文件必须保持 POSIX sh 语法：CNB 的 stage 默认用 sh（dash/ash）。
#    不要引入 [[ ]]、数组、BASH_SOURCE、$'...' 这类 bash 专有写法。

set -eu

# ── 文案表（改这里就能改所有仓库的措辞）───────────────────────────────
# 外部在 source 前 export/赋值同名变量即可覆盖任意一条，无需改本文件，例如：
#   DESC_LINUX_DEB='Debian/Ubuntu 安装包（deb）' . scripts/ci/describe-artifact.sh
# 占位符：{APP}=从文件名首段提取的应用名，{EXT}=扩展名。
# 注意：不能用 : "${VAR:=默认值}" 设默认值——默认值里的 {APP} 带 }，会提前结束
# 参数展开；统一用「未设置才赋值」的写法。外部若显式置空（VAR=''）也视为已覆盖。
[ -n "${DESC_MACOS_ZIP+x}" ] || DESC_MACOS_ZIP='macOS 应用包（zip，解压得到 {APP}.app）'
[ -n "${DESC_MACOS_TARGZ+x}" ] || DESC_MACOS_TARGZ='macOS 通用包（tar.gz，解压即用）'
[ -n "${DESC_LINUX_APPIMAGE+x}" ] || DESC_LINUX_APPIMAGE='AppImage 便携版（chmod +x 后直接运行）'
[ -n "${DESC_LINUX_DEB+x}" ] || DESC_LINUX_DEB='Debian/Ubuntu 安装包（dpkg -i）'
[ -n "${DESC_LINUX_RPM+x}" ] || DESC_LINUX_RPM='Fedora/RHEL 安装包（rpm -i）'
[ -n "${DESC_LINUX_PKG_TAR_ZST+x}" ] || DESC_LINUX_PKG_TAR_ZST='Arch Linux 安装包（pacman -U）'
[ -n "${DESC_LINUX_TARGZ+x}" ] || DESC_LINUX_TARGZ='Linux 通用包（tar.gz，解压即用）'
[ -n "${DESC_WINDOWS_INSTALLER+x}" ] || DESC_WINDOWS_INSTALLER='Windows 安装程序（exe，双击安装）'
[ -n "${DESC_WINDOWS_ZIP+x}" ] || DESC_WINDOWS_ZIP='Windows 免安装包（zip，解压运行）'
[ -n "${DESC_WINDOWS_EXE_ZIP+x}" ] || DESC_WINDOWS_EXE_ZIP='Windows 可执行程序包（zip，解压运行 .exe）'
[ -n "${DESC_FNOS_FPK+x}" ] || DESC_FNOS_FPK='飞牛 fnOS 应用包（应用中心 → 手动安装）'
[ -n "${DESC_FALLBACK+x}" ] || DESC_FALLBACK='压缩包（{EXT}），解压即用'

# parse_artifact_name <文件名>
#   解析文件名，结果写入全局变量 PLAT / ARCH / EXT / KIND / APP：
#     PLAT  平台：Linux / macOS / Windows / fnOS / 其它
#     ARCH  架构：x86_64 / ARM64 / 原样
#     EXT   扩展名（含多后缀，如 .pkg.tar.zst / .exe.zip）
#     KIND  installer 表示 Windows 安装程序，其余为空
#     APP   应用名（文件名第一个 - 或 _ 之前的段，用于 macOS 的 .app 名）
#
# 从**末尾**取 os / arch，而不是从前缀切：版本号自带连字符
# （0.2.23-nightly.20261116），按「项目名-版本-os-arch」四段切会把 os 解析成版本号。
# 下划线也统一成连字符，以兼容 项目名_amd64.deb 这类命名。
parse_artifact_name() {
  local fname="$1"
  local stem norm arch rest os
  PLAT=''
  ARCH=''
  EXT=''
  KIND=''
  APP=''

  # 长后缀优先：.pkg.tar.zst / .exe.zip 必须先于 .tar.gz / .zip 判断
  case "${fname}" in
    *.pkg.tar.zst)
      EXT='.pkg.tar.zst'
      stem="${fname%.pkg.tar.zst}"
      ;;
    *.tar.gz)
      EXT='.tar.gz'
      stem="${fname%.tar.gz}"
      ;;
    *.exe.zip)
      EXT='.exe.zip'
      stem="${fname%.exe.zip}"
      ;;
    *.AppImage)
      EXT='.AppImage'
      stem="${fname%.AppImage}"
      ;;
    *.deb)
      EXT='.deb'
      stem="${fname%.deb}"
      ;;
    *.rpm)
      EXT='.rpm'
      stem="${fname%.rpm}"
      ;;
    *.fpk)
      EXT='.fpk'
      stem="${fname%.fpk}"
      ;;
    *.zip)
      EXT='.zip'
      stem="${fname%.zip}"
      ;;
    *.exe)
      EXT='.exe'
      stem="${fname%.exe}"
      ;;
    *) return 0 ;;
  esac

  # Windows 安装程序结尾是 -install.exe：先剥掉，否则 arch 会解析成 amd64-install
  case "${stem}" in
    *-install)
      KIND='installer'
      stem="${stem%-install}"
      ;;
  esac

  norm="$(printf '%s' "${stem}" | tr '_' '-')"
  APP="${norm%%-*}"
  [ -n "${APP}" ] || APP='应用'

  arch="${norm##*-}"
  rest="${norm%-*}"
  os="${rest##*-}"

  case "${arch}" in
    amd64 | x86_64) ARCH='x86_64' ;;
    arm64 | aarch64) ARCH='ARM64' ;;
    *) ARCH="${arch}" ;;
  esac

  case "${os}" in
    linux) PLAT='Linux' ;;
    windows) PLAT='Windows' ;;
    darwin) PLAT='macOS' ;;
    *)
      # 文件名里没有 os 段（如 项目名_amd64.deb）：按扩展名推断平台
      case "${EXT}" in
        .deb | .rpm | .pkg.tar.zst | .AppImage) PLAT='Linux' ;;
        .fpk) PLAT='fnOS' ;;
        .exe | .exe.zip | .zip) PLAT='Windows' ;;
        *) PLAT="${os}" ;;
      esac
      ;;
  esac
}

# describe_artifact [文件名]
#   输出说明文案到 stdout。传了文件名就先自行解析；不传则沿用上一次
#   parse_artifact_name 设置的 PLAT / KIND / EXT / APP（兼容旧调用方式）。
describe_artifact() {
  local key tpl out
  if [ "$#" -gt 0 ]; then
    parse_artifact_name "$1"
  fi

  key="${PLAT}:${KIND}:${EXT}"
  case "${key}" in
    macOS::.zip) tpl="${DESC_MACOS_ZIP}" ;;
    macOS::.tar.gz) tpl="${DESC_MACOS_TARGZ}" ;;
    Linux::.AppImage) tpl="${DESC_LINUX_APPIMAGE}" ;;
    Linux::.deb) tpl="${DESC_LINUX_DEB}" ;;
    Linux::.rpm) tpl="${DESC_LINUX_RPM}" ;;
    Linux::.pkg.tar.zst) tpl="${DESC_LINUX_PKG_TAR_ZST}" ;;
    Linux::.tar.gz) tpl="${DESC_LINUX_TARGZ}" ;;
    Windows:installer:.exe) tpl="${DESC_WINDOWS_INSTALLER}" ;;
    Windows::.exe.zip) tpl="${DESC_WINDOWS_EXE_ZIP}" ;;
    Windows::.zip) tpl="${DESC_WINDOWS_ZIP}" ;;
    fnOS::.fpk) tpl="${DESC_FNOS_FPK}" ;;
    *) tpl="${DESC_FALLBACK}" ;;
  esac

  out="$(printf '%s' "${tpl}" | sed "s|{APP}|${APP}|g; s|{EXT}|${EXT}|g")"
  printf '%s' "${out}"
}

# 直接执行才跑自检/命令行；被 source 时只导出变量与函数。
# 判定只用 $0 的 basename（不能用 BASH_SOURCE，dash 会 Bad substitution）。
# 不能额外放行 `${1:-}=--self-test`：调用方（如 render-release-body.sh）跑自己的
# 自检时 $1 也是 --self-test，source 进来的这份若跟着放行，会跑完自己的自检并
# exit 0，把调用方的自检整个中断。source 时 $0 是调用方脚本名，天然不会误判。
if [ "${0##*/}" != 'describe-artifact.sh' ]; then
  # shellcheck disable=SC2317
  return 0 2>/dev/null || exit 0
fi

if [ "${1:-}" = '--self-test' ]; then
  failures=0

  check_desc() {
    local fname="$1" want="$2" got
    got="$(describe_artifact "${fname}")"
    if [ "${got}" = "${want}" ]; then
      printf '  ✓ %-52s -> %s\n' "${fname}" "${got}"
    else
      printf '  ✗ %-52s\n      期望: %s\n      实际: %s\n' "${fname}" "${want}" "${got}"
      failures=$((failures + 1))
    fi
  }
  check_parse() {
    local fname="$1" want="$2" got
    parse_artifact_name "${fname}"
    got="${PLAT}:${ARCH}:${EXT}:${KIND}"
    if [ "${got}" = "${want}" ]; then
      printf '  ✓ 解析 %-48s -> %s\n' "${fname}" "${got}"
    else
      printf '  ✗ 解析 %-48s 期望 %s，实际 %s\n' "${fname}" "${want}" "${got}"
      failures=$((failures + 1))
    fi
  }

  printf '解析用例\n'
  check_parse 'App-1.2.3-nightly.20260101-darwin-amd64.zip' 'macOS:x86_64:.zip:'
  check_parse 'App-1.2.3-nightly.20260101-linux-amd64.pkg.tar.zst' 'Linux:x86_64:.pkg.tar.zst:'
  check_parse 'App-1.2.3-nightly.20260101-windows-amd64-install.exe' 'Windows:x86_64:.exe:installer'
  check_parse 'App_amd64.deb' 'Linux:x86_64:.deb:'
  check_parse 'App-arm64.fpk' 'fnOS:ARM64:.fpk:'
  check_parse 'App-windows-arm64.exe.zip' 'Windows:ARM64:.exe.zip:'

  printf '说明文案用例\n'
  # 桌面应用（版本带连字符，应用名从文件名首段提取）
  check_desc 'App-1.2.3-nightly.20260101-darwin-amd64.zip' 'macOS 应用包（zip，解压得到 App.app）'
  check_desc 'App-1.2.3-nightly.20260101-darwin-arm64.zip' 'macOS 应用包（zip，解压得到 App.app）'
  check_desc 'App-1.2.3-nightly.20260101-linux-amd64.AppImage' 'AppImage 便携版（chmod +x 后直接运行）'
  check_desc 'App-1.2.3-nightly.20260101-linux-amd64.deb' 'Debian/Ubuntu 安装包（dpkg -i）'
  check_desc 'App-1.2.3-nightly.20260101-linux-amd64.rpm' 'Fedora/RHEL 安装包（rpm -i）'
  check_desc 'App-1.2.3-nightly.20260101-linux-amd64.pkg.tar.zst' 'Arch Linux 安装包（pacman -U）'
  check_desc 'App-1.2.3-nightly.20260101-windows-amd64-install.exe' 'Windows 安装程序（exe，双击安装）'
  # 下划线架构 + fnOS 包
  check_desc 'App_amd64.deb' 'Debian/Ubuntu 安装包（dpkg -i）'
  check_desc 'App-arm64.fpk' '飞牛 fnOS 应用包（应用中心 → 手动安装）'
  # 通用二进制包——解压即用 / 解压运行（二进制分发没有「安装」这一步）
  check_desc 'App-linux-amd64.tar.gz' 'Linux 通用包（tar.gz，解压即用）'
  check_desc 'App-darwin-arm64.tar.gz' 'macOS 通用包（tar.gz，解压即用）'
  check_desc 'App-windows-amd64.exe.zip' 'Windows 可执行程序包（zip，解压运行 .exe）'
  check_desc 'App-linux-arm64.tar.gz' 'Linux 通用包（tar.gz，解压即用）'
  check_desc 'App-windows-arm64.exe.zip' 'Windows 可执行程序包（zip，解压运行 .exe）'

  printf '覆盖用例\n'
  old_desc="${DESC_LINUX_DEB}"
  DESC_LINUX_DEB='Debian/Ubuntu 安装包（deb）'
  check_desc 'App-1.2.3-linux-amd64.deb' 'Debian/Ubuntu 安装包（deb）'
  DESC_LINUX_DEB="${old_desc}"

  if [ "${failures}" -ne 0 ]; then
    printf '\n自检失败: %d 项\n' "${failures}" >&2
    exit 1
  fi
  printf '\n自检通过\n'
  exit 0
fi

if [ "$#" -lt 1 ]; then
  printf '用法: %s <文件名> [文件名 ...]\n       %s --self-test\n' "${0}" "${0}" >&2
  exit 2
fi

for f in "$@"; do
  printf '%s | %s\n' "${f}" "$(describe_artifact "${f}")"
done
