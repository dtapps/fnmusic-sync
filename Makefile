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

# 二进制文件名
BINARY_NAME = fnmusic-sync

# 前端项目目录
FRONTEND_DIR = cmd/frontend

# 后端项目目录
BACKEND_DIR = cmd/fnmusic-sync

# 飞牛项目目录
FPKG_DIR = fpkg

# 备份目录
BACKUP_DIR = bin

# 版本信息（支持外部传入）
# 注意：若本地存在形如 refs/tags/vX.Y.Z 的异常标签，git describe 会返回带路径前缀的
# 版本号（如 refs/tags/v0.0.32-...），此处统一剥离该前缀，避免污染包版本号与 sed 替换。
VERSION ?= $(shell v=$$(git describe --tags --always --dirty 2>/dev/null | sed 's|^refs/tags/||'); [ -n "$$v" ] && echo "$$v" || echo "dev")
# 包版本号：去掉 git tag 的 v 前缀。
# Debian / fnOS 包元数据（manifest version、deb version、npm package.json version）
# 不使用 v 前缀；而二进制内部版本与下载链接仍使用带 v 的 VERSION（与 git tag 保持一致）。
PKG_VERSION = $(patsubst v%,%,$(VERSION))

# deb 的 ~ 预发布排序由 nfpm（goreleaser 内置）自动处理，见 .goreleaser.yaml 的 nfpms 段。

# 构建来源（标识当前二进制由哪个平台构建发布）
# CNB 构建时注入 REPO_SOURCE=cnb，GitHub 构建时注入 REPO_SOURCE=github；自建其它通道可自行传值
# 升级时根据此值选择从对应平台下载
REPO_SOURCE ?= cnb

# CNB 访问令牌（访问 CNB API release 列表时需要 Bearer 鉴权）
# CNB 构建时通过环境变量 CNB_TOKEN 传入，为空时升级接口提示无法自动升级
CNB_TOKEN ?=

# GitHub 访问令牌（GitHub API 无 Token 也可用，但有速率限制，可以为空）
# GitHub 构建时通过环境变量 GITHUB_TOKEN 传入
GITHUB_TOKEN ?=

#####################
## 开发 相关
#####################

# 一键设置项目（默认执行）
setup: deps
	@echo "🎉 Project setup completed!"

# 安装全部依赖
.PHONY: deps
deps: deps-go deps-node
	@echo "[Deps] 完成。"

# 安装全部 Go 依赖
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

# 更新全部 Go 依赖
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
## 构建 / 打包 相关
#####################

# goreleaser 版本（所有流水线与本地共用，升级时只改这里）
GORELEASER_VERSION ?= v2.20.2

# goreleaser 可执行文件（不在 PATH 时回退到 $HOME/go/bin）
GORELEASER ?= $(shell command -v goreleaser 2>/dev/null || echo $(HOME)/go/bin/goreleaser)

# goreleaser 构建时注入 buildinfo 的环境变量
GORELEASER_ENV = PKG_VERSION='$(PKG_VERSION)' REPO_SOURCE='$(REPO_SOURCE)' \
	CNB_TOKEN='$(CNB_TOKEN)' GITHUB_TOKEN='$(GITHUB_TOKEN)'

# 同时编译/链接的目标数。默认按 CPU 数并发，CI 容器内存有限时容易在链接阶段被
# OOM Killer 杀掉（`link: signal: killed`）。本地默认 1，CI 环境（CI=true）默认 2，
# 两者都可显式覆盖：make goreleaser-snapshot GORELEASER_PARALLELISM=4
GORELEASER_PARALLELISM ?= $(if $(filter true,$(CI)),2,1)

# 非 CI（本地）快照只编当前平台：`--single-target` 是 `goreleaser build` 的参数，
# `goreleaser release` 没有（传了会直接报 unknown flag），所以本地走 build，只产出
# 当前平台的三个二进制，不生成压缩包 / Linux 系统包；CI 走 release 出全平台产物。
# 本地要全平台产物（含系统包）：CI=true make goreleaser-snapshot
GORELEASER_SNAPSHOT_CMD ?= $(if $(filter true,$(CI)),release --snapshot,build --snapshot --single-target)

# build-frontend 会用 sed 把 PKG_VERSION 写进 $(FRONTEND_PKG)（与 git tag 保持一致），
# 而且 sed -i.bak 之后顺手把 .bak 删了，于是构建完工作区就残留 package.json 的版本改动。
# goreleaser-prepare 用这两个目标夹住 build-frontend：进构建前备份，出构建后还原。
# 也可单独手动调用。
# 备份放 bin/（已 gitignore），不污染源码树；restore 成功后才删备份，中途失败可手动补还原。
FRONTEND_PKG     = $(FRONTEND_DIR)/package.json
FRONTEND_PKG_BAK = $(BACKUP_DIR)/package.json.ver.bak

# 备份 package.json（已存在备份则沿用，不会用被改过的版本覆盖原始版本）
.PHONY: backup-version
backup-version:
	@mkdir -p $(BACKUP_DIR)
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
	@echo "[构建] 打包前端"
	@cd $(FRONTEND_DIR) && sed -i.bak -E 's#("version"[[:space:]]*:[[:space:]]*")[^"]*(")#\1$(PKG_VERSION)\2#' package.json && rm -f package.json.bak
	pnpm --dir $(FRONTEND_DIR) build
	@echo "[构建] 打包完成。"

# 生成注入了 BINARY_NAME 的安装/卸载脚本（替换 __BINARY_NAME__ 占位符）
# 输出到 .nfpm-scripts/，由 nfpm 引用，打包结束后清理
define prepare_scripts
	@mkdir -p .nfpm-scripts
	@sed 's|__BINARY_NAME__|$(BINARY_NAME)|g' scripts/postinstall.sh > .nfpm-scripts/postinstall.sh
	@sed 's|__BINARY_NAME__|$(BINARY_NAME)|g' scripts/preremove.sh > .nfpm-scripts/preremove.sh
	@chmod +x .nfpm-scripts/postinstall.sh .nfpm-scripts/preremove.sh
endef

# 生成 deb 安装 / 卸载脚本（替换 __BINARY_NAME__ 占位符）
.PHONY: prepare-nfpm-scripts
prepare-nfpm-scripts:
	$(call prepare_scripts)

# 安装固定版本的 goreleaser（已存在则跳过）
.PHONY: goreleaser-install
goreleaser-install:
	@if ! $(GORELEASER) --version >/dev/null 2>&1; then \
		echo "安装 goreleaser $(GORELEASER_VERSION) → $(dir $(GORELEASER))"; \
		GOBIN=$(dir $(GORELEASER)) go install github.com/goreleaser/goreleaser/v2@$(GORELEASER_VERSION); \
	fi

# goreleaser 的前置步骤：前端产物 + deb 脚本。
# 由 .goreleaser.yaml 的 before hook 调用；也可单独执行。
# 用 backup-version / restore-version 夹住 build-frontend，避免 package.json 残留版本改动。
.PHONY: goreleaser-prepare
goreleaser-prepare:
	@$(MAKE) --no-print-directory backup-version
	@$(MAKE) --no-print-directory build-frontend
	@$(MAKE) --no-print-directory restore-version
	@$(MAKE) --no-print-directory prepare-nfpm-scripts

# 校验 .goreleaser.yaml
.PHONY: goreleaser-check
goreleaser-check: goreleaser-install
	$(GORELEASER) check

# 快照构建（无需 tag）：产物在 dist/。本地非 CI 只编当前平台（goreleaser build）。
.PHONY: goreleaser-snapshot
goreleaser-snapshot: goreleaser-install
	$(GORELEASER) $(GORELEASER_SNAPSHOT_CMD) --clean --parallelism $(GORELEASER_PARALLELISM)
	@PKG_VERSION='$(PKG_VERSION)' sh scripts/goreleaser/package-fpk.sh

# 发布构建：产物在 dist/。
.PHONY: goreleaser-release
goreleaser-release: goreleaser-install
	$(GORELEASER_ENV) $(GORELEASER) release --clean --skip=validate --parallelism $(GORELEASER_PARALLELISM)
	@PKG_VERSION='$(PKG_VERSION)' sh scripts/goreleaser/package-fpk.sh

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

# 格式化严格 JSON 文件（gojq）
.PHONY: format-json
format-json:
	@bash scripts/format.sh json

# 格式化 JSON5 文件（jsonfmt，保留注释 / 无引号 key / 尾逗号）
.PHONY: format-json5
format-json5:
	@bash scripts/format.sh json5

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

# 格式化/修复项目
.PHONY: format
format: format-node format-go format-yaml format-json format-json5 format-sql format-markdown format-docker format-shell

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

# 运行 Go 静态检查
# -printf=false 关闭 printf 分析，避免 i18n.T() 作日志 format 时触发错误
.PHONY: check-go-vet
check-go-vet:
	@echo "[Check] -> 运行 Go vet 静态检查 (Tags: $(LINT_TAGS))..."
	$(LINT_ENV) go vet -tags=$(LINT_TAGS) -printf=false ./...

# 运行 Go lint 扫描
.PHONY: check-go-lint
check-go-lint:
	@echo "[Check] -> 运行 Go lint 扫描 (平台: $(LINT_PLATFORMS), Tags: $(LINT_TAGS))..."
	@for os_arch in $(LINT_PLATFORMS); do \
		os=$$(echo $$os_arch | cut -d'/' -f1); \
		arch=$$(echo $$os_arch | cut -d'/' -f2); \
		echo "  -> $$os 平台 ($$arch)"; \
		$(LINT_ENV) GOOS=$$os GOARCH=$$arch golangci-lint fmt; \
		$(LINT_ENV) GOOS=$$os GOARCH=$$arch golangci-lint run --build-tags $(LINT_TAGS); \
	done

# 运行 Go fix 问题
.PHONY: check-go-fix
check-go-fix:
	@echo "[Check] -> 运行 Go fix 问题 (Tags: $(LINT_TAGS))..."
	$(LINT_ENV) golangci-lint run --fix --build-tags $(LINT_TAGS)

# 运行 Go 单元测试
.PHONY: check-go-test
check-go-test:
	@echo "[Check] -> 运行 Go 单元测试 (Tags: $(LINT_TAGS))..."
	$(LINT_ENV) go test -tags=$(LINT_TAGS) $(GO_COVER_TEST_FLAGS) $(GO_COVER_SCAN)

# 运行 Go 赖漏洞检查
.PHONY: check-go-vuln
check-go-vuln:
	@echo "[Check] -> 运行 Go 依赖漏洞检查 (Tags: $(LINT_TAGS))..."
	$(LINT_ENV) govulncheck -tags=$(LINT_TAGS) -scan=package -show verbose ./...

# 检查后端项目
.PHONY: check-go
check-go: check-go-vet check-go-lint check-go-fix check-go-test check-go-vuln 

# 「过时依赖」是否严格把关：
#   1 = 严格（本地默认）：pnpm outdated 发现过时依赖就让 make 失败
#   0 = 宽松（流水线默认）：只提示，不阻塞流水线
# 流水线（.github/workflows / .cnb/workflows）都会显式传 CI=true，此时默认走宽松；
# 需要在流水线里也严格时显式传参：make check STRICT_CHECKS=1
STRICT_CHECKS ?= $(if $(filter true,$(CI)),0,1)

# 同步前端依赖 (确保环境一致)
.PHONY: check-node-install
check-node-install:
	@echo "[Check] -> 同步前端依赖 (确保环境一致)..."
	pnpm --dir $(FRONTEND_DIR) install --frozen-lockfile --prefer-offline

# 检查前端安全漏洞 (仅高危阻断)
.PHONY: check-node-audit
check-node-audit:
	@echo "[Check] -> 检查前端安全漏洞 (仅高危阻断)..."
	pnpm --dir $(FRONTEND_DIR) audit --production --audit-level high --registry=https://registry.npmjs.org

# 检查前端过时依赖
.PHONY: check-node-outdated
check-node-outdated:
	# pnpm outdated 只要存在过时依赖就返回 1（连 pnpm 自己这个 devDependency 也算），
	# 「有新版可用」不该卡住流水线：流水线里降级为提示，本地仍按 STRICT_CHECKS 失败。
	@echo "[Check] -> 检查前端过时依赖..."
	@if [ "$(STRICT_CHECKS)" = "1" ]; then \
		pnpm --dir $(FRONTEND_DIR) outdated; \
	else \
		pnpm --dir $(FRONTEND_DIR) outdated || echo "  -> (宽松模式) 存在过时依赖，不阻塞流水线"; \
	fi

# 检测前端未使用的依赖 (knip)
.PHONY: check-node-knip
check-node-knip:
	@echo "[Check] -> 检测前端未使用的依赖 (knip)..."
	pnpm --dir $(FRONTEND_DIR) run lint:unused

# 运行前端单元测试
.PHONY: check-node-vitest
check-node-vitest:
	@echo "[Check] -> 运行前端单元测试..."
	pnpm --dir $(FRONTEND_DIR) exec vitest run

# 检查前端项目
.PHONY: check-node
check-node: check-node-install check-node-audit check-node-outdated check-node-lint check-node-knip check-node-vitest

# 检查/测试项目
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