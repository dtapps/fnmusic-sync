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
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILDTIME ?= $(shell date -u '+%Y-%m-%d %H:%M:%S UTC')

# 构建来源（标识当前二进制由哪个平台构建发布）
# CNB 构建时注入 REPO_SOURCE=cnb，GitHub 构建时注入 REPO_SOURCE=github
# 升级时根据此值选择从对应平台下载
REPO_SOURCE ?= cnb

# CNB 访问令牌（访问 CNB API release 列表时需要 Bearer 鉴权）
# CNB 构建时通过环境变量 CNB_TOKEN 传入，为空时升级接口提示无法自动升级
CNB_TOKEN ?=

# GitHub 访问令牌（GitHub API 无 Token 也可用，但有速率限制，可以为空）
# GitHub 构建时通过环境变量 GITHUB_TOKEN 传入
GITHUB_TOKEN ?=

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
	-X '$(PKG_PATH).GithubToken=$(GITHUB_TOKEN)'

# 统一定义构建命令
# CGO_ENABLED=0: 禁用 CGO，实现完全静态链接
# CGO_ENABLED=1: 启用 CGO，实现动态链接
# -trimpath: 移除本地路径，保护隐私
# -ldflags: 注入版本信息并移除符号表
# 注意：-tags 由 build_binary 的 $(6) 参数动态拼接，此处不含
BUILD_CMD = CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)"

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

# 安装依赖
.PHONY: deps
deps: 
	@echo "[Deps] 安装依赖..."
	go mod tidy
	go mod download
	@echo "[Deps] 安装前端依赖..."
	pnpm --dir $(FRONTEND_DIR) install --frozen-lockfile --prefer-offline
	@echo "[Deps] 完成。"

# 更新依赖
.PHONY: update-deps
update-deps:
	@echo "[Deps] 更新依赖..."
	go version
	go get -u ./...
	go mod tidy
	
	@echo "[Deps] 更新前端依赖..."
	pnpm --version
	pnpm --dir $(FRONTEND_DIR) self-update
# 	pnpm --dir $(FRONTEND_DIR) update
	pnpm --dir $(FRONTEND_DIR) update --latest
	@echo "[Deps] 完成。"

# 生成 sqlc 持久化代码（需要 sqlc：go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest）
# 在 internal/db 目录执行，读取 sqlc.yaml 生成 db.go / models.go / querier.go / queries.sql.go。
# 注意：queries.sql 不能有注释（sqlc v1.31 多查询文件有解析 bug），表结构注释放在 schema.sql。
.PHONY: sqlc
sqlc:
	@echo "[SQLC] 生成 internal/db 持久化代码..."
	cd $(CURDIR)/internal/db && sqlc generate
	@echo "[SQLC] 完成。"

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
	@cd $(FRONTEND_DIR) && pnpm dev

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

# ==================== 工具 ====================

# 格式化（Go + 前端 + 配置 + 文档 + 脚本 + SQL + Docker）
.PHONY: format
format: format-go format-frontend format-yaml format-json format-markdown format-shell format-sql format-docker

# 格式化 Go 代码
.PHONY: format-go
format-go:
	@echo "[Format] 格式化后端文件 (GO)..."
	gofmt -w -s .
	go fmt ./...
	go fix ./...

# 格式化前端文件（HTML/CSS/JS/TS/Svelte）
.PHONY: format-frontend
format-frontend:
	@echo "[Format] 格式化前端文件 (HTML/CSS/JS/TS/Svelte)..."
	@cd $(FRONTEND_DIR) && pnpm run format \
		|| echo "⚠️  prettier 格式化失败，请确认已运行 pnpm install"
	@echo "[Format] 前端格式化完成。"

# 格式化 JSON 文件
.PHONY: format-json
format-json:
	@echo "[Format] 格式化 JSON 文件..."
	@npx --yes prettier@latest \
		--write \
		--tab-width 2 \
		--trailing-comma all \
		--print-width 120 \
		"cmd/frontend/src/lib/i18n/*.json" \
		"cmd/frontend/tsconfig.json" \
		|| echo "⚠️  prettier 格式化失败，请确认 npx 可用"
	@echo "[Format] JSON 格式化完成。"

# 格式化 YAML 文件（.yaml/.yml）
.PHONY: format-yaml
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
		".github/**/*.{yaml,yml}" \
		".nfpm.yaml" \
		".golangci.yml" \
		"internal/db/**/*.{yaml,yml}" \
		|| echo "⚠️  prettier 格式化失败，请确认 npx 可用"
	@echo "[Format] YAML 格式化完成。"

# 格式化 Markdown 文件（.md）
.PHONY: format-markdown
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
.PHONY: format-shell
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

# 格式化 SQL 文件（internal/db/*.sql via prettier + prettier-plugin-sql）
.PHONY: format-sql
format-sql:
	@echo "[Format] 格式化 SQL 文件 (prettier + prettier-plugin-sql)..."
	@test -d $(FRONTEND_DIR)/node_modules/prettier-plugin-sql || { echo "⚠️  prettier-plugin-sql 未安装，请先运行: make deps"; exit 1; }
	@cd $(FRONTEND_DIR) && npx prettier --plugin=prettier-plugin-sql --write \
		--tab-width 2 --print-width 120 "$(CURDIR)/internal/db/*.sql" \
		|| echo "⚠️  SQL 格式化失败，请确认 npx 可用"
	@echo "[Format] SQL 格式化完成。"

# 格式化 Dockerfile
# 使用 dockerfmt，需安装：go install github.com/reteps/dockerfmt@latest
.PHONY: format-docker
format-docker:
	@echo "[Format] 格式化 Dockerfile..."
	@command -v dockerfmt >/dev/null 2>&1 && { \
		find .cnb -type f -name "Dockerfile*" -exec dockerfmt -w {} + 2>/dev/null || true; \
		echo "[Format] Dockerfile 格式化完成（dockerfmt）。"; \
	} || { \
		echo "⚠️  dockerfmt 未安装，跳过 Dockerfile 格式化。"; \
		echo "   提示: 可通过运行 'go install github.com/reteps/dockerfmt@latest' 安装"; \
	}

# 检查（Go + HTML/CSS/JS/TS/Svelte + Linux 打包：含 fpk 与无 fpk 两种）
.PHONY: check
check: lint-go lint-go-fix lint-frontend package-linux

# Go 代码检查（有 issue 即停止）
.PHONY: lint-go
lint-go:
	@echo "[Lint] Go 代码检查 (go vet + golangci-lint)..."
	go vet ./...
	golangci-lint run ./...

# Go 代码检查（自动修复）
.PHONY: lint-go-fix
lint-go-fix:
	@echo "[Lint] Go 代码检查（自动修复）..."
	golangci-lint run --fix ./...

# 前端代码检查（类型 + ESLint + 格式只读校验，错误即停止）
.PHONY: lint-frontend
lint-frontend:
	@echo "[Lint] 前端代码检查 (svelte-check + eslint + prettier)..."
	@test -d $(FRONTEND_DIR)/node_modules || { echo "⚠️  前端依赖未安装，请先运行: make deps"; exit 1; }
	@cd $(FRONTEND_DIR) && pnpm run lint
	@cd $(FRONTEND_DIR) && pnpm run format:check
	@echo "✓ 前端代码检查通过。"

# ==================== 构建 ====================

# 构建前端（Svelte + Tailwind → internal/webui/www/）
# 打包时同步 version 到 package.json，与 git tag 保持一致
.PHONY: build-frontend
build-frontend:
	@echo ""
	@echo "┌────────────────────────────────────────────────────────────"
	@echo "│ [构建] Svelte 前端"
	@echo "└────────────────────────────────────────────────────────────"
	@cd $(FRONTEND_DIR) && sed -i.bak -E 's#("version"[[:space:]]*:[[:space:]]*")[^"]*(")#\1$(PKG_VERSION)\2#' package.json && rm -f package.json.bak
	@cd $(FRONTEND_DIR) && pnpm build
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
.PHONY: build-linux-all
build-linux-all: build-frontend build-linux-amd64 build-linux-arm64

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
	@sed -e 's|$${ARCH}|$(1)|g' -e 's|$${VERSION}|$(PKG_VERSION)|g' \
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

# ==================== 更新 / 拉取 ====================

# 拉取 CNB 最新并以 fast-forward 合并（保留本地未提交改动）
.PHONY: sync
sync:
	git fetch origin
	git merge --ff-only origin/main
	@echo "已同步 origin/main 最新代码，本地未提交改动已保留"

# 拉取 GitHub 最新并以 fast-forward 合并（保留本地未提交改动）
.PHONY: sync-github
sync-github:
	git fetch github
	git merge --ff-only github/main
	@echo "已同步 github/main 最新代码，本地未提交改动已保留"

# ==================== 推送 ====================

# 推送到所有远程仓库
.PHONY: push
push:
	git push origin HEAD
	git push github HEAD
	@echo "推送完成！"

# 强制推送到所有远程仓库（忽略冲突）
.PHONY: push-force
push-force:
	git push --force origin HEAD
	git push --force github HEAD
	@echo "强制推送完成！"