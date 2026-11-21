#!/bin/sh
# 通用 Release 删除脚本：GitHub / CNB / GitLab / Gitea / Gitee / GitCode 共用。
#
# 用法：
#   sh scripts/ci/delete-release.sh --platform github  --keep 3
#   sh scripts/ci/delete-release.sh --platform cnb     --keep 3
#   sh scripts/ci/delete-release.sh --platform gitlab  --keep 3
#   sh scripts/ci/delete-release.sh --platform gitea   --keep 3
#   sh scripts/ci/delete-release.sh --platform gitee   --keep 3
#   sh scripts/ci/delete-release.sh --platform gitcode --keep 3
#   sh scripts/ci/delete-release.sh --platform cnb --tag v1.2.4-nightly.20260101
#   sh scripts/ci/delete-release.sh --platform gitee --tag v1.2.4 --keep-tag
#
# 环境变量是统一的，任意平台都可用同一套 RELEASE_ 写法，不必记各平台原生变量名：
#   RELEASE_TOKEN / RELEASE_REPO / RELEASE_API         通用（所有平台）
#   RELEASE_<平台>_TOKEN / _REPO / _API                平台专属覆盖（优先级更高）
#   平台原生变量                                       兜底（CI 自动注入的那些）
# 优先级：RELEASE_<平台>_* > RELEASE_* > 平台原生变量。例：
#   RELEASE_TOKEN=xxx RELEASE_REPO=owner/repo \
#     sh scripts/ci/delete-release.sh --platform gitlab --keep 3
#
# 清理模式（--keep N，默认 3）：
#   只挑「预发布 + tag 以 -nightly.YYYYMMDD 结尾」的 Release，按日期保留最新 N 个，
#   其余连 Release 带 tag 一起删。清理失败只告警，始终退出 0（不能连累发布）。
# 指定版本模式（--tag TAG）：
#   删除该 tag 对应的 Release 与 tag。Release 删除失败（且不是「本来就不存在」）时退出 1，
#   调用方据此决定是否继续重建；tag 删除失败只告警。
#   --keep-tag：只删 Release、保留 tag（重建 Release 时复用原 tag，Gitee 发布流程用）。
#
# 所有平台一致的几点：
#   1. 删 Release 不会连带删 tag，必须分别删；
#   2. 删完重新查一遍，确认 Release 与 tag 都真的不存在。
#
# 平台差异：
#   - GitHub ：gh CLI，--cleanup-tag 连带删 tag，再兜底删 ref；
#   - CNB    ：tag 删除路径必须带 git 段 /-/git/tags/{tag}。写成 /-/tags/{tag}
#              会 404，而 404 又常被当成「tag 本来就不存在」，导致 tag 永远删不掉；
#   - GitLab ：删 Release 不删 tag，需再删 /repository/tags/{tag}；
#              Release 没有 prerelease 字段，只能靠 tag 后缀识别 nightly；
#   - Gitea  ：Release 可直接按 tag 删（/releases/tags/{tag}），tag 单独删；
#   - Gitee  ：v5 没有删 tag 的接口（tags 只有 GET/POST），Release 走 API 删，
#              tag 只能 git push --delete（用 token 做 Basic 认证）；因此需要能访问
#              仓库的 git 工作区。GET /releases/tags/{tag} 不存在时返回 200 + null，
#              不能只看状态码；
#   - GitCode：删 tag 有官方接口（DELETE /repos/{owner}/{repo}/tags/{tag_name}）；
#              删 Release 官方文档/OpenAPI 里没有（/releases/{tag} 只有 GET/PATCH），
#              脚本会先尝试 DELETE 一次，不支持时只删 tag 并告警 Release 记录仍在。
#
# 环境变量：
#   通用   ：RELEASE_TOKEN / RELEASE_REPO / RELEASE_API 对六个平台都生效。
#   专属   ：RELEASE_<PLATFORM>_TOKEN / _REPO / _API，PLATFORM 取
#            GITHUB / CNB / GITLAB / GITEA / GITEE / GITCODE，优先级高于通用变量。
#   原生   ：github → GH_TOKEN、GITHUB_REPOSITORY（REPO 亦可）；cnb → CNB_TOKEN /
#            CNB_API_ENDPOINT / CNB_REPO_SLUG（流水线自动注入）；gitlab → GITLAB_TOKEN /
#            GITLAB_REPO；gitea → GITEA_TOKEN / GITEA_REPO；gitee → GITEE_TOKEN /
#            GITEE_REPO；gitcode → GITCODE_TOKEN / GITCODE_REPO。
#   默认值 ：gitlab → https://gitlab.com/api/v4；gitea → https://gitea.com/api/v1；
#            gitee → https://gitee.com/api/v5；gitcode → https://api.gitcode.com/api/v5。
#
# 依赖：github → gh；gitee 删 tag 额外需要 git；其余 → curl + jq。

set -u

usage() {
  sed -n '2,/^$/p' "$0" | sed 's/^# \{0,1\}//' | sed '/^$/d'
}

PLATFORM=
KEEP=3
ONE_TAG=
KEEP_TAG=0

while [ $# -gt 0 ]; do
  case "$1" in
    --platform)
      PLATFORM="${2:-}"
      shift
      [ $# -gt 0 ] && shift
      ;;
    --keep)
      KEEP="${2:-}"
      shift
      [ $# -gt 0 ] && shift
      ;;
    --tag)
      ONE_TAG="${2:-}"
      shift
      [ $# -gt 0 ] && shift
      ;;
    --keep-tag)
      KEEP_TAG=1
      shift
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      echo "未知参数：$1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

case "$PLATFORM" in
  github | cnb | gitlab | gitea | gitee | gitcode) ;;
  *)
    echo "错误：--platform 只能是 github / cnb / gitlab / gitea / gitee / gitcode" >&2
    usage >&2
    exit 2
    ;;
esac
case "$KEEP_TAG" in
  1)
    if [ "$PLATFORM" != "gitee" ] || [ -z "$ONE_TAG" ]; then
      echo "错误：--keep-tag 只用于 Gitee 的 --tag 模式（重建 Release 时复用 tag）" >&2
      exit 2
    fi
    ;;
esac
case "$KEEP" in
  '' | *[!0-9]*)
    echo "错误：--keep 必须是数字" >&2
    exit 2
    ;;
esac

info() { printf '%s\n' "$*"; }
warn() { printf '⚠ %s\n' "$*" >&2; }

# 按 tag 末段日期倒序：v0.2.8-nightly.20261114 → 20261114
sort_by_date_desc() {
  awk -F. 'NF > 1 { print $NF "\t" $0 }' | sort -rn | cut -f2-
}

# ── 公共 HTTP（GitLab / Gitea）────────────────────────────────────────────
# 结果：HTTP_CODE 状态码，HTTP_BODY_FILE 响应体（jq 直接读文件，不塞变量）。
HTTP_BODY_FILE=
HTTP_CODE=000

api_http() {
  api_method=$1
  api_url=$2
  shift 2
  HTTP_CODE=$(curl -sS -o "$HTTP_BODY_FILE" -w '%{http_code}' -X "$api_method" "$@" "$api_url" 2>/dev/null) || HTTP_CODE=000
}

# nightly tag 与项目路径里真正需要转义的只有 /；其余字符 GitLab / Gitea 都接受原样
enc_slash() { printf '%s' "$1" | sed 's|/|%2F|g'; }

# 按顺序取第一个非空环境变量：pick_env 候选1 候选2 ...
# 用于统一环境变量：RELEASE_<平台>_* > RELEASE_* > 平台原生变量。
pick_env() {
  for pick_name in "$@"; do
    eval "pick_value=\${$pick_name:-}"
    if [ -n "$pick_value" ]; then
      printf '%s' "$pick_value"
      return 0
    fi
  done
  return 0
}

# ── GitHub ────────────────────────────────────────────────────────────────
GH_REPO=$(pick_env RELEASE_GITHUB_REPO RELEASE_REPO GITHUB_REPOSITORY REPO)
GH_TOKEN=$(pick_env RELEASE_GITHUB_TOKEN RELEASE_TOKEN GH_TOKEN)

gh_has_release() { gh release view "$1" --json tagName >/dev/null 2>&1; }
gh_has_tag() { gh api "repos/$GH_REPO/git/ref/tags/$1" >/dev/null 2>&1; }

gh_list_nightly() {
  # --limit 200：清理每天都跑，积压不会超过一页；真超了再调大。
  gh release list --limit 200 --json tagName,isPrerelease,isDraft \
    --jq '.[] | select(.isPrerelease and (.isDraft | not)) | .tagName' 2>/dev/null \
    | grep -E -- '-nightly\.[0-9]{8}$' || true
}

gh_delete_one() {
  gh_one_tag=$1
  gh_strict=$2
  gh_failed=0

  if gh_has_release "$gh_one_tag"; then
    if gh release delete "$gh_one_tag" --yes --cleanup-tag >/dev/null 2>&1; then
      info "已删除 GitHub Release 与 tag：$gh_one_tag"
    else
      warn "删除 GitHub Release 失败：$gh_one_tag"
      gh_failed=1
    fi
  fi

  # Release 不存在但 tag 还在时 --cleanup-tag 无从发挥，单独删 ref
  if gh_has_tag "$gh_one_tag"; then
    if gh api -X DELETE "repos/$GH_REPO/git/ref/tags/$gh_one_tag" >/dev/null 2>&1; then
      info "已删除 GitHub 残留 tag：$gh_one_tag"
    else
      warn "删除 GitHub tag 失败：$gh_one_tag"
    fi
  fi

  # 删除后复查，确认两个对象都真的没了
  if gh_has_release "$gh_one_tag"; then
    warn "删除后仍能查到 Release：$gh_one_tag"
    [ "$gh_strict" = "1" ] && gh_failed=1
  fi
  if gh_has_tag "$gh_one_tag"; then
    warn "删除后仍能查到 tag：$gh_one_tag"
  elif ! gh_has_release "$gh_one_tag"; then
    info "✓ 已确认 $gh_one_tag 的 Release 与 tag 都不存在"
  fi

  [ "$gh_failed" -ne 0 ] && return 1
  return 0
}

# ── CNB ───────────────────────────────────────────────────────────────────
CNB_TOKEN=$(pick_env RELEASE_CNB_TOKEN RELEASE_TOKEN CNB_TOKEN)
CNB_API_ENDPOINT=$(pick_env RELEASE_CNB_API RELEASE_API CNB_API_ENDPOINT)
CNB_REPO_SLUG=$(pick_env RELEASE_CNB_REPO RELEASE_REPO CNB_REPO_SLUG)
CNB_API=
CNB_REQ=
CNB_BODY=
CNB_CODE=

cnb_http() {
  CNB_CODE=$(curl -sS -o "$CNB_BODY" -w '%{http_code}' -X "$1" \
    -H "Authorization: Bearer $CNB_TOKEN" \
    -H "Accept: application/vnd.cnb.api+json" \
    "$CNB_API/$CNB_REQ$2" 2>/dev/null) || CNB_CODE=000
}

cnb_list_nightly() {
  cnb_page=1
  while [ "$cnb_page" -le 10 ]; do
    cnb_http GET "/-/releases?page=$cnb_page&page_size=100"
    if [ "$CNB_CODE" != "200" ]; then
      warn "获取 CNB Release 列表失败（HTTP $CNB_CODE），跳过清理"
      return 1
    fi
    if ! jq -e . <"$CNB_BODY" >/dev/null 2>&1; then
      warn "CNB Release 列表不是合法 JSON，跳过清理"
      return 1
    fi
    cnb_count=$(jq 'if type == "array" then length else ((.items // .list // .data // []) | length) end' <"$CNB_BODY")
    # 形状兼容裸数组与 {items|list|data: [...]} 包装
    jq -r '
      (if type == "array" then . else (.items // .list // .data // []) end)
      | .[]
      | select((.draft // false) | not)
      | select(.prerelease == true)
      | .tag_name
      | select(test("-nightly\\.[0-9]{8}$"))
    ' <"$CNB_BODY"
    [ "${cnb_count:-0}" -lt 100 ] && return 0
    cnb_page=$((cnb_page + 1))
  done
  return 0
}

cnb_delete_one() {
  cnb_one_tag=$1
  cnb_strict=$2
  cnb_failed=0

  # 1) 删 Release（DELETE /-/releases/{id} 不会连带删 tag）
  cnb_http GET "/-/releases/tags/$cnb_one_tag"
  if [ "$CNB_CODE" = "200" ]; then
    cnb_id=$(jq -r '.id // .release_id // empty' <"$CNB_BODY" 2>/dev/null || true)
    if [ -n "$cnb_id" ]; then
      cnb_http DELETE "/-/releases/$cnb_id"
      case "$CNB_CODE" in
        200 | 204) info "已删除 CNB Release：$cnb_one_tag（id=$cnb_id）" ;;
        *)
          warn "删除 CNB Release 失败：$cnb_one_tag（HTTP $CNB_CODE）"
          cnb_failed=1
          ;;
      esac
    else
      warn "查不到 $cnb_one_tag 的 Release id，跳过 Release 删除"
      [ "$cnb_strict" = "1" ] && cnb_failed=1
    fi
  elif [ "$CNB_CODE" != "404" ]; then
    warn "查询 CNB Release 失败：$cnb_one_tag（HTTP $CNB_CODE）"
    [ "$cnb_strict" = "1" ] && cnb_failed=1
  fi

  # 2) 删 tag：必须走 /-/git/tags/…，404 表示本来就不存在
  cnb_http DELETE "/-/git/tags/$cnb_one_tag"
  case "$CNB_CODE" in
    200 | 204) info "已删除 CNB tag：$cnb_one_tag" ;;
    404) : ;;
    *) warn "删除 CNB tag 失败：$cnb_one_tag（HTTP $CNB_CODE）" ;;
  esac

  # 3) 删除后复查，确认两个对象都真的没了
  cnb_http GET "/-/git/tags/$cnb_one_tag"
  cnb_tag_left=$CNB_CODE
  cnb_http GET "/-/releases/tags/$cnb_one_tag"
  cnb_rel_left=$CNB_CODE
  if [ "$cnb_tag_left" = "404" ] && [ "$cnb_rel_left" = "404" ]; then
    info "✓ 已确认 $cnb_one_tag 的 Release 与 tag 都不存在"
  else
    warn "删除后仍能查到或无法确认：$cnb_one_tag（tag HTTP=$cnb_tag_left，release HTTP=$cnb_rel_left）"
    [ "$cnb_rel_left" = "200" ] && [ "$cnb_strict" = "1" ] && cnb_failed=1
  fi

  [ "$cnb_failed" -ne 0 ] && return 1
  return 0
}

# ── GitLab ────────────────────────────────────────────────────────────────
GITLAB_TOKEN=$(pick_env RELEASE_GITLAB_TOKEN RELEASE_TOKEN GITLAB_TOKEN)
GITLAB_REPO=$(pick_env RELEASE_GITLAB_REPO RELEASE_REPO GITLAB_REPO)
GITLAB_API=$(pick_env RELEASE_GITLAB_API RELEASE_API GITLAB_API)
GITLAB_API="${GITLAB_API:-https://gitlab.com/api/v4}"
GITLAB_PROJECT=

gitlab_http() {
  api_http "$1" "$GITLAB_API$2" -H "PRIVATE-TOKEN: $GITLAB_TOKEN"
}

# GitLab 的 Release 没有 prerelease 字段，nightly 只能靠 tag 后缀识别；
# 后缀正则本身就把 rc / beta / alpha 与正式版排除在外。
gitlab_list_nightly() {
  gitlab_page=1
  while [ "$gitlab_page" -le 10 ]; do
    gitlab_http GET "/projects/$GITLAB_PROJECT/releases?per_page=100&page=$gitlab_page"
    if [ "$HTTP_CODE" != "200" ]; then
      warn "获取 GitLab Release 列表失败（HTTP $HTTP_CODE），跳过清理"
      return 1
    fi
    gitlab_count=$(jq 'length' <"$HTTP_BODY_FILE" 2>/dev/null) || gitlab_count=0
    jq -r '.[] | .tag_name // empty' <"$HTTP_BODY_FILE" 2>/dev/null \
      | grep -E -- '-nightly\.[0-9]{8}$' || true
    [ "${gitlab_count:-0}" -lt 100 ] && return 0
    gitlab_page=$((gitlab_page + 1))
  done
  return 0
}

gitlab_delete_one() {
  gitlab_one_tag=$1
  gitlab_strict=$2
  gitlab_failed=0
  gitlab_tag_enc=$(enc_slash "$gitlab_one_tag")

  # 1) 删 Release（GitLab 删 Release 不连带删 tag）
  gitlab_http GET "/projects/$GITLAB_PROJECT/releases/$gitlab_tag_enc"
  case "$HTTP_CODE" in
    200)
      gitlab_http DELETE "/projects/$GITLAB_PROJECT/releases/$gitlab_tag_enc"
      case "$HTTP_CODE" in
        200 | 202 | 204) info "已删除 GitLab Release：$gitlab_one_tag" ;;
        *)
          warn "删除 GitLab Release 失败：$gitlab_one_tag（HTTP $HTTP_CODE）"
          gitlab_failed=1
          ;;
      esac
      ;;
    404) : ;;
    *)
      warn "查询 GitLab Release 失败：$gitlab_one_tag（HTTP $HTTP_CODE）"
      [ "$gitlab_strict" = "1" ] && gitlab_failed=1
      ;;
  esac

  # 2) 删 tag：DELETE /projects/:id/repository/tags/:tag，404 表示本来就不存在
  gitlab_http DELETE "/projects/$GITLAB_PROJECT/repository/tags/$gitlab_tag_enc"
  case "$HTTP_CODE" in
    200 | 204) info "已删除 GitLab tag：$gitlab_one_tag" ;;
    404) : ;;
    *) warn "删除 GitLab tag 失败：$gitlab_one_tag（HTTP $HTTP_CODE）" ;;
  esac

  # 3) 删除后复查，确认两个对象都真的没了
  gitlab_http GET "/projects/$GITLAB_PROJECT/releases/$gitlab_tag_enc"
  gitlab_rel_left=$HTTP_CODE
  gitlab_http GET "/projects/$GITLAB_PROJECT/repository/tags/$gitlab_tag_enc"
  gitlab_tag_left=$HTTP_CODE
  if [ "$gitlab_rel_left" = "404" ] && [ "$gitlab_tag_left" = "404" ]; then
    info "✓ 已确认 $gitlab_one_tag 的 Release 与 tag 都不存在"
  else
    warn "删除后仍能查到或无法确认：$gitlab_one_tag（Release HTTP=$gitlab_rel_left，tag HTTP=$gitlab_tag_left）"
    [ "$gitlab_rel_left" = "200" ] && [ "$gitlab_strict" = "1" ] && gitlab_failed=1
  fi

  [ "$gitlab_failed" -ne 0 ] && return 1
  return 0
}

# ── Gitea ─────────────────────────────────────────────────────────────────
GITEA_TOKEN=$(pick_env RELEASE_GITEA_TOKEN RELEASE_TOKEN GITEA_TOKEN)
GITEA_REPO=$(pick_env RELEASE_GITEA_REPO RELEASE_REPO GITEA_REPO)
GITEA_API=$(pick_env RELEASE_GITEA_API RELEASE_API GITEA_API)
GITEA_API="${GITEA_API:-https://gitea.com/api/v1}"

gitea_http() {
  api_http "$1" "$GITEA_API$2" -H "Authorization: token $GITEA_TOKEN"
}

gitea_list_nightly() {
  gitea_page=1
  while [ "$gitea_page" -le 10 ]; do
    gitea_http GET "/repos/$GITEA_REPO/releases?page=$gitea_page&limit=50"
    if [ "$HTTP_CODE" != "200" ]; then
      warn "获取 Gitea Release 列表失败（HTTP $HTTP_CODE），跳过清理"
      return 1
    fi
    gitea_count=$(jq 'length' <"$HTTP_BODY_FILE" 2>/dev/null) || gitea_count=0
    jq -r '
      .[]
      | select((.draft // false) | not)
      | select(.prerelease == true)
      | .tag_name
      | select(test("-nightly\\.[0-9]{8}$"))
    ' <"$HTTP_BODY_FILE" 2>/dev/null || true
    [ "${gitea_count:-0}" -lt 50 ] && return 0
    gitea_page=$((gitea_page + 1))
  done
  return 0
}

gitea_delete_one() {
  gitea_one_tag=$1
  gitea_strict=$2
  gitea_failed=0
  gitea_tag_enc=$(enc_slash "$gitea_one_tag")

  # 1) 删 Release：可按 tag 直接删（不等于删 tag）
  gitea_http GET "/repos/$GITEA_REPO/releases/tags/$gitea_tag_enc"
  case "$HTTP_CODE" in
    200)
      gitea_http DELETE "/repos/$GITEA_REPO/releases/tags/$gitea_tag_enc"
      case "$HTTP_CODE" in
        200 | 204) info "已删除 Gitea Release：$gitea_one_tag" ;;
        *)
          warn "删除 Gitea Release 失败：$gitea_one_tag（HTTP $HTTP_CODE）"
          gitea_failed=1
          ;;
      esac
      ;;
    404) : ;;
    *)
      warn "查询 Gitea Release 失败：$gitea_one_tag（HTTP $HTTP_CODE）"
      [ "$gitea_strict" = "1" ] && gitea_failed=1
      ;;
  esac

  # 2) 删 tag：DELETE /repos/{owner}/{repo}/tags/{tag}
  gitea_http DELETE "/repos/$GITEA_REPO/tags/$gitea_tag_enc"
  case "$HTTP_CODE" in
    200 | 204) info "已删除 Gitea tag：$gitea_one_tag" ;;
    404) : ;;
    *) warn "删除 Gitea tag 失败：$gitea_one_tag（HTTP $HTTP_CODE，保护 tag 会 422/423）" ;;
  esac

  # 3) 删除后复查，确认两个对象都真的没了
  gitea_http GET "/repos/$GITEA_REPO/releases/tags/$gitea_tag_enc"
  gitea_rel_left=$HTTP_CODE
  gitea_http GET "/repos/$GITEA_REPO/tags/$gitea_tag_enc"
  gitea_tag_left=$HTTP_CODE
  if [ "$gitea_rel_left" = "404" ] && [ "$gitea_tag_left" = "404" ]; then
    info "✓ 已确认 $gitea_one_tag 的 Release 与 tag 都不存在"
  else
    warn "删除后仍能查到或无法确认：$gitea_one_tag（Release HTTP=$gitea_rel_left，tag HTTP=$gitea_tag_left）"
    [ "$gitea_rel_left" = "200" ] && [ "$gitea_strict" = "1" ] && gitea_failed=1
  fi

  [ "$gitea_failed" -ne 0 ] && return 1
  return 0
}

# ── Gitee ─────────────────────────────────────────────────────────────────
GITEE_TOKEN=$(pick_env RELEASE_GITEE_TOKEN RELEASE_TOKEN GITEE_TOKEN)
GITEE_REPO=$(pick_env RELEASE_GITEE_REPO RELEASE_REPO GITEE_REPO)
GITEE_API=$(pick_env RELEASE_GITEE_API RELEASE_API GITEE_API)
GITEE_API="${GITEE_API:-https://gitee.com/api/v5}"
GITEE_LOGIN=

gitee_http() {
  gitee_method=$1
  gitee_path=$2
  case "$gitee_path" in
    *\?*) gitee_url="$GITEE_API/repos/$GITEE_REPO$gitee_path&access_token=$GITEE_TOKEN" ;;
    *) gitee_url="$GITEE_API/repos/$GITEE_REPO$gitee_path?access_token=$GITEE_TOKEN" ;;
  esac
  api_http "$gitee_method" "$gitee_url"
}

# Gitee「没有这个 Release」时返回 200 + body null，不是 404，必须解析 body。
# 查询状态码与 id 写全局变量（不要用 $(...) 取，子 shell 里的赋值会丢）。
GITEE_LOOKUP_CODE=
GITEE_RELEASE_ID=
gitee_release_id() {
  gitee_http GET "/releases/tags/$1"
  GITEE_LOOKUP_CODE=$HTTP_CODE
  GITEE_RELEASE_ID=
  [ "$HTTP_CODE" = "200" ] || return 0
  GITEE_RELEASE_ID=$(jq -r 'if type == "object" then (.id // empty) else empty end' \
    <"$HTTP_BODY_FILE" 2>/dev/null || true)
}

# 输出 present / absent / unknown
gitee_tag_state() {
  gitee_page=1
  while [ "$gitee_page" -le 10 ]; do
    gitee_http GET "/tags?per_page=100&page=$gitee_page"
    if [ "$HTTP_CODE" != "200" ]; then
      echo unknown
      return
    fi
    if jq -e --arg t "$1" 'any(.[]; .name == $t)' <"$HTTP_BODY_FILE" >/dev/null 2>&1; then
      echo present
      return
    fi
    gitee_count=$(jq 'length' <"$HTTP_BODY_FILE" 2>/dev/null) || gitee_count=0
    [ "${gitee_count:-0}" -lt 100 ] && break
    gitee_page=$((gitee_page + 1))
  done
  echo absent
}

gitee_list_nightly() {
  gitee_page=1
  while [ "$gitee_page" -le 10 ]; do
    gitee_http GET "/releases?per_page=100&page=$gitee_page"
    if [ "$HTTP_CODE" != "200" ]; then
      warn "获取 Gitee Release 列表失败（HTTP $HTTP_CODE），跳过清理"
      return 1
    fi
    gitee_count=$(jq 'if type == "array" then length else 0 end' <"$HTTP_BODY_FILE" 2>/dev/null) || gitee_count=0
    jq -r '.[] | select(.prerelease == true) | .tag_name | select(test("-nightly\\.[0-9]{8}$"))' \
      <"$HTTP_BODY_FILE" 2>/dev/null || true
    [ "${gitee_count:-0}" -lt 100 ] && return 0
    gitee_page=$((gitee_page + 1))
  done
  return 0
}

# v5 没有删 tag 接口，只能 git push --delete；认证走 token（用户名从 /user 取）。
# tag 本来就不存在时 push 也会失败，按「已不存在」处理。
gitee_delete_tag() {
  gitee_tag=$1
  if ! command -v git >/dev/null 2>&1; then
    warn "当前环境没有 git，跳过 Gitee tag 删除：$gitee_tag"
    return 1
  fi
  if [ -z "$GITEE_LOGIN" ]; then
    api_http GET "$GITEE_API/user?access_token=$GITEE_TOKEN"
    if [ "$HTTP_CODE" = "200" ]; then
      GITEE_LOGIN=$(jq -r '.login // empty' <"$HTTP_BODY_FILE" 2>/dev/null || true)
    fi
    if [ -z "$GITEE_LOGIN" ]; then
      warn "取不到 Gitee 登录名（HTTP $HTTP_CODE），跳过 tag 删除：$gitee_tag"
      return 1
    fi
  fi
  gitee_auth=$(printf '%s:%s' "$GITEE_LOGIN" "$GITEE_TOKEN" | base64 | tr -d '\n')
  if GIT_TERMINAL_PROMPT=0 git -c http.extraHeader="Authorization: Basic $gitee_auth" \
    push "https://gitee.com/$GITEE_REPO.git" --delete "refs/tags/$gitee_tag" >/dev/null 2>&1; then
    info "已删除 Gitee tag：$gitee_tag"
    return 0
  fi
  if [ "$(gitee_tag_state "$gitee_tag")" = "absent" ]; then
    return 0
  fi
  warn "删除 Gitee tag 失败：$gitee_tag（git push --delete）"
  return 1
}

gitee_delete_one() {
  gitee_one_tag=$1
  gitee_strict=$2
  gitee_keep_tag=${3:-0}
  gitee_failed=0

  # 1) 删 Release（api 删 Release 不连带删 tag）
  gitee_rel_state=unknown
  gitee_release_id "$gitee_one_tag"
  gitee_id=$GITEE_RELEASE_ID
  if [ -n "$gitee_id" ]; then
    gitee_http DELETE "/releases/$gitee_id"
    case "$HTTP_CODE" in
      200 | 202 | 204)
        info "已删除 Gitee Release：$gitee_one_tag（id=$gitee_id）"
        gitee_rel_state=gone
        ;;
      *)
        warn "删除 Gitee Release 失败：$gitee_one_tag（HTTP $HTTP_CODE）"
        gitee_rel_state=present
        gitee_failed=1
        ;;
    esac
  else
    case "$GITEE_LOOKUP_CODE" in
      200 | 404) gitee_rel_state=gone ;; # 200 + null 表示本来就没有
      *)
        warn "查询 Gitee Release 失败：$gitee_one_tag（HTTP $GITEE_LOOKUP_CODE）"
        [ "$gitee_strict" = "1" ] && gitee_failed=1
        ;;
    esac
  fi

  # 2) 删 tag：API 不支持，走 git push --delete。
  # Release 状态未知或没删掉时不删 tag：否则会留下指向不存在 tag 的孤儿 Release。
  if [ "$gitee_keep_tag" = "1" ]; then
    info "保留 Gitee tag：$gitee_one_tag（重建 Release 复用该 tag）"
  elif [ "$gitee_rel_state" != "gone" ]; then
    warn "Release 未确认删除，跳过 Gitee tag 删除：$gitee_one_tag"
  else
    gitee_delete_tag "$gitee_one_tag" || :
  fi

  # 3) 删除后复查，确认对象真的没了
  gitee_release_id "$gitee_one_tag"
  gitee_left_id=$GITEE_RELEASE_ID
  case "$GITEE_LOOKUP_CODE" in
    200)
      if [ -n "$gitee_left_id" ]; then gitee_rel_state=present; else gitee_rel_state=absent; fi
      ;;
    404) gitee_rel_state=absent ;;
    *) gitee_rel_state=unknown ;;
  esac
  case "$gitee_rel_state" in
    present)
      warn "删除后仍能查到 Gitee Release：$gitee_one_tag"
      [ "$gitee_strict" = "1" ] && gitee_failed=1
      ;;
    unknown) warn "无法确认 Gitee Release 是否已删除：$gitee_one_tag（HTTP $GITEE_LOOKUP_CODE）" ;;
  esac

  if [ "$gitee_keep_tag" = "1" ]; then
    [ "$gitee_rel_state" = "absent" ] && info "✓ 已确认 Gitee Release 不存在（tag 按 --keep-tag 保留）"
  else
    case "$(gitee_tag_state "$gitee_one_tag")" in
      present) warn "删除后仍能查到 Gitee tag：$gitee_one_tag" ;;
      absent)
        [ "$gitee_rel_state" = "absent" ] && info "✓ 已确认 $gitee_one_tag 的 Release 与 tag 都不存在"
        ;;
      *) warn "无法确认 Gitee tag 是否已删除：$gitee_one_tag" ;;
    esac
  fi

  [ "$gitee_failed" -ne 0 ] && return 1
  return 0
}

# ── GitCode（AtomGit）─────────────────────────────────────────────────────
# 删 tag 有官方接口；删 Release 官方 OpenAPI 里没有（/releases/{tag} 只有 GET/PATCH），
# 所以先尝试 DELETE，404/405 就只删 tag 并告警——Release 记录会留着，但版本号（tag）已删。
GITCODE_TOKEN=$(pick_env RELEASE_GITCODE_TOKEN RELEASE_TOKEN GITCODE_TOKEN)
GITCODE_REPO=$(pick_env RELEASE_GITCODE_REPO RELEASE_REPO GITCODE_REPO)
GITCODE_API=$(pick_env RELEASE_GITCODE_API RELEASE_API GITCODE_API)
GITCODE_API="${GITCODE_API:-https://api.gitcode.com/api/v5}"
GITCODE_LOOKUP_CODE=
GITCODE_REL_STATE=

gitcode_http() {
  gitcode_method=$1
  gitcode_path=$2
  case "$gitcode_path" in
    *\?*) gitcode_url="$GITCODE_API/repos/$GITCODE_REPO$gitcode_path&access_token=$GITCODE_TOKEN" ;;
    *) gitcode_url="$GITCODE_API/repos/$GITCODE_REPO$gitcode_path?access_token=$GITCODE_TOKEN" ;;
  esac
  api_http "$gitcode_method" "$gitcode_url"
}

# 结果写 GITCODE_REL_STATE（present / absent / unknown），不要用 $(...) 取
gitcode_release_state() {
  gitcode_http GET "/releases/tags/$1"
  GITCODE_LOOKUP_CODE=$HTTP_CODE
  case "$HTTP_CODE" in
    200)
      if jq -e 'type == "null"' <"$HTTP_BODY_FILE" >/dev/null 2>&1; then
        GITCODE_REL_STATE=absent
      else
        GITCODE_REL_STATE=present
      fi
      ;;
    404) GITCODE_REL_STATE=absent ;;
    *) GITCODE_REL_STATE=unknown ;;
  esac
}

# 输出 present / absent / unknown
gitcode_tag_state() {
  gitcode_page=1
  while [ "$gitcode_page" -le 10 ]; do
    gitcode_http GET "/tags?per_page=100&page=$gitcode_page"
    if [ "$HTTP_CODE" != "200" ]; then
      echo unknown
      return
    fi
    if jq -e --arg t "$1" 'any(.[]; .name == $t)' <"$HTTP_BODY_FILE" >/dev/null 2>&1; then
      echo present
      return
    fi
    gitcode_count=$(jq 'length' <"$HTTP_BODY_FILE" 2>/dev/null) || gitcode_count=0
    [ "${gitcode_count:-0}" -lt 100 ] && break
    gitcode_page=$((gitcode_page + 1))
  done
  echo absent
}

gitcode_list_nightly() {
  gitcode_page=1
  while [ "$gitcode_page" -le 10 ]; do
    gitcode_http GET "/releases?per_page=100&page=$gitcode_page"
    if [ "$HTTP_CODE" != "200" ]; then
      warn "获取 GitCode Release 列表失败（HTTP $HTTP_CODE），跳过清理"
      return 1
    fi
    gitcode_count=$(jq 'if type == "array" then length else 0 end' <"$HTTP_BODY_FILE" 2>/dev/null) || gitcode_count=0
    jq -r '.[] | select(.prerelease == true) | .tag_name | select(test("-nightly\\.[0-9]{8}$"))' \
      <"$HTTP_BODY_FILE" 2>/dev/null || true
    [ "${gitcode_count:-0}" -lt 100 ] && return 0
    gitcode_page=$((gitcode_page + 1))
  done
  return 0
}

gitcode_delete_one() {
  gitcode_one_tag=$1
  gitcode_strict=$2
  gitcode_failed=0

  # 1) 删 Release：官方文档没有这个接口，能删就删，不能删只告警
  gitcode_release_state "$gitcode_one_tag"
  case "$GITCODE_REL_STATE" in
    present)
      gitcode_http DELETE "/releases/$gitcode_one_tag"
      case "$HTTP_CODE" in
        200 | 202 | 204) info "已删除 GitCode Release：$gitcode_one_tag" ;;
        404 | 405) warn "GitCode 没有删 Release 的接口（HTTP $HTTP_CODE），Release 记录保留" ;;
        *)
          warn "删除 GitCode Release 失败：$gitcode_one_tag（HTTP $HTTP_CODE）"
          gitcode_failed=1
          ;;
      esac
      ;;
    unknown)
      warn "查询 GitCode Release 失败：$gitcode_one_tag（HTTP $GITCODE_LOOKUP_CODE）"
      [ "$gitcode_strict" = "1" ] && gitcode_failed=1
      ;;
  esac

  # 2) 删 tag：DELETE /repos/{owner}/{repo}/tags/{tag_name}
  gitcode_http DELETE "/tags/$gitcode_one_tag"
  case "$HTTP_CODE" in
    200 | 202 | 204) info "已删除 GitCode tag：$gitcode_one_tag" ;;
    404) : ;;
    *) warn "删除 GitCode tag 失败：$gitcode_one_tag（HTTP $HTTP_CODE）" ;;
  esac

  # 3) 删除后复查
  gitcode_release_state "$gitcode_one_tag"
  case "$GITCODE_REL_STATE" in
    present) warn "删除后仍能查到 GitCode Release：$gitcode_one_tag（平台未提供删 Release 接口）" ;;
    unknown) warn "无法确认 GitCode Release 是否已删除：$gitcode_one_tag（HTTP $GITCODE_LOOKUP_CODE）" ;;
  esac
  case "$(gitcode_tag_state "$gitcode_one_tag")" in
    present) warn "删除后仍能查到 GitCode tag：$gitcode_one_tag" ;;
    absent)
      [ "$GITCODE_REL_STATE" = "absent" ] && info "✓ 已确认 $gitcode_one_tag 的 Release 与 tag 都不存在"
      ;;
    *) warn "无法确认 GitCode tag 是否已删除：$gitcode_one_tag" ;;
  esac

  [ "$gitcode_failed" -ne 0 ] && return 1
  return 0
}

# ── 主流程 ────────────────────────────────────────────────────────────────
case "$PLATFORM" in
  github)
    if [ -z "$GH_REPO" ]; then
      echo "错误：缺少仓库（RELEASE_GITHUB_REPO / RELEASE_REPO / GITHUB_REPOSITORY / REPO）" >&2
      exit 2
    fi
    # gh CLI 从环境变量取默认仓库与令牌，pick_env 解析出的值必须导出才生效
    export GH_REPO
    [ -n "$GH_TOKEN" ] && export GH_TOKEN
    ;;
  cnb)
    if [ -z "${CNB_TOKEN:-}" ] || [ -z "${CNB_API_ENDPOINT:-}" ] || [ -z "${CNB_REPO_SLUG:-}" ]; then
      echo "错误：缺少 CNB 令牌/接口/仓库（RELEASE_CNB_TOKEN / RELEASE_TOKEN、RELEASE_CNB_API / RELEASE_API、RELEASE_CNB_REPO / RELEASE_REPO，兼容 CNB_*）" >&2
      exit 2
    fi
    CNB_API="${CNB_API_ENDPOINT%/}"
    CNB_REQ="$CNB_REPO_SLUG"
    CNB_BODY=$(mktemp)
    trap 'rm -f "$CNB_BODY"' EXIT INT TERM
    ;;
  gitlab)
    if [ -z "${GITLAB_TOKEN:-}" ] || [ -z "${GITLAB_REPO:-}" ]; then
      echo "错误：缺少 GitLab 令牌/仓库（RELEASE_GITLAB_TOKEN / RELEASE_TOKEN / GITLAB_TOKEN、RELEASE_GITLAB_REPO / RELEASE_REPO / GITLAB_REPO）" >&2
      exit 2
    fi
    GITLAB_API="${GITLAB_API%/}"
    GITLAB_PROJECT=$(enc_slash "$GITLAB_REPO")
    HTTP_BODY_FILE=$(mktemp)
    trap 'rm -f "$HTTP_BODY_FILE"' EXIT INT TERM
    ;;
  gitea)
    if [ -z "${GITEA_TOKEN:-}" ] || [ -z "${GITEA_REPO:-}" ]; then
      echo "错误：缺少 Gitea 令牌/仓库（RELEASE_GITEA_TOKEN / RELEASE_TOKEN / GITEA_TOKEN、RELEASE_GITEA_REPO / RELEASE_REPO / GITEA_REPO）" >&2
      exit 2
    fi
    GITEA_API="${GITEA_API%/}"
    HTTP_BODY_FILE=$(mktemp)
    trap 'rm -f "$HTTP_BODY_FILE"' EXIT INT TERM
    ;;
  gitee)
    if [ -z "$GITEE_TOKEN" ] || [ -z "$GITEE_REPO" ]; then
      echo "错误：缺少 Gitee 令牌/仓库（RELEASE_GITEE_TOKEN / RELEASE_TOKEN / GITEE_TOKEN、RELEASE_GITEE_REPO / RELEASE_REPO / GITEE_REPO）" >&2
      exit 2
    fi
    GITEE_API="${GITEE_API%/}"
    HTTP_BODY_FILE=$(mktemp)
    trap 'rm -f "$HTTP_BODY_FILE"' EXIT INT TERM
    ;;
  gitcode)
    if [ -z "$GITCODE_TOKEN" ] || [ -z "$GITCODE_REPO" ]; then
      echo "错误：缺少 GitCode 令牌/仓库（RELEASE_GITCODE_TOKEN / RELEASE_TOKEN / GITCODE_TOKEN、RELEASE_GITCODE_REPO / RELEASE_REPO / GITCODE_REPO）" >&2
      exit 2
    fi
    GITCODE_API="${GITCODE_API%/}"
    HTTP_BODY_FILE=$(mktemp)
    trap 'rm -f "$HTTP_BODY_FILE"' EXIT INT TERM
    ;;
esac

delete_one() {
  case "$PLATFORM" in
    github) gh_delete_one "$@" ;;
    cnb) cnb_delete_one "$@" ;;
    gitlab) gitlab_delete_one "$@" ;;
    gitea) gitea_delete_one "$@" ;;
    gitee) gitee_delete_one "$@" ;;
    gitcode) gitcode_delete_one "$@" ;;
  esac
}

list_nightly() {
  case "$PLATFORM" in
    github) gh_list_nightly ;;
    cnb) cnb_list_nightly ;;
    gitlab) gitlab_list_nightly ;;
    gitea) gitea_list_nightly ;;
    gitee) gitee_list_nightly ;;
    gitcode) gitcode_list_nightly ;;
  esac
}

if [ -n "$ONE_TAG" ]; then
  delete_one "$ONE_TAG" 1 "$KEEP_TAG" || exit 1
  exit 0
fi

NIGHTLY=$(list_nightly) || exit 0
NIGHTLY=$(printf '%s\n' "$NIGHTLY" | sed '/^$/d' | sort_by_date_desc)
TOTAL=$(printf '%s\n' "$NIGHTLY" | grep -c . || true)

if [ "${TOTAL:-0}" -le "$KEEP" ]; then
  info "nightly Release 共 ${TOTAL:-0} 个，无需清理（保留 $KEEP 个）"
  exit 0
fi

info "nightly Release 共 $TOTAL 个，保留最新 $KEEP 个，清理其余 $((TOTAL - KEEP)) 个"
LIST=$(mktemp)
printf '%s\n' "$NIGHTLY" | tail -n +$((KEEP + 1)) >"$LIST"
FAILED=0
while IFS= read -r t; do
  [ -n "$t" ] || continue
  delete_one "$t" 0 || FAILED=$((FAILED + 1))
done <"$LIST"
rm -f "$LIST"

if [ "$FAILED" -eq 0 ]; then
  info "清理完成"
else
  warn "清理完成，但有 $FAILED 个删除失败（不影响发布）"
fi
exit 0
