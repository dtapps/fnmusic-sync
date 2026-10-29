# 使用 golang 镜像作为基础
FROM golang:1.27.3-trixie

# ==========================================
# 定义版本变量
# https://github.com/upx/upx/releases
# https://github.com/goreleaser/nfpm/releases
# https://developer.fnnas.com/docs/cli/fnpack/
# https://github.com/nodejs/node/releases
# https://github.com/pnpm/pnpm/releases
# https://github.com/sqlc-dev/sqlc/releases
# https://github.com/golangci/golangci-lint/releases
# https://go.googlesource.com/vuln
# https://github.com/mvdan/sh/releases
# https://github.com/reteps/dockerfmt/releases
# https://github.com/google/yamlfmt/releases
# https://github.com/itchyny/gojq/releases
# https://github.com/wasilibs/go-sql-formatter/releases
# https://github.com/gotestyourself/gotestsum/releases
# https://github.com/boumenot/gocover-cobertura/releases
# https://github.com/Kunde21/markdownfmt/releases
# ==========================================
ARG UPX_VERSION=5.2.1
ARG NFPM_VERSION=2.48.0
ARG FNPACK_VERSION=1.2.3
ARG NODE_VERSION=26.12.1
ARG PNPM_VERSION=12.16.0
ARG SQLC_VERSION=v1.31.1
ARG GOLANGCI_LINT_VERSION=v2.15.0
ARG GOVULNCHECK_VERSION=v1.9.0
ARG SHFMT_VERSION=v3.14.1
ARG DOCKERFMT_VERSION=v0.5.4
ARG YAMLFMT_VERSION=v0.21.0
ARG GOJQ_VERSION=v0.12.19
ARG GOSQLFORMATTER_VERSION=v15.8.2
ARG GOTESTSUM_VERSION=v1.13.0
ARG GOCOVER_COBERTURA_VERSION=v1.5.0
ARG MARKDOWNFMT_VERSION=v3.1.0

# 1. 设置 Go 环境变量
ENV GOINSECURE=go.dtapp.net
ENV GOPATH=/go
ENV PATH="$GOPATH/bin:$PATH"

# 2. 缓存失效锚点（只给 GitLab 定时流水线用，其它平台没这两行）
#   BuildKit 的 layer cache 键里没有「今天是几号」：base image digest 没变、命令没变，
#   下面 apt-get install 那层就是命中的，镜像里的包版本会永远停在第一次构建那天。
#   定时流水线传进来的 CACHE_BUST 每次不同，这一层随之失效，从它往后全部重建，
#   才真吃到源上更新的包。默认值固定 —— 普通 push / 手动构建仍然全命中缓存。
ARG CACHE_BUST=static
RUN echo "cache-bust=$CACHE_BUST"

# 3. 安装基础构建工具
RUN apt-get update && apt-get install -y --no-install-recommends \
    wget \
    xz-utils \
    ca-certificates \
    curl \
    gnupg \
    git \
    make \
    jq \
    && rm -rf /var/lib/apt/lists/*

# 4. 安装 UPX 工具
RUN ARCH=$(uname -m) && \
    if [ "$ARCH" = "x86_64" ]; then UPX_ARCH="amd64"; \
    elif [ "$ARCH" = "aarch64" ]; then UPX_ARCH="arm64"; \
    else echo "不支持的架构: $ARCH" && exit 1; fi && \
    \
    # 构建下载地址
    UPX_DIR="upx-${UPX_VERSION}-${UPX_ARCH}_linux" && \
    UPX_URL="https://github.com/upx/upx/releases/download/v${UPX_VERSION}/${UPX_DIR}.tar.xz" && \
    \
    cd /tmp && \
    wget -q -O upx.tar.xz "${UPX_URL}" && \
    tar -xf upx.tar.xz && \
    mv ${UPX_DIR}/upx /usr/local/bin/upx && \
    chmod +x /usr/local/bin/upx && \
    # 清理
    rm -rf upx.tar.xz ${UPX_DIR} && \
    echo "--- UPX 安装的 ---" && \
    upx --version

# 5. 安装 nfpm 工具
RUN ARCH=$(uname -m) && \
    if [ "$ARCH" = "x86_64" ]; then NFPM_ARCH="x86_64"; \
    elif [ "$ARCH" = "aarch64" ]; then NFPM_ARCH="arm64"; \
    else echo "不支持的架构: $ARCH" && exit 1; fi && \
    \
    NFPM_URL="https://github.com/goreleaser/nfpm/releases/download/v${NFPM_VERSION}/nfpm_${NFPM_VERSION}_Linux_${NFPM_ARCH}.tar.gz" && \
    \
    cd /tmp && \
    wget -q -O nfpm.tar.gz "${NFPM_URL}" && \
    tar -xzf nfpm.tar.gz nfpm && \
    mv nfpm /usr/local/bin/nfpm && \
    chmod +x /usr/local/bin/nfpm && \
    rm -rf nfpm.tar.gz && \
    echo "--- nfpm 安装的 ---" && \
    nfpm --version

# 6. 安装 fnpack 工具
RUN ARCH=$(uname -m) && \
    if [ "$ARCH" = "x86_64" ]; then FNPACK_ARCH="amd64"; \
    elif [ "$ARCH" = "aarch64" ]; then FNPACK_ARCH="arm64"; \
    else echo "不支持的架构: $ARCH" && exit 1; fi && \
    \
    FNPACK_URL="https://static2.fnnas.com/fnpack/fnpack-${FNPACK_VERSION}-linux-${FNPACK_ARCH}" && \
    \
    wget -q -O /usr/local/bin/fnpack "${FNPACK_URL}" && \
    chmod +x /usr/local/bin/fnpack && \
    echo "--- fnpack 安装的 ---" && \
    fnpack --help >/dev/null 2>&1 || true

# 7. 安装 Node.js 工具
RUN ARCH=$(uname -m) && \
    if [ "$ARCH" = "x86_64" ]; then NODE_ARCH="x64"; \
    elif [ "$ARCH" = "aarch64" ]; then NODE_ARCH="arm64"; \
    else echo "不支持的架构: $ARCH" && exit 1; fi && \
    \
    # 构建下载地址
    NODE_DIR="node-v${NODE_VERSION}-linux-${NODE_ARCH}" && \
    NODE_URL="https://nodejs.org/dist/v${NODE_VERSION}/${NODE_DIR}.tar.xz" && \
    \
    cd /tmp && \
    wget -q -O node.tar.xz "${NODE_URL}" && \
    tar -xJf node.tar.xz -C /usr/local --strip-components=1 && \
    \
    # 配置 npm/pnpm
    npm install -g pnpm@${PNPM_VERSION} && \
    \
    # 清理
    rm -rf node.tar.xz /root/.npm /root/.cache/node && \
    echo "--- Node.js 安装的 ---" && \
    node -v && npm -v && pnpm -v

# 8 预安装 Go 工具链
# 同一层内 go install + 清缓存，避免 go-build 和 mod 缓存留在独立层里
RUN echo "==> 预安装 Go 工具链..." \
    && go install github.com/sqlc-dev/sqlc/cmd/sqlc@${SQLC_VERSION} \
    && echo "==> sqlc 安装完成 (${SQLC_VERSION})" \
    && go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@${GOLANGCI_LINT_VERSION} \
    && echo "==> golangci-lint 安装完成 (${GOLANGCI_LINT_VERSION})" \
    && go install golang.org/x/vuln/cmd/govulncheck@${GOVULNCHECK_VERSION} \
    && echo "==> govulncheck 安装完成 (${GOVULNCHECK_VERSION})" \
    && go install mvdan.cc/sh/v3/cmd/shfmt@${SHFMT_VERSION} \
    && echo "==> shfmt 安装完成 (${SHFMT_VERSION})" \
    && go install github.com/reteps/dockerfmt@${DOCKERFMT_VERSION} \
    && echo "==> dockerfmt 安装完成 (${DOCKERFMT_VERSION})" \
    && go install github.com/google/yamlfmt/cmd/yamlfmt@${YAMLFMT_VERSION} \
    && echo "==> yamlfmt 安装完成 (${YAMLFMT_VERSION})" \
    && go install github.com/itchyny/gojq/cmd/gojq@${GOJQ_VERSION} \
    && echo "==> gojq 安装完成 (${GOJQ_VERSION})" \
    && go install github.com/wasilibs/go-sql-formatter/v15/cmd/sql-formatter@${GOSQLFORMATTER_VERSION} \
    && echo "==> go-sql-formatter 安装完成 (${GOSQLFORMATTER_VERSION})" \
    && go install gotest.tools/gotestsum@${GOTESTSUM_VERSION} \
    && echo "==> gotestsum 安装完成 (${GOTESTSUM_VERSION})" \
    && go install github.com/boumenot/gocover-cobertura@${GOCOVER_COBERTURA_VERSION} \
    && echo "==> gocover-cobertura 安装完成 (${GOCOVER_COBERTURA_VERSION})" \
    && go install github.com/Kunde21/markdownfmt/v3/cmd/markdownfmt@${MARKDOWNFMT_VERSION} \
    && echo "==> markdownfmt 安装完成 (${MARKDOWNFMT_VERSION})" \
    && echo "--- Go 工具链安装完成 ---" \
    && ls -l $GOPATH/bin \
    && rm -rf /root/.cache/go-build /go/pkg/mod

# 9. 卸载不需要的工具，并清空所有 apt 缓存
RUN apt-get purge -y wget xz-utils \
    && apt-get autoremove -y \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/* /tmp/* /root/.npm

CMD ["bash"]