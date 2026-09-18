# 使用 golang 镜像作为基础
FROM golang:1.27.1-trixie

ARG GITHUB_MIRROR="https://files.m.daocloud.io"
# ==========================================
# 定义版本变量
# https://github.com/upx/upx/releases
# https://github.com/goreleaser/nfpm/releases
# https://developer.fnnas.com/docs/cli/fnpack/
# https://github.com/nodejs/node/releases
# https://github.com/golangci/golangci-lint/releases
# https://github.com/sqlc-dev/sqlc/releases
# ==========================================
ARG UPX_VERSION=5.2.1
ARG NFPM_VERSION=2.47.0
ARG FNPACK_VERSION=1.2.3
ARG NODE_VERSION=26.8.2
ARG GOLANGCI_LINT_VERSION=2.13.2
ARG SQLC_VERSION=1.31.1

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

# 8. 安装 golangci-lint 工具
RUN ARCH=$(uname -m) && \
    if [ "$ARCH" = "x86_64" ]; then LINT_ARCH="amd64"; \
    elif [ "$ARCH" = "aarch64" ]; then LINT_ARCH="arm64"; \
    else echo "Unsupported architecture: $ARCH" && exit 1; fi && \
    \
    # 构建下载地址
    LINT_DIR="golangci-lint-${GOLANGCI_LINT_VERSION}-linux-${LINT_ARCH}" && \
    LINT_URL="${GITHUB_MIRROR}/github.com/golangci/golangci-lint/releases/download/v${GOLANGCI_LINT_VERSION}/${LINT_DIR}.tar.gz" && \
    \
    cd /tmp && \
    wget -q -O lint.tar.gz "${LINT_URL}" && \
    tar -xf lint.tar.gz && \
    mv ${LINT_DIR}/golangci-lint /usr/local/bin/golangci-lint && \
    chmod +x /usr/local/bin/golangci-lint && \
    rm -rf lint.tar.gz ${LINT_DIR} && \
    # 清理
    echo "--- golangci-lint installed ---" && \
    golangci-lint --version

# 9. 安装 sqlc (用于从 SQL 生成类型安全的 Go 代码)
RUN ARCH=$(uname -m) && \
    if [ "$ARCH" = "x86_64" ]; then SQLC_ARCH="amd64"; \
    elif [ "$ARCH" = "aarch64" ]; then SQLC_ARCH="arm64"; \
    else echo "Unsupported architecture: $ARCH" && exit 1; fi && \
    \
    # 构建下载地址 (使用 DaoCloud 镜像加速)
    SQLC_URL="${GITHUB_MIRROR}/github.com/sqlc-dev/sqlc/releases/download/v${SQLC_VERSION}/sqlc_${SQLC_VERSION}_linux_${SQLC_ARCH}.tar.gz" && \
    \
    cd /tmp && \
    wget -q -O sqlc.tar.gz "${SQLC_URL}" && \
    tar -xzf sqlc.tar.gz && \
    # 兼容压缩包内是否有顶层目录的情况，确保能准确移动二进制文件
    if [ -f sqlc ]; then mv sqlc /usr/local/bin/sqlc; else mv */sqlc /usr/local/bin/sqlc; fi && \
    chmod +x /usr/local/bin/sqlc && \
    rm -rf sqlc.tar.gz && \
    echo "--- sqlc installed ---" && \
    sqlc version

# 10. 卸载不需要的工具，并清空所有 apt 缓存
RUN apt-get purge -y wget xz-utils \
    && apt-get autoremove -y \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/* /tmp/* /root/.npm

CMD ["bash"]