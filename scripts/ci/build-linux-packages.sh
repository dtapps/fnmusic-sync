#!/usr/bin/env bash
# 在「没有预构建 CI 镜像」的平台上从零打出 deb / fpk
#
# 背景：GitHub / CNB / GitLab 三个平台都是先有一份 CI 镜像（Go 1.27.3 + Node + pnpm
# + nfpm 2.48.0 + fnpack 1.2.3 + upx 5.2.1 全烤在里面），构建 job 直接跑在镜像里。
# GitCode 和 Gitee 没有「用自己的镜像跑 job」这个能力，只能用平台给的固定环境插件，
# 而每个插件只自带一种语言运行时（Gitee 的 build@golang 给 Go 1.27 + make，
# build@nodejs 给 Node），另一个运行时只能现场补。
# 本脚本就是那份「补齐 + 打包」逻辑的唯一副本 —— 两个平台都调它，别各写一份。
#
# 能省的就省：镜像里已有 Go >= 1.27 就直接用（省 90MB 下载），
# 已有 Node 满足 20.19+ 也直接用。装不上的只有 nfpm / fnpack / upx 三个小二进制。
#
# 产物与另外三个平台逐字一致：
#   bin/fnmusic-sync_amd64.deb  bin/fnmusic-sync_arm64.deb
#   bin/fnmusic-sync-amd64.fpk  bin/fnmusic-sync-arm64.fpk
# 外加 Release 正文 release_body.md（格式与 GitHub / CNB 的生成脚本一致）。
#
# 用法：
#   VERSION=v0.1.31 \
#   DL_BASE=https://gitcode.com/dtapp/fnmusic-sync/releases/download/v0.1.31 \
#   REPO_SOURCE=gitcode \
#   bash scripts/ci/build-linux-packages.sh
#
# 环境变量（都可不传，有默认值）：
#   VERSION        版本号，带不带 v 都行；为空时回落到 git describe
#   DL_BASE        正文下载表里的链接前缀，指向本平台本 tag 的 releases/download（必填）
#   REPO_SOURCE    写进二进制的构建来源标识（buildinfo.RepoSource），如 gitcode / gitee
#   TOOLCHAIN_DIR  工具链落地目录，默认 /tmp/fnmusic-build-tools
#   REPO_URL       仓库地址（正文里「完整记录」链接用），选填
#   COMMIT_URL     提交链接前缀（拼成 ${COMMIT_URL}/<sha>），选填
#   COMMIT_SHA     提交 SHA；有则正文「发布信息」里多一行提交链接，选填
#   STABLE_URL     正式版列表页 URL，预发布时正文提示用户去哪找稳定版，选填
#
# 没有 PRERELEASE 这个入参了：正文里的「发布类型」由 VERSION 的后缀决定
# （v0.1.32-rc.1 / v0.1.32-nightly.20260101 是预发布，v0.1.31 不是），
# 判定在 scripts/ci/resolve-prerelease.sh。
set -euo pipefail

log() { printf '\n\033[1m>>> %s\033[0m\n' "$*"; }
die() {
  printf '\n\033[1;31m错误：%s\033[0m\n' "$*" >&2
  exit 1
}
have() { command -v "$1" >/dev/null 2>&1; }

# 版本基准是统一 CI 镜像 dtapps/golang/fnpack（本仓库已无 Dockerfile，见 docs/ci-image.md）。
# 改版本时与镜像项目的 ARG 一起改，否则各平台打出来的包装的不是同一个 Go。
GO_VERSION="${GO_VERSION:-1.27.3}"
NODE_MIN_MAJOR="${NODE_MIN_MAJOR:-20}" # vite 8 要求 ^20.19.0 || >=22.12.0
NODE_MIN_MINOR="${NODE_MIN_MINOR:-19}"
NODE_VERSION="${NODE_VERSION:-22.12.0}" # 仅当系统 node 不满足最低版本时才下
PNPM_VERSION="${PNPM_VERSION:-12.16.0}"
NFPM_VERSION="${NFPM_VERSION:-2.48.0}"
FNPACK_VERSION="${FNPACK_VERSION:-1.2.3}"
UPX_VERSION="${UPX_VERSION:-5.2.1}" # 与统一 CI 镜像的 ARG UPX_VERSION 一致

# ---------- 国内网络：一切外国源都要换镜像 ----------
# 取值照抄统一 CI 镜像 dtapps/golang/fnpack（CNB 是五个平台里唯一纯国内链路跑通的）。Gitee / GitCode
# 的网络条件与 CNB 同类；GitHub / GitLab 那两个跑在境外机房里，直连 github.com、
# proxy.golang.org 都没问题，照抄反而不对 —— 但本脚本只服务前两个平台。
#
# 1) GitHub Releases 反代：与统一 CI 镜像里同一个值。
#    nfpm / upx 只在 GitHub 放二进制，走它。
export GITHUB_MIRROR="${GITHUB_MIRROR:-https://files.m.daocloud.io}"
# 2) Go 模块与工具链：腾讯云源（CNB 用的就是它）排第一，goproxy.cn / 阿里云兜后手。
#    分隔符用 `|` 不是 `,`：`,` 只在 404/410 时才换下一个源，遇到断流、5xx 直接放弃。
#    实测腾讯云对大 zip 会断流（circl 48.6MB，吐出 1.7MB 后
#    `stream error: stream ID 211; INTERNAL_ERROR`），而 goproxy.cn 同文件 4.6s 拿全。
#    留着腾讯云打头是照 CNB 的配置；`|` + 后手是让断流只降级、不炸流水线。
case "${GOPROXY:-}" in
  "" | *proxy.golang.org* | *golang.org*)
    export GOPROXY="https://mirrors.cloud.tencent.com/go/|https://goproxy.cn|https://mirrors.aliyun.com/goproxy|direct"
    ;;
esac
# 3) 校验库：sum.golang.org 在国内不通，用它的官方国内镜像
case "${GOSUMDB:-}" in
  "" | *sum.golang.org*) export GOSUMDB="sum.golang.google.cn" ;;
esac
# 4) 禁止 go 自己补工具链。go.mod 锁的是 1.27.3，下面 ensure_go 保证机器上就是 1.27.3+，
#    没有需要「补」的补丁差 —— auto 是那次 30 秒超时的直接来源。
export GOTOOLCHAIN=local
# 5) npm / pnpm：腾讯云源，与 CNB 的 npm config set registry 一致
export npm_config_registry="${npm_config_registry:-https://mirrors.cloud.tencent.com/npm/}"

TOOLCHAIN="${TOOLCHAIN_DIR:-/tmp/fnmusic-build-tools}"
mkdir -p "$TOOLCHAIN/bin"

# ============================== 下载 ==============================
# 三种下载器依次兜底：插件镜像都是精简容器，curl / wget / node 哪个在都不能假设
# （Gitee 的 plugin-24 实测连 python3 都没有）。
fetch() {
  local url="$1" out="$2"
  if have curl; then
    # --speed-limit/--speed-time 是给「反代连上了但不吐数据」准备的：断流 60 秒内
    # 低于 2KB/s 就自己断，交给 fetch_any 换官方源，而不是挂在那里等平台的超时。
    # 腾讯云那个 Go 反代就是这么断的（48MB 吐到 1.7MB 停住）。
    curl -fsSL --retry 3 --retry-delay 2 --connect-timeout 20 \
      --speed-limit 2048 --speed-time 60 "$url" -o "$out"
  elif have wget; then
    # wget 的 --timeout 就是读超时：卡住 30 秒没有新数据即失败
    wget -q --tries=3 --timeout=30 -O "$out" "$url"
  elif have node; then
    URL="$url" OUT="$out" node -e '
      const https = require("https"), http = require("http"), fs = require("fs");
      const go = (u, n) => {
        if (n > 6) { throw new Error("重定向过多"); }
        const mod = u.startsWith("https:") ? https : http;
        mod.get(u, { headers: { "User-Agent": "fnmusic-sync-ci" } }, (r) => {
          if ([301, 302, 303, 307, 308].includes(r.statusCode)) {
            return go(new URL(r.headers.location, u).href, n + 1);
          }
          if (r.statusCode !== 200) { throw new Error("HTTP " + r.statusCode + " " + u); }
          r.pipe(fs.createWriteStream(process.env.OUT));
        }).on("error", (e) => { throw e; });
      };
      go(process.env.URL, 0);
    '
  else
    return 1
  fi
}

# fetch_any OUT URL... —— 依次尝试，第一个成功即返回。
# 每个源都重试（curl --retry 3 / wget --tries=3），换源时把话打明白：
# 第一个源是反代（GITHUB_MIRROR），它不通就换官方地址重新下一遍。
fetch_any() {
  local out="$1"
  shift
  local url n=0 total=$#
  for url in "$@"; do
    n=$((n + 1))
    printf '    尝试 [%d/%d] %s\n' "$n" "$total" "$url"
    if fetch "$url" "$out"; then
      printf '    ✓ %s\n' "$(basename "$out")"
      return 0
    fi
    printf '    ✗ 失败：%s\n' "$url"
    if [ "$n" -lt "$total" ]; then
      printf '    → 换下一个源重试（反代不通就走官方）\n'
    fi
  done
  return 1
}

apt_install() {
  have apt-get || return 1
  if [ "$(id -u)" = "0" ]; then
    apt-get update -qq >/dev/null 2>&1 || return 1
    apt-get install -y --no-install-recommends "$@" >/dev/null 2>&1
  elif have sudo; then
    sudo apt-get update -qq >/dev/null 2>&1 || return 1
    sudo apt-get install -y --no-install-recommends "$@" >/dev/null 2>&1
  else
    return 1
  fi
}

# ver_ge A B —— A >= B 返回 0
ver_ge() { [ "$(printf '%s\n%s\n' "$2" "$1" | sort -V | head -n1)" = "$2" ]; }

# ============================== 版本号 ==============================
RAW="${VERSION:-}"
if [ -z "$RAW" ]; then
  RAW="$(git describe --tags --always 2>/dev/null || echo dev)"
  log "VERSION 未传，回落到 git describe：$RAW"
fi
# 版本号归一化与预发布判定只此一份（scripts/ci/resolve-prerelease.sh）：只看后缀。
# source 而不是自己写 case —— 五个平台的 yml 也 source 同一个文件；各写一遍的话，
# 改一处的语义会在另一处悄悄失守（Gitee 曾经完全不看后缀，把 v0.1.32-rc.1 当正式版发了）。
# source 只带进函数，必须显式调用 resolve_version 才会算出 TAG / PRERELEASE；
# 少了这一句，下面的 ${PRERELEASE} 在 set -u 下就是 unbound variable。
export VERSION="$RAW"
# shellcheck source=scripts/ci/resolve-prerelease.sh
. "$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)/resolve-prerelease.sh"
resolve_version "$RAW" || die "版本号解析失败：${RAW:-<空>}"
PRE="$PRERELEASE"
# PKG_VERSION（去 v 的包版本）也由 resolve_version 算出并导出，
# 与 Makefile 的 PKG_VERSION = $(patsubst v%,%,$(VERSION)) 同一条规则。

printf '%s' "$PKG_VERSION" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+' \
  || die "版本号格式不对：${RAW}（期望 0.1.31 / v0.1.31 / v0.1.32-rc.1 / v0.1.32-nightly.20260101）"

REPO_SOURCE="${REPO_SOURCE:-cnb}"
export VERSION="$TAG"

log "构建参数：TAG=$TAG  PKG_VERSION=$PKG_VERSION  PRERELEASE=$PRE  REPO_SOURCE=$REPO_SOURCE"

# ============================== 工具链 ==============================
case "$(uname -m)" in
  x86_64 | amd64)
    HOST_ARCH=amd64
    NFPM_ARCH=x86_64
    ;;
  aarch64 | arm64)
    HOST_ARCH=arm64
    NFPM_ARCH=arm64
    ;;
  *) die "不支持的构建机架构：$(uname -m)" ;;
esac

# ---------- 下载器 ----------
# fetch() 靠 curl / wget / node 三者之一。三个都没有就什么都装不了，
# 先试一次 apt 把 curl 补上（构建镜像基本都是 Debian 系；CentOS 系自带 curl）。
ensure_downloader() {
  if have curl || have wget || have node; then
    return 0
  fi
  printf '    curl / wget / node 都没有，尝试 apt 装 curl\n'
  apt_install curl || die "没有任何下载器，也装不上 curl，无法继续"
}

# ---------- Go ----------
ensure_go() {
  # 先看缓存：Gitee 的 yml 把 TOOLCHAIN_DIR 指到被 caches 挂载的绝对路径
  # （GitCode 的 runner 没有缓存机制，走默认 /tmp，每次重下）。
  local cur
  if [ -x "$TOOLCHAIN/go/bin/go" ]; then
    cur="$(GOTOOLCHAIN=local "$TOOLCHAIN/go/bin/go" env GOVERSION 2>/dev/null || true)"
    cur="${cur#go}"
    if [ -n "$cur" ] && ver_ge "$cur" "$GO_VERSION"; then
      printf '    ✓ 命中缓存的 Go %s（%s）\n' "$cur" "$TOOLCHAIN/go"
      return 0
    fi
    printf '    缓存的 Go %s 不满足 %s，重新下载\n' "${cur:-已损坏}" "$GO_VERSION"
  fi
  if have go; then
    # GOTOOLCHAIN=local 双重保险（顶层已 export），|| true 是必须的：
    # set -e 下 `cur="$(go env GOVERSION)"` 一失败就把整个脚本带走，
    # 日志里只剩「准备工具链」一行 —— 上一版就是这么无声死掉的（exit 255）。
    # 版本门槛对齐 go.mod 的 1.27.3，不是 1.27：go 1.27.0 用不了 go.mod 锁的补丁版本，
    # 留着它只会让 go 反复回来要工具链。
    cur="$(GOTOOLCHAIN=local go env GOVERSION 2>/dev/null || true)"
    cur="${cur#go}"
    if [ -n "$cur" ] && ver_ge "$cur" "$GO_VERSION"; then
      printf '    ✓ 用镜像自带 Go %s（>= go.mod 要求的 %s，跳过下载）\n' "$cur" "$GO_VERSION"
      return 0
    fi
    printf '    系统 Go %s 低于 go.mod 要求的 %s，下载官方包\n' "${cur:-未知}" "$GO_VERSION"
  fi
  local tarball="$TOOLCHAIN/go.tar.gz"
  # 三个源都按国内可达性排序：阿里云镜像最快（实测 200），golang.google.cn 是 Google 的
  # 国内镜像（境外也通），go.dev 只在前面都不通时才轮到 —— 它在国内基本是超时。
  # 腾讯云镜像站不托管 Go 官方包（/golang/ 是 404），所以不在链上。
  fetch_any "$tarball" \
    "https://mirrors.aliyun.com/golang/go${GO_VERSION}.linux-${HOST_ARCH}.tar.gz" \
    "https://golang.google.cn/dl/go${GO_VERSION}.linux-${HOST_ARCH}.tar.gz" \
    "https://go.dev/dl/go${GO_VERSION}.linux-${HOST_ARCH}.tar.gz" \
    || die "Go ${GO_VERSION} 下载失败（没有 curl / wget / node 可用？）"
  rm -rf "$TOOLCHAIN/go"
  mkdir -p "$TOOLCHAIN/go"
  tar -xzf "$tarball" -C "$TOOLCHAIN/go" --strip-components=1
  rm -f "$tarball"
  printf '    ✓ 已安装 Go %s 到 %s\n' "$GO_VERSION" "$TOOLCHAIN/go"
}

# ---------- Node ----------
ensure_node() {
  if have node; then
    local cur major minor
    cur="$(node -p 'process.versions.node')"
    major="${cur%%.*}"
    minor="$(printf '%s' "$cur" | cut -d. -f2)"
    # 只比大版本会放过 20.0.0，而 vite 8 卡的是 20.19 → 大版本相等时还要比小版本
    if [ "$major" -gt "$NODE_MIN_MAJOR" ] \
      || { [ "$major" -eq "$NODE_MIN_MAJOR" ] && [ "${minor:-0}" -ge "$NODE_MIN_MINOR" ]; }; then
      printf '    ✓ 系统 Node %s\n' "$cur"
      return 0
    fi
    printf '    系统 Node %s 不满足 %s.%s+，下载 %s\n' \
      "$cur" "$NODE_MIN_MAJOR" "$NODE_MIN_MINOR" "$NODE_VERSION"
  fi
  local tgz="$TOOLCHAIN/node.tar.gz"
  # 架构不能写死 x64：插件换成 arm64 时会拿到一个跑不起来的 node
  local node_arch=x64
  [ "$HOST_ARCH" = "arm64" ] && node_arch=arm64
  fetch_any "$tgz" \
    "https://npmmirror.com/mirrors/node/v${NODE_VERSION}/node-v${NODE_VERSION}-linux-${node_arch}.tar.gz" \
    "https://nodejs.org/dist/v${NODE_VERSION}/node-v${NODE_VERSION}-linux-${node_arch}.tar.gz" \
    || die "Node ${NODE_VERSION} 下载失败"
  rm -rf "$TOOLCHAIN/node"
  mkdir -p "$TOOLCHAIN/node"
  tar -xzf "$tgz" -C "$TOOLCHAIN/node" --strip-components=1
  rm -f "$tgz"
  printf '    ✓ 已安装 Node 到 %s\n' "$TOOLCHAIN/node"
}

# ---------- pnpm ----------
# 不信任系统里的 pnpm：版本随镜像漂移，lockfile 与 vite 对 pnpm 版本敏感。
# 固定装一份到 TOOLCHAIN，PATH 里排第一。
#
# pnpm ≥11 的 npm 包只是个壳：postinstall 把 @pnpm/exe.<target> 里那个 59MB 的原生
# 二进制（Node SEA）覆盖到 bin 上，没有纯 JS 实现可退。Gitee 的 plugin-24 容器里它
# 一启动就 Segmentation fault（同一份二进制在 CNB 的 Debian 13 镜像里跑得好好的），
# 所以这里只认「真跑得起来」：原生版起不来就退到 pnpm 10.x —— 它的 bin/pnpm.cjs
# 是纯 JS，只要有 node 就能跑，lockfile 一样是 9.0。
PNPM_FALLBACK_VERSION="${PNPM_FALLBACK_VERSION:-10.34.9}"

# pnpm_works PATH —— 真跑一次 -v。段错误返回 139、缺库返回 127、占位文件不是可执行
# 文件，三种都落进非零，比看版本号可靠
pnpm_works() {
  [ -x "$1" ] || return 1
  local v
  v="$("$1" -v 2>/dev/null)" || return 1
  [ -n "$v" ]
}

# install_pnpm VER —— npm 一次只认一个 registry，所以失败换一个再来
install_pnpm() {
  local ver="$1" reg
  for reg in "$npm_config_registry" "https://registry.npmmirror.com" "https://registry.npmjs.org"; do
    printf '    安装 pnpm %s（registry=%s）\n' "$ver" "$reg"
    # 写成 if 而不是 `cmd && break`：后者在 npm 失败时会让整条 AND 列表返回非零，
    # set -e 直接把脚本带走，第二个源根本没机会试
    if npm install -g --prefix "$TOOLCHAIN" "pnpm@${ver}" \
      --registry "$reg" --no-audit --no-fund --loglevel=error; then
      return 0
    fi
    printf '    ✗ %s 装不上，换源重试\n' "$reg"
  done
  return 1
}

ensure_pnpm() {
  local bin="$TOOLCHAIN/bin/pnpm"
  if pnpm_works "$bin"; then
    printf '    ✓ 命中缓存的 pnpm %s\n' "$("$bin" -v)"
    return 0
  fi
  have npm || die "没有 npm，无法安装 pnpm"

  install_pnpm "$PNPM_VERSION" || die "pnpm ${PNPM_VERSION} 三个源都装不上"
  # npm 的 prefix 布局在不同版本间有 bin/ 与 lib/node_modules 两种，统一做个软链
  if [ ! -x "$bin" ] && [ -x "$TOOLCHAIN/node/bin/pnpm" ]; then
    ln -sf "$TOOLCHAIN/node/bin/pnpm" "$bin"
  fi

  if ! pnpm_works "$bin"; then
    printf '    原生 pnpm %s 起不来（容器里段错误），退回纯 JS 的 %s\n' \
      "$PNPM_VERSION" "$PNPM_FALLBACK_VERSION"
    install_pnpm "$PNPM_FALLBACK_VERSION" || die "pnpm ${PNPM_FALLBACK_VERSION} 也装不上"
    pnpm_works "$bin" || die "pnpm 装了但跑不起来（原生 ${PNPM_VERSION} 与纯 JS ${PNPM_FALLBACK_VERSION} 都失败）"
  fi

  # 与统一 CI 镜像里的 `pnpm config set registry` 对齐：pnpm 也读 npm_config_registry，
  # 但显式写一份进它自己的配置，免得某个版本只认本地配置、转头去 registry.npmjs.org
  "$bin" config set registry "$npm_config_registry" >/dev/null 2>&1 || true
  # cmd/frontend/package.json 里写的是 packageManager: pnpm@12.15.0。10.x 若开着
  # manage-package-manager-versions（默认开），会自己去拉 12 的原生二进制 —— 又撞回段错误
  "$bin" config set manage-package-manager-versions false >/dev/null 2>&1 || true
  printf '    ✓ pnpm %s\n' "$("$bin" -v)"
}

# ---------- nfpm（打 deb）----------
# 与统一 CI 镜像里用的同一条 URL（API 反代 + 官方 release 路径），拿到的是编译好的
# 二进制，不用现场编。GOPROXY 那条只在反代彻底不可用时兜底 —— go install 编 nfpm 要
# 一两分钟，还要拉 48MB 的 circl，不该是常态路径。
ensure_nfpm() {
  have nfpm && {
    printf '    ✓ 系统 nfpm %s\n' "$(nfpm --version 2>/dev/null || echo '')"
    return 0
  }
  # 缓存命中要认版本：缓存跨次保留，NFPM_VERSION 升级后不能继续用旧的
  if [ -x "$TOOLCHAIN/bin/nfpm" ] \
    && "$TOOLCHAIN/bin/nfpm" --version 2>/dev/null | grep -q "$NFPM_VERSION"; then
    printf '    ✓ 命中缓存的 nfpm %s\n' "$("$TOOLCHAIN/bin/nfpm" --version 2>/dev/null)"
    return 0
  fi
  local tgz="$TOOLCHAIN/nfpm.tar.gz"
  local rel="github.com/goreleaser/nfpm/releases/download/v${NFPM_VERSION}/nfpm_${NFPM_VERSION}_Linux_${NFPM_ARCH}.tar.gz"
  if fetch_any "$tgz" "${GITHUB_MIRROR}/${rel}" "https://${rel}"; then
    tar -xzf "$tgz" -C "$TOOLCHAIN/bin" nfpm
    rm -f "$tgz"
    chmod +x "$TOOLCHAIN/bin/nfpm"
  else
    printf '    反代和直连都拿不到，改用 go install（要编一两分钟）\n'
    GOBIN="$TOOLCHAIN/bin" go install "github.com/goreleaser/nfpm/v2/cmd/nfpm@v${NFPM_VERSION}" \
      || die "nfpm 安装失败（镜像与 go install 都不通）"
  fi
  printf '    ✓ nfpm %s\n' "$("$TOOLCHAIN/bin/nfpm" --version 2>/dev/null || echo "$NFPM_VERSION")"
}

# ---------- fnpack（打 fpk）----------
# 分发地址在飞牛自家 CDN，国内直连很快；没有 GitHub 依赖。
ensure_fnpack() {
  have fnpack && {
    printf '    ✓ 系统 fnpack\n'
    return 0
  }
  [ -x "$TOOLCHAIN/bin/fnpack" ] && {
    printf '    ✓ 已就绪 fnpack\n'
    return 0
  }
  fetch_any "$TOOLCHAIN/bin/fnpack" \
    "https://static2.fnnas.com/fnpack/fnpack-${FNPACK_VERSION}-linux-${HOST_ARCH}" \
    || die "fnpack ${FNPACK_VERSION} 下载失败（打 fpk 必须要它）"
  chmod +x "$TOOLCHAIN/bin/fnpack"
  printf '    ✓ fnpack %s\n' "$FNPACK_VERSION"
}

# ---------- upx（可选）----------
# 与统一 CI 镜像里同一个版本、同一个 URL 形态（tarball 里是
# upx-<ver>-<arch>_linux/upx）。解 tar.xz 要 xz，插件镜像给什么就不知道了，
# 缺了先 apt 补。整条都失败也不终止：Makefile 里 ENABLE_UPX=1 找不到 upx 只打警告。
ensure_upx() {
  [ -x "$TOOLCHAIN/bin/upx" ] && {
    printf '    ✓ 命中缓存的 upx\n'
    return 0
  }
  have upx && {
    printf '    ✓ 系统 upx %s\n' "$(upx --version 2>/dev/null | head -n1)"
    return 0
  }

  local dir="upx-${UPX_VERSION}-${HOST_ARCH}_linux" rel tgz="$TOOLCHAIN/upx.tar.xz"
  rel="github.com/upx/upx/releases/download/v${UPX_VERSION}/${dir}.tar.xz"
  if ! have xz && ! have unxz; then
    apt_install xz-utils || true
  fi
  if { have xz || have unxz; } \
    && fetch_any "$tgz" "${GITHUB_MIRROR}/${rel}" "https://${rel}"; then
    rm -rf "$TOOLCHAIN/$dir"
    tar -xf "$tgz" -C "$TOOLCHAIN" "$dir/upx" 2>/dev/null \
      && mv "$TOOLCHAIN/$dir/upx" "$TOOLCHAIN/bin/upx" \
      && chmod +x "$TOOLCHAIN/bin/upx" \
      && rm -rf "$tgz" "$TOOLCHAIN/$dir"
  fi
  if [ -x "$TOOLCHAIN/bin/upx" ]; then
    printf '    ✓ upx %s（反代）\n' "$("$TOOLCHAIN/bin/upx" --version 2>/dev/null | head -n1)"
  elif apt_install upx-ucl && have upx; then
    printf '    ✓ upx %s（apt）\n' "$(upx --version 2>/dev/null | head -n1)"
  else
    printf '    ⚠ upx 装不上，跳过压缩（包会大一些，不影响功能）\n'
  fi
}

ensure_make() {
  have make && return 0
  apt_install make && have make && return 0
  die "没有 make，且装不上"
}

log "准备工具链 → $TOOLCHAIN"
ensure_downloader
ensure_go
ensure_node
export PATH="$TOOLCHAIN/bin:$TOOLCHAIN/go/bin:$TOOLCHAIN/node/bin:$PATH"
export GOBIN="$TOOLCHAIN/bin"
# 这里不再有 auto 分支：ensure_go 已经把 go 钉在 1.27.3+（镜像自带或官方包），
# 放 auto 只会让它去 proxy.golang.org 再拉一套然后超时。
export GOTOOLCHAIN=local
export npm_config_registry="${npm_config_registry:-https://registry.npmmirror.com}"
ensure_pnpm
ensure_nfpm
ensure_fnpack
ensure_upx
ensure_make

log "工具链自检"
go version
node -v
pnpm -v
nfpm --version 2>/dev/null || true
fnpack --help >/dev/null 2>&1 || true

# ============================== 构建 ==============================
# 与 GitHub / GitLab 完全相同的三步：装依赖 → 打包 → 清掉裸二进制。
# package-linux-all 自己会跑 build-frontend（Svelte → internal/webui/www）和两架构交叉编译，
# 所以不需要在 CI 里单独拆分前端与后端。
log "安装依赖（make deps）"
make deps

log "打包 deb + fpk（两架构）"
VERSION="$TAG" REPO_SOURCE="$REPO_SOURCE" make package-linux-all

log "清理裸二进制（保留安装包）"
make clean-archives

# 少一个都说明打包没走完，别让下游发布一个残缺的版本
EXPECTED="fnmusic-sync_amd64.deb fnmusic-sync_arm64.deb fnmusic-sync-amd64.fpk fnmusic-sync-arm64.fpk"
for f in $EXPECTED; do
  [ -f "bin/$f" ] || die "缺少产物 bin/$f"
done
log "产物清单"
ls -lh bin/

# ============================== 校验文件 ==============================
# 正文的「校验」一节只在 bin/SHA256SUMS 存在时才写，所以这一步不能省。
log "生成校验文件 bin/SHA256SUMS"
(
  cd bin
  : >SHA256SUMS
  for f in *.deb *.fpk; do
    [ -e "$f" ] || continue
    sha256sum "$f" >>SHA256SUMS
  done
  cat SHA256SUMS
)

# ============================== Release 正文 ==============================
# 正文与更新内容都只有一份实现：
#   scripts/ci/render-release-body.sh   表格/标题/下载链接/校验说明
#   scripts/ci/release-changelog.sh     按发布渠道选对比基准的提交摘要
# 另外四个平台的流水线（CNB / GitHub / GitLab / Gitee）调用的也是这两个脚本，
# 别在这里另写一套表格 —— 改一处忘一处就是这么来的。
DL_BASE="${DL_BASE:-}"
[ -n "$DL_BASE" ] || die "DL_BASE 未传，正文里的下载链接没有前缀"

SELF_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"

log "生成更新内容（按渠道选对比基准）"
# 挑不出基准时脚本会 exit 1（而不是谎报「首次发布」）：宁可在这里失败，
# 也不要把一份错误的正文发出去。
CHANGELOG="$(REPO_URL="${REPO_URL:-}" COMMIT_URL="${COMMIT_URL:-}" \
  sh "$SELF_DIR/release-changelog.sh" "$TAG")"

log "生成 release_body.md"
VERSION="$TAG" \
  PRERELEASE="$PRE" \
  DL_BASE="$DL_BASE" \
  STABLE_URL="${STABLE_URL:-}" \
  REPO_URL="${REPO_URL:-}" \
  COMMIT_URL="${COMMIT_URL:-}" \
  COMMIT_SHA="${COMMIT_SHA:-}" \
  BUILD_TIME="$(date -u '+%Y-%m-%d %H:%M:%S UTC')" \
  ARTIFACT_DIR=bin \
  OUT_FILE=release_body.md \
  CHANGELOG="$CHANGELOG" \
  sh "$SELF_DIR/render-release-body.sh"

log "完成：bin/ 下 4 个安装包 + SHA256SUMS + release_body.md"
