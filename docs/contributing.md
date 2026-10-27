# 贡献指南

提交信息怎么写、PR 怎么过、三套 CI 各跑什么、加一个配置键或 API 要动哪几处、发版怎么走。

> 相关：[development.md](development.md)（环境、Make 目标、测试）、[deployment.md](deployment.md)（安装与升级）、[codecov.md](codecov.md)、[coveralls.md](coveralls.md)、[coverage-go.md](coverage-go.md)、[coverage-node.md](coverage-node.md)、[gitlab-ci.md](gitlab-ci.md)、[ci-image.md](ci-image.md)。

## 提交规范

格式：`type(scope): 中文描述`。scope 可省略，描述用中文、动词开头、说清「改了什么」而不是「改了哪里」。

对仓库全部 67 条非 merge 提交做主类型统计（`git log --no-merges`，按 `type(scope)` 归一）：

| type       | 条数 | 用途                                                                                                        |
|------------|------|-------------------------------------------------------------------------------------------------------------|
| `build`    | 33   | 依赖升级、CI 镜像/工作流、打包配置、构建脚本                                                                |
| `feat`     | 12   | 新功能（`fpkg`、`updater`、`synclog`、`playback`、`db`、`webui`、`debug`、`日志`、`build` 及 3 条无 scope） |
| `fix`      | 7    | 缺陷修复（`db` 3 条、`deps` 1 条、无 scope 3 条）                                                           |
| `chore`    | 6    | 与产物逻辑无关的杂务（全部带 `ci` scope）                                                                   |
| `docs`     | 3    | 文档（`ci` 1 条、无 scope 2 条）                                                                            |
| `refactor` | 2    | 不改行为的重构（`webui` 1 条、无 scope 1 条）                                                               |
| `test`     | 1    | 补测试                                                                                                      |
| `ci`       | 1    | 单独用 `ci` 作 type（`ci: 添加 golangci-lint 与前端 ESLint 检查`）                                          |

另有 2 条早于规范的提交（`init`、`更新许可证信息`），不必模仿。

历史上实际用过的 scope（按出现次数）：`deps-dev`(15)、`ci`(15)、`deps`(7)、`db`(4)、`webui`(2)，以及各 1 次的 `fpkg`、`updater`、`frontend`、`security`、`cnb`、`build`、`playback`、`synclog`、`debug`、`日志`。Dependabot 自动提交会带 `bump X from A to B in /cmd/frontend` 这类英文描述与 `build(deps-dev)` scope，不需要人工改。

真实提交示例（`git log` 原文，可直接照这个语气写）：

```text
build(deps): 升级前端依赖并同步 pnpm 至 12.16.0
chore(ci): Vitest JUnit 的 classname 归一化为仓库相对路径以修复 Codecov Tests 跨 run 关联
feat(updater): 下载接入中转并失败自动回退源站
fix(db): 修正 users 表迁移的列引用错位并加固 PRAGMA 查询
docs(ci): 新增 CI 镜像说明文档，梳理双平台镜像差异与升级流程
```

几条从历史里读出来的习惯：

- 一条提交只做一件事。CI 拆分、镜像统一、覆盖率接入各自独立，回滚时不用连着拆。
- 描述里写清**为什么**。例如「以修复 Codecov Tests 跨 run 关联」「避免徽章互相覆盖」——这类后半句是仓库的常态，评审时很受用。
- 不写 `update`、`fix bug`、`优化` 这种空话；动词要具体到「拆分 / 迁移 / 回退 / 归一化 / 预装」。
- 破坏性变更在描述里点明影响面（如「CI 镜像仅构建 amd64」），不靠 `!` 标记。

## 分支与 PR 流程

仓库有 5 个远端，主分支统一叫 `main`：`origin`（CNB，主开发）、`github`、`gitee`、`gitcode`、`gitlab`。`Makefile` 提供了 `sync`（`fetch` + `merge --ff-only`）与 `push`（依次推五个远端）。

| 场景       | 做法                                                                                                      |
|------------|-----------------------------------------------------------------------------------------------------------|
| 日常开发   | 从 `main` 切分支：`git switch -c feat/lb-playlist-range`                                                  |
| 分支命名   | `feat/`、`fix/`、`docs/`、`chore/` + 短横线英文；Dependabot 用 `dependabot/<ecosystem>/<pkg>`             |
| 同步上游   | `git fetch origin && git merge --ff-only origin/main`（等价 `make sync-cnb`）                             |
| 提 PR      | 目标分支 `main`；标题直接用最终 squash 后的提交信息（`type(scope): 中文描述`）                            |
| 关联 issue | 描述里写 `Closes #12` / `Refs #12`，CNB 的 Issue 会同步进知识库（见 `.cnb/workflows/knowledge-base.yml`） |
| 合并方式   | squash 合并，保持 `main` 上一条提交一件事                                                                 |

PR 描述建议包含四段（照这个写，评审就能直接看）：

1. **背景**：为什么要改，对应哪个现象/issue。
2. **改动**：按文件或模块列 3-5 条，点出关键设计取舍。
3. **验证**：本地跑了什么（`make format`、`make check`、`go test ./...`、真机验证了哪一步）。
4. **影响面**：是否动到 socket 接管、配置键、数据库 schema、CI 镜像、发布产物。

合并前 checklist：

```text
[ ] make format 已跑，工作区干净（markdownfmt 也要装）
[ ] make check 通过（含 golangci-lint / tsc / oxlint / prettier / 打包链路）
[ ] 改过 schema.sql 或 queries.sql：make sqlc 后生成文件已一起提交
[ ] 改了 config 结构：config.example.yaml、README、docs/configuration.md 同步
[ ] 改了 webui API：cmd/devserver 的副本、api.ts、types.ts 同步
[ ] 新增/修改的 Markdown 已过 markdownfmt
[ ] 真机验证过受影响链路（socket 接管 / scrobble / 歌单 / 升级）
```

## CI 门禁

同一个仓库挂了三套流水线，职责不同，不能互相替代：

| 平台      | 文件                                                                        | 触发                            | 跑什么                                                                                                                                                                       |
|-----------|-----------------------------------------------------------------------------|---------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| CNB（主） | `.cnb/workflows/push.yml`                                                   | push                            | `make deps` → `make format` → `make check`（镜像 `.cnb/ci/Dockerfile`）                                                                                                      |
| CNB       | `.cnb/workflows/push-coverage.yml`                                          | push                            | Go/Node 单元测试 + `testing:coverage` 全量上报                                                                                                                               |
| CNB       | `.cnb/workflows/pull-coverage.yml`                                          | pull_request                    | 同上，PR 事件额外算增量覆盖率                                                                                                                                                |
| CNB       | `.cnb/workflows/release.yml`                                                | `web_trigger_build_go_project`  | 打包 deb/fpk → `git:release` → 上传附件                                                                                                                                      |
| CNB       | `.cnb/workflows/code-review.yml`                                            | pull_request                    | AI 代码评审（`cnbcool/code-review`），critical 不阻断（`fail_on_critical: false`）                                                                                           |
| CNB       | `.cnb/workflows/sca-sbom.yml` / `sca-license.yml` / `sca-vulnerability.yml` | pull_request / push             | PR：增量扫描 + `failOnSeverity: high` 门禁；push：全量扫描（`scanMode: full`、`publish: true`、无门禁），把 `sca.*.full.*` 元数据写到默认分支最新 commit，供 README 徽章读取 |
| CNB       | `.cnb/workflows/cnb-sechook.yml`                                            | pull_request                    | 敏感信息与代码扫描统计                                                                                                                                                       |
| CNB       | `.cnb/workflows/codewiki.yml`                                               | `tag_push`                      | 生成 Code Wiki 并入库                                                                                                                                                        |
| CNB       | `.cnb/workflows/knowledge-base.yml`                                         | push / issue 事件               | 把 `docs/**/**.md` 与 `README*.md`、`CHANGELOG.md` 等建进知识库                                                                                                              |
| GitHub    | `.github/workflows/push.yml`                                                | push / PR                       | 与 CNB push 同四步，跑在 GHCR 的 `ci:latest` 镜像里                                                                                                                          |
| GitHub    | `.github/workflows/coverage.yml`                                            | push / PR                       | 覆盖率 + Bundle Analysis + 测试结果上报 Codecov                                                                                                                              |
| GitHub    | `.github/workflows/coveralls.yml`                                           | push / PR                       | 覆盖率上报 Coveralls（独立 job，刻意与 Codecov 解耦）                                                                                                                        |
| GitHub    | `.github/workflows/release.yml`                                             | `workflow_dispatch`             | 输入版本号 → 打包 → 发 GitHub Release                                                                                                                                        |
| GitHub    | `.github/workflows/ci-image.yml`                                            | Dockerfile 变更 / 每周一 / 手动 | 构建并推送 CI 镜像，清理旧版本只留 5 个                                                                                                                                      |
| GitLab    | `.gitlab-ci.yml` + `.gitlab/workflows/*`                                    | 默认分支 push / 网页手动        | 只做镜像与发布（`package-release`），**不跑覆盖率**                                                                                                                          |

再加两个「转发壳」：`.gitcode/workflows/release.yml` 与 `.workflow/release.yml` 不构建，只把 GitHub Release 的产物转发到 GitCode / Gitee。

### 失败怎么定位

| 现象                                    | 看哪里                                                                                                 |
|-----------------------------------------|--------------------------------------------------------------------------------------------------------|
| CNB push 在「格式化」或「检查」阶段失败 | 该 step 的日志末尾；`make format` 会就地改文件，失败常常是「格式化后仍有 lint 错」                     |
| GitHub 的 `check` job 拉不到镜像        | 先跑一次 `.github/workflows/ci-image.yml`，镜像不存在时 `container.image` 直接失败                     |
| lint 只在 CI 报、本地不报               | 本地 `golangci-lint` 版本比镜像新或旧；镜像固定 `GOLANGCI_LINT_VERSION=v2.15.0`                        |
| 覆盖率 job 报 `no coverage data`        | Go 侧 `breakIfNoCoverage: false`（只缺失不阻断），Node 侧是 `true`（直接失败），优先查前端用例是否全挂 |
| 覆盖率徽章数字变了但测试没动            | CNB 的 `testing:coverage` 是硬门禁，Codecov/Coveralls 只是可视化；两边口径不同属正常                   |
| 发布产物少了 `.tar.gz`                  | release 步骤里 `make clean-archives` 会删掉压缩包，只留安装包                                          |
| GitLab 流水线一个 job 都没有            | `workflow.rules` 只允许默认分支 push 与网页手动触发；MR 上不会建流水线                                 |

### 覆盖率为什么分 Go / Node 两个 key

CNB 侧用 `testing:coverage` 的 `key: go` / `key: node` 分别写入 meta map，避免两份报告互相覆盖、徽章串台；Codecov 侧对应 `flags: go` / `flags: node`，Coveralls 侧用 `flag-name`。这三处的配置动机与历史坑都写在 `docs/coverage-go.md`、`docs/coverage-node.md`、`docs/codecov.md`、`docs/coveralls.md` 里，这里不重复。

只记一条口径差异：CNB 的 Go 覆盖率命令排除 `internal/db`、`internal/datadb`（sqlc 生成代码，约占 11% 语句），并带 `-vet=off`；Codecov 的忽略列表（`.github/codecov.yml`）另外排除了 `cmd/fnmusic-sync/main.go`、`**/mock_*.go` 与前端构建产物。改覆盖率口径要两边一起改，否则两边数字对不上。

## 代码与文档规范

| 对象           | 工具                                              | 命令                                                       | 硬性要求                                               |
|----------------|---------------------------------------------------|------------------------------------------------------------|--------------------------------------------------------|
| Go             | `gofmt`、`go fmt`、`go fix`、`golangci-lint`      | `make format-go`、`make lint-go`                           | `make check` 必须过                                    |
| 前端 TS/Svelte | `tsc --noEmit`、`oxlint`、`prettier`              | `pnpm --dir cmd/frontend run lint`、`make format-frontend` | `prettier --check` 必须过                              |
| Markdown       | `markdownfmt` v3                                  | `~/go/bin/markdownfmt -w <file>`                           | 全仓 `*.md` 都要过；`format-markdown` 缺工具会静默跳过 |
| YAML           | `yamlfmt`（`.yamlfmt`：2 空格 / 120 列 / 单引号） | `make format-yaml`                                         | 只管 `.cnb`、`.github`、`.gitlab` 等                   |
| Shell          | `shfmt -i 2 -ci -bn -s -ln bash`                  | `make format-shell`                                        | **只覆盖 `fpkg/cmd/*`**，`scripts/*.sh` 不在范围内     |
| SQL            | `sql-formatter --fix -l sql`                      | `make format-sql`                                          | 只覆盖 `internal/{db,datadb}/*.sql`                    |
| JSON           | `gojq .`                                          | `make format-json`                                         | 只覆盖 `fpkg` 下几个 JSON                              |
| Dockerfile     | `dockerfmt`                                       | `make format-docker`                                       | 三个 CI 目录                                           |

提交前的正确顺序（顺序错了会来回打脸）：

```bash
make deps            # 前端依赖不在位时 lint-frontend 直接拦
make sqlc            # 改过 SQL 时；放在 format 之前，让 sql-formatter 先规整
make format          # 就地改写：Go / 前端 / YAML / JSON / MD / shell / SQL / Docker
make check           # lint + lint-fix + 前端检查 + 完整打包链路
git status           # 确认 format 的改动都进本次提交
```

两点说明：

- `make check` 内部会跑 `lint-go-fix`（`golangci-lint run --fix`），也就是说它可能**改代码**，而且依赖 `package-linux`（需要 `nfpm` + `fnpack`）。本地缺这两样时，`make check` 必失败——这与代码质量无关。
- Markdown 的格式一致性由 `markdownfmt` 决定，它会把表格对齐、列表符号、标题空行统一。手写的对齐一旦提交，CI 会把它改回去。装工具：`go install github.com/Kunde21/markdownfmt/v3/cmd/markdownfmt@latest`。

## 新增功能 checklist

### 加一个配置键

| 必改位置                                    | 改什么                                                                                            |
|---------------------------------------------|---------------------------------------------------------------------------------------------------|
| `internal/config/config.go`                 | 结构体字段（`mapstructure` + `json` tag）；`setDefaults` 加默认值                                 |
| `configs/config.example.yaml`               | 加示例与注释（这是用户最先抄的文件）                                                              |
| `README.md`                                 | 「配置」段的示例 YAML 与说明表                                                                    |
| `docs/configuration.md`                     | 完整说明（另一个工程师维护，改键要同步）                                                          |
| `cmd/fnmusic-sync/main.go` 的 `applyConfig` | 若需要运行时生效：加进 apply 逻辑（否则只在启动时读一次）                                         |
| `internal/webui/handler.go` 的对应 handler  | 若要在 Web UI 可改：`handleSettings` / `handleUser` / `handleLibrary` 里的结构体与 `config.Save*` |

热加载不是自动的：`applyConfig` 只处理它显式调用的那些键（provider 重建、阈值、日志级别、歌单同步配置）。新键如果不在其中，改完仍要重启。

### 加一个 Web UI API

| 必改位置                                         | 说明                                                                            |
|--------------------------------------------------|---------------------------------------------------------------------------------|
| `internal/webui/handler.go`                      | 注册路由（`/app/fnmusic-sync/api/...`）+ handler；管理员接口套 `s.requireAdmin` |
| `cmd/devserver/main.go`                          | **必须同步一份**——它导不进 `fpk` tag 的 webui 包，是独立副本                    |
| `cmd/frontend/src/lib/api.ts`                    | 加 `apiCall<T>('/xxx')`                                                         |
| `cmd/frontend/src/lib/types.ts`                  | 请求/响应的 TS 类型                                                             |
| `cmd/frontend/src/pages/*.svelte` 或 `stores.ts` | 页面调用与状态                                                                  |
| `docs/webui-api.md`                              | 接口文档（另一个工程师维护）                                                    |

路由必须挂在 `gatewayPrefix` 下，离开这个前缀飞牛网关不会转发。

### 加一个推送平台（provider）

| 必改位置                                      | 说明                                                                                      |
|-----------------------------------------------|-------------------------------------------------------------------------------------------|
| `internal/scrobbler/scrobbler.go`             | 实现 `Scrobbler` 接口：`Name()`、`UpdateNowPlaying()`、`Scrobble()`                       |
| `internal/<platform>/`                        | 平台客户端（参照 `internal/lastfm`、`internal/listenbrainz` 的分包方式）                  |
| `cmd/fnmusic-sync/setup.go`                   | `buildUserProviders`（注册 provider）与 `providerStatusOf`（判断用户是否开通）            |
| `internal/config/config.go`                   | 用户凭证结构 + 默认值                                                                     |
| `internal/reqlog`                             | 若需要独立请求日志，加一个 `reqlog.New(..., "<platform>.log", ...)` 并在 `main.go` 里创建 |
| `configs/config.example.yaml` / README / docs | 配置说明                                                                                  |

### 改数据库 schema

流程与注意事项见 [development.md](development.md) 的「数据层与代码生成」。记住三条：`queries.sql` 不能写注释、`make sqlc` 必须跑、生成文件（含 `db.go` 头部的 sqlc 版本号）一起提交。

### 加 CLI 子命令

| 必改位置                    | 说明                                                                                          |
|-----------------------------|-----------------------------------------------------------------------------------------------|
| `cmd/fnmusic-sync/*.go`     | 新建文件或挂到 `serviceCmd`；用 `rootCmd.AddCommand` 注册                                     |
| build tag                   | 依赖 systemd / 包管理器的命令要加 `//go:build !fpk`（`self-upgrade`、`service` 都是这么做的） |
| `README.md` / `docs/cli.md` | 命令表与参数说明                                                                              |

## 发布流程概览

1. 合并到 `main`，确认 CNB 的 push 与覆盖率流水线都绿。
2. 触发发布（三套任选其一，语义一致）：
   - CNB：自定义按钮触发 `.cnb/workflows/release.yml`（`web_trigger_build_go_project`），填 `version` 与 `pre_release`；
   - GitHub：`workflow_dispatch` 跑 `.github/workflows/release.yml`，输入 `version` 与 `prerelease`；
   - GitLab：`CI/CD → Pipelines → Run pipeline`，填 `VERSION`（留空只维护镜像）、`PRE_RELEASE`。
3. 流水线做的事：版本号归一化（自动补 `v` 前缀）→ `make deps` → `VERSION=<tag> REPO_SOURCE=<平台> make package-linux-all` → `make clean-archives` → 生成 Release 正文 → 建 Release 并挂附件。

产物清单（`bin/` 下）：

| 文件                                      | 用途                                                        |
|-------------------------------------------|-------------------------------------------------------------|
| `fnmusic-sync_amd64.deb`                  | Debian/Ubuntu 与普通 Linux 安装                             |
| `fnmusic-sync_arm64.deb`                  | ARM64 同类                                                  |
| `fnmusic-sync-amd64.fpk`                  | 飞牛应用中心（amd64）                                       |
| `fnmusic-sync-arm64.fpk`                  | 飞牛应用中心（arm64）                                       |
| `fnmusic-sync-linux-{amd64,arm64}.tar.gz` | 裸跑/自升级下载用（`clean-archives` 后会从 Release 里消失） |

版本号来源：`Makefile` 的 `VERSION = git describe --tags --always --dirty`（剥掉 `refs/tags/` 前缀），`PKG_VERSION` 去 `v` 给包元数据用，`VERSION` 带 `v` 给二进制与下载链接。全部经 `-ldflags -X` 注入 `internal/buildinfo`，所以 `fnmusic-sync version` 显示的就是真版本；本地裸 `go build` 则显示 `dev`。

一个需要知道的现状：**流水线不生成校验文件**。`SHA256SUMS` 只出现在 Release 正文的过滤列表（`[ "$fname" = "SHA256SUMS" ] && continue`）与 `internal/updater` 的「不走下载中转」名单里，仓库里没有任何生成它的步骤；`scripts/install.sh` 也不校验下载物的哈希。要加校验，得同时补「发布时产出 SHA256SUMS」与「安装/升级时比对」两处，只补一处没有任何作用。

安装、升级、回滚的用户视角操作见 [deployment.md](deployment.md)。

## 行为准则与安全披露

仓库里**没有** `CODE_OF_CONDUCT.md`、`SECURITY.md`、`CONTRIBUTING.md` 模板或 `.github/ISSUE_TEMPLATE/`（`.github/` 下只有 `codecov.yml`、`dependabot.yml`、`workflows/`、`ci/`）。所以按仓库现状给一份最小约定：

**协作行为**

- 对代码不对人。评审意见指向具体行、具体现象、具体替代方案。
- 不提交他人隐私：抓包日志、`state.db`、含 token 的 `config.yaml` 一律不进仓库（`.gitignore` 已挡 `coverage.out`、`/logs`、`/configs/config.yaml` 等，但档不住手滑）。
- 不为了过 CI 而删测试、关 lint 规则或放宽覆盖率红线；要调整门禁，在 PR 里说明依据。

**安全披露**

- 本项目会持有 Last.fm `api_secret`/`session_key`、ListenBrainz `token`，以及飞牛客户端的 token（仅内存 + 脱敏前 8 位入库）。发现可能导致凭证泄漏的问题（日志打印明文、请求日志未打码、配置回写覆盖他人文件、升级流程可被替换包劫持等），**不要开公开 issue**。
- 私有渠道：GitHub 的 Security → Report a vulnerability；或邮件联系包元数据里的维护者地址（`.nfpm.yaml` 的 `maintainer`）。
- 披露内容请附：版本（`fnmusic-sync version`）、运行模式（deb/fpk）、复现步骤、影响面判断。
- 收到后会在修复并发布新版本后再公开细节；修复期请勿在公开渠道贴 PoC。

**许可**

仓库为 MIT（`LICENSE`，Copyright (c) 2026 liguangchun）。提交即表示同意以同一许可分发。
