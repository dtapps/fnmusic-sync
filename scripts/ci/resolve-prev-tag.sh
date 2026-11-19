#!/usr/bin/env sh
#
# 输出「上一个版本 tag」：给定本次要发布的版本号，按发布渠道挑对比基准。
#
# 渠道口径与 resolve-prerelease.sh 完全一致（只看版本号后缀）：
#   1.2.3                    → 正式版
#   1.2.3-rc.1               → 预发布（rc 渠道）
#   1.2.3-beta.1             → 预发布（beta 渠道）
#   1.2.3-alpha.1            → 预发布（alpha 渠道）
#   1.2.3-nightly.20260101   → 预发布（nightly 渠道）
#
# 规则（「对应的上一个」，不是「最近一个 tag」）：
#   - 正式版：对比上一个**正式版**，不掺预发布
#       发 v1.0.45 → v1.0.43（中间的 v1.0.44-rc.1 / -rc.2 不算基准）
#   - 预发布：对比**同渠道**的上一个 tag
#       发 v1.0.44-rc.2 → v1.0.44-rc.1；发 v1.2.4-nightly.20260102 → 上一个 nightly
#   - 该渠道还没有过 tag：回退到上一个正式版
#       首次发 v1.0.44-rc.1 → v1.0.43
#
# 为什么不用 `git describe --tags --abbrev=0`：
#   1) 它取「最近的可达 tag」，不区分渠道 —— 发正式版时会取到刚发的 rc，
#      前面整轮改动全丢；
#   2) 会被无关 tag 干扰（仓库里另有 docker-v1.0.x 系列），基准直接错到别的线上。
#
# 为什么单独成文件：Release 正文的「更新内容」与发版公告评论的「对比哪两个 tag」
# 必须同一个口径。此前判定只在 release-changelog.sh 里，公告侧要么去调渲染脚本、
# 要么自己抄一套 tag 排序 —— 抄一套的结果就是两边区间对不上。
#
# 用法：
#   sh scripts/ci/resolve-prev-tag.sh <版本号>
#   # 找到基准：stdout 打印该 tag，exit 0
#   # 没有基准（仓库里还没有可比的版本 tag，即首次发布）：stdout 为空，exit 0
#   # 取数故障（有别的 tag 却挑不出基准）：原因写 stderr，exit 1
#
# 拉取 tag 是调用方的责任：GitHub 用 fetch-depth: 0，CNB 用 git fetch --tags。
#
# ⚠️ 必须保持 POSIX sh 语法：CNB 的 stage 默认用 sh（dash/ash）执行。
#   不要用 [[ ]]、数组、$'...'、BASH_SOURCE。

set -eu

version="${1:-}"
if [ -z "${version}" ]; then
  printf '用法: %s <版本号>\n' "${0##*/}" >&2
  exit 2
fi

# ── 渠道判定 ────────────────────────────────────────────────────────────────
# 预发布后缀一律用连字符（-rc.N / -beta.N / -alpha.N / -nightly.YYYYMMDD）。
channel='stable'
case "${version}" in
  *-rc*) channel='rc' ;;
  *-beta*) channel='beta' ;;
  *-alpha*) channel='alpha' ;;
  *-nightly*) channel='nightly' ;;
esac

# ── 收集候选 tag ────────────────────────────────────────────────────────────
# 只认合法 SemVer：vX.Y.Z 或 vX.Y.Z-rc.N / -beta.N / -alpha.N / -nightly.N。
# 不合规的 tag 不进候选集，否则一个随手建的 v1.0.44.rc1 会变成对比基准。
all_tags="$(git tag -l 'v*' 2>/dev/null \
  | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+(-(rc|beta|alpha|nightly)(\.[0-9]+)?)?$' \
  || true)"

# 按渠道过滤出「同渠道」的 tag。排除当前版本自己：重跑同一次发布时当前 tag
# 可能已建好，不排除就会自比自（diff 为空）。
same_channel=''
for t in ${all_tags}; do
  [ "${t}" = "${version}" ] && continue
  t_channel='stable'
  case "${t}" in
    *-rc*) t_channel='rc' ;;
    *-beta*) t_channel='beta' ;;
    *-alpha*) t_channel='alpha' ;;
    *-nightly*) t_channel='nightly' ;;
  esac
  [ "${t_channel}" = "${channel}" ] || continue
  same_channel="${same_channel}${same_channel:+
}${t}"
done

# ── 选基准 ──────────────────────────────────────────────────────────────────
# 同渠道里挑版本号最大的那个。用 sort -V 而不是 --sort=-v:refname：
# 后者对 rc.2 / rc.10 这类排序不符合直觉，且不同 git 版本行为有差异。
pick_latest() {
  printf '%s\n' "$1" | sed '/^$/d' | sort -V -r | head -n 1
}

# 只保留「比当前版本旧」的 tag：补发历史版本（已发过 rc.2 再回头发 rc.1）时，
# 候选集里会有比当前更新的 tag，不过滤就会拿「未来」当基准，diff 变成反向的。
older_than_version() {
  while IFS= read -r t; do
    [ -n "${t}" ] || continue
    # sort -V -r 后取首行：若首行是当前版本，说明 t 不比它新
    latest="$(printf '%s\n%s\n' "${t}" "${version}" | sort -V -r | head -n 1)"
    [ "${latest}" = "${version}" ] && printf '%s\n' "${t}"
  done <<EOF
$1
EOF
}

base=''
if [ -n "${same_channel}" ]; then
  base="$(pick_latest "$(older_than_version "${same_channel}")")"
fi

# 同渠道没有更早的 tag（首次发这个渠道）→ 回退到上一个正式版
if [ -z "${base}" ] && [ "${channel}" != 'stable' ]; then
  stable_tags=''
  for t in ${all_tags}; do
    [ "${t}" = "${version}" ] && continue
    case "${t}" in
      *-rc* | *-beta* | *-alpha* | *-nightly*) continue ;;
    esac
    stable_tags="${stable_tags}${stable_tags:+
}${t}"
  done
  if [ -n "${stable_tags}" ]; then
    base="$(pick_latest "$(older_than_version "${stable_tags}")")"
  fi
fi

# ── 输出 ────────────────────────────────────────────────────────────────────
if [ -n "${base}" ]; then
  printf '%s\n' "${base}"
  exit 0
fi

# 排掉「当前版本自己」之后还剩几个 tag
others="$(printf '%s\n' "${all_tags}" | sed '/^$/d' | grep -vxF "${version}" || true)"
if [ -z "${others}" ]; then
  # 一个可用版本 tag 都没有 → 首次发布，stdout 留空由调用方兜底
  exit 0
fi

# 有 tag 却挑不出基准 = 取数故障，不能拿「首次发布」把故障盖住
printf '错误: 仓库里有 %d 个其它版本 tag，但挑不出 %s 渠道的对比基准。\n' \
  "$(printf '%s\n' "${others}" | sed '/^$/d' | wc -l | tr -d ' ')" "${channel}" >&2
printf '常见原因：clone 时没拉取 tag（GitHub 需要 fetch-depth: 0），或 tag 命名不符合预期。\n' >&2
exit 1
