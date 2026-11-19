#!/bin/sh
# 用 GoReleaser 产出的 fpk 变体二进制打包 fnOS fpk。
# 由 make goreleaser-snapshot / goreleaser-release 在 goreleaser 结束后调用。
# 需要 fnpack；没有时可用 GORELEASER_SKIP_FPK=1 显式跳过。
set -eu

DIST_DIR="${DIST_DIR:-dist}"
ROOT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$ROOT_DIR"

if [ "${GORELEASER_SKIP_FPK:-}" = "1" ]; then
  echo "[fpk] GORELEASER_SKIP_FPK=1，跳过 fpk 打包"
  exit 0
fi

if ! command -v fnpack >/dev/null 2>&1; then
  echo "❌ 未找到 fnpack，无法生成 fpk；安装 fnpack 或设置 GORELEASER_SKIP_FPK=1" >&2
  exit 1
fi

# 版本与 Makefile 同口径：去掉 v 前缀（make 调用时会直接传 PKG_VERSION）
tag="${GORELEASER_CURRENT_TAG:-}"
version="${PKG_VERSION:-}"
if [ -z "$version" ]; then
  [ -n "$tag" ] || tag=$(git describe --tags --always --dirty 2>/dev/null || echo dev)
  version=$(printf '%s' "$tag" | sed 's|^refs/tags/||; s|^v||')
fi
# resolve-prev-tag.sh 要的是 tag 形态（vX.Y.Z[-渠道.N]）
tag=$(printf '%s' "${tag:-v${version}}" | sed 's|^refs/tags/||')

# 更新内容：同渠道的上一个版本 → HEAD 的提交摘要。
# 基准必须走 resolve-prev-tag.sh，与 Release 正文 / 发版公告保持同一口径：
# git describe 不区分渠道，发正式版会把刚发的 rc 当基准，也可能被无关 tag 带偏。
changelog="${CHANGELOG:-}"
if [ -z "$changelog" ]; then
  prev_tag=""
  if printf '%s' "$tag" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(-(rc|beta|alpha|nightly)(\.[0-9]+)?)?$'; then
    prev_tag=$(sh scripts/ci/resolve-prev-tag.sh "$tag") || {
      echo "❌ 解析上一个版本 tag 失败（$tag）" >&2
      exit 1
    }
  else
    # 快照 / dev 等非发布版本没有渠道口径，退回最近可达 tag
    prev_tag=$(git describe --tags --abbrev=0 2>/dev/null || true)
  fi
  if [ -n "$prev_tag" ]; then
    changelog=$(git log --oneline --no-decorate "$prev_tag"..HEAD 2>/dev/null || true)
  else
    changelog=$(git log --oneline --no-decorate -20 2>/dev/null || true)
  fi
  changelog=$(printf '%s' "$changelog" | head -20 | sed 's/^[a-f0-9]* //' | tr '\n' ';' | sed 's/;$/./')
fi
# sed 替换串里的 \ & | 需要转义（提交标题里可能出现）
version_escaped=$(printf '%s' "$version" | sed -e 's/\\/\\\\/g' -e 's/&/\\&/g' -e 's/|/\\|/g')
changelog_escaped=$(printf '%s' "$changelog" | sed -e 's/\\/\\\\/g' -e 's/&/\\&/g' -e 's/|/\\|/g')

manifest_backup=$(mktemp "${TMPDIR:-/tmp}/fnmusic-sync-manifest.XXXXXX")
work_dir=""
cleanup() {
  if [ -n "$work_dir" ]; then
    rm -rf "$work_dir"
  fi
  rm -f fpkg/app/fnmusic-sync
  cp "$manifest_backup" fpkg/manifest
  rm -f "$manifest_backup"
}
trap cleanup EXIT INT TERM

cp fpkg/manifest "$manifest_backup"
sed -e "s|__VERSION__|$version_escaped|g" -e "s|__CHANGELOG__|$changelog_escaped|g" \
  "$manifest_backup" >fpkg/manifest.tmp
mv fpkg/manifest.tmp fpkg/manifest

for arch in amd64 arm64; do
  archive="$DIST_DIR/fnmusic-sync-fpk-linux-$arch.tar.gz"
  if [ ! -f "$archive" ]; then
    echo "❌ 缺少 $archive（goreleaser 构建未产出？）" >&2
    exit 1
  fi

  work_dir=$(mktemp -d "${TMPDIR:-/tmp}/fnmusic-sync-fpk-$arch.XXXXXX")
  tar -xzf "$archive" -C "$work_dir"

  mkdir -p fpkg/app
  cp "$work_dir/fnmusic-sync-fpk" fpkg/app/fnmusic-sync
  chmod 0755 fpkg/app/fnmusic-sync

  (cd fpkg && fnpack build)
  mv fpkg/fnmusic-sync.fpk "$DIST_DIR/fnmusic-sync-$arch.fpk"
  echo "✓ 已生成 $DIST_DIR/fnmusic-sync-$arch.fpk"

  rm -rf "$work_dir"
  work_dir=""
done
