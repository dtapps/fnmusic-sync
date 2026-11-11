#!/usr/bin/env bash
#
# 生成 Release 正文的「更新内容」：按发布渠道找对比基准，输出提交摘要。
#
# 需求（同渠道对比）：
#   - 预发布（rc / beta / alpha / nightly）：和**同渠道**的上一个 tag 对比
#       发 v1.0.44-rc.2 → 对比 v1.0.44-rc.1
#       发 v1.0.44-beta.2 → 对比 v1.0.44-beta.1
#       发 v1.0.44-nightly.20260102 → 对比 v1.0.44-nightly.20260101
#   - 正式版：和上一个**正式版**对比，不掺预发布阶段的提交
#       发 v1.0.45 → 对比 v1.0.43（中间那些 -rc.1/-rc.2 不算基准）
#   - 该渠道还没有过 tag 时，回退到上一个正式版
#       首次发 v1.0.44-rc.1 / v1.0.44-nightly.20260101 → 对比 v1.0.43
#
# 为什么不用 `git describe --tags --abbrev=0 'HEAD^'`：
#   1) 它取的是「最近的可达 tag」，不区分渠道。发正式版时会取到刚发的 rc，
#      于是正式版的更新内容只剩 rc 之后那几个提交，前面整轮的改动全丢了。
#   2) 它还会被无关 tag 干扰：仓库里另有 docker-v1.0.x 系列 tag，只要某个 docker
#      tag 比版本 tag 更靠近 HEAD，describe 就会选它，基准直接错到别的线上。
#   所以这里显式按 tag 名前缀（v*）和渠道过滤。
#
# 用法：
#   sh scripts/ci/release-changelog.sh <版本号>
#   # 输出（多行 Markdown）到 stdout；挑不出基准时 exit 1，由工作流中断发布
#
# 环境变量：
#   COMMIT_URL  选填，提交链接前缀。拼成 ${COMMIT_URL}/<sha>。
#               GitHub 是 ${REPO_URL}/commit，CNB 是 ${REPO_URL}/-/commit。
#               两个平台的 Web 路径不同，不能只靠 REPO_URL 猜。
#   REPO_URL    选填，仓库地址；只在截断提示的「提交历史」链接里用。
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
# 只看版本号后缀决定渠道，和 resolve-prerelease.sh 的口径保持一致：
# 预发布后缀一律用连字符（-rc.N / -beta.N / -alpha.N / -nightly.YYYYMMDD），
# 这是 SemVer 的规定。nightly 也是独立渠道：今天的 nightly 只对比昨天的 nightly，
# 不要拿 rc 或正式版当基准。
channel='stable'
case "${version}" in
  *-nightly*) channel='nightly' ;;
  *-rc*) channel='rc' ;;
  *-beta*) channel='beta' ;;
  *-alpha*) channel='alpha' ;;
esac

# ── 收集候选 tag ────────────────────────────────────────────────────────────
# 只认合法的 SemVer 版本 tag：vX.Y.Z 或 vX.Y.Z-rc.N / -beta.N / -alpha.N /
# -nightly.YYYYMMDD（数字段用 * 而不是 ?，这样 -rc.1.2、-nightly.20260101.2
# 这类多段后缀也能进候选集，和 resolve-prerelease.sh 接受的字符集保持一致）。
# 不合规的 tag 一律不进候选集 —— 否则一个随手建的 v1.0.44.rc1 之类就会变成
# 对比基准，把 diff 算到错误的区间上。
# 拉取 tag 是调用方的责任：GitHub 用 fetch-depth: 0，CNB 用 git fetch --tags，
# 工作流里都有独立一步校验，这里不做兜底 fetch。
all_tags="$(git tag -l 'v*' 2>/dev/null \
  | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+(-(rc|beta|alpha|nightly)(\.[0-9]+)*)?$' \
  || true)"

# 按渠道过滤出「同渠道」的 tag。注意排除当前版本自己：重跑同一次发布时，
# 当前 tag 可能已经建好了，不排除就会变成自己和自己对比（diff 为空）。
same_channel=''
for t in ${all_tags}; do
  [ "${t}" = "${version}" ] && continue
  t_channel='stable'
  case "${t}" in
    *-nightly*) t_channel='nightly' ;;
    *-rc*) t_channel='rc' ;;
    *-beta*) t_channel='beta' ;;
    *-alpha*) t_channel='alpha' ;;
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

# 只保留「比当前版本旧」的 tag。
# 补发历史版本（比如已经发过 rc.2 再回头发 rc.1）时，候选集里会有比当前版本更新的
# tag，不过滤就会拿「未来」当基准，diff 变成反向的。
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
if [ -z "${base}" ]; then
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
# 只有仓库里一个版本 tag 都没有时，才是真的「首次发布」。
# 有 tag 但挑不出基准，说明是取数出了问题（比如 clone 时没拉 tag），
# 这时报「首次发布」会盖住真正的故障，必须显式报错让人去看。
if [ -z "${base}" ]; then
  # 排掉「当前版本自己」之后还剩几个 tag
  others="$(printf '%s\n' "${all_tags}" | sed '/^$/d' | grep -vxF "${version}" || true)"
  # 一个 tag 都没有，或只有当前版本自己 → 这就是这个仓库/渠道的第一次发布
  if [ -z "${others}" ]; then
    printf '首次发布\n'
    exit 0
  fi
  # 有别的 tag 却挑不出基准，说明取数有问题，不能拿「首次发布」把故障盖住
  printf '错误: 仓库里有 %d 个其它版本 tag，但挑不出 %s 渠道的对比基准。\n' \
    "$(printf '%s\n' "${others}" | sed '/^$/d' | wc -l | tr -d ' ')" "${channel}" >&2
  printf '常见原因：clone 时没拉取 tag（GitHub 需要 fetch-depth: 0），或 tag 命名不符合预期。\n' >&2
  exit 1
fi

# 基准就是当前版本（重跑同一次发布）→ 没有可对比的提交
if [ "${base}" = "${version}" ]; then
  printf '与上一个版本（%s）相比没有新提交。\n' "${base}"
  exit 0
fi

# 取 短哈希 / 完整标题 / 作者 三段，用 （单元分隔符）隔开：
# 标题里可能出现 | 或 : 这类字符，用它们当分隔符会串位。
# 中间结果落临时文件：不用 $( ) 包 while + case，理由见下方 render_one 的注释。
parsed_file="$(mktemp "${TMPDIR:-/tmp}/fnmusic-changelog.XXXXXX")"
# shellcheck disable=SC2064  # 双引号是故意的：立即展开路径，而不是退出时再算
trap "rm -f '${parsed_file}'" EXIT

SEP=$(printf '\037')

# --no-merges：dependabot 的 PR merge 会带两条几乎相同的记录，正文里很乱
# 同时取 %H（完整 SHA）和 %h（缩写）：正文里**显示**用短号，读起来清爽；
# **链接**用完整 SHA，保证一定点得开（缩写长度随仓库规模变化，链接里用缩写
# 部分平台会 404）。
raw="$(git log --no-merges --pretty=format:"%H${SEP}%h${SEP}%s${SEP}%an" "${base}..HEAD" 2>/dev/null || true)"

if [ -z "${raw}" ]; then
  # 有基准但区间内没有非 merge 提交（比如只合了 PR）→ 退一步带上 merge 提交
  raw="$(git log --pretty=format:"%H${SEP}%h${SEP}%s${SEP}%an" "${base}..HEAD" 2>/dev/null || true)"
fi

if [ -z "${raw}" ]; then
  printf '与上一个版本（%s）相比没有新提交。\n' "${base}"
  exit 0
fi

# ── 逐条解析成「分组名 <TAB> 渲染行」 ───────────────────────────────────────
# 渲染格式对齐 cnbcool/changelog 插件的输出：
#   [6c24383] - ci: 注释掉推送流水线中的代码格式化步骤 (dtapps)
# 即「[短哈希] - scope: subject (作者)」，没有 scope 时只写 type。
#
# 分组规则（同样是反推插件行为得到的）：
#   - 按 Conventional Commits 的 type 分组，scope 不参与分组，只作为行内前缀
#     所以 build(ci) / build(deps) / build(deps-dev) 全归到 Build System 一组
#   - 组内按「scope: subject」的字节序排序（LC_ALL=C），这样 deps-dev 排在 deps 前
#     （第 5 字节 '-'=0x2D < ':'=0x3A），和插件输出一致
# ── 解析并渲染 ──────────────────────────────────────────────────────────────
# 渲染格式对齐 cnbcool/changelog 插件（用户已确认要这个样式）：
#   [6c24383] - ci: 注释掉推送流水线中的代码格式化步骤 (dtapps)
# 即「[短哈希] - scope: subject (作者)」，没有 scope 时写 type。
#
# 分组规则：
#   - 按 Conventional Commits 的 type 分组，scope 不参与分组，只作行内前缀
#     （所以 build(ci) / build(deps) / build(deps-dev) 全归到 Build System）
#   - 组内按行内文字（scope: subject）排序，和插件输出一致
#
# ⚠️ 这里刻意把解析逻辑写成函数、而不是 parsed="$( ... while ... done )"：
#    bash 在 $( ) 内部解析 case 模式时，`*\(*\)` 这类转义括号会让命令替换
#    提前闭合，报出的错却是「后面的变量 unbound」，极难定位。
#    函数直接往 stdout 写、外层用 while 读，就绕开了这个坑。
render_one() {
  _subject="$1"
  _sha="$2"
  _short="$3"
  _author="$4"

  # 拆 Conventional Commits 前缀：type(scope)!: subject
  _body="${_subject}"
  _type=''
  _scope=''
  case "${_subject}" in
    *:*)
      _prefix="${_subject%%:*}"
      # 先剥掉 (scope) 得到纯 type，再校验字符集
      _base="${_prefix%%(*}"
      case "${_prefix}" in
        *\(*\))
          _scope="${_prefix#*\(}"
          _scope="${_scope%\)}"
          ;;
      esac
      # type 只允许字母数字下划线连字符，避免把 "http://..." 误当前缀
      case "${_base}" in
        '' | *[!A-Za-z0-9_-]*) _type='' ;;
        *)
          _type="${_base}"
          _body="${_subject#*:}"
          _body="${_body# }"
          ;;
      esac
      ;;
  esac
  [ -n "${_type}" ] || _type='other'

  # 分组显示名：常见 type 用插件里的英文名，其余原样
  case "${_type}" in
    build) _group='Build System' ;;
    chore) _group='chore' ;;
    ci) _group='CI' ;;
    docs) _group='Documentation' ;;
    feat) _group='Features' ;;
    fix) _group='Bug Fixes' ;;
    perf) _group='Performance' ;;
    refactor) _group='Refactor' ;;
    revert) _group='Revert' ;;
    style) _group='Style' ;;
    test) _group='Tests' ;;
    other) _group='Other' ;;
    *) _group="${_type}" ;;
  esac

  # 行内前缀：有 scope 用 scope，否则用 type
  if [ -n "${_scope}" ]; then
    _label="${_scope}: ${_body}"
  else
    _label="${_type}: ${_body}"
  fi

  # 渲染成「[ [短号](完整SHA链接) ]」：
  #   - 显示用 %h 短号，和 GitHub 页面习惯一致，读起来清爽
  #   - 链接目标用完整 SHA，保证一定点得开
  #   - 方括号写在链接**外面**，只是普通字符，不参与链接；
  #     写成 [[短号]](url) 的话方括号会被包进链接文字里，点方括号也会跳转。
  # COMMIT_URL 没传时只显示短号：拼出来是 "/commit/abc" 这种点不开的相对路径。
  if [ -n "${COMMIT_URL:-}" ]; then
    _hash_link="[[${_short}](${COMMIT_URL}/${_sha})]"
  else
    _hash_link="${_short}"
  fi

  # 第 1 段组名排序键（小写，让 Build System 排在 chore 前）、
  # 第 2 段组显示名、第 3 段组内排序键、第 4 段最终渲染行
  _lower="$(printf '%s' "${_group}" | tr '[:upper:]' '[:lower:]')"
  printf '%s\t%s\t%s\t%s - %s (%s)\n' \
    "${_lower}" "${_group}" "${_label}" "${_hash_link}" "${_label}" "${_author}"
}

# 逐条渲染。用 while 读进程替换而不是 $( )，理由见上面的注释。
# 组内排序用 LC_ALL=C 固定成字节序：不同机器的 locale 会让大小写排序结果不一致，
# 而 CI（Linux）和本地（macOS）必须产出一模一样的正文。
printf '%s\n' "${raw}" \
  | while IFS= read -r _line; do
    [ -n "${_line}" ] || continue
    _sha="${_line%%"${SEP}"*}"
    _rest="${_line#*"${SEP}"}"
    _short="${_rest%%"${SEP}"*}"
    _rest="${_rest#*"${SEP}"}"
    _s="${_rest%%"${SEP}"*}"
    _a="${_rest#*"${SEP}"}"
    render_one "${_s}" "${_sha}" "${_short}" "${_a}"
  done \
  | LC_ALL=C sort -t "$(printf '\t')" -k1,1 -k3,3 \
    >"${parsed_file}"

if [ ! -s "${parsed_file}" ]; then
  rm -f "${parsed_file}"
  printf '与上一个版本（%s）相比没有新提交。\n' "${base}"
  exit 0
fi

# ── 截断 ────────────────────────────────────────────────────────────────────
# 长时间没发版时提交可能上百条，正文会被刷屏，每组只保留前 N 条。
# 按「组」而不是全局截断：全局切会让排在后面的组整体消失（chore / Tests 直接不见），
# 读者会以为这次没改那些地方。
CHANGELOG_LIMIT="${CHANGELOG_LIMIT:-50}"
total="$(wc -l <"${parsed_file}" | tr -d ' ')"
awk -F '\t' -v limit="${CHANGELOG_LIMIT}" '
  { cnt[$2]++; if (cnt[$2] <= limit) print }
  END {
    kept = 0
    for (g in cnt) kept += (cnt[g] < limit ? cnt[g] : limit)
    printf "%d\n", kept > "/dev/stderr"
  }
' "${parsed_file}" >"${parsed_file}.cut" 2>"${parsed_file}.kept"
kept="$(cat "${parsed_file}.kept")"
omitted=$((total - kept))
mv "${parsed_file}.cut" "${parsed_file}"
rm -f "${parsed_file}.kept"

# ── 按组输出 ────────────────────────────────────────────────────────────────
while IFS="$(printf '\t')" read -r _lower _group _label _rendered; do
  if [ "${_group}" != "${_prev_group:-}" ]; then
    # 组之间空一行，第一组前面不留空行
    [ -z "${_prev_group:-}" ] || printf '\n'
    printf '### %s\n\n' "${_group}"
    _prev_group="${_group}"
  fi
  printf '%s\n' "${_rendered}"
done <"${parsed_file}"

if [ "${omitted}" -gt 0 ]; then
  # REPO_URL 没传时不拼链接：拼出来是 "/commits/..." 这种点不开的相对路径
  if [ -n "${REPO_URL:-}" ]; then
    printf '\n> 另有 %d 条提交未列出，完整记录见 [提交历史](%s/commits/%s..HEAD)。\n' \
      "${omitted}" "${REPO_URL}" "${base}"
  else
    printf '\n> 另有 %d 条提交未列出。\n' "${omitted}"
  fi
fi
