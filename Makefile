# 设置默认目标
.DEFAULT_GOAL := setup

# 环境变量优先级高于 .env：include 的赋值会覆盖环境值，先暂存一份
ifeq ($(origin GIT_REMOTE),environment)
    GIT_REMOTE_ENV := $(GIT_REMOTE)
endif

# 加载 .env 文件（如果存在）
ifneq (,$(wildcard ./.env))
    include .env
    export
endif

ifneq ($(GIT_REMOTE_ENV),)
    GIT_REMOTE := $(GIT_REMOTE_ENV)
endif

# 同步/推送目标远程，逗号分隔：GIT_REMOTE=origin,github,gitlab
# 可通过 .env / 环境变量 / 命令行覆盖，未配置时默认 origin。
GIT_REMOTE ?= origin
export GIT_REMOTE

# 名称
BINARY := fnmusic-sync

# 包名
PKG    := cnb.cool/dtapp/fnmusic-sync

# 二进制文件名
BINARY_NAME=fnmusic-sync

# 前端项目目录
FRONTEND_DIR = cmd/frontend

# 后端项目目录
BACKEND_DIR = cmd/fnmusic-sync

# 飞牛项目目录
FPKG_DIR = fpkg

# 编译输出目录
BUILD_DIR=bin

# 版本信息（支持外部传入）
# 注意：若本地存在形如 refs/tags/vX.Y.Z 的异常标签，git describe 会返回带路径前缀的
# 版本号（如 refs/tags/v0.0.32-...），此处统一剥离该前缀，避免污染包版本号与 sed 替换。
VERSION ?= $(shell v=$$(git describe --tags --always --dirty 2>/dev/null | sed 's|^refs/tags/||'); [ -n "$$v" ] && echo "$$v" || echo "dev")
# 包版本号：去掉 git tag 的 v 前缀。
# Debian / fnOS 包元数据（manifest version、deb version、npm package.json version）
# 不使用 v 前缀；而二进制内部版本与下载链接仍使用带 v 的 VERSION（与 git tag 保持一致）。
PKG_VERSION = $(patsubst v%,%,$(VERSION))

# deb 专用版本号：把 semver 的预发布后缀换成 Debian 的 ~ 写法。
# dpkg 的版本是 upstream-revision，同 upstream 时「有 revision 的那份更大」——
# 也就是 0.1.32-rc.1 > 0.1.32，用户装了 rc 之后正式版反而成了降级（apt 会拒绝）。
# 换成 0.1.32~rc.1（tilde 排序低于空串）才对。fnOS 的 fpk 与前端 package.json
# 仍用原样 semver，只有 deb 元数据需要这一步。
DEB_VERSION = $(subst -,~,$(PKG_VERSION))
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILDTIME ?= $(shell date -u '+%Y-%m-%d %H:%M:%S UTC')

# 构建来源（标识当前二进制由哪个平台构建发布）
# CNB 构建时注入 REPO_SOURCE=cnb，GitHub 构建时注入 REPO_SOURCE=github，GitLab 构建时注入 REPO_SOURCE=gitlab
# 升级时根据此值选择从对应平台下载
REPO_SOURCE ?= cnb

# CNB 访问令牌（访问 CNB API release 列表时需要 Bearer 鉴权）
# CNB 构建时通过环境变量 CNB_TOKEN 传入，为空时升级接口提示无法自动升级
CNB_TOKEN ?=

# GitHub 访问令牌（GitHub API 无 Token 也可用，但有速率限制，可以为空）
# GitHub 构建时通过环境变量 GITHUB_TOKEN 传入
GITHUB_TOKEN ?=

# GitLab 访问令牌（read_api 权限）。公开项目可匿名读 releases API，因此可以为空；
# 私有项目不配就会在升级时提示「项目不可读」。GitLab 构建时通过环境变量 GITLAB_TOKEN 传入。
GITLAB_TOKEN ?=

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
	-X '$(PKG_PATH).BinaryName=$(BINARY_NAME)' \
	-X '$(PKG_PATH).RepoSource=$(REPO_SOURCE)' \
	-X '$(PKG_PATH).CnbToken=$(CNB_TOKEN)' \
	-X '$(PKG_PATH).GithubToken=$(GITHUB_TOKEN)' \
	-X '$(PKG_PATH).GitlabToken=$(GITLAB_TOKEN)'

# 统一定义构建命令
# CGO_ENABLED=0: 禁用 CGO，实现完全静态链接
# CGO_ENABLED=1: 启用 CGO，实现动态链接
# -trimpath: 移除本地路径，保护隐私
# -buildvcs=false: 不向二进制 stamp git 版本信息——CI 容器内 workspace 属宿主 uid，
#   git 报 dubious ownership（exit 128）导致构建失败；且版本信息已全部由
#   -ldflags 注入 buildinfo 包，buildvcs 的 vcs.* 元数据无人读取，砍掉零损失。
# -ldflags: 注入版本信息并移除符号表
# 注意：-tags 由 build_binary 的 $(6) 参数动态拼接，此处不含
BUILD_CMD = CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags="$(LDFLAGS)"

# 构建 tag 名
BUILD_TAG = fpk

# 功能开关（设置为 1 开启，0 关闭）
ENABLE_UPX ?= 1
ENABLE_ARCHIVE ?= 1

# 检测是否存在 upx 压缩工具
UPX = $(shell command -v upx 2> /dev/null)
# 检测是否存在 tar 和 zip
TAR = $(shell command -v tar 2> /dev/null)
ZIP = $(shell command -v zip 2> /dev/null)

# ==================== 开发 ====================

tool-deps: ## 工具依赖
	@echo "==> 安装必要的工具依赖..."

	sqlc version || true
	go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest
	-sqlc version
	@echo "==> sqlc 工具安装或更新完成"

	golangci-lint version || true
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
	-golangci-lint version
	@echo "==> golangci-lint 工具安装或更新完成"

	govulncheck version || true
	go install golang.org/x/vuln/cmd/govulncheck@latest
	-govulncheck version
	@echo "==> govulncheck 工具安装或更新完成"

	gojq --version || true
	go install github.com/itchyny/gojq/cmd/gojq@latest
	-gojq --version
	@echo "==> gojq 工具安装或更新完成"

	yamlfmt -version || true
	go install github.com/google/yamlfmt/cmd/yamlfmt@latest
	-yamlfmt -version
	@echo "==> yamlfmt 工具安装或更新完成"

	shfmt -version || true
	go install mvdan.cc/sh/v3/cmd/shfmt@latest
	-shfmt -version
	@echo "==> shfmt 工具安装或更新完成"

	sql-formatter -version || true
	go install github.com/wasilibs/go-sql-formatter/v15/cmd/sql-formatter@latest
	-sql-formatter -version
	@echo "==> sql-formatter 工具安装或更新完成"

	dockerfmt version || true
	go install github.com/reteps/dockerfmt@latest
	-dockerfmt version
	@echo "==> dockerfmt 工具安装或更新完成"

# 一键设置项目（默认执行）
setup: deps
	@echo "🎉 Project setup completed!"

# 安装全部依赖
.PHONY: deps
deps: deps-go deps-node
	@echo "[Deps] 完成。"

# 安装全部Go依赖
.PHONY: deps-go
deps-go:
	@echo "[Deps] 安装 Go 模块依赖..."
	go mod tidy
	go mod download

# 安装全部前端依赖
# --frozen-lockfile (冻结锁文件)
# --prefer-offline (优先使用离线缓存)
.PHONY: deps-node
deps-node:
	@echo "[Deps] 安装前端依赖..."
	pnpm --dir $(FRONTEND_DIR) install --frozen-lockfile --prefer-offline

# 更新全部依赖
# --latest (无视版本限制，强制升最新)
.PHONY: update-deps
update-deps: update-deps-go update-deps-node
	@echo "[Deps] 完成。"

# 更新全部Go依赖
.PHONY: update-deps-go
update-deps-go:
	@echo "[Deps] 更新 Go 模块依赖..."
	go get -u ./...
	go mod tidy

# 更新全部前端依赖
# --latest (无视版本范围，强制升最新，并改写 package.json 的版本范围)
.PHONY: update-deps-node
update-deps-node:
	@echo "[Deps] 更新前端依赖..."
	pnpm --dir $(FRONTEND_DIR) update --latest
	pnpm --dir $(FRONTEND_DIR) prune

# 更新 pnpm 自身的版本锁定（package.json 的 packageManager 字段）
.PHONY: update-deps-pnpm
update-deps-pnpm:
	@echo "[Deps] 更新 pnpm 版本锁定..."
	@latest="$$(npm view pnpm version)"; \
	echo "[Deps] packageManager → pnpm@$${latest}"; \
	pnpm --dir $(FRONTEND_DIR) pkg set packageManager=pnpm@$${latest}

# 删除未使用的依赖：knip --fix 把它们从 package.json 摘掉，
.PHONY: deps-clean
deps-clean:
	@echo "[Deps] 删除未使用的依赖..."
	pnpm --dir $(FRONTEND_DIR) run deps:clean
	pnpm --dir $(FRONTEND_DIR) install --lockfile-only
	pnpm --dir $(FRONTEND_DIR) prune

# 生成 sqlc 持久化代码（需要 sqlc：go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest）
# internal/db 生成运营状态表（state.db），internal/datadb 生成数据表（data.db，MBID 映射）。
# 注意：queries.sql 不能有注释（sqlc v1.31 多查询文件有解析 bug），表结构注释放在 schema.sql。
.PHONY: sqlc
sqlc:
	@echo "[SQLC] 生成 internal/db 持久化代码 (state.db)..."
	cd $(CURDIR)/internal/db && sqlc generate
	@echo "[SQLC] 生成 internal/datadb 持久化代码 (data.db)..."
	cd $(CURDIR)/internal/datadb && sqlc generate
	@echo "[SQLC] 完成。"

#####################
## 开发 相关
#####################

# 启动本地开发后端（模拟飞牛网关 + API，监听 :8080）
.PHONY: dev-server
dev-server:
	@echo "[Dev] 启动本地开发后端 (Go API :8080)..."
	@go run ./cmd/devserver

# 开发模式启动前端 dev server
.PHONY: dev-frontend
dev-frontend:
	@echo "[Frontend] 启动 Vite dev server..."
	@echo "  API proxy: /app/fnmusic-sync/api -> http://localhost:8080"
	@echo "  请确保 Go 后端在 8080 端口运行"
	pnpm --dir $(FRONTEND_DIR) dev

# 同时启动前端和后端开发服务器（需要两个终端，或用 tmux）
.PHONY: dev
dev:
	@echo "╔══════════════════════════════════════════════════════════════"
	@echo "║ 🚀 本地开发模式启动说明"
	@echo "╠══════════════════════════════════════════════════════════════"
	@echo "║ 需要两个终端分别运行："
	@echo "║   终端 1: make dev-server   (Go 后端 API :8080)"
	@echo "║   终端 2: make dev-frontend (Vite 前端 :5173)"
	@echo "║"
	@echo "║ 访问 http://localhost:5173 即可热更新开发"
	@echo "║ 后端 API 也可直接访问 http://localhost:8080/app/fnmusic-sync/"
	@echo "╚══════════════════════════════════════════════════════════════"

#####################
## 构建 相关
#####################

# ==================== 版本备份 / 还原 ====================

# build-frontend 会用 sed 把 PKG_VERSION 写进 $(FRONTEND_PKG)（与 git tag 保持一致），
# 而且 sed -i.bak 之后顺手把 .bak 删了，于是构建完工作区就残留 package.json 的版本改动。
# 会在外围构建的目标（build-linux-all → package-linux-deb/fpk → package-linux → check）
# 统一用这两个目标夹一下：进构建前备份，出构建后还原。也可单独手动调用。
# 备份放 bin/（已 gitignore），不污染源码树；restore 成功后才删备份，中途失败可手动补还原。
FRONTEND_PKG     = $(FRONTEND_DIR)/package.json
FRONTEND_PKG_BAK = $(BUILD_DIR)/package.json.ver.bak

# 备份 package.json（已存在备份则沿用，不会用被改过的版本覆盖原始版本）
.PHONY: backup-version
backup-version:
	@mkdir -p $(BUILD_DIR)
	@if [ -f $(FRONTEND_PKG_BAK) ]; then \
		echo "[版本] 备份已存在（上次未还原），沿用：$(FRONTEND_PKG_BAK)"; \
	else \
		cp $(FRONTEND_PKG) $(FRONTEND_PKG_BAK); \
		echo "[版本] 已备份 $(FRONTEND_PKG) → $(FRONTEND_PKG_BAK)"; \
	fi

# 还原 package.json，并删除备份
.PHONY: restore-version
restore-version:
	@if [ -f $(FRONTEND_PKG_BAK) ]; then \
		cp $(FRONTEND_PKG_BAK) $(FRONTEND_PKG); \
		rm -f $(FRONTEND_PKG_BAK); \
		echo "[版本] 已还原 $(FRONTEND_PKG)"; \
	else \
		echo "[版本] 无备份，跳过还原"; \
	fi

# 构建前端（Svelte + Tailwind → internal/webui/www/）
# 打包时同步 version 到 package.json，与 git tag 保持一致
.PHONY: build-frontend
build-frontend:
	@echo ""
	@echo "┌────────────────────────────────────────────────────────────"
	@echo "│ [构建] Svelte 前端"
	@echo "└────────────────────────────────────────────────────────────"
	@cd $(FRONTEND_DIR) && sed -i.bak -E 's#("version"[[:space:]]*:[[:space:]]*")[^"]*(")#\1$(PKG_VERSION)\2#' package.json && rm -f package.json.bak
	pnpm --dir $(FRONTEND_DIR) build
	@echo "✓ 构建完成。"

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

# 清理构建目录
.PHONY: clean
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
	@cd $(BACKEND_DIR) && $(5) GOOS=$(1) GOARCH=$(2) $(BUILD_CMD) -o $(CURDIR)/$(BUILD_DIR)/$(BINARY_NAME)-$(1)-$(2)$(4) .
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
	@cd $(BACKEND_DIR) && GOOS=$(1) GOARCH=$(2) $(BUILD_CMD) -tags "$(4)" -o $(CURDIR)/$(BUILD_DIR)/$(BINARY_NAME)-$(4)-$(1)-$(2) .
	$(call compress_file,./$(BUILD_DIR)/$(BINARY_NAME)-$(4)-$(1)-$(2))
	$(call archive_binary,./$(BUILD_DIR)/$(BINARY_NAME)-$(4)-$(1)-$(2))
endef

# 构建 Linux Amd64 平台（不带标签 + 带标签）
.PHONY: build-linux-amd64
build-linux-amd64:
	$(call build_binary,linux,amd64,Linux x86_64,,,)
	$(call build_tag_binary,linux,amd64,Linux x86_64 带标签,$(BUILD_TAG))

# 构建 Linux Arm64 平台（不带标签 + 带标签）
.PHONY: build-linux-arm64
build-linux-arm64:
	$(call build_binary,linux,arm64,Linux ARM64,,,)
	$(call build_tag_binary,linux,arm64,Linux ARM64 带标签,$(BUILD_TAG))

# 构建 Linux 平台（不带标签 + 带标签）
# 两端夹 backup-version / restore-version：build-frontend 会改 cmd/frontend/package.json，
# 构建完直接还原，使 package-linux-deb/fpk、package-linux、check 之后工作区保持干净。
.PHONY: build-linux-all
build-linux-all: backup-version build-frontend build-linux-amd64 build-linux-arm64 restore-version

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

# ==================== 打包 ====================

# 生成 nfpm 临时配置文件（替换变量）
# 参数: $(1)=架构名 (amd64)
define generate_nfpm_config
	@sed -e 's|$${ARCH}|$(1)|g' -e 's|$${VERSION}|$(DEB_VERSION)|g' \
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

# 打包单个架构的 deb 包
# 参数: $(1)=GOARCH, $(2)=deb 架构名 (amd64/arm64), $(3)=deb 文件名后缀
define package_nfpm_formats
	$(call prepare_scripts)
	$(call generate_nfpm_config,$(2))
	nfpm package -p deb -f .nfpm-$(2).yaml -t $(BUILD_DIR)/$(BINARY_NAME)_$(3).deb
	@rm -f .nfpm-$(2).yaml
	$(call cleanup_scripts)
	@echo "✓ deb 包已创建: $(BUILD_DIR)/$(BINARY_NAME)_$(3).deb"
endef

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
	@sed 's|__VERSION__|$(PKG_VERSION)|g' $(FPKG_DIR)/manifest.bak > $(FPKG_DIR)/manifest.tmp
	@sed "s|__CHANGELOG__|$(CHANGELOG)|g" $(FPKG_DIR)/manifest.tmp > $(FPKG_DIR)/manifest
	@rm -f $(FPKG_DIR)/manifest.tmp
	@cd $(FPKG_DIR) && fnpack build
	@mv $(FPKG_DIR)/$(BINARY_NAME).fpk $(BUILD_DIR)/$(BINARY_NAME)-$(1).fpk
	@rm -f $(FPKG_DIR)/app/$(BINARY_NAME)
	@mv $(FPKG_DIR)/manifest.bak $(FPKG_DIR)/manifest
	@echo "✓ fpk 包已创建: $(BUILD_DIR)/$(BINARY_NAME)-$(1).fpk"
endef

# deb/fpk 都复用 build-linux-all 的二进制（不带标签 + 带标签）
package-linux-deb package-linux-fpk: build-linux-all

# 打包 Linux deb（全部 2 架构）
.PHONY: package-linux-deb
package-linux-deb:
	@echo ""
	@echo "┌────────────────────────────────────────────────────────────"
	@echo "│ [打包] 创建 Linux deb 包 (amd64/arm64)..."
	@echo "└────────────────────────────────────────────────────────────"
	$(call package_nfpm_formats,amd64,amd64,amd64)
	$(call package_nfpm_formats,arm64,arm64,arm64)

# 打包 Linux fpk（FnOS，全部 2 架构）
.PHONY: package-linux-fpk
package-linux-fpk:
	@echo ""
	@echo "┌────────────────────────────────────────────────────────────"
	@echo "│ [打包] 创建 Linux fpk 包 (amd64/arm64)..."
	@echo "└────────────────────────────────────────────────────────────"
	$(call build_fpk,amd64)
	$(call build_fpk,arm64)

# 打包所有 Linux deb/fpk 包（构建一次，各格式复用）
.PHONY: package-linux-all
package-linux-all: package-linux-deb package-linux-fpk
	@echo ""
	@echo "╔════════════════════════════════════════════════════════════"
	@echo "║ ✓ 所有包已创建 (deb/fpk, 多架构)"
	@echo "╚════════════════════════════════════════════════════════════"

# 打包所有 Linux 包（deb/fpk）
.PHONY: package-linux
package-linux: package-linux-all

#####################
## 格式化 相关
#####################

# 格式化后端文件
.PHONY: format-go
format-go:
	@echo "[Format] 格式化后端文件 (GO)..."
	gofmt -w -s .
	go fmt ./...
	go fix ./...

# 格式前端文件
.PHONY: format-node
format-node:
	@echo "[Format] 格式化前端文件 (PNPM)..."
	pnpm --dir $(FRONTEND_DIR) format

# 格式化 YAML 文件
.PHONY: format-yaml
format-yaml:
	@bash scripts/format.sh yaml

# 格式化 JSON 文件
.PHONY: format-json
format-json:
	@bash scripts/format.sh json

# 格式化 SQL 文件
.PHONY: format-sql
format-sql:
	@bash scripts/format.sh sql

# 格式化 Markdown 文件
.PHONY: format-markdown
format-markdown:
	@bash scripts/format.sh markdown

# 格式化 Dockerfile 文件
.PHONY: format-docker
format-docker:
	@bash scripts/format.sh docker

# 格式化 Shell 脚本
.PHONY: format-shell
format-shell:
	@bash scripts/format.sh shell

# 格式化
.PHONY: format
format: format-node format-go format-yaml format-json format-sql format-markdown format-docker format-shell

#####################
## 检查 / 测试 相关
#####################

# 统一定义构建命令
# CGO_ENABLED=0: 禁用 CGO，实现完全静态链接
# CGO_ENABLED=1: 启用 CGO，实现动态链接
LINT_ENV = CGO_ENABLED=0

# 定义 Lint 过程中需要激活的 Go Build Tags
LINT_TAGS = goolm,stdjson

# Lint 扫描的目标平台，默认当前平台；CI 通过 LINT_PLATFORMS=... 覆盖为全平台
LINT_PLATFORMS ?= $(shell go env GOOS)/$(shell go env GOARCH)

# 检查后端代码
.PHONY: check-go
check-go:
	@echo "[Check] 运行 go vet 静态检查..."
	go vet ./...

	@echo "[Check] 运行 golangci-lint 扫描 (平台: $(LINT_PLATFORMS), Tags: $(LINT_TAGS))..."
	@for os_arch in $(LINT_PLATFORMS); do \
		os=$$(echo $$os_arch | cut -d'/' -f1); \
		arch=$$(echo $$os_arch | cut -d'/' -f2); \
		echo "  -> $$os 平台 ($$arch)"; \
		$(LINT_ENV) GOOS=$$os GOARCH=$$arch golangci-lint fmt; \
		$(LINT_ENV) GOOS=$$os GOARCH=$$arch golangci-lint run --build-tags $(LINT_TAGS); \
	done

	@echo "[Check] 自动修复代码风格问题..."
	$(LINT_ENV) golangci-lint run --fix --build-tags $(LINT_TAGS)

	@echo "[Check] 运行 Go 单元测试..."
	go test $(GO_COVER_TEST_FLAGS) $(GO_COVER_SCAN)

	@echo "[Check] 运行 Go 赖漏洞检查..."
	govulncheck -show verbose ./...

	@echo "[Check] 完成。"


# 检查前端代码
.PHONY: check-node
check-node:
	@echo "[Check] 检查前端依赖与质量..."
	@echo "  -> 同步依赖 (确保环境一致)..."
	pnpm --dir $(FRONTEND_DIR) install --frozen-lockfile --prefer-offline

	@echo "  -> 检查安全漏洞 (仅高危阻断)..."
	pnpm --dir $(FRONTEND_DIR) audit --production --audit-level high --registry=https://registry.npmjs.org

	@echo "  -> 检查过时依赖..."
	pnpm --dir $(FRONTEND_DIR) outdated

	@echo "  -> 检查代码风格 (Lint)..."
	pnpm --dir $(FRONTEND_DIR) run lint

	@echo "  -> 检测未使用的依赖 (knip)..."
	pnpm --dir $(FRONTEND_DIR) run lint:unused

	@echo "  -> 运行前端单元测试..."
	pnpm --dir $(FRONTEND_DIR) exec vitest run

	@echo "[Check] 前端检查完成。"

# 检查代码
.PHONY: check
check: check-node check-go

#####################
## 更新 / 拉取 / 推送 相关
#####################

# 按 GIT_REMOTE 顺序先全部拉取合并，再统一推送（保留本地未提交改动）
.PHONY: sync
sync:
	@IFS=,; for r in $$GIT_REMOTE; do \
		git fetch "$$r" && git merge --ff-only "$$r/main" || exit 1; \
	done
	@IFS=,; for r in $$GIT_REMOTE; do git push "$$r" HEAD || exit 1; done
	@echo "同步完成！"

# 推送到所有远程仓库
.PHONY: push
push:
	@IFS=,; for r in $$GIT_REMOTE; do git push "$$r" HEAD || exit 1; done
	@echo "推送完成！"

# 强制推送到所有远程仓库（忽略冲突）
.PHONY: push-force
push-force:
	@IFS=,; for r in $$GIT_REMOTE; do git push --force "$$r" HEAD || exit 1; done
	@echo "强制推送完成！"

#####################
## 测试覆盖率 相关
#####################

# ── 清单配置：新增目录 / 生成代码只改这里，下面的命令不散落路径 ──────────────
# Go 扫描范围：用 ./...（不是 ./internal/...）——cmd/fnmusic-sync 下也有测试文件
# （goroutine_diag_test.go），只扫 internal/ 会漏掉这部分分母。
# internal/webui 带 //go:build fpk 标签，默认构建不参与测试，无需额外排除。
GO_COVER_SCAN = ./...
# Go 排除目录（包路径前缀，空格分隔）：sqlc 生成代码占约 11% 语句
# （实测全量 13088 → 排除后 11672），计入会稀释指标。
# 注意：本项目没有 internal/ent（那是别的项目的结构），照抄 ent 排除项在这里是空转。
GO_COVER_EXCLUDE = /internal/db /internal/datadb
# -vet=off：i18n.T() 这类函数作 printf 参数会触发 vet 的 non-constant format 报错。
GO_COVER_TEST_FLAGS = -vet=off

# 前端目录与产物（相对仓库根）
NODE_DIR = $(FRONTEND_DIR)
NODE_COVER_DIR = $(NODE_DIR)/coverage
NODE_COVER_LCOV = $(NODE_COVER_DIR)/lcov.info
NODE_JUNIT_XML = $(NODE_COVER_DIR)/junit.xml
# 前端扫描 / 排除清单（glob 相对 vitest root=cmd/frontend，由 cmd/frontend/vitest.config.ts 读取）：
#   NODE_TEST_SCAN      测试用例
#   NODE_COVER_SCAN     计入覆盖率的源码
#   NODE_COVER_EXCLUDE  不计入的源码（测试自身、入口文件、类型声明）
# 多个 glob 用空格或逗号分隔；留空则回退到配置文件里的默认值。
NODE_TEST_SCAN = src/test/**/*.test.ts
NODE_COVER_SCAN = src/**/*.ts
NODE_COVER_EXCLUDE = src/test/** src/app.ts src/**/*.d.ts
NODE_VITEST_ENV = VITEST_TEST_INCLUDE="$(NODE_TEST_SCAN)" VITEST_COVER_INCLUDE="$(NODE_COVER_SCAN)" VITEST_COVER_EXCLUDE="$(NODE_COVER_EXCLUDE)"

# -coverpkg 只收「扫描范围内且不在排除列表」的包。用 $$(...) 让 go list 在配方执行时才跑，
# 避免任何 make 目标都触发一次包扫描；grep 参数由排除列表生成，加目录不用改命令。
GO_COVER_EXCLUDE_ARGS = $(foreach d,$(GO_COVER_EXCLUDE),-e '$(d)')
GO_COVER_PKGS = $$(go list $(GO_COVER_SCAN) | grep -v $(GO_COVER_EXCLUDE_ARGS) | paste -sd, -)
GO_COVER_ARGS = $(GO_COVER_TEST_FLAGS) -coverpkg=$(GO_COVER_PKGS) -coverprofile=coverage.out $(GO_COVER_SCAN)

# Go 覆盖率（golang 格式）→ coverage.out；CNB 的 testing:coverage 与 Codecov 都读这份
.PHONY: cover-go
cover-go:
	@echo "[Coverage] Go 测试覆盖率 → coverage.out"
	go test $(GO_COVER_ARGS)
	go tool cover -func=coverage.out

# Go 覆盖率 + JUnit（Codecov Test Analytics 用），依赖 CI 镜像预装的 gotestsum
.PHONY: junit-go
junit-go:
	@echo "[Coverage] Go 测试 → coverage.out + go-test-report.xml"
	gotestsum --junitfile go-test-report.xml -- $(GO_COVER_ARGS)
	go tool cover -func=coverage.out

# Coveralls 的 golang parser 会把包前缀压成 basename，改用 Cobertura 保留完整 filename 路径。
# gocover-cobertura 由统一 CI 镜像预装；-strict 让「包/模块信息缺失被静默跳过」直接失败。
.PHONY: cover-go-cobertura
cover-go-cobertura: cover-go
	@echo "[Coverage] coverage.out → coverage.xml（Cobertura）"
	gocover-cobertura -by-files -strict < coverage.out > coverage.xml

# 前端覆盖率（Vitest v8）→ cmd/frontend/coverage/lcov.info
.PHONY: cover-node
cover-node:
	@echo "[Coverage] 前端覆盖率 → $(NODE_COVER_LCOV)"
	cd $(NODE_DIR) && $(NODE_VITEST_ENV) pnpm exec vitest run --coverage

# 前端 JUnit（Codecov Test Analytics 用）；outputFile 用绝对路径避开 vitest root 的相对路径歧义
.PHONY: junit-node
junit-node:
	@echo "[Coverage] 前端 JUnit → $(NODE_JUNIT_XML)"
	@mkdir -p $(NODE_COVER_DIR)
	cd $(NODE_DIR) && $(NODE_VITEST_ENV) pnpm exec vitest run --reporter=junit --outputFile=$(CURDIR)/$(NODE_JUNIT_XML)