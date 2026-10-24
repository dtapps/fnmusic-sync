# Codecov 接入说明（fnmusic-sync）

本仓库的覆盖率 / 打包体积 / 测试结果统一上报到 **Codecov**，由 GitHub Actions 工作流驱动（与 CNB 的硬性红线门禁相互独立）。

## 1. 总览：三个支柱 + 职责分离

| 支柱                     | 内容            | 本仓库实现                                                                                                         | Codecov 里看什么                      |
|--------------------------|-----------------|--------------------------------------------------------------------------------------------------------------------|---------------------------------------|
| 覆盖率 Coverage          | 代码行覆盖率    | `codecov/codecov-action` 上传 Go `coverage.out` 与前端 `lcov.info`                                                 | Coverage 面板、PR 评论、patch 状态    |
| 打包体积 Bundle Analysis | 前端 chunk 体积 | `@codecov/vite-plugin`（构建时上传）                                                                               | Bundle 面板（体积趋势 / 回归）        |
| 测试结果 Test Analytics  | 单测运行结果    | Go：`gotestsum --junitfile`；前端(Svelte)：Vitest `json` → 脚本转 JUnit；合并由 `codecov/test-results-action` 上传 | Tests 面板（时长 / flaky / 失败分析） |

> **职责分离（重要）**：CNB 流水线（`testing:coverage`）负责**硬性红线门禁**（`lines` / `diffLines`），会阻断低覆盖率合并；GitHub Actions 只负责**把数据上报到 Codecov** 做可视化（PR 评论、徽章、patch 状态）。因此 `.github/codecov.yml` 里的 `status` 全部设为 `informational`，避免与 CNB 双重门禁。

## 2. 涉及文件清单

| 文件                                            | 作用                                                                                                                |
|-------------------------------------------------|---------------------------------------------------------------------------------------------------------------------|
| `.github/codecov.yml`                           | Codecov 规则：忽略 `internal/db`、`internal/datadb` 等 sqlc 生成代码、`flags`（go / node）、`status: informational` |
| `.github/workflows/coverage.yml`                | GitHub Actions 流水线：装依赖 → 跑 Go/前端测试 → 构建前端 → 上传覆盖率 / 测试结果                                   |
| `cmd/frontend/vite.config.ts`                   | 条件加载 `@codecov/vite-plugin`（仅当 `CODECOV_TOKEN` 存在时实例化）                                                |
| `cmd/frontend/scripts/vitest-json-to-junit.mjs` | 零依赖脚本：Vitest JSON → JUnit XML                                                                                 |
| `cmd/frontend/package.json`                     | `devDependencies`：`@codecov/vite-plugin` 等                                                                        |

## 3. 各功能详解

### 3.1 覆盖率（Coverage）

- Go：复用 CNB 的同款口径

  ```
  go test -vet=off -coverpkg=$(go list ./... | grep -vE '/(db|datadb)$' | paste -sd, -) -coverprofile=coverage.out ./...
  ```
  - 本项目**无 ent**；生成代码是 sqlc 的 `internal/db`、`internal/datadb`（合计约占 11% 语句块），计入分母会稀释指标，故从 `-coverpkg` 排除。
  - 其余口径见 `docs/coverage-go.md`（范围 `./...`、`-vet=off` 原因等）。
- 前端：`pnpm test:coverage`（Vitest v8 覆盖率，产出 `cmd/frontend/coverage/lcov.info`）。
- 上传：`codecov/codecov-action@v5`，分别带 `flags: go` / `flags: node`（对应 `docs/coverage-go.md` / `docs/coverage-node.md` 的 CNB key）。

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
- 构建步骤设了 `continue-on-error: true`：万一插件与 Vite 8 不兼容导致 `vite build` 崩溃，也**不会拖垮**上面的覆盖率上传。
- 与 certflow 不同，本仓库前端 `vite build` 的产物写到 **`internal/webui/www`**（Go embed 目录，而非 `dist/`），这是有意设计，不影响 Bundle 上报。

### 3.3 测试结果（Test Analytics）

Go 与前端(Svelte) 都不原生产出 JUnit，分别用不同方式转换后**合并上传**到同一个 `codecov/test-results-action@v1`：

- **Go**：`go test` 不原生产 JUnit，用 `gotestsum` 包裹（参数原样透传，`coverage.out` 照常产出），额外生成 `go-test-report.xml`：

  ```
  go install gotest.tools/gotestsum@v1.13.0
  export PATH="$(go env GOPATH)/bin:$PATH"
  gotestsum --junitfile go-test-report.xml -- -vet=off -coverpkg=$(go list ./... | grep -vE '/(db|datadb)$' | paste -sd, -) -coverprofile=coverage.out ./...
  ```
  - ⚠️ `gotestsum` 必须固定为 **v1.13.0**：v1.12.0 依赖的旧版 `golang.org/x/tools` 无法在 Go 1.27 上编译（`invalid array length`），v1.13.0 升到 x/tools v0.36.0 后正常。
- **前端(Svelte / Vitest 5.0.5)**：无内置 junit reporter（社区 `@vitest/junit` 包在 npm 也已不可得），改用内置 `json` reporter 产出 `test-results.json`，再由零依赖脚本转 JUnit：
  1. 测试步骤额外挂内置 `json` reporter 产出 `test-results.json`：

     ```
     pnpm test:coverage --reporter=default --reporter=json --outputFile=test-results.json
     ```

     （`coverage/lcov.info` 仍正常产出，不受影响。）
  2. 转换步骤用 `node scripts/vitest-json-to-junit.mjs` 把 JSON 转成 `test-report.xml`（JUnit 格式）。
- 上传步骤的 `files` 已合并两份报告：`cmd/frontend/test-report.xml,./go-test-report.xml`。
- 最终 Codecov 的 **Tests** 面板同时包含 Go 与前端(Svelte) 的测试结果。

#### classname 必须是「仓库相对 + `/` 分隔」

Codecov Test Analytics **只用 `testcase` 的 `classname` 属性**定位用例归属文件（官方 Jest 示例即 `JEST_JUNIT_CLASSNAME="{filepath}"`；`file` / `filepath` 属性文档未提及，不可依赖）。`classname` 同时决定文件分组、跨 run 关联（flaky 判定）与失败位置跳转。

差异来自各自默认产出：

- gotestsum（Go）：`classname` = 包导入路径 `cnb.cool/dtapp/fnmusic-sync/internal/auth` → 天然稳定 ✅
- Vitest `json` reporter：`testResults[].name` 是**测试文件绝对路径**，直接写进 `classname` 就带上 runner 前缀 ❌

runner 前缀**随 CI 执行形态变化**，同一仓库前后两次 run 就会把同一条用例记成两条（`…/a.test.ts > 用例A` 与 `/__w/…/a.test.ts > 用例B`）：

| 执行形态                                               | 典型 checkout 路径                |
|--------------------------------------------------------|-----------------------------------|
| GitHub Actions 裸 runner（`runs-on:` 无 `container:`） | `/home/runner/work/<repo>/<repo>` |
| GitHub Actions 容器 job（`container:`）                | `/__w/<repo>/<repo>`              |
| GitLab CI                                              | `/builds/<group>/<repo>`          |
| CNB / 常见容器默认 workdir                             | `/workspace/<repo>`               |
| 本地                                                   | `/Users/<me>/<repo>`              |

因此 `vitest-json-to-junit.mjs` 在写 XML 前做了**两层**归一化，结果与具体前缀无关：

1. 从**脚本自身位置**（`import.meta.url`）向上找 `go.mod` / `.git` 得到仓库根；
2. **第 1 层（常规，覆盖 99% 场景）**：JSON 与脚本在同一份 checkout 树内，直接 `path.relative(repoRoot, abs)` 再 `split(path.sep).join("/")`；
3. **第 2 层（兜底）**：路径不在这棵树里（测试在容器内跑 `/__w/…`、转换在宿主上跑；或本机复现别的 CI 产出的 JSON）时，从尾部取最长的「在仓库里真实存在」的后缀。认不出来就原样返回，绝不编造路径。

四个坑：

- **不要用 `process.cwd()` 推仓库根**：转换步骤的 `working-directory` 是 `cmd/frontend`，cwd 不是仓库根，会算出 `../src/...`。
- **不要用 `codecov.yml` 的 `fixes:` 兜底**：`fixes` 属覆盖率侧路径修补，对 Test Analytics 无效；即便生效也只能写死某个 CI 的前缀，换个 runner 形态（无容器的 `/home/runner/work/...`）立刻失效。
- **历史数据不会迁移**：已上传的绝对路径记录保留到 Codecov 60 天保留期满才自然淘汰。UI 上还能看到旧记录 ≠ 修复没生效，要看最新一次 run。
- **兜底层不能用 `existsSync`**：会命中 `cmd/frontend/src/test` 这类同名目录，必须 `statSync(...).isFile()`；循环上限也保留 `i >= 2`，避免误命中仓库根的 `go.mod` / `package.json`。

自查：

```bash
grep -o 'classname="[^"]*"' cmd/frontend/test-report.xml | sort -u | head
# 期望 cmd/frontend/src/test/x.test.ts；出现以 / 或 C:\ 开头即为此问题
```

脚本在两层都失败时（文件确实不在当前 checkout）会打 `⚠️` 警告，而不是静默产出坏数据。

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

- **Go 覆盖率 / Tests 上报 0% 或空**：本项目 `*_test.go` 被 `.gitignore` 忽略，GitHub Actions checkout 后**无 Go 测试可跑**，`go test`/`gotestsum` 仍会产出全 0% 的 `coverage.out` 与（空）`go-test-report.xml`（不会因 `breakIfNoCoverage` 失败）。要拿到真实 Go 数据，需先把测试文件纳入 git（从 `.gitignore` 移除 `*_test.go` 或 `git add -f`）。前端不受影响（测试已跟踪）。
- **Bundle 面板空白**：检查「构建前端」步骤是否真的上传成功；插件与 Vite 8 不兼容时该步骤会 `continue-on-error` 失败但不阻断。
- **Tests 面板空白**：检查 `test-results.json` 与 `go-test-report.xml` 是否生成、`vitest-json-to-junit.mjs` 是否产出 `test-report.xml`、`codecov/test-results-action` 是否拿到 token。
- **Tests 面板用例名带 `/__w/...`、`/home/runner/work/...` 等前缀**：JUnit 里 `classname` 是绝对路径，跨 run 无法关联（详见 3.3「classname 必须是仓库相对」）。用 `grep -o 'classname="[^"]*"' cmd/frontend/test-report.xml | sort -u` 自查；脚本已在生成端归一化，并会在仍有绝对路径时打 `⚠️` 警告。注意已污染的历史数据不会迁移，只有新 run 才正确。
- **覆盖率双份/互相覆盖**：确认 `.github/codecov.yml` 的 `flags` 已区分 `go` / `node`，且 `status` 为 `informational`。

## 7. 注意事项

- GA 与 CNB 都跑一遍测试，各管各的、互不依赖（GA 生成产物并上报 Codecov，CNB 负责门禁）。
- `cmd/frontend/package.json` 的 `packageManager` 为 `pnpm@12.14.0`，`cmd/frontend/.nvmrc` 为 Node 版本——工作流用 `node-version-file` / `go-version-file` 动态读取，不要写死版本号。
- 文档命名注意 `docs/*-*-*.md`（双横线）被 `.gitignore` 忽略，本文档用单横线命名（`docs/codecov.md`）。
