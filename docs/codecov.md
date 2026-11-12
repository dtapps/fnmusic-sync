# Codecov 接入说明（fnmusic-sync）

本仓库的覆盖率 / 打包体积 / 测试结果统一上报到 **Codecov**，由 GitHub Actions 工作流驱动（与 CNB 的硬性红线门禁相互独立）。

## 1. 总览：三个支柱 + 职责分离

| 支柱                     | 内容            | 本仓库实现                                                                                                                                      | Codecov 里看什么                      |
|--------------------------|-----------------|-------------------------------------------------------------------------------------------------------------------------------------------------|---------------------------------------|
| 覆盖率 Coverage          | 代码行覆盖率    | `codecov/codecov-action` 上传 Go `coverage.out` 与前端 `lcov.info`                                                                              | Coverage 面板、PR 评论、patch 状态    |
| 打包体积 Bundle Analysis | 前端 chunk 体积 | `@codecov/vite-plugin`（构建时上传）                                                                                                            | Bundle 面板（体积趋势 / 回归）        |
| 测试结果 Test Analytics  | 单测运行结果    | Go：`gotestsum --junitfile`；前端(Svelte)：Vitest 内置 `junit` reporter；合并由 `codecov/codecov-action@v7` 的 `report_type: test_results` 上传 | Tests 面板（时长 / flaky / 失败分析） |

> **职责分离（重要）**：CNB 流水线（`testing:coverage`）负责**硬性红线门禁**（`lines` / `diffLines`），会阻断低覆盖率合并；GitHub Actions 只负责**把数据上报到 Codecov** 做可视化（PR 评论、徽章、patch 状态）。因此 `.github/codecov.yml` 里的 `status` 全部设为 `informational`，避免与 CNB 双重门禁。

## 2. 涉及文件清单

| 文件                            | 作用                                                                                                                |
|---------------------------------|---------------------------------------------------------------------------------------------------------------------|
| `.github/codecov.yml`           | Codecov 规则：忽略 `internal/db`、`internal/datadb` 等 sqlc 生成代码、`flags`（go / node）、`status: informational` |
| `.github/workflows/codecov.yml` | GitHub Actions 流水线：装依赖 → 跑 Go/前端测试 → 构建前端 → 上传覆盖率 / 测试结果                                   |
| `cmd/frontend/vite.config.ts`   | 条件加载 `@codecov/vite-plugin`（仅当 `CODECOV_TOKEN` 存在时实例化）                                                |
| `Makefile`（`测试覆盖率` 一节） | 覆盖率命令与扫描 / 排除清单的唯一来源：`cover-go` / `junit-go` / `cover-node` / `junit-node`                        |
| `cmd/frontend/vitest.config.ts` | 读取 Makefile 注入的 `VITEST_TEST_INCLUDE` / `VITEST_COVER_INCLUDE` / `VITEST_COVER_EXCLUDE`                        |
| `cmd/frontend/package.json`     | `devDependencies`：`@codecov/vite-plugin` 等                                                                        |

## 3. 各功能详解

### 3.1 覆盖率（Coverage）

- Go：复用 CNB 的同款口径，命令收敛在 Makefile 的 `cover-go` 目标

  ```
  make cover-go     # = go test -vet=off -coverpkg=$(go list ./... | grep -vE '/(db|datadb)$' | paste -sd, -) -coverprofile=coverage.out ./...
  ```
  - 本项目**无 ent**；生成代码是 sqlc 的 `internal/db`、`internal/datadb`（合计约占 11% 语句块），计入分母会稀释指标，故从 `-coverpkg` 排除。
  - 其余口径见 `docs/coverage-go.md`（范围 `./...`、`-vet=off` 原因等）。
- 前端：`make cover-node`（Vitest v8 覆盖率，产出 `cmd/frontend/coverage/lcov.info`；扫描 / 排除清单由 Makefile 注入 vitest）。
- 上传：`codecov/codecov-action@v7`，分别带 `flags: go` / `flags: node`（对应 `docs/coverage-go.md` / `docs/coverage-node.md` 的 CNB key）。

### 3.2 打包体积（Bundle Analysis）

- 插件 `@codecov/vite-plugin@2.0.1` 在 `cmd/frontend/vite.config.ts` 中**条件加载**：

  ```ts
  ...(process.env.CODECOV_TOKEN
    ? [
        codecovVitePlugin({
          enableBundleAnalysis: true,
          bundleName: "fnmusic-sync-frontend",
          uploadToken: process.env.CODECOV_TOKEN,
        }),
      ]
    : [])
  ```
- 上传发生在 `pnpm build`（= `vite build`）这一步。
- ⚠️ **兼容性注意**：该插件 peer 仅声明支持 Vite `4/5/6`，而本仓库为 **Vite 8.3.5**。因此特意用「条件数组」而非无条件 `import`——CNB 生产构建与本地构建都**没有** `CODECOV_TOKEN`，插件根本不会实例化，绝不影响主构建。若日后插件更新支持 Vite 8，逻辑无需改动。
- 构建步骤（`make build-frontend`）排在两次覆盖率上传**之后**：插件与 Vite 8 不兼容导致 `vite build` 崩溃时，覆盖率 / 测试结果已经上传完毕，只是 job 会被标红。
- 与 certflow 不同，本仓库前端 `vite build` 的产物写到 **`internal/webui/www`**（Go embed 目录，而非 `dist/`），这是有意设计，不影响 Bundle 上报。

### 3.3 测试结果（Test Analytics）

Go 与前端(Svelte) 的 JUnit 由**两个 Makefile 目标**产出，再**合并上传**到同一步 `codecov/codecov-action@v7`（`report_type: test_results`）：

> `codecov/test-results-action@v1` 已弃用：它固定跑在 Node 20 上，GitHub runner 强制提升到 Node 24 会报 deprecation 警告；官方迁移路径是 `codecov/codecov-action`（v5 起新增 `report_type` 输入）为覆盖率与测试结果各跑至少一次。本工作流里覆盖率跑两次（`flags: go` / `flags: node`），测试结果跑一次，并设 `disable_search: true` 只上传显式列出的两份 JUnit。
>
> **别丢掉 `github-actions` reporter**：Vitest 的默认 reporters = `[default, github-actions]`（`GITHUB_ACTIONS=true` 时追加），它写 `$GITHUB_STEP_SUMMARY` 的「Vitest Test Report / Summary」并输出失败注解。**一旦显式传 `--reporter` 就会整体替换这个列表**，所以 `make cover-node` 刻意不传 `--reporter`（注解与 Summary 区块由它产出）；`make junit-node` 必须显式传 `--reporter=junit` 才能出 XML，那一次没有该 reporter，注解由前一步提供。`github-actions` reporter 在非 GHA 环境安全空转（无 `GITHUB_STEP_SUMMARY` 即跳过写文件），CNB 侧不受影响。

- **Go**：`go test` 不原生产 JUnit，用 `gotestsum` 包裹（参数原样透传，`coverage.out` 照常产出），额外生成 `go-test-report.xml`：

  ```
  make junit-go     # = gotestsum --junitfile go-test-report.xml -- <cover-go 的参数>
  ```
  - ⚠️ `gotestsum` 必须固定为 **v1.13.0**：v1.12.0 依赖的旧版 `golang.org/x/tools` 无法在 Go 1.27 上编译（`invalid array length`），v1.13.0 升到 x/tools v0.36.0 后正常。
- **前端(Svelte / Vitest 5.1)**：vitest 5.1 **已内置 `junit` reporter**，直接产出 `cmd/frontend/coverage/junit.xml`：

  ```
  make junit-node    # = vitest run --reporter=junit --outputFile=<abs>/cmd/frontend/coverage/junit.xml
  ```

  （`coverage/lcov.info` 由 `make cover-node` 单独产出，两者互不影响。）
- 上传步骤的 `files` 已合并两份报告：`./go-test-report.xml,./cmd/frontend/coverage/junit.xml`。
- 最终 Codecov 的 **Tests** 面板同时包含 Go 与前端(Svelte) 的测试结果。

#### classname 必须是「仓库相对 + `/` 分隔」

Codecov Test Analytics **只用 `testcase` 的 `classname` 属性**定位用例归属文件（官方 Jest 示例即 `JEST_JUNIT_CLASSNAME="{filepath}"`；`file` / `filepath` 属性文档未提及，不可依赖）。`classname` 同时决定文件分组、跨 run 关联（flaky 判定）与失败位置跳转。

两个产出方的 `classname`：

- gotestsum（Go）：`classname` = 包导入路径 `cnb.cool/dtapp/fnmusic-sync/internal/auth` → 天然稳定 ✅
- Vitest 内置 `junit` reporter：`classname` = **相对 vitest root 的测试文件路径**（`src/test/api.test.ts`），不带 runner 前缀 ✅

> ⚠️ 注意：vitest 的 `classname` 相对的是 **vitest root**（本项目为 `cmd/frontend`），所以是 `src/test/x.test.ts` 而不是 `cmd/frontend/src/test/x.test.ts`。Codecov 按路径后缀匹配文件，能正常对上；若将来发现匹配不上，用 `.github/codecov.yml` 的 `fixes:` 补前缀即可（属覆盖率侧路径修补，不影响 `classname` 语义）。

> 历史包袱：早期用 Vitest `json` reporter + 自写脚本转 JUnit，脚本拿到的 `testResults[].name` 是**绝对路径**，会把 `/__w/<repo>/<repo>/...` 之类的 runner 前缀写进 `classname`，导致同一用例跨 run 无法关联（flaky 判定失效、失败位置跳不过去）。换成 vitest 内置 `junit` reporter 后，`classname` 天然是相对路径，这类归一化逻辑连同 `cmd/frontend/scripts/vitest-json-to-junit.mjs` 一起删掉了。已上传的脏历史数据不会迁移（Codecov 侧保留 60 天自然淘汰），要看最新一次 run。

自查：

```bash
grep -o 'classname="[^"]*"' cmd/frontend/coverage/junit.xml | sort -u | head
# 期望 src/test/x.test.ts；出现以 / 或 C:\ 开头即为此问题
```

### 3.4 Components 面板

在 `.github/codecov.yml` 的顶层 `component_management` 里按业务模块把覆盖率切块，Codecov 会多出一个 **Components** 面板，分别看各模块的趋势与回归。它和 `coverage.flags` 不同：flags 是「按语言分开上传」，components 是「同一份覆盖率数据按目录再切块」。

| component_id | 名称                | 路径                                                                                                                                                                                                    |
|--------------|---------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| go-playback  | Go · 播放与队列     | `internal/playback/**`、`internal/playlist/**`                                                                                                                                                          |
| go-scrobble  | Go · 打点与元数据   | `internal/scrobbler/**`、`internal/lastfm/**`、`internal/listenbrainz/**`、`internal/mbid/**`                                                                                                           |
| go-platform  | Go · 平台与基础设施 | `internal/config/**`、`internal/model/**`、`internal/proxy/**`、`internal/feiniu/**`、`internal/reqlog/**`、`internal/safego/**`、`internal/strutil/**`、`internal/updater/**`、`internal/buildinfo/**` |
| frontend     | Svelte 前端         | `cmd/frontend/src/**`                                                                                                                                                                                   |

- 组件状态同样为 `informational`（只展示不阻塞），硬门禁仍由 CNB 负责。
- **无需改 workflow**：组件覆盖率由已上传的 Go / 前端报告按路径自动计算；`coverage.ignore` 里的 `internal/db`、`internal/datadb`、`*_test.go`、入口等不计入组件。

## 4. 必需的密钥

- `CODECOV_TOKEN`：在 GitHub 仓库 **Settings → Secrets → Actions** 中配置（Codecov 项目页获取）。覆盖率、Bundle、测试结果三个动作共用此 token。
- 未配置时：Bundle 插件不会实例化（条件加载）、覆盖率 / 测试结果仍能上传（公开仓库可免 token，私有仓库会失败）。

## 5. 运行环境

- `runs-on: ubuntu-latest`。**与 certflow 不同**：本仓库**不是 Wails 应用**（无 `wails` 依赖），Go 测试不需要 CGO 链接 GTK4/WebKit 系统库，因此**不需要** `ubuntu-24.04` 与 `libwebkitgtk-6.0-dev` 那套，标准 runner 即可。
- 工作流用 `node-version-file`（`cmd/frontend/.nvmrc`）/ `go-version-file`（`go.mod`）动态读取版本，不要写死。

## 6. 验证与排错

在 Codecov 项目页应能看到三个面板：**Coverage / Bundle / Tests**。

- **Go 覆盖率 / Tests 上报 0% 或空**：Go 测试文件已全部纳入 git（`.gitignore` 只忽略 `/internal/reqlog/*_test.go`），若仍为 0% 先本地跑 `make cover-go` 对照（当前基线约 16.5%）。注意 `go test -coverprofile` 即使一个测试文件都没有也会产出全 0% 的 `coverage.out`，不会因 `breakIfNoCoverage` 报错。
- **Bundle 面板空白**：检查「构建前端」步骤（`make build-frontend`）是否真的上传成功；插件与 Vite 8 不兼容时该步骤会失败并把 job 标红（覆盖率上传在其之前，不受影响）。
- **Tests 面板空白**：检查 `go-test-report.xml` 与 `cmd/frontend/coverage/junit.xml` 是否生成、`codecov/codecov-action` 的测试结果上传步骤（`report_type: test_results`）是否拿到 token。
- **Job Summary 里没有 Vitest 测试报告区块 / 没有失败注解**：多半是给 `make cover-node` 加了 `--reporter`（显式传会整体替换 vitest 的默认 reporters 列表，`github-actions` 就没了）；`cover-node` 保持不传即可。
- **Tests 面板用例名带 `/__w/...`、`/home/runner/work/...` 等前缀**：说明用的是旧版 JSON→JUnit 脚本的产物；当前用 vitest 内置 `junit` reporter，`classname` 天然相对，用 `grep -o 'classname="[^"]*"' cmd/frontend/coverage/junit.xml | sort -u` 自查。已污染的历史数据不会迁移，只有新 run 才正确。
- **覆盖率双份/互相覆盖**：确认 `.github/codecov.yml` 的 `flags` 已区分 `go` / `node`，且 `status` 为 `informational`。

## 7. 注意事项

- GA 与 CNB 都跑一遍测试，各管各的、互不依赖（GA 生成产物并上报 Codecov，CNB 负责门禁）。
- `cmd/frontend/package.json` 的 `packageManager` 为 `pnpm@12.14.0`，`cmd/frontend/.nvmrc` 为 Node 版本——工作流用 `node-version-file` / `go-version-file` 动态读取，不要写死版本号。
- 文档命名注意 `docs/*-*-*.md`（双横线）被 `.gitignore` 忽略，本文档用单横线命名（`docs/codecov.md`）。
