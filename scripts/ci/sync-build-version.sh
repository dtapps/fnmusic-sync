#!/bin/sh
# shellcheck disable=SC3043  # local 不是 POSIX 标准，但 dash / ash / bash 都支持（仓库内 scripts/ci/*.sh 同样用法）
# 把发布版本号写进 build/config.yml 的 info.version / ios.version。
#
# 为什么不在 workflow 里直接内联 sed：
#   1. build/config.yml 里的值用的是**单引号**（version: '0.0.1'），早先 workflow 里内联的
#      正则只匹配双引号（"[^"]*"）。sed 一行都没匹配到，但**退出码仍然是 0**——「版本号
#      没写进去」被静默吞掉。Linux job 因为后面紧跟一句 grep（grep 没匹配到 → 退出码 1）
#      才暴露成 "Process completed with exit code 1"；macOS/Windows job 那条 sed 同样
#      没生效，却一路绿灯，打出来的 App 版本号还是 0.0.1。
#   2. build/config.yml **顶格**的 version: '3' 是 wails3 配置的 schema 版本，绝不能被当成
#      应用版本改掉，所以只改**缩进**的 version 行（info.version / ios.version）。
#   3. macOS 用 BSD sed、Linux 容器用 GNU sed，`sed -i` 的后缀参数两边语义不同；
#      本脚本改用「临时文件 + cp 覆盖」，两端行为一致，且保留原文件属主与权限
#      （容器 job 的工作区是 bind mount，sed -i 换 inode 会把属主变成 root）。
#
# 用法：
#   sh scripts/ci/sync-build-version.sh 0.2.9 [build/config.yml]
#
# 改了这里先跑自检：
#   sh scripts/ci/sync-build-version.sh --self-test
#
# ⚠️ 本文件必须保持 POSIX sh 语法：GitHub Actions 用 bash，CNB 的 stage 默认用 sh
# （可能是 dash/ash）。不要引入 [[ ]]、数组、BASH_SOURCE、$'...' 这类 bash 专有写法。

set -eu

die() {
  # 出错路径统一清掉临时文件（TMP_CLEANUP 由 sync_version 设置）。
  if [ -n "${TMP_CLEANUP:-}" ]; then
    rm -f "${TMP_CLEANUP}" || true
  fi
  printf 'error: %s\n' "$*" >&2
  exit 1
}

# sync_version <版本号> <配置文件>
#   就地改写配置文件里的应用版本号。改完必须校验：校验不过直接失败，不写回原文件。
sync_version() {
  local version="${1:-}"
  local config="${2:-}"

  # 只接受 X.Y.Z 三段纯数字：info.version 会流到 Windows NSIS 的 VIProductVersion
  # （模板自己补 ".0" 成 4 段）和 macOS 的 CFBundleShortVersionString，两者都只吃纯数字。
  printf '%s' "${version}" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$' \
    || die "版本号必须是 X.Y.Z 三段纯数字，收到: ${version}"
  [ -n "${config}" ] || die "缺少配置文件路径"
  [ -f "${config}" ] || die "找不到配置文件: ${config}"

  local tmp_out
  tmp_out="$(mktemp "${TMPDIR:-/tmp}/certflow-config.XXXXXX")"
  TMP_CLEANUP="${tmp_out}"

  # 只替换「缩进的 version 行」：
  #   ^[[:space:]]+ 要求行首至少一个空白 → 顶格的 version: '3'（wails3 schema 版本）
  #     天然不匹配，不会被误改
  #   [^[:space:]#]+ 匹配值本身，不关心是单引号还是双引号（换引号不影响 YAML 语义）
  #   行尾注释（如 "# The application version"）原样保留
  sed -E "s|^([[:space:]]+)version:[[:space:]]*[^[:space:]#]+|\1version: '${version}'|" \
    "${config}" >"${tmp_out}"

  # 校验 1：应用版本确实写进去了（info.version / ios.version 都算）
  grep -Eq "^[[:space:]]+version:[[:space:]]*'${version}'[[:space:]]*(#.*)?$" "${tmp_out}" \
    || die "版本号写入失败: ${config}"

  # 校验 2：所有缩进的 version 行都必须是新版本（不能有漏改的旧值）
  local stale
  stale="$(grep -E "^[[:space:]]+version:" "${tmp_out}" \
    | grep -Ev "version:[[:space:]]*'${version}'[[:space:]]*(#.*)?$" || true)"
  [ -z "${stale}" ] || die "仍有未更新的 version 行: ${stale}"

  # 校验 3：wails3 配置的 schema 版本没被误改
  grep -Eq "^version:[[:space:]]*'3'[[:space:]]*$" "${tmp_out}" \
    || die "wails3 配置 schema 版本（顶格 version: '3'）缺失或被误改: ${config}"

  # cp 覆盖而不是 mv：目标是已存在文件，cp 只截断重写，保留原文件的属主与权限
  cp "${tmp_out}" "${config}"
  rm -f "${tmp_out}"
  TMP_CLEANUP=""

  printf '✔ %s 版本号已同步为 %s\n' "${config}" "${version}"
}

# ── 自检 ─────────────────────────────────────────────────────────────────────

fixture_single_quote() {
  cat <<'EOF'
# This file contains the configuration for this project.
version: '3'

info:
  companyName: 'DTApp'
  productName: 'CertFlow'
  version: '0.0.1' # The application version

ios:
  bundleID: 'net.dtapp.certflow'
  version: '0.0.1'
EOF
}

fixture_double_quote() {
  cat <<'EOF'
version: '3'

info:
  productName: 'CertFlow'
  version: "0.0.1" # The application version

ios:
  version: "0.0.1"
EOF
}

self_test() {
  local failures=0
  local dir
  dir="$(mktemp -d "${TMPDIR:-/tmp}/certflow-sync.XXXXXX")"
  # shellcheck disable=SC2064
  trap "rm -rf '${dir}'" EXIT

  check() {
    local name="$1" want="$2" got="$3"
    if [ "${want}" = "${got}" ]; then
      printf '  ✓ %s\n' "${name}"
    else
      printf '  ✗ %s（期望 %s，实际 %s）\n' "${name}" "${want}" "${got}"
      failures=$((failures + 1))
    fi
  }

  printf 'sync-build-version 自检\n'

  # 用例 1：单引号（仓库当前写法）
  local cfg="${dir}/single.yml"
  fixture_single_quote >"${cfg}"
  sync_version '0.2.9' "${cfg}" >/dev/null
  check '单引号：info.version 已更新' \
    "  version: '0.2.9' # The application version" "$(grep -F "version: '0.2.9' # The application version" "${cfg}" | head -1)"
  check '单引号：ios.version 已更新' \
    "  version: '0.2.9'" "$(grep -xF "  version: '0.2.9'" "${cfg}" | head -1)"
  check '单引号：顶格 schema 版本未被动' \
    "version: '3'" "$(grep -F "version: '3'" "${cfg}" | head -1)"
  check '单引号：更新后无残留旧值' \
    '0' "$(grep -c "0\.0\.1" "${cfg}" || true)"

  # 用例 2：双引号（旧写法，也必须能改）
  cfg="${dir}/double.yml"
  fixture_double_quote >"${cfg}"
  sync_version '0.2.9' "${cfg}" >/dev/null
  check '双引号：info.version 已更新' \
    "  version: '0.2.9' # The application version" "$(grep -F "version: '0.2.9' # The application version" "${cfg}" | head -1)"
  check '双引号：顶格 schema 版本未被动' \
    "version: '3'" "$(grep -F "version: '3'" "${cfg}" | head -1)"

  # 用例 3：非法版本号必须被拒（预发布串带后缀，直接流到 NSIS 会报 invalid VIFileVersion）
  # 预期失败的调用放进子 shell：die 里的 exit 1 只会结束子 shell，不会带走整个自检。
  cfg="${dir}/invalid.yml"
  fixture_single_quote >"${cfg}"
  if (sync_version '0.2.9-rc.1' "${cfg}") >/dev/null 2>&1; then
    printf '  ✗ 非法版本号 0.2.9-rc.1 应被拒绝\n'
    failures=$((failures + 1))
  else
    printf '  ✓ 非法版本号 0.2.9-rc.1 被拒绝\n'
  fi

  # 用例 4：文件不存在必须被拒
  if (sync_version '0.2.9' "${dir}/nope.yml") >/dev/null 2>&1; then
    printf '  ✗ 缺失的配置文件应被拒绝\n'
    failures=$((failures + 1))
  else
    printf '  ✓ 缺失的配置文件被拒绝\n'
  fi

  if [ "${failures}" -eq 0 ]; then
    printf '全部通过\n'
    return 0
  fi
  printf '%s 个用例失败\n' "${failures}"
  return 1
}

# 直接执行时：--self-test 跑自检，否则按「版本号 [配置文件]」使用。
# 判定用 $0 的 basename（不能用 BASH_SOURCE，dash 会 Bad substitution）。
if [ "${0##*/}" = 'sync-build-version.sh' ]; then
  if [ "${1:-}" = '--self-test' ]; then
    self_test
    exit $?
  fi
  if [ "$#" -lt 1 ] || [ "$#" -gt 2 ]; then
    printf '用法: %s <版本号> [配置文件]\n       %s --self-test\n' "${0}" "${0}" >&2
    exit 2
  fi
  sync_version "${1}" "${2:-build/config.yml}"
  exit $?
fi
