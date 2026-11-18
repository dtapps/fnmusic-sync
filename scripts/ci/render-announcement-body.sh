#!/bin/sh
#
# 生成「发版公告」Issue 正文（Markdown）。
#
# 为什么单独一份脚本：CNB 发布流水线在 Release 发布成功后会自动开一条 Issue，
# 公告正文的字段口径必须与 Issue 模板（.cnb/ISSUE_TEMPLATE/release-announcement.yml）
# 一致，而下载表 / 校验 / 更新内容又必须与 Release 正文（render-release-body.sh 生成）
# 同源——两处各写一遍必然慢慢跑偏，所以公告正文只从这里出。
#
# 只用环境变量传参（不解析位置参数），流水线里可以直接：
#   VERSION=v1.2.3 PRERELEASE=false sh scripts/ci/render-announcement-body.sh
#
# 环境变量：
#   VERSION       必填，归一化后的版本号（带 v 前缀），如 v1.2.3 / v1.2.3-rc.1
#   PRERELEASE    必填，true / false，决定是否加「预发布」警示块
#   RELEASE_BODY  选填，Release 正文文件路径，默认 release_body.md
#   OUT_FILE      选填，输出文件，默认 announcement_body.md
#   RELEASE_DATE  选填，发布日期（YYYY-MM-DD），默认取 CNB_BUILD_START_TIME 再退回当前 UTC 日期
#   COMMIT_SHA    选填，发布提交完整 SHA；有则显示短号并链接到提交页
#   COMMIT_URL    选填，提交链接前缀；缺省时用 CNB_WEB_BASE 拼「<base>/commit」
#   CNB_WEB_BASE  选填，CNB 仓库 Web 前缀（含 /-），如 https://cnb.cool/dtapp/fnmusic-sync/-
#   GITHUB_WEB    选填，GitHub 仓库地址，默认 https://github.com/dtapps/fnmusic-sync
#   STABLE_URL    选填，正式版列表页；预发布时提示用户去哪找稳定版
#
# 自检（改了格式先跑它）：
#   sh scripts/ci/render-announcement-body.sh --self-test
#
# ⚠️ 必须保持 POSIX sh 语法：CNB 的 stage 默认用 sh（dash/ash）执行。

set -eu

# 从 Release 正文里截取「下载 / 校验 / 更新内容」三节。
# 发布信息表（版本号 / 发布类型 / 构建时间）不取：公告自己有一张表，
# 两张表并排会让读者对着同一批数字找差异。
extract_sections() {
  if grep -q '^## 下载' "$1"; then
    awk '/^## 下载/{f=1} f' "$1"
  else
    # Release 正文里没有「## 下载」小节（理论上不会发生）：整篇兜底，别把公告写空。
    cat "$1"
  fi
}

render_announcement() {
  : "${VERSION:?VERSION 为空}"
  : "${PRERELEASE:?PRERELEASE 为空}"

  release_body="${RELEASE_BODY:-release_body.md}"
  out_file="${OUT_FILE:-announcement_body.md}"

  if [ ! -f "${release_body}" ]; then
    printf '❌ Release 正文不存在: %s\n' "${release_body}" >&2
    return 1
  fi

  if [ "${PRERELEASE}" = 'true' ]; then
    release_type='🚧 预发布版本 (Pre-release)'
  else
    release_type='✅ 正式发布版本 (Stable)'
  fi

  # 发布日期：优先显式传入，其次流水线构建时间，最后当前 UTC 日期。
  if [ -n "${RELEASE_DATE:-}" ]; then
    release_date="${RELEASE_DATE}"
  elif [ -n "${CNB_BUILD_START_TIME:-}" ]; then
    # CNB_BUILD_START_TIME 是 ISO8601（2025-08-21T09:13:45.803Z），取日期部分即可。
    release_date="$(printf '%s' "${CNB_BUILD_START_TIME}" | cut -c1-10)"
  else
    release_date="$(date -u '+%Y-%m-%d')"
  fi

  # 标题：与 Issue 模板预填的标题同格式（[发版公告] FnMusicSync <版本号>）。
  # CNB 的 Issue 与 GitHub 的 Discussion 共用这一份，别在两边各写一遍。
  title="[发版公告] FnMusicSync ${VERSION}"
  if [ -n "${TITLE_FILE:-}" ]; then
    printf %s "${title}" >"${TITLE_FILE}"
  fi

  cnb_web_base="${CNB_WEB_BASE:-}"
  github_web="${GITHUB_WEB:-https://github.com/dtapps/fnmusic-sync}"

  # 提交号保留完整 SHA 作为**链接目标**（缩写长度随仓库规模变化，链接里用缩写
  # 部分平台会 404）；另算一个短号只用于**显示**，和 GitHub 页面习惯一致。
  commit_short=''
  if [ -n "${COMMIT_SHA:-}" ]; then
    COMMIT_SHA="$(printf '%s' "${COMMIT_SHA}" | tr -d '[:space:]')"
    commit_short="$(printf '%s' "${COMMIT_SHA}" | cut -c1-7)"
    commit_url="${COMMIT_URL:-}"
    if [ -z "${commit_url}" ] && [ -n "${cnb_web_base}" ]; then
      commit_url="${cnb_web_base%/}/commit"
    fi
  fi

  {
    echo '## 📣 发版公告'
    echo ''
    echo '> 本公告由发布流水线在 Release 发布成功后自动创建；下载表、校验与更新内容与 Release 正文同源。'
    echo ''

    # 预发布警示放在最前面：读者往下滚到下载表才发现是 rc，人已经点完了。
    if [ "${PRERELEASE}" = 'true' ]; then
      echo '## ⚠️ 预发布版本'
      echo ''
      echo '> 这是预发布版本，可能包含未充分测试的功能，不建议用于生产环境。'
      if [ -n "${STABLE_URL:-}" ]; then
        printf '> 如需稳定版本，请使用 [最新正式版](%s)。\n' "${STABLE_URL}"
      fi
      echo ''
    fi

    echo '| 项目 | 内容 |'
    echo '| --- | --- |'
    printf '| 版本号 | %s |\n' "${VERSION}"
    printf '| 发布类型 | %s |\n' "${release_type}"
    printf '| 发布日期 | %s |\n' "${release_date}"
    if [ -n "${commit_short}" ] && [ -n "${commit_url:-}" ]; then
      printf '| 发布提交 | [%s](%s/%s) |\n' "${commit_short}" "${commit_url}" "${COMMIT_SHA}"
    elif [ -n "${commit_short}" ]; then
      printf '| 发布提交 | %s |\n' "${commit_short}"
    fi
    if [ -n "${cnb_web_base}" ]; then
      printf '| Release 页面 | [CNB](%s/releases/tag/%s) · [GitHub](%s/releases/tag/%s) |\n' \
        "${cnb_web_base}" "${VERSION}" "${github_web}" "${VERSION}"
    else
      printf '| Release 页面 | [GitHub](%s/releases/tag/%s) |\n' "${github_web}" "${VERSION}"
    fi
    echo ''

    # 下载表 / 校验 / 更新内容：整段取自 Release 正文，不另编一套。
    extract_sections "${release_body}"
    echo ''

    echo '## 发布前检查'
    echo ''
    # 反引号是 Markdown 代码标记，不是命令替换，单引号正是要它原样输出（SC2016 属误报）
    # shellcheck disable=SC2016
    echo '- [x] Release 附件已上传，`SHA256SUMS` 可校验'
    echo '- [x] 下载与校验以 Release 页为准（CNB / GitHub，地址见 README「仓库」表）'
    echo '- [x] 本公告由流水线自动创建，内容与 Release 正文同源'
  } >"${out_file}"

  cat "${out_file}"
}

# 直接执行才渲染/自检；被 source 时只导出函数。
# 判定用 $0 的 basename（不能用 BASH_SOURCE，dash 会 Bad substitution）。
if [ "${0##*/}" != 'render-announcement-body.sh' ] && [ "${1:-}" != '--self-test' ]; then
  # shellcheck disable=SC2317
  return 0 2>/dev/null || exit 0
fi

if [ "${1:-}" = '--self-test' ]; then
  tmp_dir="$(mktemp -d)"
  trap 'rm -rf "${tmp_dir}"' EXIT

  # Release 正文样例：结构对齐 render-release-body.sh 的输出（发布信息 / 下载 / 校验 / 更新内容）。
  cat >"${tmp_dir}/release_body.md" <<'EOF'
## 发布信息

| 项目 | 内容 |
| --- | --- |
| 版本号 | v1.2.3 |
| 发布类型 | ✅ 正式发布版本 (Stable) |
| 构建时间 | 2026-11-17 09:13:45 UTC |

## 下载

| 平台 | 架构 | 文件 | 大小 | 说明 |
| --- | --- | --- | --- | --- |
| Linux | x86_64 | [fnmusic-sync_amd64.deb](https://cnb.cool/dtapp/fnmusic-sync/-/releases/download/v1.2.3/fnmusic-sync_amd64.deb) | 12M | Debian/Ubuntu 安装包（dpkg -i） |

## 校验

附件含 `SHA256SUMS`。

## 更新内容

- 修复 A
- 优化 B
EOF

  failures=0
  expect() {
    # expect <文件> <期望出现的行> <说明>
    if grep -qF -- "$2" "$1"; then
      printf '  ✓ %s\n' "$3"
    else
      printf '  ✗ %s（未找到: %s）\n' "$3" "$2"
      failures=$((failures + 1))
    fi
  }
  reject() {
    # reject <文件> <不应出现的行> <说明>
    if grep -qF -- "$2" "$1"; then
      printf '  ✗ %s（不应出现: %s）\n' "$3" "$2"
      failures=$((failures + 1))
    else
      printf '  ✓ %s\n' "$3"
    fi
  }

  echo '── 正式版 ──'
  VERSION='v1.2.3' PRERELEASE='false' \
    RELEASE_BODY="${tmp_dir}/release_body.md" OUT_FILE="${tmp_dir}/stable.md" \
    RELEASE_DATE='2026-11-17' COMMIT_SHA='0123456789abcdef0123456789abcdef01234567' \
    CNB_WEB_BASE='https://cnb.cool/dtapp/fnmusic-sync/-' \
    render_announcement >/dev/null
  expect "${tmp_dir}/stable.md" '## 📣 发版公告' '公告标题'
  expect "${tmp_dir}/stable.md" '| 版本号 | v1.2.3 |' '版本号行'
  expect "${tmp_dir}/stable.md" '| 发布类型 | ✅ 正式发布版本 (Stable) |' '正式版标记'
  expect "${tmp_dir}/stable.md" '| 发布日期 | 2026-11-17 |' '发布日期行'
  expect "${tmp_dir}/stable.md" \
    '| 发布提交 | [0123456](https://cnb.cool/dtapp/fnmusic-sync/-/commit/0123456789abcdef0123456789abcdef01234567) |' \
    '提交行：短号显示、完整 SHA 进链接'
  expect "${tmp_dir}/stable.md" \
    '| Release 页面 | [CNB](https://cnb.cool/dtapp/fnmusic-sync/-/releases/tag/v1.2.3) · [GitHub](https://github.com/dtapps/fnmusic-sync/releases/tag/v1.2.3) |' \
    'Release 页面行（CNB + GitHub）'
  expect "${tmp_dir}/stable.md" '## 下载' '下载小节来自 Release 正文'
  expect "${tmp_dir}/stable.md" 'fnmusic-sync_amd64.deb' '下载表内容保留'
  expect "${tmp_dir}/stable.md" '## 校验' '校验小节保留'
  expect "${tmp_dir}/stable.md" '## 更新内容' '更新内容小节保留'
  expect "${tmp_dir}/stable.md" '- 修复 A' '更新内容条目保留'
  expect "${tmp_dir}/stable.md" '## 发布前检查' '发布前检查小节'
  # 发布信息表不并排出现：公告自己有一张表，再抄一张会让读者对着同一批数字找差异
  reject "${tmp_dir}/stable.md" '| 构建时间 |' '不重复 Release 正文的发布信息表'
  reject "${tmp_dir}/stable.md" '## ⚠️ 预发布版本' '正式版不出现预发布警示'

  echo '── 预发布 ──'
  VERSION='v1.2.3-rc.1' PRERELEASE='true' \
    RELEASE_BODY="${tmp_dir}/release_body.md" OUT_FILE="${tmp_dir}/rc.md" \
    RELEASE_DATE='2026-11-17' STABLE_URL='https://cnb.cool/dtapp/fnmusic-sync/-/releases/latest' \
    CNB_WEB_BASE='https://cnb.cool/dtapp/fnmusic-sync/-' \
    render_announcement >/dev/null
  expect "${tmp_dir}/rc.md" '## ⚠️ 预发布版本' '预发布警示块'
  expect "${tmp_dir}/rc.md" '🚧 预发布版本 (Pre-release)' '预发布标记'
  expect "${tmp_dir}/rc.md" '[最新正式版](https://cnb.cool/dtapp/fnmusic-sync/-/releases/latest)' '稳定版指引链接'
  expect "${tmp_dir}/rc.md" '| 版本号 | v1.2.3-rc.1 |' '预发布版本号'

  echo '── 无提交号 / 无 CNB 前缀 ──'
  # 注意：POSIX sh 里 `VAR=x func` 的赋值在函数返回后仍然生效（会泄漏到后续调用），
  # 所以这里必须显式清空，不能靠「上一次的调用没传」。
  VERSION='v1.2.3' PRERELEASE='false' \
    RELEASE_BODY="${tmp_dir}/release_body.md" OUT_FILE="${tmp_dir}/nocommit.md" \
    RELEASE_DATE='2026-11-17' COMMIT_SHA='' CNB_WEB_BASE='' \
    render_announcement >/dev/null
  reject "${tmp_dir}/nocommit.md" '| 发布提交 |' '没有提交号时不出现空的提交行'
  expect "${tmp_dir}/nocommit.md" '| Release 页面 | [GitHub](https://github.com/dtapps/fnmusic-sync/releases/tag/v1.2.3) |' \
    '没有 CNB 前缀时只给 GitHub Release 链接'

  echo '── Release 正文缺「## 下载」 ──'
  printf '## 更新内容\n\n- 只有更新内容\n' >"${tmp_dir}/partial.md"
  VERSION='v1.2.3' PRERELEASE='false' \
    RELEASE_BODY="${tmp_dir}/partial.md" OUT_FILE="${tmp_dir}/partial.out" \
    RELEASE_DATE='2026-11-17' \
    render_announcement >/dev/null
  expect "${tmp_dir}/partial.out" '- 只有更新内容' '缺小节时整篇兜底，不丢内容'

  echo '── 缺 Release 正文 ──'
  if VERSION='v1.2.3' PRERELEASE='false' RELEASE_BODY="${tmp_dir}/none.md" \
    OUT_FILE="${tmp_dir}/none.out" render_announcement >/dev/null 2>&1; then
    printf '  ✗ 缺 Release 正文时应失败\n'
    failures=$((failures + 1))
  else
    printf '  ✓ 缺 Release 正文时失败\n'
  fi

  if [ "$failures" -ne 0 ]; then
    printf '\n自检失败: %d 项\n' "$failures" >&2
    exit 1
  fi
  printf '\n自检通过\n'
  exit 0
fi

render_announcement
