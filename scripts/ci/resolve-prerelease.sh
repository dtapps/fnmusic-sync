#!/usr/bin/env bash
# 发布判定（归一化 + 格式校验 + 预发布）—— 全仓库唯一的一份。
# 五个平台（CNB / GitHub / GitLab / Gitee / GitCode）与构建脚本都 source 它。
# 被 source 使用，不要直接执行。
#
# 输入只有一个（调用方 export 好）：
#   VERSION   版本号，带不带 v 都行，前后空白会被裁掉
#
# 输出（export 回调用方的 shell）：
#   TAG           归一化后的 tag，始终带 v
#   PKG_VERSION   去掉 v 的版本号，与 .nfpm.yaml 的版本段一致
#   PRERELEASE    最终 true / false —— 由版本号形态决定，没有别的输入
#   PRE_NOTE      这次是怎么判的，一句话，调用方直接 echo 进日志
#
# 版本号形态（就是全部规则，没有开关、没有表单字段）：
#   v1.2.3            → 正式版      PRERELEASE=false
#   v1.2.3-rc.1       → 预发布      PRERELEASE=true
#   v1.2.3-beta.1     → 预发布      PRERELEASE=true
#   v1.2.3-alpha.1    → 预发布      PRERELEASE=true
# 即 semver 的 `-<prerelease>` 段：有就是预发布，没有就是正式版。别的后缀（-dev、-pre）
# 同理 —— 判法与客户端 internal/updater.isPrereleaseTag 完全一致，两边必须同源：
# 不一致的后果是「发出去的版本没人能升上去」，或者「稳定用户被推到 rc 上」，两种都撤不回来。
#
# 为什么把 pre_release 参数彻底删掉：
#   - 它跟版本号会打架，而且要打架就必然在「用户什么都没做错」的时候打 —— CNB 的 switch
#     写死 default: 'false' 却传来 true，流水线在第一步红掉，页面上没有任何东西能改。
#   - 客户端本来就只看 tag：GitLab 的 Release 连 prerelease 字段都没有，tag 是五个平台
#     唯一共通的契约。版本号里已经写了 -rc.1，再让人勾一遍开关只是多一个出错的地方。
#
# 版本号不合法（空、v、1.2、abc、v1.2.3+build）直接 exit 2：脏 tag 比失败的流水线贵得多。
#
# 用法：
#   export VERSION=v1.2.3-rc.1
#   . scripts/ci/resolve-prerelease.sh
#   echo "$TAG $PKG_VERSION $PRERELEASE"   # v1.2.3-rc.1 1.2.3-rc.1 true

# ---------------------------------------------------------------- 版本号归一化
# 手填的东西什么都可能带：前后空白（复制粘贴最常见）、v、大写 V。先削干净再判，
# 否则 " v1.2.3" 会被当成「没带 v」，拼出 "v v1.2.3" 这种带空格的 tag。
_release_raw="${VERSION:-}"
_release_raw="${_release_raw#"${_release_raw%%[![:space:]]*}"}" # 去掉前导空白
_release_raw="${_release_raw%"${_release_raw##*[![:space:]]}"}" # 去掉尾随空白
_release_raw="${_release_raw#v}"
_release_raw="${_release_raw#V}"

if [ -z "$_release_raw" ]; then
  printf '错误：版本号为空（VERSION 没传，或传了个空的）\n' >&2
  exit 2
fi

# 只认 semver 三段式 + 可选后缀：1.2.3 / 1.2.3-rc.1 / 1.2.3-alpha.1
# 不认 +build 元数据：deb 的 DEB_VERSION 与 fpk manifest 都没为它做过处理，收进来就是脏 tag。
if ! printf '%s' "$_release_raw" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z]+)*$'; then
  printf '错误：版本号不合法：%s（期望 1.2.3 / v1.2.3 / v1.2.3-rc.1 / v1.2.3-beta.1 / v1.2.3-alpha.1）\n' \
    "$_release_raw" >&2
  exit 2
fi

TAG="v${_release_raw}"
PKG_VERSION="${_release_raw}"

# ---------------------------------------------------------------- 预发布：只看后缀
# 只看 semver 的 -<prerelease> 段：1.2.3-rc.1 有，1.2.3 没有。
case "$PKG_VERSION" in
  *-*)
    PRERELEASE=true
    PRE_NOTE="${TAG} 带预发布后缀，按预发布发"
    ;;
  *)
    PRERELEASE=false
    PRE_NOTE="${TAG} 无预发布后缀，按正式版发"
    ;;
esac

# 上游可能还留着旧的 PRE_RELEASE/PRERELEASE 环境变量（老表单、老的 CI 变量）。
# 这里以本脚本算出来的为准：先清掉再赋值，不让遗留值从任何一条缝里漏进 Release。
# 写成 if 而不是 `[ ... ] && echo`：后者在变量为空时返回 1，调用方带着 set -e 会直接退出。
if [ -n "${PRE_RELEASE:-}" ]; then
  printf '提示：忽略环境里的 PRE_RELEASE=%s，预发布只看版本号\n' "$PRE_RELEASE" >&2
fi
unset PRE_RELEASE

export TAG PKG_VERSION PRERELEASE PRE_NOTE
unset _release_raw
