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

# 格式化（Go + 前端 + 配置 + 文档 + 脚本）
format: format-go format-frontend format-yaml format-markdown format-shell

# 格式化 Go 代码
format-go:
	@echo "[Format] 格式化后端文件 (GO)..."
	gofmt -w -s .
	go fmt ./...
	go fix ./...
	go vet ./...

# 格式化前端文件（HTML/CSS/JS）
# 使用 prettier，无需全局安装，npx 自动临时下载
format-frontend:
	@echo "[Format] 格式化前端文件 (HTML/CSS/JS)..."
	@npx --yes prettier@latest \
		--write \
		--tab-width 2 \
		--semi true \
		--single-quote true \
		--trailing-comma all \
		--print-width 120 \
		--html-whitespace-sensitivity css \
		"internal/webui/www/**/*.{html,css,js}" \
		|| echo "⚠️  prettier 格式化失败，请确认 npx 可用"
	@echo "[Format] 前端格式化完成。"

# 格式化 YAML 文件（.yaml/.yml）
format-yaml:
	@echo "[Format] 格式化配置文件 (YAML)…"
	@npx --yes prettier@latest \
		--write \
		--tab-width 2 \
		--single-quote true \
		--trailing-comma all \
		--print-width 120 \
		"configs/**/*.{yaml,yml}" \
		".cnb/**/*.{yaml,yml}" \
		".cnb.yml" \
		".nfpm.yaml" \
		|| echo "⚠️  prettier 格式化失败，请确认 npx 可用"
	@echo "[Format] YAML 格式化完成。"

# 格式化 Markdown 文件（.md）
format-markdown:
	@echo "[Format] 格式化文档 (Markdown)…"
	@npx --yes prettier@latest \
		--write \
		--tab-width 2 \
		--print-width 120 \
		--prose-wrap preserve \
		"**/*.md" \
		|| echo "⚠️  prettier 格式化失败，请确认 npx 可用"
	@echo "[Format] Markdown 格式化完成。"

# 格式化 Shell 脚本（.sh）
# 使用 shfmt，需安装：go install mvdan.cc/sh/v3/cmd/shfmt@latest
# 退化为 shellcheck 检查（仅警告，不自动修复）
format-shell:
	@echo "[Format] 格式化脚本 (Shell)…"
	@command -v shfmt >/dev/null 2>&1 && { \
		find . -name "*.sh" -not -path "./.git/*" -not -path "./vendor/*" -not -path "./node_modules/*" -exec shfmt -w -i 2 -ci -bn -s {} + ; \
		echo "[Format] Shell 格式化完成（shfmt）。" ; \
	} || { \
		echo "⚠️  shfmt 未安装，尝试 shellcheck 检查…" ; \
		command -v shellcheck >/dev/null 2>&1 && { \
			find . -name "*.sh" -not -path "./.git/*" -not -path "./vendor/*" -not -path "./node_modules/*" -exec shellcheck -S warning {} + || true ; \
			echo "[Format] Shell 检查完成（shellcheck，仅检查未修复）。" ; \
		} || echo "⚠️  shfmt 和 shellcheck 均未安装，跳过 Shell 格式化。" ; \
	}

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

# 定义清理压缩包文件逻辑：删除 .tar.gz / .zip 压缩包
# 参数 $(1) 为二进制文件路径（据此推导压缩包名）
# 源文件命名规范: {binary-name}-{os}-{arch}[.exe]
define clean_archive
	@SOURCE_FILE=$(1); \
	BIN_NAME=$$(basename "$${SOURCE_FILE}"); \
	if echo "$${BIN_NAME}" | grep -q "\.exe$$"; then \
		ARCHIVE_NAME="$(BUILD_DIR)/$${BIN_NAME}.zip"; \
	else \
		ARCHIVE_NAME="$(BUILD_DIR)/$${BIN_NAME}.tar.gz"; \
	fi; \
	if [ -f "$${ARCHIVE_NAME}" ]; then \
		rm -f "$${ARCHIVE_NAME}"; \
		echo "  🗑️  已清理压缩包: $${ARCHIVE_NAME}"; \
	else \
		echo "  ⏭️  未找到压缩包，跳过: $${ARCHIVE_NAME}"; \
	fi
endef

# 版本信息（支持外部传入）
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILDTIME ?= $(shell date -u '+%Y-%m-%d %H:%M:%S UTC')

# 路径（ldflags -X 注入版本变量的包路径，需与 go.mod 模块路径一致）
PKG_PATH = cnb.cool/dtapp/fnmusic-sync/internal/buildinfo

# ldflags 模板
# -s -w: 移除符号表和调试信息，减小体积
# -buildid=: 移除构建指纹
# 所有构建信息统一注入到 buildinfo 包
LDFLAGS = -s -w \
	-X '$(PKG_PATH).Version=$(VERSION)' \
	-X '$(PKG_PATH).GitCommit=$(COMMIT)' \
	-X '$(PKG_PATH).BuildTime=$(BUILDTIME)' \
	-X '$(PKG_PATH).BinaryName=$(BINARY_NAME)'

# 统一定义构建命令
# CGO_ENABLED=0: 禁用 CGO，实现完全静态链接
# CGO_ENABLED=1: 启用 CGO，实现动态链接
# -trimpath: 移除本地路径，保护隐私
# -ldflags: 注入版本信息并移除符号表
# 注意：-tags 由 build_binary 的 $(6) 参数动态拼接，此处不含
BUILD_CMD = CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)"

# 构建所有 Linux 平台
build-all: clean \
	build-linux-amd64 \
	build-linux-arm64

# 构建 Linux 平台（不带标签 + 带标签）
build-linux-all: build-linux-amd64 build-linux-arm64

clean:
	@echo "[Clean] 清理构建目录..."
	@rm -rf $(BUILD_DIR)
	@mkdir -p $(BUILD_DIR)

# 定义构建单个二进制文件的通用逻辑（编译 + UPX + .tar.gz）
# 参数: $(1)=GOOS, $(2)=GOARCH, $(3)=描述信息, $(4)=扩展名(.exe 或空), $(5)=额外 env (如 GOARM=7 GOMIPS=softfloat), $(6)=build tags (为空则无)
define build_binary
	@echo ""
	@echo "┌────────────────────────────────────────────────────────────"
	@echo "│ [构建] $(3)"
	@echo "└────────────────────────────────────────────────────────────"
	@cd $(BUILD_SRC_DIR) && $(5) GOOS=$(1) GOARCH=$(2) $(BUILD_CMD) -o $(CURDIR)/$(BUILD_DIR)/$(BINARY_NAME)-$(1)-$(2)$(4) .
	$(call compress_file,./$(BUILD_DIR)/$(BINARY_NAME)-$(1)-$(2)$(4))
	$(call archive_binary,./$(BUILD_DIR)/$(BINARY_NAME)-$(1)-$(2)$(4))
endef

# 定义构建带 tag 二进制文件的逻辑（编译 + UPX + .tar.gz，输出名带 tag 前缀）
# 参数: $(1)=GOOS, $(2)=GOARCH, $(3)=描述信息, $(4)=tag 名
# 输出名: $(BINARY_NAME)-$(4)-$(1)-$(2)
define build_tag_binary
	@echo ""
	@echo "┌────────────────────────────────────────────────────────────"
	@echo "│ [构建] $(3)"
	@echo "└────────────────────────────────────────────────────────────"
	@cd $(BUILD_SRC_DIR) && GOOS=$(1) GOARCH=$(2) $(BUILD_CMD) -tags "$(4)" -o $(CURDIR)/$(BUILD_DIR)/$(BINARY_NAME)-$(4)-$(1)-$(2) .
	$(call compress_file,./$(BUILD_DIR)/$(BINARY_NAME)-$(4)-$(1)-$(2))
	$(call archive_binary,./$(BUILD_DIR)/$(BINARY_NAME)-$(4)-$(1)-$(2))
endef

build-linux-amd64:
	$(call build_binary,linux,amd64,Linux x86_64,,,)
	$(call build_tag_binary,linux,amd64,Linux x86_64 带标签,$(BUILD_TAG))

build-linux-arm64:
	$(call build_binary,linux,arm64,Linux ARM64,,,)
	$(call build_tag_binary,linux,arm64,Linux ARM64 带标签,$(BUILD_TAG))

# 清理裸二进制（保留 .tar.gz 压缩包供自升级下载，上传前执行）
.PHONY: clean-binaries
clean-binaries:
	@echo "[清理] 删除裸二进制（保留 .tar.gz 与安装包）..."
	$(call clean_binary,./$(BUILD_DIR)/$(BINARY_NAME)-linux-amd64)
	$(call clean_binary,./$(BUILD_DIR)/$(BINARY_NAME)-linux-arm64)
	$(call clean_binary,./$(BUILD_DIR)/$(BINARY_NAME)-$(BUILD_TAG)-linux-amd64)
	$(call clean_binary,./$(BUILD_DIR)/$(BINARY_NAME)-$(BUILD_TAG)-linux-arm64)
	@echo "✓ 清理完成（bin/ 保留 .tar.gz + 安装包）"

# 清理裸二进制 + .tar.gz 压缩包（保留安装包，上传前执行）
.PHONY: clean-archives
clean-archives:
	@echo "[清理] 删除裸二进制 + .tar.gz 压缩包（保留安装包）..."
	$(call clean_binary,./$(BUILD_DIR)/$(BINARY_NAME)-linux-amd64)
	$(call clean_binary,./$(BUILD_DIR)/$(BINARY_NAME)-linux-arm64)
	$(call clean_binary,./$(BUILD_DIR)/$(BINARY_NAME)-$(BUILD_TAG)-linux-amd64)
	$(call clean_binary,./$(BUILD_DIR)/$(BINARY_NAME)-$(BUILD_TAG)-linux-arm64)
	$(call clean_archive,./$(BUILD_DIR)/$(BINARY_NAME)-linux-amd64)
	$(call clean_archive,./$(BUILD_DIR)/$(BINARY_NAME)-linux-arm64)
	$(call clean_archive,./$(BUILD_DIR)/$(BINARY_NAME)-$(BUILD_TAG)-linux-amd64)
	$(call clean_archive,./$(BUILD_DIR)/$(BINARY_NAME)-$(BUILD_TAG)-linux-arm64)
	@echo "✓ 清理完成（bin/ 保留安装包）"

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

# 打包目录（飞牛 fnOS 应用包源码目录）
FPKG_DIR = fpkg
# 构建 tag 名
BUILD_TAG = fpk

# 从 git log 自动生成 changelog 文本（取上一个 tag 到 HEAD 的提交摘要）
# 如果没有上一个 tag，则取最近 20 条提交
# 可通过环境变量 CHANGELOG 覆盖（CI 可注入 git log 或自定义文本）
CHANGELOG ?= $(shell \
	prev_tag=$$(git describe --tags --abbrev=0 2>/dev/null); \
	if [ -n "$$prev_tag" ]; then \
		git log --oneline --no-decorate "$$prev_tag"..HEAD 2>/dev/null; \
	else \
		git log --oneline --no-decorate -20 2>/dev/null; \
	fi | head -20 | sed 's/^[a-f0-9]* //' | tr '\n' ';' | sed 's/;$$/./' \
)

# 打包（单架构）：复制已构建好的带 tag 二进制到 fpkg/app/，再 fnpack build
# 带 tag 二进制由 build-linux-amd64/arm64 构建，此处只打包
# manifest 中的 __VERSION__ 和 __CHANGELOG__ 占位符在打包时替换为实际值
# 参数: $(1)=GOARCH
define build_fpk
	@echo ""
	@echo "┌────────────────────────────────────────────────────────────"
	@echo "│ [打包] 创建 fpk 包 ($(1))"
	@echo "└────────────────────────────────────────────────────────────"
	@mkdir -p $(FPKG_DIR)/app
	@cp $(BUILD_DIR)/$(BINARY_NAME)-$(BUILD_TAG)-linux-$(1) $(FPKG_DIR)/app/$(BINARY_NAME)
	@cp $(FPKG_DIR)/manifest $(FPKG_DIR)/manifest.bak
	@sed 's|__VERSION__|$(VERSION)|g' $(FPKG_DIR)/manifest.bak > $(FPKG_DIR)/manifest.tmp
	@sed "s|__CHANGELOG__|$(CHANGELOG)|g" $(FPKG_DIR)/manifest.tmp > $(FPKG_DIR)/manifest
	@rm -f $(FPKG_DIR)/manifest.tmp
	@cd $(FPKG_DIR) && fnpack build
	@mv $(FPKG_DIR)/$(BINARY_NAME).fpk $(BUILD_DIR)/$(BINARY_NAME)-$(1).fpk
	@rm -f $(FPKG_DIR)/app/$(BINARY_NAME)
	@mv $(FPKG_DIR)/manifest.bak $(FPKG_DIR)/manifest
	@echo "✓ fpk 包已创建: $(BUILD_DIR)/$(BINARY_NAME)-$(1).fpk"
endef

# deb/rpm/apk/fpk 都复用 build-linux-all 的二进制（不带标签 + 带标签）
package-linux-deb package-linux-rpm package-linux-apk package-linux-fpk: build-linux-all

# 打包 Linux deb（全部 2 架构）
.PHONY: package-linux-deb
package-linux-deb:
	@echo ""
	@echo "┌────────────────────────────────────────────────────────────"
	@echo "│ [打包] 创建 Linux deb 包 (amd64/arm64)..."
	@echo "└────────────────────────────────────────────────────────────"
	$(call package_nfpm_formats,amd64,amd64,amd64,x86_64)
	$(call package_nfpm_formats,arm64,arm64,arm64,aarch64)

# 打包 Linux rpm（全部 2 架构）
.PHONY: package-linux-rpm
package-linux-rpm:
	@echo ""
	@echo "┌────────────────────────────────────────────────────────────"
	@echo "│ [打包] 创建 Linux rpm 包 (x86_64/aarch64)..."
	@echo "└────────────────────────────────────────────────────────────"
	$(call package_nfpm_formats,amd64,amd64,amd64,x86_64)
	$(call package_nfpm_formats,arm64,arm64,arm64,aarch64)

# 打包 Linux apk（Alpine，全部 2 架构）
.PHONY: package-linux-apk
package-linux-apk:
	@echo ""
	@echo "┌────────────────────────────────────────────────────────────"
	@echo "│ [打包] 创建 Linux apk 包 (x86_64/aarch64)..."
	@echo "└────────────────────────────────────────────────────────────"
	$(call package_nfpm_formats,amd64,amd64,amd64,x86_64)
	$(call package_nfpm_formats,arm64,arm64,arm64,aarch64)

# 打包 Linux fpk（FnOS，全部 2 架构）
.PHONY: package-linux-fpk
package-linux-fpk:
	@echo ""
	@echo "┌────────────────────────────────────────────────────────────"
	@echo "│ [打包] 创建 Linux fpk 包 (amd64/arm64)..."
	@echo "└────────────────────────────────────────────────────────────"
	$(call build_fpk,amd64)
	$(call build_fpk,arm64)

# 打包所有 Linux deb/rpm/apk 包（构建一次，各格式复用）
.PHONY: package-linux-all
package-linux-all: package-linux-deb package-linux-rpm package-linux-apk package-linux-fpk
	@echo ""
	@echo "╔════════════════════════════════════════════════════════════"
	@echo "║ ✓ 所有包已创建 (deb/rpm/apk/fpk, 多架构)"
	@echo "╚════════════════════════════════════════════════════════════"

# 打包所有 Linux 包（deb/rpm/apk）
.PHONY: package-linux
package-linux: package-linux-all

# 构建并打包所有 Linux 包
.PHONY: package-all
package-all: package-linux

# ==================== 更新 / 拉取 ====================

sync: ## 拉取最新并以 fast-forward 合并（保留本地未提交改动）
	git fetch origin
	git merge --ff-only origin/master
	@echo "已同步 origin/master 最新代码，本地未提交改动已保留"

pull: sync ## 拉取所有远程最新

# ==================== 推送 ====================

push: ## 推送到所有远程仓库
	git push origin HEAD
	@echo "推送完成！"

push-force: ## 强制推送到所有远程仓库（忽略冲突）
	git push --force origin HEAD
	@echo "强制推送完成！"