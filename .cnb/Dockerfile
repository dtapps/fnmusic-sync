# 使用 golang 镜像作为基础
FROM golang:1.27.0-trixie

# ==========================================
# 定义版本变量
# https://github.com/upx/upx/releases
# https://github.com/goreleaser/nfpm/releases
# ==========================================
ARG UPX_VERSION=5.2.0
ARG NFPM_VERSION=2.47.0

# 1. 设置 Go 环境变量
ENV GO111MODULE=on
ENV GOPROXY=https://mirrors.cloud.tencent.com/go/,direct
ENV GOPATH=/go
ENV PATH="$GOPATH/bin:$PATH"

# 2. 替换 Debian APT 加速源
RUN sed -i 's|deb.debian.org|mirrors.cloud.tencent.com|g' /etc/apt/sources.list.d/debian.sources 2>/dev/null || \
    sed -i 's|deb.debian.org|mirrors.cloud.tencent.com|g' /etc/apt/sources.list

# 3. 安装基础构建工具
RUN apt-get update && apt-get install -y --no-install-recommends \
    wget \
    xz-utils \
    ca-certificates \
    curl \
    gnupg

# 4. 安装 UPX 压缩工具
RUN ARCH=$(uname -m) && \
    if [ "$ARCH" = "x86_64" ]; then UPX_ARCH="amd64"; \
    elif [ "$ARCH" = "aarch64" ]; then UPX_ARCH="arm64"; \
    else echo "Unsupported architecture: $ARCH" && exit 1; fi && \
    \
    # 构建下载地址
    UPX_DIR="upx-${UPX_VERSION}-${UPX_ARCH}_linux" && \
    UPX_URL="https://files.m.daocloud.io/github.com/upx/upx/releases/download/v${UPX_VERSION}/${UPX_DIR}.tar.xz" && \
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

# 5. 安装 nfpm (用于打包 deb/rpm/apk，一份配置三种格式)
RUN ARCH=$(uname -m) && \
    if [ "$ARCH" = "x86_64" ]; then NFPM_ARCH="x86_64"; \
    elif [ "$ARCH" = "aarch64" ]; then NFPM_ARCH="arm64"; \
    else echo "Unsupported architecture: $ARCH" && exit 1; fi && \
    \
    NFPM_URL="https://files.m.daocloud.io/github.com/goreleaser/nfpm/releases/download/v${NFPM_VERSION}/nfpm_${NFPM_VERSION}_Linux_${NFPM_ARCH}.tar.gz" && \
    \
    cd /tmp && \
    wget -q -O nfpm.tar.gz "${NFPM_URL}" && \
    tar -xf nfpm.tar.gz && \
    mv nfpm /usr/local/bin/nfpm && \
    chmod +x /usr/local/bin/nfpm && \
    rm -rf nfpm.tar.gz && \
    echo "--- nfpm installed ---" && \
    nfpm --version

# 6. 卸载不需要的工具，并清空所有 apt 缓存
RUN apt-get purge -y wget xz-utils && \
    apt-get autoremove -y && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/* /tmp/* /root/.npm

CMD ["bash"]