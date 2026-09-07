BINARY := fnmusic-sync
PKG    := cnb.cool/dtapp/fnmusic-sync

.PHONY: deps update-deps

# 安装依赖
deps: 
	@echo "[Deps] 安装依赖..."
	go mod tidy
	go mod download
	@echo "[Deps] 完成。"

# 更新依赖
update-deps:
	@echo "[Deps] 更新依赖..."
	go get -u ./...
	go mod tidy
	@echo "[Deps] 完成。"

# 格式化
format:
	gofmt -w -s .
	go fmt ./...
	go fix ./...

#####################
## 构建相关
#####################

# 二进制文件名
BINARY_NAME=fnmusic-sync

# 主包源码目录（本项目 main 包位于 cmd/fnmusic-sync）
BUILD_SRC_DIR ?= cmd/fnmusic-sync

# 编译输出目录
BUILD_DIR=bin

# 功能开关（设置为 1 开启，0 关闭）
ENABLE_UPX ?= 1
ENABLE_ARCHIVE ?= 1

# 检测是否存在 upx 压缩工具
UPX = $(shell command -v upx 2> /dev/null)
# 检测是否存在 tar 和 zip
TAR = $(shell command -v tar 2> /dev/null)
ZIP = $(shell command -v zip 2> /dev/null)

# 定义压缩逻辑：参数 $(1) 为文件路径
define compress_file
	@if [ "$(ENABLE_UPX)" = "1" ] && [ "$(UPX)" ]; then \
		FILE_SIZE=$$(stat -c%s "$(1)" 2>/dev/null || stat -f%z "$(1)"); \
		ORIG_SIZE_H=$$(du -h "$(1)" | cut -f1); \
		\
		if [ $$FILE_SIZE -lt 10485760 ]; then \
			LEVEL="--best"; \
			DESC="小文件-极限压缩"; \
		elif [ $$FILE_SIZE -lt 52428800 ]; then \
			LEVEL="-6"; \
			DESC="中等文件-平衡模式"; \
		else \
			LEVEL="-1"; \
			DESC="大文件-极速模式"; \
		fi; \
		\
		echo "  📦 正在以 [$$DESC ($$LEVEL)] 压缩..."; \
		ERR_MSG=$$($(UPX) $$LEVEL "$(1)" 2>&1); \
		\
		if [ $$? -eq 0 ]; then \
			NEW_SIZE_H=$$(du -h "$(1)" | cut -f1); \
			echo "  ⚡ UPX 压缩完成: $(1) [ $${ORIG_SIZE_H} -> $${NEW_SIZE_H} ]"; \
		else \
			echo "  ⚠️  UPX 压缩跳过: $(1) ($$ERR_MSG)"; \
		fi \
	elif [ "$(ENABLE_UPX)" = "1" ] && [ ! "$(UPX)" ]; then \
		echo "  ⏭️  UPX 未安装，跳过压缩"; \
	elif [ "$(ENABLE_UPX)" = "0" ]; then \
		echo "  ⏭️  UPX 已禁用"; \
	fi
endef

# 定义打包压缩逻辑：参数 $(1) 为源文件路径
# 源文件命名规范: {binary-name}-{os}-{arch}[.exe]，例如: backend-linux-amd64, reporter-windows-amd64.exe
# Linux/macOS 使用 tar.gz, Windows 使用 zip
define archive_binary
	@if [ "$(ENABLE_ARCHIVE)" = "1" ]; then \
		SOURCE_FILE=$(1); \
		BIN_NAME=$$(basename "$${SOURCE_FILE}"); \
		ARCHIVE_NAME="$(BUILD_DIR)/$${BIN_NAME}"; \
		\
		if echo "$${BIN_NAME}" | grep -q "\.exe$$"; then \
			if [ "$(ZIP)" ]; then \
				ARCHIVE_NAME="$${ARCHIVE_NAME}.zip"; \
				echo "  📦 正在打包 Windows 压缩包: $${ARCHIVE_NAME}..."; \
				cd $(BUILD_DIR) && zip -q "$$(basename $${ARCHIVE_NAME})" "$$(basename $${SOURCE_FILE})"; \
				cd ..; \
				ARCHIVE_SIZE=$$(du -h "$${ARCHIVE_NAME}" | cut -f1); \
				BIN_SIZE=$$(du -h "$${SOURCE_FILE}" | cut -f1); \
				echo "  ✓ 打包完成: $${ARCHIVE_NAME} [ 二进制: $${BIN_SIZE} -> 压缩包: $${ARCHIVE_SIZE} ]"; \
			else \
				echo "  ⚠️  zip 未安装，无法打包 Windows 压缩包"; \
			fi \
		else \
			if [ "$(TAR)" ]; then \
				ARCHIVE_NAME="$${ARCHIVE_NAME}.tar.gz"; \
				echo "  📦 正在打包 Unix 压缩包: $${ARCHIVE_NAME}..."; \
				tar -czf "$${ARCHIVE_NAME}" -C $(BUILD_DIR) "$$(basename $${SOURCE_FILE})"; \
				ARCHIVE_SIZE=$$(du -h "$${ARCHIVE_NAME}" | cut -f1); \
				BIN_SIZE=$$(du -h "$${SOURCE_FILE}" | cut -f1); \
				echo "  ✓ 打包完成: $${ARCHIVE_NAME} [ 二进制: $${BIN_SIZE} -> 压缩包: $${ARCHIVE_SIZE} ]"; \
			else \
				echo "  ⚠️  tar 未安装，无法打包 Unix 压缩包"; \
			fi \
		fi; \
	elif [ "$(ENABLE_ARCHIVE)" = "0" ]; then \
		echo "  ⏭️  压缩包已禁用"; \
	fi
endef

# 定义清理二进制文件逻辑：如果存在对应压缩包则删除二进制
# 参数 $(1) 为二进制文件路径
# 源文件命名规范: {binary-name}-{os}-{arch}[.exe]，例如: backend-linux-amd64, reporter-windows-amd64.exe
define clean_binary
	@SOURCE_FILE=$(1); \
	BIN_NAME=$$(basename "$${SOURCE_FILE}"); \
	if echo "$${BIN_NAME}" | grep -q "\.exe$$"; then \
		ARCHIVE_NAME="$(BUILD_DIR)/$${BIN_NAME}.zip"; \
	else \
		ARCHIVE_NAME="$(BUILD_DIR)/$${BIN_NAME}.tar.gz"; \
	fi; \
	if [ -f "$${ARCHIVE_NAME}" ] && [ -f "$${SOURCE_FILE}" ]; then \
		rm -f "$${SOURCE_FILE}"; \
		echo "  🗑️  已清理二进制: $${SOURCE_FILE} (存在 $${ARCHIVE_NAME})"; \
	else \
		echo "  ⏭️  未找到压缩包或二进制，跳过: $${SOURCE_FILE}"; \
	fi
endef

# 版本信息（支持外部传入）
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILDTIME ?= $(shell date -u '+%Y-%m-%d %H:%M:%S UTC')

# 路径（ldflags -X 注入版本变量的包路径；package main 用 "main" 即可）
PKG_PATH = main

# ldflags 模板
# -s -w: 移除符号表和调试信息，减小体积
# -buildid=: 移除构建指纹
LDFLAGS = -s -w \
	-X '$(PKG_PATH).Version=$(VERSION)' \
	-X '$(PKG_PATH).GitCommit=$(COMMIT)' \
	-X '$(PKG_PATH).BuildTime=$(BUILDTIME)' \
	-X '$(PKG_PATH).BinaryName=$(BINARY_NAME)' \
	-X 'fnmusic-sync/internal/scrobbler.Version=$(VERSION)'

# 统一定义构建命令
# CGO_ENABLED=0: 禁用 CGO，实现完全静态链接
# CGO_ENABLED=1: 启用 CGO，实现动态链接
# -trimpath: 移除本地路径，保护隐私
BUILD_CMD = CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)"

# 构建所有 Linux 平台
build-all: clean \
	build-linux-amd64 \
	build-linux-arm64 \
	build-linux-mips \
	build-linux-mipsle \
	build-linux-arm \
	build-linux-386

# 构建 Linux 平台
build-linux-all: build-linux-amd64 build-linux-arm64 build-linux-mips build-linux-mipsle build-linux-arm build-linux-386

clean:
	@echo "[Clean] 清理构建目录..."
	@rm -rf $(BUILD_DIR)
	@mkdir -p $(BUILD_DIR)

# 定义构建单个二进制文件的通用逻辑
# 参数: $(1)=GOOS, $(2)=GOARCH, $(3)=描述信息, $(4)=扩展名(.exe 或空), $(5)=额外 env (如 GOARM=7 GOMIPS=softfloat)
define build_binary
	@echo ""
	@echo "┌────────────────────────────────────────────────────────────"
	@echo "│ [构建] $(3)"
	@echo "└────────────────────────────────────────────────────────────"
	@cd $(BUILD_SRC_DIR) && $(5) GOOS=$(1) GOARCH=$(2) $(BUILD_CMD) -o $(CURDIR)/$(BUILD_DIR)/$(BINARY_NAME)-$(1)-$(2)$(4) .
	$(call compress_file,./$(BUILD_DIR)/$(BINARY_NAME)-$(1)-$(2)$(4))
	$(call archive_binary,./$(BUILD_DIR)/$(BINARY_NAME)-$(1)-$(2)$(4))
endef

build-linux-amd64:
	$(call build_binary,linux,amd64,Linux x86_64,,)

build-linux-arm64:
	$(call build_binary,linux,arm64,Linux ARM64,,)

build-linux-mips:
	$(call build_binary,linux,mips,Linux MIPS,,GOMIPS=softfloat)

build-linux-mipsle:
	$(call build_binary,linux,mipsle,Linux MIPSLE,,GOMIPS=softfloat)

build-linux-arm:
	$(call build_binary,linux,arm,Linux ARMv7,,GOARM=7)

build-linux-386:
	$(call build_binary,linux,386,Linux x86 32位,,)

# 清理裸二进制（保留 .tar.gz 压缩包供自升级下载，上传前执行）
.PHONY: clean-binaries
clean-binaries:
	@echo "[清理] 删除裸二进制（保留 .tar.gz 与安装包）..."
	$(call clean_binary,./$(BUILD_DIR)/$(BINARY_NAME)-linux-amd64)
	$(call clean_binary,./$(BUILD_DIR)/$(BINARY_NAME)-linux-arm64)
	$(call clean_binary,./$(BUILD_DIR)/$(BINARY_NAME)-linux-mips)
	$(call clean_binary,./$(BUILD_DIR)/$(BINARY_NAME)-linux-mipsle)
	$(call clean_binary,./$(BUILD_DIR)/$(BINARY_NAME)-linux-arm)
	$(call clean_binary,./$(BUILD_DIR)/$(BINARY_NAME)-linux-386)
	@echo "✓ 清理完成（bin/ 保留 .tar.gz + deb/rpm/apk）"

#####################
## 打包 (Linux deb/rpm/apk)
#####################

# 生成 nfpm 临时配置文件（替换变量）
# 参数: $(1)=架构名 (amd64 / x86_64 / aarch64)
define generate_nfpm_config
	@sed -e 's|$${ARCH}|$(1)|g' -e 's|$${VERSION}|$(VERSION)|g' \
	     -e 's|$${POSTINSTALL}|.nfpm-scripts/postinstall.sh|g' \
	     -e 's|$${PREREMOVE}|.nfpm-scripts/preremove.sh|g' \
	     .nfpm.yaml > .nfpm-$(1).yaml
endef

# 生成注入了 BINARY_NAME 的安装/卸载脚本（替换 __BINARY_NAME__ 占位符）
# 输出到 .nfpm-scripts/，由 nfpm 引用，打包结束后清理
define prepare_scripts
	@mkdir -p .nfpm-scripts
	@sed 's|__BINARY_NAME__|$(BINARY_NAME)|g' scripts/postinstall.sh > .nfpm-scripts/postinstall.sh
	@sed 's|__BINARY_NAME__|$(BINARY_NAME)|g' scripts/preremove.sh > .nfpm-scripts/preremove.sh
	@chmod +x .nfpm-scripts/postinstall.sh .nfpm-scripts/preremove.sh
endef

# 清理生成的临时脚本
define cleanup_scripts
	@rm -rf .nfpm-scripts
endef

# 打包单个架构的 deb/rpm/apk 包
# 参数: $(1)=GOARCH (amd64/arm64/mips/mipsle/arm), $(2)=deb 架构名, $(3)=deb 文件名后缀
# 参数: $(4)=rpm/apk 架构名 (x86_64/aarch64/mips/mipsel/arm)
define package_nfpm_formats
	$(call prepare_scripts)
	@if [ "$(1)" != "$(4)" ]; then \
		cp $(BUILD_DIR)/$(BINARY_NAME)-linux-$(1) $(BUILD_DIR)/$(BINARY_NAME)-linux-$(4); \
	fi
	$(call generate_nfpm_config,$(2))
	nfpm package -p deb -f .nfpm-$(2).yaml -t $(BUILD_DIR)/$(BINARY_NAME)_$(3).deb
	@rm -f .nfpm-$(2).yaml
	@echo "✓ deb 包已创建: $(BUILD_DIR)/$(BINARY_NAME)_$(3).deb"
	$(call generate_nfpm_config,$(4))
	nfpm package -p rpm -f .nfpm-$(4).yaml -t $(BUILD_DIR)/$(BINARY_NAME).$(4).rpm
	@echo "✓ rpm 包已创建: $(BUILD_DIR)/$(BINARY_NAME).$(4).rpm"
	nfpm package -p apk -f .nfpm-$(4).yaml -t $(BUILD_DIR)/$(BINARY_NAME).$(4).apk
	@echo "✓ apk 包已创建: $(BUILD_DIR)/$(BINARY_NAME).$(4).apk"
	@rm -f .nfpm-$(4).yaml
	@if [ "$(1)" != "$(4)" ]; then \
		rm -f $(BUILD_DIR)/$(BINARY_NAME)-linux-$(4); \
	fi
	$(call cleanup_scripts)
	@echo "✓ 包已创建 (deb/rpm/apk): $(BINARY_NAME)"
endef

# 各打包目标统一依赖一次性构建全部架构（避免重复编译）
package-linux-deb package-linux-rpm package-linux-apk: build-linux-all

# 打包 Linux deb（全部 6 架构）
.PHONY: package-linux-deb
package-linux-deb:
	@echo ""
	@echo "┌────────────────────────────────────────────────────────────"
	@echo "│ [打包] 创建 Linux deb 包 (amd64/arm64/mips/mipsle/arm/386)..."
	@echo "└────────────────────────────────────────────────────────────"
	$(call package_nfpm_formats,amd64,amd64,amd64,x86_64)
	$(call package_nfpm_formats,arm64,arm64,arm64,aarch64)
	$(call package_nfpm_formats,mips,mips,mips,mips)
	$(call package_nfpm_formats,mipsle,mipsel,mipsel,mipsel)
	$(call package_nfpm_formats,arm,arm,arm,arm)
	$(call package_nfpm_formats,386,i386,i386,i386)

# 打包 Linux rpm（全部 6 架构）
.PHONY: package-linux-rpm
package-linux-rpm:
	@echo ""
	@echo "┌────────────────────────────────────────────────────────────"
	@echo "│ [打包] 创建 Linux rpm 包 (x86_64/aarch64/mips/mipsel/arm/386)..."
	@echo "└────────────────────────────────────────────────────────────"
	$(call package_nfpm_formats,amd64,amd64,amd64,x86_64)
	$(call package_nfpm_formats,arm64,arm64,arm64,aarch64)
	$(call package_nfpm_formats,mips,mips,mips,mips)
	$(call package_nfpm_formats,mipsle,mipsel,mipsel,mipsel)
	$(call package_nfpm_formats,arm,arm,arm,arm)
	$(call package_nfpm_formats,386,i386,i386,i386)

# 打包 Linux apk（Alpine，全部 6 架构）
.PHONY: package-linux-apk
package-linux-apk:
	@echo ""
	@echo "┌────────────────────────────────────────────────────────────"
	@echo "│ [打包] 创建 Linux apk 包 (x86_64/aarch64/mips/mipsel/arm/386)..."
	@echo "└────────────────────────────────────────────────────────────"
	$(call package_nfpm_formats,amd64,amd64,amd64,x86_64)
	$(call package_nfpm_formats,arm64,arm64,arm64,aarch64)
	$(call package_nfpm_formats,mips,mips,mips,mips)
	$(call package_nfpm_formats,mipsle,mipsel,mipsel,mipsel)
	$(call package_nfpm_formats,arm,arm,arm,arm)
	$(call package_nfpm_formats,386,i386,i386,i386)

# 打包所有 Linux deb/rpm/apk 包（构建一次，各格式复用）
.PHONY: package-linux-all
package-linux-all: package-linux-deb package-linux-rpm package-linux-apk
	@echo ""
	@echo "╔════════════════════════════════════════════════════════════"
	@echo "║ ✓ 所有包已创建 (deb/rpm/apk, 多架构)"
	@echo "╚════════════════════════════════════════════════════════════"

# 打包所有 Linux 包（deb/rpm/apk）
.PHONY: package-linux
package-linux: package-linux-all

# 仅打包 Linux deb（快速验证用）
.PHONY: package-linux-only
package-linux-only: package-linux-deb

# 构建并打包所有 Linux 包
.PHONY: package-all
package-all: package-linux

.PHONY: clean-binaries
