# 贡献指南

提交信息怎么写、PR 怎么过、三套 CI 各跑什么、加一个配置键或 API 要动哪几处、发版怎么走。

> 相关：[development.md](development.md)（环境、Make 目标、测试）、[deployment.md](deployment.md)（安装与升级）、[codecov.md](codecov.md)、[coveralls.md](coveralls.md)、[coverage-go.md](coverage-go.md)、[coverage-node.md](coverage-node.md)、[ci-image.md](ci-image.md)。

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
[ ] make check 通过（含 golangci-lint / tsc / oxlint / prettier）
[ ] 改过 schema.sql 或 queries.sql：make sqlc 后生成文件已一起提交
[ ] 改了 config 结构：config.example.yaml、README、docs/configuration.md 同步
[ ] 改了 webui API：cmd/devserver 的副本、api.ts、types.ts 同步
[ ] 新增/修改的 Markdown 已过 markdownfmt
[ ] 真机验证过受影响链路（socket 接管 / scrobble / 歌单 / 升级）
```

## CI 门禁

CNB、GitHub Actions 两套 CI 负责构建与门禁，职责不同，不能互相替代（发布另有 Gitee 一条转发流水线，见表后说明）：

| 平台      | 文件                                                                                | 触发                                             | 跑什么                                                                                                                                                      |
|-----------|-------------------------------------------------------------------------------------|--------------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------|
| CNB（主） | `.cnb/workflows/push.yml`                                                           | push                                             | `make deps` → `make check`（`make format` 目前注释掉；镜像 `docker.cnb.cool/dtapp/golang/fnpack`）                                                          |
| CNB       | `.cnb/workflows/push-coverage.yml`                                                  | push                                             | Go/Node 单元测试 + `testing:coverage` 全量上报                                                                                                              |
| CNB       | `.cnb/workflows/pull-coverage.yml`                                                  | pull_request                                     | 同上，PR 事件额外算增量覆盖率                                                                                                                               |
| CNB       | `.cnb/workflows/release.yml`                                                        | `web_trigger_build_go_project`                   | 打包 deb/fpk → `git:release` → 上传附件                                                                                                                     |
| CNB       | `.cnb/workflows/code-review.yml`                                                    | pull_request                                     | AI 代码评审（`cnbcool/code-review`），critical 不阻断（`fail_on_critical: false`）                                                                          |
| CNB       | `.cnb/workflows/sca.yml`（漏洞 / License / SBOM 三段） / `code-security-config.yml` | pull_request / push                              | PR：增量扫描 + `failOnSeverity: high` 门禁；push：全量扫描（`scanMode: full`、无门禁），把 `sca.*.full.*` 元数据写到默认分支最新 commit，供 README 徽章读取 |
| CNB       | `.cnb/workflows/cnb-sechook.yml`                                                    | pull_request                                     | 敏感信息与代码扫描统计                                                                                                                                      |
| CNB       | `.cnb/workflows/codewiki.yml`                                                       | `tag_push`                                       | 生成 Code Wiki 并入库                                                                                                                                       |
| CNB       | `.cnb/workflows/knowledge-base.yml`                                                 | push / issue 事件                                | 把 `docs/**/**.md` 与 `README*.md`、`CHANGELOG.md` 等建进知识库                                                                                             |
| GitHub    | `.github/workflows/push.yml`                                                        | push / PR                                        | 与 CNB push 同一套检查，跑在 GHCR 的 `ci:latest` 镜像里                                                                                                     |
| GitHub    | `.github/workflows/codecov.yml`                                                     | push / PR                                        | 覆盖率 + Bundle Analysis + 测试结果上报 Codecov                                                                                                             |
| GitHub    | `.github/workflows/coveralls.yml`                                                   | push / PR                                        | 覆盖率上报 Coveralls（独立 job，刻意与 Codecov 解耦）                                                                                                       |
| GitHub    | `.github/workflows/release.yml`                                                     | `workflow_dispatch` + `schedule`（每天 nightly） | 输入版本号 → 打包 → 发 GitHub Release（nightly 自动推导版本号）                                                                                             |
| 共享镜像  | `dtapps/golang/fnpack`（CNB / GHCR 两处）                                           | 由镜像项目自己构建推送                           | 所有平台的 CI 环境来源；本仓库不再有 Dockerfile，见 [ci-image.md](ci-image.md)                                                                              |

另有一条**角色不同**的发布流水线：`.workflow/release.yml`（Gitee Go）是**转发壳**，只搬不建，从 CNB / GitHub 的 Release 取已编好的安装包，再发到 Gitee。

### 失败怎么定位

| 现象                                    | 看哪里                                                                                                                   |
|-----------------------------------------|--------------------------------------------------------------------------------------------------------------------------|
| CNB push 在「格式化」或「检查」阶段失败 | 该 step 的日志末尾；`make format` 会就地改文件，失败常常是「格式化后仍有 lint 错」                                       |
| GitHub 的 `check` job 拉不到镜像        | `container.image` 是 `ghcr.io/dtapps/golang/fnpack:latest`；拉不到就查该镜像是否还在、是否私有未授权（本仓库不构建镜像） |
| lint 只在 CI 报、本地不报               | 本地 `golangci-lint` 版本比镜像新或旧；镜像固定 `GOLANGCI_LINT_VERSION=v2.15.0`                                          |
| 覆盖率 job 报 `no coverage data`        | Go 侧 `breakIfNoCoverage: false`（只缺失不阻断），Node 侧是 `true`（直接失败），优先查前端用例是否全挂                   |
| 覆盖率徽章数字变了但测试没动            | CNB 的 `testing:coverage` 是硬门禁，Codecov/Coveralls 只是可视化；两边口径不同属正常                                     |
| 发布产物没有 `.tar.gz`                  | 流水线只上传 deb/fpk；tar.gz 留在 `dist/` 供本地/自升级使用                                                              |

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
| Dockerfile     | `dockerfmt`                                       | `make format-docker`                                       | `.cnb`、`.github`（当前仓库无 Dockerfile）             |

提交前的正确顺序（顺序错了会来回打脸）：

```bash
make deps            # 前端依赖不在位时 lint-frontend 直接拦
make sqlc            # 改过 SQL 时；放在 format 之前，让 sql-formatter 先规整
make format          # 就地改写：Go / 前端 / YAML / JSON / MD / shell / SQL / Docker
make check           # lint + lint-fix + 前端检查 + 测试
git status           # 确认 format 的改动都进本次提交
```

两点说明：

- `make check` 内部会跑 `lint-go-fix`（`golangci-lint run --fix`），也就是说它可能**改代码**；它只做 lint 与测试，不再触发打包。
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
   - CNB：自定义按钮触发 `.cnb/workflows/release.yml`（`web_trigger_build_go_project`），只填 `version`；也可以由 GitHub 发布后经 API 触发（`api_trigger_release`）；
   - GitHub：`workflow_dispatch` 跑 `.github/workflows/release.yml`，填 `version`；另有每天 UTC 00:00（北京时间 08:00）的定时任务，版本号留空自动走 nightly（见下）；
   - Gitee：转发壳（`.workflow/release.yml`），按 `DOWNLOAD_SOURCE` 从 CNB / GitHub 取已编好的包再发 Release，`VERSION` 可留空按源查最新。
   - **没有预发布开关**：版本号写成 `1.2.3-rc.1`（或 `-beta.1` / `-alpha.1` / `-nightly.20260101`）就是预发布，不带后缀就是正式版，见 [deployment.md](deployment.md) §升级。后缀同时决定 Release 正文里更新内容的对比渠道（同渠道对比）。
   - **构建指定提交**：GitHub 的 `workflow_dispatch` 有第二个输入 `commit`（留空 = 默认分支最新提交），填了就构建那个提交（SHA 或分支名）—— checkout 用 `ref: ${{ inputs.commit || github.sha }}`，三个 job（prepare / build / release）都拉同一个提交。因为本仓库各平台是**自己构建**而不是镜像 GitHub 的附件，GitHub 会把同一个提交传给 CNB（`commit`），保证「同一个版本号在各平台是同一份内容」。填的提交不在对方仓库里时，那边的流水线会明确报错而不是构建出另一份。
   - **tag 落在实际被构建的那个提交上**：GitHub 侧给 `softprops/action-gh-release` 传 `target_commitish`（不传的话它默认用 `github.sha`，即触发工作流时默认分支的 HEAD），CNB 的 `git:release` 落在当前工作区 HEAD。否则会出现「包是 A 构建的、tag 却指向 B」，用户按 tag 拉源码拿到的不是包里的代码。
   - **边界**：被检出的提交里带着它自己的 `scripts/`。所以目标提交必须与当前工作流兼容（例如函数式的 `resolve_version` 是 2026-11 这次改造才有的），构建更早的提交会明确报错（`resolve_version: command not found` / `版本号不合法`），而不是算出一个错的版本号。
3. 流水线做的事：版本号归一化 + 校验（自动补 `v` 前缀，规则在 `scripts/ci/resolve-prerelease.sh`）→ `make deps` → `VERSION=<tag> GORELEASER_CURRENT_TAG=<tag> REPO_SOURCE=<平台> make goreleaser-release` → 生成 Release 正文 → 建 Release 并挂附件（只传 deb/fpk 与 `SHA256SUMS`）。
4. **GitHub 是发版的总入口**：它发布成功后会自动把同版本号同步到 CNB / Gitee（两个 `sync-*` job），最后推一条通知（`notify` job，ServerChan3）。各平台各自的发布流水线仍可手动触发。

### 版本号规则（三个平台同一份实现）

实现见 `scripts/ci/resolve-prerelease.sh`（可 `bash scripts/ci/resolve-prerelease.sh --self-test` 跑自检），CNB / GitHub / Gitee 都 source 它：

| 版本号形态               | 判定   | 说明                                                                                                     |
|--------------------------|--------|----------------------------------------------------------------------------------------------------------|
| `1.2.3`                  | 正式版 | `PRERELEASE=false`                                                                                       |
| `1.2.3-rc.1`             | 预发布 | rc 渠道，changelog 对比上一个 rc                                                                         |
| `1.2.3-beta.1`           | 预发布 | beta 渠道，changelog 对比上一个 beta                                                                     |
| `1.2.3-alpha.1`          | 预发布 | alpha 渠道，changelog 对比上一个 alpha                                                                   |
| `1.2.3-nightly.20260101` | 预发布 | nightly 渠道，changelog 对比上一个 nightly                                                               |
| `nightly`                | 预发布 | 关键词：自动推导成 `<最近正式版+1 补丁>-nightly.<UTC 日期>`，最近正式版 v0.2.7 → v0.2.8-nightly.20261114 |
| `nightly.20261114`       | 预发布 | 关键词 + 指定 UTC 日期（手动补发某一天的 nightly）                                                       |

- `nightly` 的基准是仓库里最大的 `vX.Y.Z` tag，补丁号 +1 是因为 nightly 要走「下一个正式版」的版本号，否则 nightly 会抢走正式版的 tag 名。推导要完整 tag 历史，所以各平台都要 `git fetch --tags` / `fetch-depth: 0`。
- nightly 是预发布（`prerelease=true`），稳定用户不会被推上 nightly。
- 保留策略：nightly 只保留**最近 3 个** Release，多余的连同 git tag 一起删（GitHub / CNB 各自清理）。Gitee 的 `release@gitee` 插件只能覆盖同名 Release、没有删除能力，那边的 nightly 暂靠手工清理。

### GitHub 发版后的同步与通知

`.github/workflows/release.yml` 发布成功后依次做三件事（任一环节失败都只告警，不影响已发出的 Release）：

| job          | 做什么                                                               | 需要的 secret                                 |
|--------------|----------------------------------------------------------------------|-----------------------------------------------|
| `sync-cnb`   | 调 CNB Open API 拉起 `api_trigger_release`（CNB 自己构建并发布）     | `SYNC_CNB_TOKEN`（`repo-cnb-trigger:rw`）     |
| `sync-gitee` | POST Gitee Go 的 Webhook（`eventType=fnmusic-sync-release`）         | `SYNC_GITEE_WEBHOOK_URL` + `SYNC_GITEE_TOKEN` |
| `notify`     | ServerChan3（方糖）推送构建完成通知，含各平台 Release 链接与同步状态 | `SERVERCHAN_SENDKEY`                          |

- 这些 secret 全部可缺省：没配就整步跳过（通知里显示「⏭ 未配置（跳过）」），发版本身照常成功。
- 仓库标识的坑：**CNB 是 `dtapp`（不带 s），Gitee 是 `dtapps`（带 s）**，写错会 404。
- 与 certflow / kai 的差别：那两个项目各平台是「镜像 GitHub 的附件」，本仓库各平台**自己构建**（CNB / GitHub），好处是每个平台的包带自己的 `buildinfo.RepoSource`、客户端按各自通道查更新，代价是同一次发版要各平台各构建一次。
- Gitee 的下载源由 Gitee 侧的全局参数 `DOWNLOAD_SOURCE` 决定（`cnb` / `github`）：走 `cnb` 源时要等 CNB 那条流水线发完，否则拉不到包。

### 各平台的「更新内容」为什么曾经对不上

正文里的「更新内容」是 `scripts/ci/release-changelog.sh` 算的：它按渠道选**上一个同渠道 tag**作基准（nightly 对 nightly、正式版对正式版），然后跑 `git log <基准>..HEAD`。所以它依赖两样东西：**基准 tag 在不在**、**仓库历史全不全**。

曾经出现过「同一个版本号 v0.1.31-nightly.20261116，GitHub 侧 43 条、CNB 侧只有 1 条」，根因是第二样：

| 平台            | 工作区                        | `git log <上一个 tag>..HEAD` |
|-----------------|-------------------------------|------------------------------|
| GitHub          | `fetch-depth: 0` 完整历史     | 43 条（整个周期）            |
| CNB             | 平台检出 = **depth=1 浅克隆** | **只剩 HEAD 一条**           |
| Gitee（转发壳） | 平台检出（浅）                | 同 CNB                       |

浅克隆下 `git fetch --tags` **能拿到全部 tag**（所以基准选择是对的），但历史只有 HEAD 一个提交 —— `git log` 无法把不在本地的基准排除掉，于是只剩 HEAD 那一条，正文里的「更新内容」就退化成「只有最新那条提交」。GitHub 侧是完整历史，所以两边对不上。

修法（现在各平台都做了）：

- CNB / Gitee：生成正文前 `git fetch --unshallow`（失败则 `git fetch --depth 1000`），并打印「本地可见提交数」，不足 2 条时告警提示正文可能不完整。

还有一个**残留差异**要知道：各平台的 tag 是**各自仓库各自创建**的，不是彼此同步。若某个平台单独发过版本（没走同步），它的 tag 集合就与别的平台不同 → 基准不同 → 「更新内容」自然不同步。这是独立发版的固有结果，不是 bug；走同步发布（GitHub → CNB / Gitee）时 tag 名一致，正文也一致。

产物清单（`dist/` 下）：

| 文件                                      | 用途                                                |
|-------------------------------------------|-----------------------------------------------------|
| `fnmusic-sync_amd64.deb`                  | Debian/Ubuntu 与普通 Linux 安装                     |
| `fnmusic-sync_arm64.deb`                  | ARM64 同类                                          |
| `fnmusic-sync-amd64.fpk`                  | 飞牛应用中心（amd64）                               |
| `fnmusic-sync-arm64.fpk`                  | 飞牛应用中心（arm64）                               |
| `fnmusic-sync-linux-{amd64,arm64}.tar.gz` | 裸跑/自升级下载用（只留在 `dist/`，不上传 Release） |

版本号来源：CI 把版本号同时传给 `VERSION` 与 `GORELEASER_CURRENT_TAG`（tag 尚未创建也能构建），goreleaser 通过 `-ldflags -X` 注入 `internal/buildinfo`；`PKG_VERSION` 去 `v` 给 deb/fpk 元数据用。所以 `fnmusic-sync version` 显示的就是真版本；本地裸 `go build` 则显示 `dev`。

流水线会在 `dist/` 生成 `SHA256SUMS`（只覆盖 deb/fpk）并随 Release 上传；`scripts/install.sh` 目前不校验下载物的哈希。

安装、升级、回滚的用户视角操作见 [deployment.md](deployment.md)。

## 行为准则与安全披露

仓库里**没有** `CODE_OF_CONDUCT.md`、`SECURITY.md`、`CONTRIBUTING.md` 模板或 `.github/ISSUE_TEMPLATE/`（`.github/` 下只有 `codecov.yml`、`dependabot.yml`、`workflows/`、`ci/`）。所以按仓库现状给一份最小约定：

**协作行为**

- 对代码不对人。评审意见指向具体行、具体现象、具体替代方案。
- 不提交他人隐私：抓包日志、`state.db`、含 token 的 `config.yaml` 一律不进仓库（`.gitignore` 已挡 `coverage.out`、`/logs`、`/configs/config.yaml` 等，但档不住手滑）。
- 不为了过 CI 而删测试、关 lint 规则或放宽覆盖率红线；要调整门禁，在 PR 里说明依据。

**安全披露**

- 本项目会持有 Last.fm `api_secret`/`session_key`、ListenBrainz `token`，以及飞牛客户端的 token（仅内存 + 脱敏前 8 位入库）。发现可能导致凭证泄漏的问题（日志打印明文、请求日志未打码、配置回写覆盖他人文件、升级流程可被替换包劫持等），**不要开公开 issue**。
- 私有渠道：GitHub 的 Security → Report a vulnerability；或邮件联系包元数据里的维护者地址（`.goreleaser.yaml` 的 `maintainer`）。
- 披露内容请附：版本（`fnmusic-sync version`）、运行模式（deb/fpk）、复现步骤、影响面判断。
- 收到后会在修复并发布新版本后再公开细节；修复期请勿在公开渠道贴 PoC。

**许可**

仓库为 MIT（`LICENSE`，Copyright (c) 2026 liguangchun）。提交即表示同意以同一许可分发。
