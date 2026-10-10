# 使用 golang 镜像作为基础
FROM golang:1.27.2-trixie

ARG GITHUB_MIRROR="https://files.m.daocloud.io"
# ==========================================
# 定义版本变量
# https://github.com/upx/upx/releases
# https://github.com/goreleaser/nfpm/releases
# https://developer.fnnas.com/docs/cli/fnpack/
# https://github.com/nodejs/node/releases
# https://github.com/sqlc-dev/sqlc/releases
# https://github.com/golangci/golangci-lint/releases
# https://go.googlesource.com/vuln
# https://github.com/mvdan/sh/releases
# https://github.com/reteps/dockerfmt/releases
# https://github.com/google/yamlfmt/releases
# https://github.com/itchyny/gojq/releases
# https://github.com/wasilibs/go-sql-formatter/releases
# ==========================================
ARG UPX_VERSION=5.2.1
ARG NFPM_VERSION=2.47.0
ARG FNPACK_VERSION=1.2.3
ARG NODE_VERSION=26.11.1
ARG SQLC_VERSION=v1.31.1
ARG GOLANGCI_LINT_VERSION=v2.14.0
ARG GOVULNCHECK_VERSION=v1.8.0
ARG SHFMT_VERSION=v3.14.1
ARG DOCKERFMT_VERSION=v0.5.4
ARG YAMLFMT_VERSION=v0.21.0
ARG GOJQ_VERSION=v0.12.19
ARG GOSQLFORMATTER_VERSION=v15.8.2

# 1. 设置 Go 环境变量
ENV GO111MODULE=on
ENV GOPROXY=https://mirrors.cloud.tencent.com/go/,direct
ENV GOPATH=/go
ENV PATH="$GOPATH/bin:$PATH"

# 2. 替换 Debian APT 加速源
RUN sed -i 's|deb.debian.org|mirrors.cloud.tencent.com|g' /etc/apt/sources.list.d/debian.sources 2>/dev/null \
    || sed -i 's|deb.debian.org|mirrors.cloud.tencent.com|g' /etc/apt/sources.list

# 3. 安装基础构建工具
RUN apt-get update && apt-get install -y --no-install-recommends \
    wget \
    xz-utils \
    ca-certificates \
    curl \
    gnupg \
    jq

# 4. 安装 UPX 压缩工具
RUN ARCH=$(uname -m) && \
    if [ "$ARCH" = "x86_64" ]; then UPX_ARCH="amd64"; \
    elif [ "$ARCH" = "aarch64" ]; then UPX_ARCH="arm64"; \
    else echo "Unsupported architecture: $ARCH" && exit 1; fi && \
    \
    # 构建下载地址
    UPX_DIR="upx-${UPX_VERSION}-${UPX_ARCH}_linux" && \
    UPX_URL="${GITHUB_MIRROR}/github.com/upx/upx/releases/download/v${UPX_VERSION}/${UPX_DIR}.tar.xz" && \
    \
    cd /tmp && \
    wget -q -O upx.tar.xz "${UPX_URL}" && \
    tar -xf upx.tar.xz && \
    mv ${UPX_DIR}/upx /usr/local/bin/upx && \
    chmod +x /usr/local/bin/upx && \
    # 清理
    rm -rf upx.tar.xz ${UPX_DIR} && \
    echo "--- UPX installed ---" && \
    upx --version

# 5. 安装 nfpm (用于打包 deb，一份配置三种格式)
RUN ARCH=$(uname -m) && \
    if [ "$ARCH" = "x86_64" ]; then NFPM_ARCH="x86_64"; \
    elif [ "$ARCH" = "aarch64" ]; then NFPM_ARCH="arm64"; \
    else echo "Unsupported architecture: $ARCH" && exit 1; fi && \
    \
    NFPM_URL="${GITHUB_MIRROR}/github.com/goreleaser/nfpm/releases/download/v${NFPM_VERSION}/nfpm_${NFPM_VERSION}_Linux_${NFPM_ARCH}.tar.gz" && \
    \
    cd /tmp && \
    wget -q -O nfpm.tar.gz "${NFPM_URL}" && \
    tar -xf nfpm.tar.gz && \
    mv nfpm /usr/local/bin/nfpm && \
    chmod +x /usr/local/bin/nfpm && \
    rm -rf nfpm.tar.gz && \
    echo "--- nfpm installed ---" && \
    nfpm --version

# 6. 安装 fnpack
RUN ARCH=$(uname -m) && \
    if [ "$ARCH" = "x86_64" ]; then FNPACK_ARCH="amd64"; \
    elif [ "$ARCH" = "aarch64" ]; then FNPACK_ARCH="arm64"; \
    else echo "Unsupported architecture: $ARCH" && exit 1; fi && \
    \
    FNPACK_URL="https://static2.fnnas.com/fnpack/fnpack-${FNPACK_VERSION}-linux-${FNPACK_ARCH}" && \
    \
    wget -q -O /usr/local/bin/fnpack "${FNPACK_URL}" && \
    chmod +x /usr/local/bin/fnpack && \
    echo "--- fnpack installed ---" && \
    fnpack --help >/dev/null 2>&1 || true

# 7. 安装 Node.js 工具
RUN ARCH=$(uname -m) && \
    if [ "$ARCH" = "x86_64" ]; then NODE_ARCH="x64"; \
    elif [ "$ARCH" = "aarch64" ]; then NODE_ARCH="arm64"; \
    else echo "Unsupported architecture: $ARCH" && exit 1; fi && \
    \
    # 构建下载地址
    NODE_DIR="node-v${NODE_VERSION}-linux-${NODE_ARCH}" && \
    NODE_URL="https://nodejs.org/dist/v${NODE_VERSION}/${NODE_DIR}.tar.xz" && \
    \
    cd /tmp && \
    wget -q -O node.tar.xz "${NODE_URL}" && \
    tar -xJf node.tar.xz -C /usr/local --strip-components=1 && \
    \
    # 配置 npm/pnpm 使用腾讯云源
    npm config set registry https://mirrors.cloud.tencent.com/npm/ && \
    npm install -g pnpm && \
    pnpm config set registry https://mirrors.cloud.tencent.com/npm/ && \
    pnpm config set ignore-scripts false && \
    \
    # 清理
    rm -rf node.tar.xz && \
    echo "--- Node.js installed ---" && \
    node -v && npm -v && pnpm -v

# 8 预安装 Go 工具链（版本已锁定）
RUN echo "==> 预安装 Go 工具链（版本已锁定）..." \
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
    && echo "--- Go 工具链安装完成 ---" \
    && ls -l $GOPATH/bin

# 10. 卸载不需要的工具，并清空所有 apt 缓存
RUN apt-get purge -y wget xz-utils \
    && apt-get autoremove -y \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/* /tmp/* /root/.npm

CMD ["bash"]