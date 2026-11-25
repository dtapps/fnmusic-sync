#!/bin/sh
# 模板脚本拉取器：把 ci-templates 里的脚本按「与模板仓库完全一致的相对路径」
# 落到当前仓库（默认 scripts/ci/），供 CI stage 直接 sh scripts/ci/xxx.sh 调用。
#
# 背景：ci-templates 在 CNB / GitHub / GitLab / Gitee / GitCode 各放一份，内容一致，
#       只有平台差异（下载地址格式）。本文件下方的清单决定要拉哪些脚本；
#       业务仓库只需保留本文件，不必手工复制、同步。
#
# 用法（在任意仓库根目录执行）：
#   sh scripts/ci/fetch-scripts.sh                    # 同步：缺失的下载，已存在的比 sha256，不同则更新
#   sh scripts/ci/fetch-scripts.sh --force            # 不做比对，已存在也直接覆盖
#   sh scripts/ci/fetch-scripts.sh --check            # 只查存在性（不下载、不比对），缺失时退出码 1
#   sh scripts/ci/fetch-scripts.sh --list             # 打印清单与候选源顺序
#   sh scripts/ci/fetch-scripts.sh --source gitlab    # 指定起点源（cnb|github|gitee|gitlab）
#   sh scripts/ci/fetch-scripts.sh --no-fallback      # 只用起点源，失败不换源
#   sh scripts/ci/fetch-scripts.sh delete-release.sh  # 只处理指定脚本（可多个，也可写全路径）
#
# 源选择与回退：
#   1) --source <平台> / 环境变量 CI_TEMPLATES_SOURCE 显式指定「起点源」
#   2) 否则自动探测当前 CI 平台（CNB / GitHub / Gitee / GitLab）作为起点
#   3) 一个都探测不到（如本地开发）：按 CNB → GitHub → Gitee → GitLab 取第一个，即默认 CNB
#   4) 起点源取不到文件时，按同一顺序继续换下一个源，直到拿到或全部失败；
#      回退是「每个文件独立」的，所以某个源缺某个脚本也能自动从别的源补齐
#      --no-fallback（或 CI_TEMPLATES_NO_FALLBACK=1）可关掉回退，只用起点源
#
# 四个源仓库（内容一致，按上面的顺序尝试）：
#   CNB    ：https://cnb.cool/dtapp/ci-templates       /-/git/raw/<ref>/<path>
#   GitHub ：https://github.com/dtapps/ci-templates    raw.githubusercontent.com/dtapps/ci-templates/<ref>/<path>
#   Gitee  ：https://gitee.com/dtapps/ci-templates     /raw/<ref>/<path>（302 到内容域，靠 -L 跟随）
#   GitLab ：https://gitlab.com/dtapps/ci-templates    /-/raw/<ref>/<path>
#   注：GitCode 没有可用的 raw 地址（网页 raw 对 .sh 一律返回 HTML 或 403「暂不支持预览」，
#       只有 api.gitcode.com 的 raw API 能取到文件），故未列入。
#
# 平台探测（只看各平台强特征变量，避免业务仓库里配的 GITEE_TOKEN 之类被误判成 CI）：
#   CNB     CNB=true，或 CNB_BUILD_ID / CNB_PIPELINE_ID / CNB_BUILD_WORKSPACE 任一非空
#   GitHub  GITHUB_ACTIONS=true
#   Gitee   GITEE_CI / GITEE_GO_PIPELINE_ID 任一非空
#   GitLab  GITLAB_CI=true
#
# 环境变量：
#   CI_TEMPLATES_SOURCE      cnb | github | gitee | gitlab，等价于 --source
#   CI_TEMPLATES_REF         模板仓库分支或 tag，默认 main
#   CI_TEMPLATES_NO_FALLBACK 非空 = 不回退，只用起点源
#   CI_TEMPLATES_CNB_URL     CNB 下载基址（默认 https://cnb.cool/dtapp/ci-templates/-/git/raw）
#   CI_TEMPLATES_GITHUB_URL  GitHub 下载基址（默认 https://raw.githubusercontent.com/dtapps/ci-templates）
#   CI_TEMPLATES_GITEE_URL   Gitee 下载基址（默认 https://gitee.com/dtapps/ci-templates/raw）
#   CI_TEMPLATES_GITLAB_URL  GitLab 下载基址（默认 https://gitlab.com/dtapps/ci-templates/-/raw）
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
# 依赖：curl（缺失时退到 wget）。sha256 依次尝试 sha256sum / shasum / openssl dgst，
#       三者都没有时退化为「直接覆盖」——结果仍是最新，只是不再跳过。

set -u

# ── 四个源仓库的下载基址（内容一致，只差平台）────────────────────────────
TEMPLATE_CNB_URL='https://cnb.cool/dtapp/ci-templates/-/git/raw'
TEMPLATE_GITHUB_URL='https://raw.githubusercontent.com/dtapps/ci-templates'
TEMPLATE_GITEE_URL='https://gitee.com/dtapps/ci-templates/raw'
TEMPLATE_GITLAB_URL='https://gitlab.com/dtapps/ci-templates/-/raw'

# 回退顺序：探测不到 CI 时取第一个；某个源失败时按同一顺序继续往后切
SOURCE_ORDER='cnb github gitee gitlab'

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
TIMEOUT=${CI_SCRIPTS_TIMEOUT:-30}
FORCE=0
CHECK_ONLY=0
LIST_ONLY=0
FALLBACK=1
WANTED=
fs_tmp=

# 环境变量已开启「不回退」时，后面再遇到 --fallback 也能被命令行改回来
[ -n "${CI_TEMPLATES_NO_FALLBACK:-}" ] && FALLBACK=0

while [ $# -gt 0 ]; do
  case "$1" in
    --source | --platform)
      SOURCE=${2:-}
      [ -n "$SOURCE" ] || die "$1 需要一个值：cnb / github / gitlab / gitee / gitcode"
      shift
      [ $# -gt 0 ] && shift
      ;;
    --ref)
      REF=${2:-}
      [ -n "$REF" ] || die "--ref 需要一个值（分支或 tag）"
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
  cnb | github | gitee | gitlab) ;;
  *)
    die "源平台只能是 cnb / github / gitee / gitlab（当前：${SOURCE}）"
    ;;
esac

# 每个源的下载基址（都可被 CI_TEMPLATES_<平台>_URL 覆盖）
base_url_for() {
  case "$1" in
    cnb) printf '%s' "${CI_TEMPLATES_CNB_URL:-$TEMPLATE_CNB_URL}" ;;
    github) printf '%s' "${CI_TEMPLATES_GITHUB_URL:-$TEMPLATE_GITHUB_URL}" ;;
    gitee) printf '%s' "${CI_TEMPLATES_GITEE_URL:-$TEMPLATE_GITEE_URL}" ;;
    gitlab) printf '%s' "${CI_TEMPLATES_GITLAB_URL:-$TEMPLATE_GITLAB_URL}" ;;
  esac
}

# 拼下载地址：四个平台都是 <base>/<ref>/<path>
source_url() {
  fs_base=$(base_url_for "$1")
  printf '%s/%s/%s' "${fs_base%/}" "$REF" "$2"
}

# 候选源：起点源打头，再按 SOURCE_ORDER 绕一圈；关掉回退时只留起点源
source_candidates() {
  if [ "$FALLBACK" != '1' ]; then
    printf '%s\n' "$SOURCE"
    return 0
  fi
  fs_head=
  fs_tail=
  fs_hit=0
  for fs_s in $SOURCE_ORDER; do
    if [ "$fs_hit" = '1' ]; then
      fs_tail="${fs_tail}${fs_tail:+ }$fs_s"
    elif [ "$fs_s" = "$SOURCE" ]; then
      fs_hit=1
    else
      fs_head="${fs_head}${fs_head:+ }$fs_s"
    fi
  done
  for fs_s in $SOURCE $fs_tail $fs_head; do
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
  info "起点源：${SOURCE}（ref=${REF}）"
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

cleanup() { [ -n "${fs_tmp:-}" ] && rm -f "$fs_tmp"; }
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
fetch() {
  fs_url=$1
  fs_dest=$2
  fs_dir=${fs_dest%/*}
  fs_tmp="$fs_dest.tmp.$$"

  [ -d "$fs_dir" ] || mkdir -p "$fs_dir" || return 1

  if command -v curl >/dev/null 2>&1; then
    curl -fsSL --connect-timeout 10 --max-time "$TIMEOUT" "$fs_url" -o "$fs_tmp" 2>/dev/null \
      || {
        rm -f "$fs_tmp"
        fs_tmp=
        return 1
      }
  elif command -v wget >/dev/null 2>&1; then
    wget -q -T "$TIMEOUT" -O "$fs_tmp" "$fs_url" \
      || {
        rm -f "$fs_tmp"
        fs_tmp=
        return 1
      }
  else
    warn '需要 curl 或 wget 才能下载模板脚本'
    return 1
  fi

  if [ ! -s "$fs_tmp" ]; then
    rm -f "$fs_tmp"
    fs_tmp=
    return 1
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

fs_created=0
fs_updated=0
fs_same=0
fs_missing=0
fs_failed=0

CANDIDATES=$(source_candidates)
CANDIDATE_COUNT=$(printf '%s\n' "$CANDIDATES" | grep -c .)

info "起点源：${SOURCE}（ref=${REF}） → ${ROOT_DIR}"
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
    fs_url=$(source_url "$fs_src" "$fs_rel")
    if fetch "$fs_url" "$fs_dest"; then
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
  warn "有 ${fs_failed} 个脚本在所有源都取不到（起点：${SOURCE}，ref：${REF}）"
  warn "可指定源重试：sh $0 --source <cnb|github|gitee|gitlab> --force"
  exit 1
fi

info "完成：新增 ${fs_created} 个，更新 ${fs_updated} 个，一致 ${fs_same} 个"
exit 0
