#!/usr/bin/env bash
#
# 版本号归一化 + 预发布判定。
#
# 事实来源：GitHub Actions 的 release.yml / build-image.yml 与 CNB 的
# .cnb/workflows/release.yml / build-image.yml 都 source 这一份实现，避免四处各写一套
# grep 正则、判断口径慢慢跑偏：
#
#   v1.2.3 / 1.2.3        -> TAG=v1.2.3        PRERELEASE=false   正式版
#   v1.2.3-rc.1           -> TAG=v1.2.3-rc.1   PRERELEASE=true    预发布
#   v1.2.3-beta.1         -> TAG=v1.2.3-beta.1 PRERELEASE=true    预发布
#   v1.2.3-alpha.1        -> TAG=v1.2.3-alpha.1 PRERELEASE=true   预发布
#
# 只用版本号后缀判定，不接受「是不是预发布」这种布尔输入：布尔输入会和版本号打架。
#
# 用法（被 source）：
#   . scripts/ci/resolve-prerelease.sh
#   resolve_version "${VERSION}"
#   printf '%s %s\n' "${TAG}" "${PRERELEASE}"
#
# 直接执行可以跑内置用例自检（改了正则以后先跑它）：
#   bash scripts/ci/resolve-prerelease.sh --self-test
#
# ⚠️ 本文件必须保持 POSIX sh 语法：CNB 的 stage 默认用 `sh`（可能是 dash/ash）执行，
# GitHub Actions 用 bash。不要引入 [[ ]]、数组、BASH_SOURCE、$'...' 这类 bash 专有写法。

# resolve_version <版本号>
#   归一化版本号（补 v 前缀）并判定是否预发布；
#   结果写入全局变量 TAG / PKG_VERSION / PRERELEASE / VERSION_TYPE / PRE_NOTE
#   并导出给后续步骤。
#   PKG_VERSION 是去掉 v 的版本号 —— deb/fpk 的包元数据、前端 package.json 用的都是它
#   （Makefile 里 PKG_VERSION = patsubst v%,% 系同一条规则，两处必须一致）。
#   版本号为空时返回 1，由调用方决定是 `set -e` 直接失败还是自行处理。
resolve_version() {
  local input="${1:-}"

  # 去掉所有空白字符：workflow_dispatch 输入框粘贴时经常带上首尾空格/换行
  input="$(printf '%s' "${input}" | tr -d '[:space:]')"

  if [ -z "${input}" ]; then
    printf '错误: 版本号不能为空。示例: v1.2.3 / v1.2.3-rc.1 / v1.2.3-beta.1 / v1.2.3-alpha.1\n' >&2
    return 1
  fi

  # 字符白名单：版本号最终会变成 git tag、Docker tag 和 JSON 字段值。
  # 含引号/反斜杠/换行的输入既不是合法版本号，拼进 JSON 时还会破坏结构（甚至注入字段），
  # 所以在这里一次性挡掉。允许的字符与 Docker tag 的字符集一致。
  if printf '%s' "${input}" | grep -qvE '^[0-9A-Za-z._+-]+$'; then
    printf '错误: 版本号含非法字符（只允许字母、数字、. _ + -）: %s\n' "${input}" >&2
    return 1
  fi

  # 统一补 v 前缀：输入 1.2.3 与 v1.2.3 等价；大写 V 前缀也算已经有了，
  # 否则会拼出 vV1.2.3 这种既不像 tag 又不像版本号的东西
  case "${input}" in
    v* | V*) TAG="${input}" ;;
    *) TAG="v${input}" ;;
  esac

  # 包版本 = 去掉 v 前缀。deb/fpk 的元数据、前端 package.json 都用它
  # （Makefile 里是 PKG_VERSION = $(patsubst v%,%,$(VERSION))，两边口径必须一致）。
  PKG_VERSION="${TAG#v}"
  PKG_VERSION="${PKG_VERSION#V}"

  # 后缀判定：-rc / -beta / -alpha（大小写不敏感，rc1 / rc.1 / 裸 rc 都算预发布）
  # 匹配要求后缀出现在版本号末尾，避免 v1.2.3-source、v1.2.3-hotfix 这类被误判
  if printf '%s' "${TAG}" | grep -qiE -- '-(rc|beta|alpha)([.-]?[0-9A-Za-z.-]*)$'; then
    PRERELEASE="true"
    VERSION_TYPE="预发布"
  else
    PRERELEASE="false"
    VERSION_TYPE="正式版"
  fi

  PRE_NOTE="版本号: ${TAG}（${VERSION_TYPE}，prerelease=${PRERELEASE}）"
  export TAG PKG_VERSION PRERELEASE VERSION_TYPE PRE_NOTE

  return 0
}

# 下面是「直接执行」才跑的自检，被 source 时只导出函数就收工。
#
# 判定方式不能用 `[ "${BASH_SOURCE[0]}" = "${0}" ]`：数组下标是 bash 专有语法，CNB 的 stage
# 默认用 sh（dash）执行，`${BASH_SOURCE[0]:-}` 会直接 "Bad substitution" 让整个 stage 挂掉。
# 这里改用 $0 的 basename：直接执行时 $0 是本文件名，被 source 时 $0 是调用方
# （bash / sh / /bin/dash 或调用脚本），在 POSIX 范围内可靠。
# 另外放行显式的 --self-test：source 的调用方不会传这个参数，不会误触发。
if [ "${0##*/}" != 'resolve-prerelease.sh' ] && [ "${1:-}" != '--self-test' ]; then
  # `return` 在被 source 时生效；万一本文件被 `bash < file` 之类方式执行（$0 不是文件名、
  # return 会失败），退回 exit 0。shellcheck 认为 return 不会失败，故显式关掉该提示。
  # shellcheck disable=SC2317
  return 0 2>/dev/null || exit 0
fi

if [ "${1:-}" != "--self-test" ]; then
  printf '用法: %s --self-test\n通常不需要单独执行，CI 里是 source 使用：%s\n' "${0}" '. scripts/ci/resolve-prerelease.sh' >&2
  exit 2
fi

failures=0

check_version() {
  local input="$1" want_tag="$2" want_pre="$3"
  # 期望的包版本 = tag 去掉前导 v/V（与 Makefile 的 patsubst v% , % 一致）
  local want_pkg="${want_tag#v}"
  want_pkg="${want_pkg#V}"

  if ! resolve_version "${input}" 2>/dev/null; then
    printf '  ✗ %-20s 解析失败（期望 %s）\n' "${input:-<空>}" "${want_tag}"
    failures=$((failures + 1))
    return 0
  fi

  if [ "${TAG}" = "${want_tag}" ] && [ "${PRERELEASE}" = "${want_pre}" ] \
    && [ "${PKG_VERSION}" = "${want_pkg}" ]; then
    printf '  ✓ %-20s -> %-20s pkg=%-14s prerelease=%s\n' \
      "${input}" "${TAG}" "${PKG_VERSION}" "${PRERELEASE}"
  else
    printf '  ✗ %-20s -> %-20s pkg=%s prerelease=%s（期望 %s / 包版本 %s / %s）\n' \
      "${input}" "${TAG}" "${PKG_VERSION}" "${PRERELEASE}" "${want_tag}" "${want_pkg}" "${want_pre}"
    failures=$((failures + 1))
  fi
}

# source 只带进函数，不调用 resolve_version 就什么都算不出来。
# 五个平台的流水线都靠这个调用，漏一处第一步就会在 set -u 下红掉 —— 这里把约定钉死。
if [ -n "${TAG:-}" ]; then
  printf '  ✗ 仅 source 就已经设置了 TAG=%s，函数不该有副作用\n' "${TAG}"
  failures=$((failures + 1))
else
  printf '  ✓ 仅 source 不产生 TAG（调用方必须显式 resolve_version）\n'
fi

check_version 'v1.2.3' 'v1.2.3' 'false'
check_version '1.2.3' 'v1.2.3' 'false'
check_version 'v1.2.3-rc.1' 'v1.2.3-rc.1' 'true'
check_version 'v1.2.3-beta.1' 'v1.2.3-beta.1' 'true'
check_version 'v1.2.3-alpha.1' 'v1.2.3-alpha.1' 'true'
check_version 'v1.2.3-rc' 'v1.2.3-rc' 'true'
check_version 'V1.2.3-RC.1' 'V1.2.3-RC.1' 'true'
check_version 'v1.2.3-alpha.1.2' 'v1.2.3-alpha.1.2' 'true'
check_version ' v1.2.3 ' 'v1.2.3' 'false'
# 不带 rc/beta/alpha 后缀的一律是正式版
check_version 'v1.2.3-hotfix' 'v1.2.3-hotfix' 'false'
check_version 'v2.0.0-core' 'v2.0.0-core' 'false'

# 非法输入必须被拒绝，不能漏到 git tag / Docker tag / JSON 里：
# 空版本号历史上会打出 :v 这种非法标签；引号/分号会破坏 optionsFrom 的 JSON 结构。
# 注意不要把 "v1.2.3 beta" 当非法输入：空白会先被去掉（粘贴常带空格），结果 v1.2.3beta 合法
for bad_input in '' 'v1.2.3"x' 'v1.2.3;rm -rf /' 'v1.2.3@1' 'v1/2/3'; do
  if resolve_version "${bad_input}" 2>/dev/null; then
    printf '  ✗ %-20s 应当报错，实际通过（TAG=%s）\n' "${bad_input:-<空>}" "${TAG}"
    failures=$((failures + 1))
  else
    printf '  ✓ %-20s 被拒绝（返回非 0）\n' "${bad_input:-<空>}"
  fi
done

if [ "${failures}" -ne 0 ]; then
  printf '\n自检失败: %d 项\n' "${failures}" >&2
  exit 1
fi

printf '\n自检通过\n'
