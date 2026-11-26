#!/bin/sh
# 模板脚本拉取器：把 ci-templates 里的脚本按「与模板仓库完全一致的相对路径」
# 落到当前仓库（默认 scripts/ci/），供 CI stage 直接 sh scripts/ci/xxx.sh 调用。
#
# 背景：ci-templates 在 CNB / GitHub / GitLab / Gitee 各放一份，内容一致，
#       只有平台差异（下载地址格式）；同时发布到 npm（@dtapp/ci-templates），
#       仓库源全部取不到时还能靠 npm CDN 或国内镜像兜底。
#       本文件下方的清单决定要拉哪些脚本；业务仓库只需保留本文件，不必手工复制、同步。
#
# 用法（在任意仓库根目录执行）：
#   sh scripts/ci/fetch-scripts.sh                    # 同步：缺失的下载，已存在的比 sha256，不同则更新
#   sh scripts/ci/fetch-scripts.sh --force            # 不做比对，已存在也直接覆盖
#   sh scripts/ci/fetch-scripts.sh --check            # 只查存在性（不下载、不比对），缺失时退出码 1
#   sh scripts/ci/fetch-scripts.sh --list             # 打印清单与候选源顺序
#   sh scripts/ci/fetch-scripts.sh --source gitlab    # 指定起点源（见下方源清单）
#   sh scripts/ci/fetch-scripts.sh --npm-version 0.0.1-nightly.20261126  # 指定 npm 包版本或 dist-tag
#   sh scripts/ci/fetch-scripts.sh --no-fallback      # 只用起点源，失败不换源
#   sh scripts/ci/fetch-scripts.sh delete-release.sh  # 只处理指定脚本（可多个，也可写全路径）
#
# 源选择与回退：
#   1) --source <平台> / 环境变量 CI_TEMPLATES_SOURCE 显式指定「起点源」
#   2) 否则自动探测当前 CI 平台（CNB / GitHub / Gitee / GitLab）作为起点
#   3) 一个都探测不到（如本地开发）：按 CNB → GitHub → Gitee → GitLab 取第一个，即默认 CNB
#   4) 起点源取不到文件时，其余源按固定顺序 SOURCE_ORDER 依次尝试（四个仓库源始终
#      排在 npm 源之前），直到拿到或全部失败；回退是「每个文件独立」的，
#      所以某个源缺某个脚本也能自动从别的源补齐
#      --no-fallback（或 CI_TEMPLATES_NO_FALLBACK=1）可关掉回退，只用起点源
#
# 源清单（内容一致，按 SOURCE_ORDER 顺序尝试）：
# ① 仓库源：跟 CI_TEMPLATES_REF（默认 main）走，代码最新
#   CNB    ：https://cnb.cool/dtapp/ci-templates       /-/git/raw/<ref>/<path>
#   GitHub ：https://github.com/dtapps/ci-templates    raw.githubusercontent.com/dtapps/ci-templates/<ref>/<path>
#   Gitee  ：https://gitee.com/dtapps/ci-templates     /raw/<ref>/<path>（302 到内容域，靠 -L 跟随）
#   GitLab ：https://gitlab.com/dtapps/ci-templates    /-/raw/<ref>/<path>
#
# ② npm 源：跟 npm 包版本走（CI_TEMPLATES_NPM_VERSION，默认 latest 标签）
#   jsdelivr：https://cdn.jsdelivr.net/npm/@dtapp/ci-templates@<ver>/<path>
#   unpkg   ：https://unpkg.com/@dtapp/ci-templates@<ver>/<path>
#             （https://www.npmjs.com/package/@dtapp/ci-templates 与 app.unpkg.com 只是
#               包页面/文件浏览界面，真正能按路径取文件的是这两个 CDN）
#   npm     ：https://registry.npmjs.org/@dtapp/ci-templates/-/ci-templates-<ver>.tgz
#             （官方 registry 没有单文件接口，只能下整包然后用 tar 取单个文件）
#
# ③ npm 镜像源：②全部失败才用，国内网络更稳
#   npmmirror：https://registry.npmmirror.com/@dtapp/ci-templates/-/ci-templates-<ver>.tgz
#   tencent  ：https://mirrors.cloud.tencent.com/npm/@dtapp/ci-templates/-/ci-templates-<ver>.tgz
#              （镜像同样只提供整包 tgz，每次运行每个镜像最多下载一次并复用）
#
# 平台探测（只看各平台强特征变量，避免业务仓库里配的 GITEE_TOKEN 之类被误判成 CI）：
#   CNB     CNB=true，或 CNB_BUILD_ID / CNB_PIPELINE_ID / CNB_BUILD_WORKSPACE 任一非空
#   GitHub  GITHUB_ACTIONS=true
#   Gitee   GITEE_CI / GITEE_GO_PIPELINE_ID 任一非空
#   GitLab  GITLAB_CI=true
#
# 环境变量：
#   CI_TEMPLATES_SOURCE      cnb | github | gitee | gitlab | jsdelivr | unpkg | npm | npmmirror | tencent，等价于 --source
#   CI_TEMPLATES_REF         模板仓库分支或 tag，默认 main
#   CI_TEMPLATES_NPM_PACKAGE npm 包名，默认 @dtapp/ci-templates
#   CI_TEMPLATES_NPM_VERSION npm 版本号或 dist-tag（latest / nightly ...），默认 latest
#   CI_TEMPLATES_NO_FALLBACK 非空 = 不回退，只用起点源
#   CI_TEMPLATES_CNB_URL     CNB 下载基址（默认 https://cnb.cool/dtapp/ci-templates/-/git/raw）
#   CI_TEMPLATES_GITHUB_URL  GitHub 下载基址（默认 https://raw.githubusercontent.com/dtapps/ci-templates）
#   CI_TEMPLATES_GITEE_URL   Gitee 下载基址（默认 https://gitee.com/dtapps/ci-templates/raw）
#   CI_TEMPLATES_GITLAB_URL  GitLab 下载基址（默认 https://gitlab.com/dtapps/ci-templates/-/raw）
#   CI_TEMPLATES_JSDELIVR_URL  jsDelivr 基址（默认 https://cdn.jsdelivr.net/npm）
#   CI_TEMPLATES_UNPKG_URL   unpkg 基址（默认 https://unpkg.com）
#   CI_TEMPLATES_NPM_URL     官方 npm registry 基址（默认 https://registry.npmjs.org）
#   CI_TEMPLATES_NPMMIRROR_URL  npmmirror 基址（默认 https://registry.npmmirror.com）
#   CI_TEMPLATES_TENCENT_NPM_URL 腾讯云 npm 镜像基址（默认 https://mirrors.cloud.tencent.com/npm）
#   CI_SCRIPTS_ROOT          落盘根目录，默认本脚本所在的仓库根
#   CI_SCRIPTS_TIMEOUT       单文件下载超时秒数，默认 30
#
# 行为约定：
#   - 落盘路径与模板仓库完全一致（清单里写 scripts/ci/x.sh 就落到 <repo>/scripts/ci/x.sh）；
#   - 已存在的文件会先下载远端内容再比 sha256：一致就跳过，不一致就覆盖，本地不会残留旧版本；
#   - 先下到临时文件，比对通过才 mv，下载失败绝不破坏本地已有文件；
#   - .sh 必须以 #! 开头才落盘，避免把 404 页面 / 网关错误页当成脚本；
#   - 每个 stage 都可以安全地重复调用（内容一致时只多一次下载，不写盘）。
#
# 退出码：0 成功；1 有脚本缺失或下载失败；2 用法错误。
#
# 依赖：curl（缺失时退到 wget）；npm 镜像源额外需要 tar（只解包单个文件）。
#       sha256 依次尝试 sha256sum / shasum / openssl dgst，
#       三者都没有时退化为「直接覆盖」——结果仍是最新，只是不再跳过。

set -u

# ── 各源的下载基址（内容一致，只差平台与取文件方式）─────────────────────
TEMPLATE_CNB_URL='https://cnb.cool/dtapp/ci-templates/-/git/raw'
TEMPLATE_GITHUB_URL='https://raw.githubusercontent.com/dtapps/ci-templates'
TEMPLATE_GITEE_URL='https://gitee.com/dtapps/ci-templates/raw'
TEMPLATE_GITLAB_URL='https://gitlab.com/dtapps/ci-templates/-/raw'
TEMPLATE_JSDELIVR_URL='https://cdn.jsdelivr.net/npm'
TEMPLATE_UNPKG_URL='https://unpkg.com'
TEMPLATE_NPM_URL='https://registry.npmjs.org'
TEMPLATE_NPMMIRROR_URL='https://registry.npmmirror.com'
TEMPLATE_TENCENT_NPM_URL='https://mirrors.cloud.tencent.com/npm'

# 回退顺序：探测不到 CI 时取第一个；某个源失败时按同一顺序继续往后切
# 仓库源（跟 main，最新）→ npm CDN（跟包版本）→ npm 镜像（兜底，国内可用）
SOURCE_ORDER='cnb github gitee gitlab jsdelivr unpkg npm npmmirror tencent'

# 只有这几个源是「整包 tgz」，其余都能按路径直接取单文件
TARBALL_SOURCES='npm npmmirror tencent'

# ── 需要同步的脚本清单（路径相对模板仓库根，落盘路径与之完全一致）─────────
# 新增脚本只要在这里加一行，业务仓库无需改动其它地方。
manifest() {
  cat <<'EOF'
scripts/ci/delete-release.sh
scripts/ci/describe-artifact.sh
scripts/ci/render-ci-notice.sh
EOF
}

usage() {
  sed -n '2,/^$/p' "$0" | sed 's/^# \{0,1\}//' | sed '/^$/d'
}

info() { printf '%s\n' "$*"; }
warn() { printf '⚠ %s\n' "$*" >&2; }
die() {
  printf '错误：%s\n' "$*" >&2
  exit 2
}

SOURCE=${CI_TEMPLATES_SOURCE:-}
REF=${CI_TEMPLATES_REF:-main}
NPM_PACKAGE=${CI_TEMPLATES_NPM_PACKAGE:-@dtapp/ci-templates}
NPM_VERSION=${CI_TEMPLATES_NPM_VERSION:-latest}
TIMEOUT=${CI_SCRIPTS_TIMEOUT:-30}
FORCE=0
CHECK_ONLY=0
LIST_ONLY=0
FALLBACK=1
WANTED=
fs_tmp=
# npm 整包与版本解析的临时目录（只在用到 registry 源时才真正创建）
NPM_TMP=${TMPDIR:-/tmp}/ci-templates-npm.$$
# pid 被复用时可能撞上上次异常退出残留的目录，先清掉，避免复用旧版本的整包/版本号
[ -e "$NPM_TMP" ] && rm -rf "$NPM_TMP"

# 环境变量已开启「不回退」时，后面再遇到 --fallback 也能被命令行改回来
[ -n "${CI_TEMPLATES_NO_FALLBACK:-}" ] && FALLBACK=0

while [ $# -gt 0 ]; do
  case "$1" in
    --source | --platform)
      SOURCE=${2:-}
      [ -n "$SOURCE" ] || die "$1 需要一个值：cnb / github / gitee / gitlab / jsdelivr / unpkg / npm / npmmirror / tencent"
      shift
      [ $# -gt 0 ] && shift
      ;;
    --ref)
      REF=${2:-}
      [ -n "$REF" ] || die "--ref 需要一个值（分支或 tag）"
      shift
      [ $# -gt 0 ] && shift
      ;;
    --npm-version)
      NPM_VERSION=${2:-}
      [ -n "$NPM_VERSION" ] || die "--npm-version 需要一个值（版本号或 dist-tag，如 latest / 0.0.1-nightly.20261126）"
      shift
      [ $# -gt 0 ] && shift
      ;;
    --npm-package)
      NPM_PACKAGE=${2:-}
      [ -n "$NPM_PACKAGE" ] || die "--npm-package 需要一个值（如 @dtapp/ci-templates）"
      shift
      [ $# -gt 0 ] && shift
      ;;
    --force | -f)
      FORCE=1
      shift
      ;;
    --check)
      CHECK_ONLY=1
      shift
      ;;
    --list)
      LIST_ONLY=1
      shift
      ;;
    --no-fallback)
      FALLBACK=0
      shift
      ;;
    --fallback)
      FALLBACK=1
      shift
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    -*)
      die "未知参数：$1（--help 查看用法）"
      ;;
    *)
      WANTED="${WANTED}${WANTED:+ }$1"
      shift
      ;;
  esac
done

case "$TIMEOUT" in
  '' | *[!0-9]*) die "CI_SCRIPTS_TIMEOUT 必须是数字（当前：${TIMEOUT}）" ;;
esac

# ── 探测当前 CI 平台；一个都命中不了就按同一顺序取第一个（CNB）──────────
detect_source() {
  if [ "${CNB:-}" = 'true' ] || [ -n "${CNB_BUILD_ID:-}" ] \
    || [ -n "${CNB_PIPELINE_ID:-}" ] || [ -n "${CNB_BUILD_WORKSPACE:-}" ]; then
    printf 'cnb'
    return 0
  fi
  if [ "${GITHUB_ACTIONS:-}" = 'true' ]; then
    printf 'github'
    return 0
  fi
  if [ -n "${GITEE_CI:-}" ] || [ -n "${GITEE_GO_PIPELINE_ID:-}" ]; then
    printf 'gitee'
    return 0
  fi
  if [ "${GITLAB_CI:-}" = 'true' ]; then
    printf 'gitlab'
    return 0
  fi
  printf 'cnb'
}

if [ -z "$SOURCE" ]; then
  SOURCE=$(detect_source)
fi

case "$SOURCE" in
  cnb | github | gitee | gitlab | jsdelivr | unpkg | npm | npmmirror | tencent) ;;
  *)
    die "源只能是 cnb / github / gitee / gitlab / jsdelivr / unpkg / npm / npmmirror / tencent（当前：${SOURCE}）"
    ;;
esac

# 每个源的下载基址（都可被 CI_TEMPLATES_<平台>_URL 覆盖）
base_url_for() {
  case "$1" in
    cnb) printf '%s' "${CI_TEMPLATES_CNB_URL:-$TEMPLATE_CNB_URL}" ;;
    github) printf '%s' "${CI_TEMPLATES_GITHUB_URL:-$TEMPLATE_GITHUB_URL}" ;;
    gitee) printf '%s' "${CI_TEMPLATES_GITEE_URL:-$TEMPLATE_GITEE_URL}" ;;
    gitlab) printf '%s' "${CI_TEMPLATES_GITLAB_URL:-$TEMPLATE_GITLAB_URL}" ;;
    jsdelivr) printf '%s' "${CI_TEMPLATES_JSDELIVR_URL:-$TEMPLATE_JSDELIVR_URL}" ;;
    unpkg) printf '%s' "${CI_TEMPLATES_UNPKG_URL:-$TEMPLATE_UNPKG_URL}" ;;
    npm) printf '%s' "${CI_TEMPLATES_NPM_URL:-$TEMPLATE_NPM_URL}" ;;
    npmmirror) printf '%s' "${CI_TEMPLATES_NPMMIRROR_URL:-$TEMPLATE_NPMMIRROR_URL}" ;;
    tencent) printf '%s' "${CI_TEMPLATES_TENCENT_NPM_URL:-$TEMPLATE_TENCENT_NPM_URL}" ;;
  esac
}

# 这个源是不是「只能下整包 tgz」的 registry 源
is_tarball_source() {
  case " $TARBALL_SOURCES " in
    *" $1 "*) return 0 ;;
  esac
  return 1
}

# 纯下载：文件非空即成功，不做脚本格式校验（元数据 / tgz 也走这里）
download() {
  fs_dl_url=$1
  fs_dl_out=$2
  [ -d "${fs_dl_out%/*}" ] || mkdir -p "${fs_dl_out%/*}" || return 1

  if command -v curl >/dev/null 2>&1; then
    curl -fsSL --connect-timeout 10 --max-time "$TIMEOUT" "$fs_dl_url" -o "$fs_dl_out" 2>/dev/null \
      || {
        rm -f "$fs_dl_out"
        return 1
      }
  elif command -v wget >/dev/null 2>&1; then
    wget -q -T "$TIMEOUT" -O "$fs_dl_out" "$fs_dl_url" \
      || {
        rm -f "$fs_dl_out"
        return 1
      }
  else
    warn '需要 curl 或 wget 才能下载模板脚本'
    return 1
  fi

  if [ ! -s "$fs_dl_out" ]; then
    rm -f "$fs_dl_out"
    return 1
  fi
  return 0
}

# 拼下载地址：
#   仓库源  <base>/<ref>/<path>
#   npm CDN <base>/<包名>@<版本或 tag>/<path>
#   镜像源  <base>/<包名>/-/<包名末段>-<具体版本>.tgz（版本稍后解析）
source_url() {
  fs_url_base=$(base_url_for "$1")
  fs_url_base=${fs_url_base%/}
  case "$1" in
    jsdelivr | unpkg)
      printf '%s/%s@%s/%s' "$fs_url_base" "$NPM_PACKAGE" "$NPM_VERSION" "$2"
      ;;
    npm | npmmirror | tencent)
      fs_url_ver=$(npm_version_for "$1" || true)
      [ -n "$fs_url_ver" ] || return 1
      printf '%s/%s/-/%s-%s.tgz' "$fs_url_base" "$NPM_PACKAGE" "${NPM_PACKAGE##*/}" "$fs_url_ver"
      ;;
    *)
      printf '%s/%s/%s' "$fs_url_base" "$REF" "$2"
      ;;
  esac
}

# 把 latest / dist-tag 解析成具体版本号（registry 的整包路径不接受 tag）
npm_version_for() {
  case "$NPM_VERSION" in
    [0-9]*) printf '%s\n' "$NPM_VERSION" && return 0 ;;
  esac

  fs_ver_reg=$(base_url_for "$1")
  fs_ver_reg=${fs_ver_reg%/}

  [ -d "$NPM_TMP" ] || mkdir -p "$NPM_TMP" || return 1

  fs_ver_cache="$NPM_TMP/version.$1"
  if [ -s "$fs_ver_cache" ]; then
    cat "$fs_ver_cache"
    return 0
  fi

  fs_ver_json="$NPM_TMP/meta.$1.$$"
  if ! download "${fs_ver_reg}/${NPM_PACKAGE}/${NPM_VERSION}" "$fs_ver_json"; then
    warn "源 $1 解析版本失败（${NPM_PACKAGE}@${NPM_VERSION}）"
    return 1
  fi
  fs_ver=$(grep -o '"version"[[:space:]]*:[[:space:]]*"[^"]*"' "$fs_ver_json" 2>/dev/null \
    | head -n 1 | sed 's/.*"\([^"]*\)"$/\1/')
  rm -f "$fs_ver_json"
  [ -n "$fs_ver" ] || return 1
  printf '%s\n' "$fs_ver" >"$fs_ver_cache"
  printf '%s\n' "$fs_ver"
}

# 候选源：起点源打头，其余按 SOURCE_ORDER 固定顺序（仓库源始终在 npm 源之前）；
# 关掉回退时只留起点源
source_candidates() {
  if [ "$FALLBACK" != '1' ]; then
    printf '%s\n' "$SOURCE"
    return 0
  fi
  printf '%s\n' "$SOURCE"
  for fs_s in $SOURCE_ORDER; do
    [ "$fs_s" != "$SOURCE" ] || continue
    printf '%s\n' "$fs_s"
  done
}

# ── 落盘根目录：默认本脚本所在仓库根（本脚本位于 scripts/ci/）─────────────
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd) || die "无法定位脚本所在目录"
if [ -n "${CI_SCRIPTS_ROOT:-}" ]; then
  ROOT_DIR=${CI_SCRIPTS_ROOT%/}
elif [ "${SCRIPT_DIR##*/}" = 'ci' ] && [ -d "$SCRIPT_DIR/../.." ]; then
  ROOT_DIR=$(CDPATH= cd -- "$SCRIPT_DIR/../.." && pwd)
else
  ROOT_DIR=$SCRIPT_DIR
fi

# 清单里是否存在该脚本（支持完整相对路径或纯文件名）
manifest_has() {
  manifest | while IFS= read -r fs_rel; do
    [ -n "$fs_rel" ] || continue
    if [ "$1" = "$fs_rel" ] || [ "$1" = "${fs_rel##*/}" ]; then
      printf 'yes\n'
    fi
  done
}

selected() {
  if [ -z "$WANTED" ]; then
    manifest
    return 0
  fi
  manifest | while IFS= read -r fs_rel; do
    [ -n "$fs_rel" ] || continue
    for fs_w in $WANTED; do
      if [ "$fs_w" = "$fs_rel" ] || [ "$fs_w" = "${fs_rel##*/}" ]; then
        printf '%s\n' "$fs_rel"
        break
      fi
    done
  done
}

if [ "$LIST_ONLY" = '1' ]; then
  info "起点源：${SOURCE}（ref=${REF}；npm 包=${NPM_PACKAGE}@${NPM_VERSION}）"
  info "源顺序：$(source_candidates | tr '\n' ' ')"
  info "示例：$(source_url "$SOURCE" "$(manifest | head -n 1)")"
  info "落盘目录：${ROOT_DIR}"
  selected
  exit 0
fi

if [ -n "$WANTED" ]; then
  for fs_w in $WANTED; do
    [ "$(manifest_has "$fs_w")" = 'yes' ] \
      || die "清单里没有 ${fs_w}（用 --list 查看可用脚本）"
  done
fi

cleanup() {
  [ -n "${fs_tmp:-}" ] && rm -f "$fs_tmp"
  [ -n "${NPM_TMP:-}" ] && rm -rf "$NPM_TMP"
}
trap cleanup EXIT HUP INT TERM

# 计算文件 sha256；没有可用工具时返回 1
sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" 2>/dev/null | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" 2>/dev/null | awk '{print $1}'
  elif command -v openssl >/dev/null 2>&1; then
    openssl dgst -sha256 "$1" 2>/dev/null | awk '{print $NF}'
  else
    return 1
  fi
}

# 比较两个文件内容：0 相同，1 不同，2 无法计算 sha256（缺工具/读不了）
content_state() {
  fs_sum_a=$(sha256_of "$1" || true)
  fs_sum_b=$(sha256_of "$2" || true)
  if [ -z "$fs_sum_a" ] || [ -z "$fs_sum_b" ]; then
    return 2
  fi
  if [ "$fs_sum_a" = "$fs_sum_b" ]; then
    return 0
  fi
  return 1
}

# 只下载到临时文件（fs_tmp），不落盘；由调用方决定 mv 还是丢弃
# fetch <源> <脚本相对路径> <落盘路径>
fetch() {
  fs_source=$1
  fs_rel=$2
  fs_dest=$3
  fs_dir=${fs_dest%/*}
  fs_tmp="$fs_dest.tmp.$$"

  [ -d "$fs_dir" ] || mkdir -p "$fs_dir" || return 1

  if is_tarball_source "$fs_source"; then
    # registry 镜像没有单文件接口：整包下一次，用 tar 直接解出单个文件
    fs_tgz=$(tarball_for "$fs_source") || return 1
    if ! command -v tar >/dev/null 2>&1; then
      warn '需要 tar 才能从 npm 整包里取脚本'
      return 1
    fi
    if ! tar -xzOf "$fs_tgz" "package/${fs_rel}" >"$fs_tmp" 2>/dev/null; then
      rm -f "$fs_tmp"
      fs_tmp=
      return 1
    fi
    if [ ! -s "$fs_tmp" ]; then
      rm -f "$fs_tmp"
      fs_tmp=
      return 1
    fi
  else
    fs_url=$(source_url "$fs_source" "$fs_rel") || return 1
    if ! download "$fs_url" "$fs_tmp"; then
      fs_tmp=
      return 1
    fi
  fi

  # 防止把 404 页面 / 网关错误页当成脚本落盘
  case "$fs_dest" in
    *.sh)
      if ! head -n 1 "$fs_tmp" | grep -q '^#!'; then
        rm -f "$fs_tmp"
        fs_tmp=
        return 1
      fi
      ;;
  esac

  return 0
}

# registry 源的整包：第一次调用下载，之后复用同一个 tgz
tarball_for() {
  fs_tb_ver=$(npm_version_for "$1") || return 1
  [ -n "$fs_tb_ver" ] || return 1

  [ -d "$NPM_TMP" ] || mkdir -p "$NPM_TMP" || return 1

  fs_tb_tgz="$NPM_TMP/$1.tgz"
  if [ ! -s "$fs_tb_tgz" ]; then
    fs_tb_url=$(base_url_for "$1")
    fs_tb_url=$(printf '%s/%s/-/%s-%s.tgz' \
      "${fs_tb_url%/}" "$NPM_PACKAGE" "${NPM_PACKAGE##*/}" "$fs_tb_ver")
    if ! download "$fs_tb_url" "$fs_tb_tgz"; then
      warn "源 $1 整包下载失败：${fs_tb_url}"
      rm -f "$fs_tb_tgz"
      return 1
    fi
  fi
  printf '%s\n' "$fs_tb_tgz"
}

fs_created=0
fs_updated=0
fs_same=0
fs_missing=0
fs_failed=0

CANDIDATES=$(source_candidates)
CANDIDATE_COUNT=$(printf '%s\n' "$CANDIDATES" | grep -c .)

info "起点源：${SOURCE}（ref=${REF}；npm 包=${NPM_PACKAGE}@${NPM_VERSION}） → ${ROOT_DIR}"
if [ "$FALLBACK" = '1' ]; then
  info "失败回退顺序：$(printf '%s' "$CANDIDATES" | tr '\n' ' ')"
fi

while IFS= read -r fs_rel; do
  [ -n "$fs_rel" ] || continue
  fs_dest="$ROOT_DIR/$fs_rel"
  fs_exists=0
  fs_cmp=1
  [ -f "$fs_dest" ] && fs_exists=1

  # --check 只查存在性，不下载所以也不比对内容
  if [ "$CHECK_ONLY" = '1' ]; then
    if [ "$fs_exists" = '1' ]; then
      fs_same=$((fs_same + 1))
      info "存在：$fs_rel"
    else
      fs_missing=$((fs_missing + 1))
      warn "缺失：$fs_rel"
    fi
    continue
  fi

  # 按候选源依次尝试：某个源取不到就自动换下一个
  fs_ok=0
  fs_used=
  for fs_src in $CANDIDATES; do
    if fetch "$fs_src" "$fs_rel" "$fs_dest"; then
      fs_ok=1
      fs_used=$fs_src
      break
    fi
    if [ "$CANDIDATE_COUNT" -gt 1 ]; then
      warn "源 ${fs_src} 取不到 ${fs_rel}，换下一个"
    else
      warn "源 ${fs_src} 取不到 ${fs_rel}"
    fi
  done
  if [ "$fs_ok" != '1' ]; then
    fs_failed=$((fs_failed + 1))
    warn "所有源都取不到：$fs_rel"
    continue
  fi

  # 已存在且没加 --force：比 sha256，一致就不写盘（不许留旧版本，也不无谓改动文件）
  if [ "$fs_exists" = '1' ] && [ "$FORCE" != '1' ]; then
    content_state "$fs_dest" "$fs_tmp"
    fs_cmp=$?
    if [ "$fs_cmp" -eq 0 ]; then
      rm -f "$fs_tmp"
      fs_tmp=
      fs_same=$((fs_same + 1))
      info "一致（sha256 相同）：${fs_rel}（源：${fs_used}）"
      continue
    fi
    if [ "$fs_cmp" -eq 2 ]; then
      warn "无法计算 sha256（缺 sha256sum / shasum / openssl），按更新处理：${fs_rel}"
    fi
  fi

  if ! mv -f "$fs_tmp" "$fs_dest"; then
    rm -f "$fs_tmp"
    fs_tmp=
    fs_failed=$((fs_failed + 1))
    warn "写入失败：$fs_dest"
    continue
  fi
  fs_tmp=
  chmod +x "$fs_dest" 2>/dev/null || :

  if [ "$fs_exists" = '1' ]; then
    fs_updated=$((fs_updated + 1))
    if [ "$FORCE" = '1' ]; then
      info "✓ 已覆盖（--force）：${fs_rel}（源：${fs_used}）"
    elif [ "$fs_cmp" -eq 2 ]; then
      info "✓ 已更新：${fs_rel}（源：${fs_used}）"
    else
      info "✓ 已更新（sha256 变化）：${fs_rel}（源：${fs_used}）"
    fi
  else
    fs_created=$((fs_created + 1))
    info "✓ 新增：${fs_rel}（源：${fs_used}）"
  fi
done <<EOF
$(selected)
EOF

if [ "$CHECK_ONLY" = '1' ]; then
  if [ "$fs_missing" -ne 0 ]; then
    warn "共 ${fs_missing} 个脚本缺失，可执行：sh $0"
    exit 1
  fi
  info "脚本齐全（${fs_same} 个已存在；--check 不比对内容）"
  exit 0
fi

if [ "$fs_failed" -ne 0 ]; then
  warn "有 ${fs_failed} 个脚本在所有源都取不到（起点：${SOURCE}，ref：${REF}，npm：${NPM_PACKAGE}@${NPM_VERSION}）"
  warn "可指定源重试：sh $0 --source <cnb|github|gitee|gitlab|jsdelivr|unpkg|npm|npmmirror|tencent> --force"
  exit 1
fi

info "完成：新增 ${fs_created} 个，更新 ${fs_updated} 个，一致 ${fs_same} 个"
exit 0
