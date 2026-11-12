# Coveralls 接入说明（fnmusic-sync）

本仓库在 **Codecov** 之外，额外把覆盖率上报到 **Coveralls**，由独立工作流 `.github/workflows/coveralls.yml` 驱动。两条通道**互不依赖**：Codecov 挂了不影响 Coveralls，反之亦然；硬性门禁仍由 CNB 负责。

> 与 `docs/codecov.md` 的关系：Coveralls 与 Codecov **共用同一套 CI 镜像、同一条 Go 覆盖率命令、相同的 `go` / `node` flag**，只是上报目标不同。Coveralls 不提供 Bundle Analysis / Test Analytics，也**不读仓库内配置文件**——阈值、PR 注释、徽章都在 Coveralls 后台仓库设置里配。

## 1. 总览：一条独立通道

| 项         | 内容             | 本仓库实现                                                                                    | Coveralls 里看什么          |
|------------|------------------|-----------------------------------------------------------------------------------------------|-----------------------------|
| Go 覆盖率  | 后端代码行覆盖率 | `make cover-go-cobertura` → `coverage.xml`，`coverallsapp/github-action@v2` 上传（flag `go`） | 总覆盖率 / 趋势 / flag 明细 |
| 前端覆盖率 | Svelte 行覆盖率  | `make cover-node` → `lcov.info`，`coverallsapp/github-action@v2` 上传（flag `node`）          | 同上，按 flag 合并          |
| 合并       | 两份报告         | 同一 job、同一 build，靠 flag 合并                                                            | 一个 build 下同时有 go/node |

> **职责分离**：与 Codecov 一致——GitHub Actions 只负责上报，CNB 负责硬门禁。上传步骤全部 `continue-on-error: true`，Coveralls 故障不阻断合并。

## 2. 涉及文件清单

| 文件                              | 作用                                                                                                                                                             |
|-----------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `.github/workflows/coveralls.yml` | 唯一入口：装依赖 → 跑 Go/前端测试 → 分别上报 `flag=go` / `flag=node`                                                                                             |
| `dtapps/golang/fnpack` 镜像       | 提供 `ghcr.io/dtapps/golang/fnpack:latest`（**与 codecov.yml 同一个**），已预装 `gotestsum`、`gojq`、`goveralls` 等；本仓库不自建，见 [ci-image.md](ci-image.md) |
| `Makefile`（`deps` 目标）         | `go mod tidy` + `go mod download` + `pnpm --dir cmd/frontend install --frozen-lockfile`                                                                          |
| `Makefile`（`测试覆盖率` 一节）   | 覆盖率命令与扫描 / 排除清单的唯一来源：`cover-go` / `cover-go-cobertura` / `cover-node`                                                                          |
| `coverage.xml`                    | Go 覆盖率（Cobertura 格式，由 `gocover-cobertura` 从 `coverage.out` 转换而来）                                                                                   |
| `cmd/frontend/coverage/lcov.info` | 前端覆盖率产物（`make cover-node` 生成）                                                                                                                         |

> **没有 `.github/coveralls.yml`**：与 Codecov 的 `.github/codecov.yml` 不同，Coveralls 的阈值 / PR 注释 / 徽章都在 Coveralls 后台仓库设置里配，仓库内不需要配置文件。

## 3. 各功能详解

### 3.1 Go 覆盖率：Cobertura XML

- 与 Codecov **完全相同的口径**（都走 Makefile 的 `cover-go`，扫描 / 排除清单同源）：

  ```
  make cover-go-cobertura   # = go test -coverpkg=... -coverprofile=coverage.out ./... && gocover-cobertura -by-files -strict < coverage.out > coverage.xml
  ```

  - 本项目**无 ent**；生成代码是 sqlc 的 `internal/db`、`internal/datadb`（合计约占 11% 语句块），从 `-coverpkg` 排除。
  - 范围是 `./...`（含 `cmd/fnmusic-sync` 的 `goroutine_diag_test.go`），不要写成 `./internal/...`。

- **为什么转 Cobertura 而不是直接喂 `coverage.out`**：Coveralls 的 `golang` parser 会用正则硬吃 3 段包前缀、只留 basename，导致所有文件坍缩到同一级目录，文件级明细全废；`cobertura` parser 直接取 `<class filename="...">` 的完整相对路径，绕开该正则。`gocover-cobertura` 由镜像预装。

- 两步（**无安装步骤**，工具都来自镜像）：

  ```yaml
  - name: 生成 Go 测试覆盖率（Cobertura）
    run: make cover-go-cobertura

  - name: 上传 Go 覆盖率到 Coveralls
    if: env.COVERALLS_REPO_TOKEN != ''
    continue-on-error: true
    uses: coverallsapp/github-action@v2
    with:
      github-token: ${{ secrets.GITHUB_TOKEN }}
      file: coverage.xml
      format: cobertura
      flag-name: go
  ```

- 三个必须知道的细节：
  - **Cobertura 里是仓库相对路径**（`internal/config/config.go`），不再有模块前缀问题——前缀剥离那套坑（`package cmd/... is not in std`）随格式切换自然消失。
  - **`-strict`**：让「包/模块信息缺失被静默跳过」直接失败，避免产出空报告还当成功。
  - **`-flagname=go` / `flag-name: go`**：与前端 `node` 配对，合并进同一 build。

- **不要在工作流里 `go install goveralls`**：本工作流用 `coverallsapp/github-action@v2`（composite action），镜像里无需额外装 goveralls；真要改回 `goveralls` CLI，版本由 `dtapps/golang/fnpack` 的 `GOVERALLS_VERSION` 钉死，升级在那边改 ARG 后重推镜像。
- `make junit-go` 的 `go-test-report.xml` 只用于本地排查 / Codecov Test Analytics，Coveralls **不接收** JUnit。

### 3.2 前端覆盖率：coveralls action

```yaml
- name: 生成前端测试覆盖率
  run: make cover-node

- name: 上传前端覆盖率到 Coveralls
  if: env.COVERALLS_REPO_TOKEN != ''
  continue-on-error: true
  uses: coverallsapp/github-action@v2
  with:
    github-token: ${{ secrets.GITHUB_TOKEN }}
    path-to-lcov: ./cmd/frontend/coverage/lcov.info
    flag-name: node
```

- 输入名是 **`path-to-lcov`**，不是 `file`。
- 与 Codecov 不同，这里**不产 JUnit**：Coveralls 只吃 lcov，不需要 Test Analytics 那套。
- 前端在 `cmd/frontend`，技术栈是 **Svelte**（Vitest 5.1）；`make cover-node` 的扫描 / 排除清单与 CNB、Codecov 完全同源。

### 3.3 为什么是单个 job

Coveralls 以 **build** 为单位聚合报告，go / node 两份数据必须落进**同一个 build**，才能靠 flag 合并成一份总覆盖率：

- **单 job**：两次上传天然同 build、同 commit、同 job id，flag 直接合并，确定性最高。
- **多 job + parallel**：需要 `parallel: true` + 最后一个 `parallel-finished: true` 收尾，goveralls 侧还要 `-parallel -jobid`。两套工具混用 parallel 协议，容易出现「build 一直不 finalize」或「两份报告各算各的」。

当前用单 job 顺序执行，用一点时间换确定性。将来真要拆（比如 Go 测试太慢），拆之前先确认 goveralls 版本的 `-parallel` / `-jobid` 行为，别照抄。

## 4. 必需的密钥

- `COVERALLS_REPO_TOKEN`：GitHub 仓库 **Settings → Secrets → Actions** 添加。取值路径：Coveralls → 选择仓库 → **Settings / Tokens**。
- 与 Codecov 的 `CODECOV_TOKEN` **各自独立**，各配各的。
- **fork PR**：GitHub 不向 fork 的 PR 传递 secrets，token 为空 → 上传步骤被 `if: env.COVERALLS_REPO_TOKEN != ''` 跳过，不会因为缺 token 而红。
- **上传失败不阻断 CI**：两个上传步骤都有 `continue-on-error: true`。Coveralls 是观测工具，不该成为合并的阻塞项。

## 5. 运行环境

- `runs-on: ubuntu-latest` + `container: ghcr.io/dtapps/golang/fnpack:latest`，**与 `codecov.yml`（Codecov）完全同一个镜像**。
- 镜像基于 `golang:1.27.3-trixie`，内置 Node / pnpm，以及钉好版本的 Go 工具链：

| 工具      | 版本       | 来源                                |
|-----------|------------|-------------------------------------|
| gotestsum | `v1.13.0`  | 镜像内 `go install`                 |
| gojq      | `v0.12.19` | 同上                                |
| goveralls | `v0.0.12`  | 同上，装到 `$GOPATH/bin`（在 PATH） |

- 因此工作流**不需要** `setup-go` / `setup-node` / `apt-get install`。
- **本仓库非 Wails**：镜像不含 GTK/WebKit，Go 测试也不需要 CGO 链接系统库。
- 镜像由 `dtapps/golang/fnpack` 统一维护并推送到 GHCR，**首次运行不需要任何准备**（本仓库不再有 `ci-image.yml`）。

## 6. 验证与排错

跑完后到 Coveralls 仓库页确认：**最新 build 下同时有 `go` 与 `node` 两个 flag，且合并出一个总覆盖率**。

| 现象                                   | 原因 / 处理                                                                                                                     |
|----------------------------------------|---------------------------------------------------------------------------------------------------------------------------------|
| 上传步骤显示 skipped                   | `COVERALLS_REPO_TOKEN` 为空（fork PR 正常；主仓 PR 则是 secret 没配）                                                           |
| `gocover-cobertura: command not found` | 镜像里没装该工具，或 `/go/bin` 不在 PATH；**改镜像，别用 `go install` 兜底**                                                    |
| 文件路径对不上 / 覆盖率 0%             | 先本地 `make cover-go-cobertura` 看 `coverage.xml`：`<class filename="...">` 应是仓库相对路径（如 `internal/config/config.go`） |
| Go 覆盖率明显偏低                      | 确认 `-coverpkg` 排除了 `internal/db`、`internal/datadb`（sqlc 生成代码），排除项写在 Makefile 清单里                           |
| 只看到 `go` 或只看到 `node`            | 某一步上传失败被 `continue-on-error` 吞了，展开该 step 日志看 API 返回                                                          |
| 两个 flag 各算各的、没有合并           | 两次上传不在同一 build；确认仍在同一个 job 内（拆分 job 就必须上 parallel 协议）                                                |
| 私有仓库 401 / 未授权                  | Coveralls 后台未授权该仓库，或 token 类型不对；确认 `COVERALLS_REPO_TOKEN` 属于该仓库                                           |

本地预演（不真的上传）：

```bash
make cover-go-cobertura
head -3 coverage.xml   # 应为 <coverage ...> + <sources><source>仓库根</source>
# Go：Cobertura（仓库相对路径）；前端：make cover-node 产出 cmd/frontend/coverage/lcov.info
```

## 7. 与 Codecov 的关系（同一口径，两条通道）

| 维度          | Codecov（`codecov.yml` + `.github/codecov.yml`）                          | Coveralls（`coveralls.yml`）                                                           |
|---------------|---------------------------------------------------------------------------|----------------------------------------------------------------------------------------|
| Go 覆盖率命令 | 同一条（`make cover-go`，`./...` 排除 `db`/`datadb`）                     | **相同**（转成 Cobertura 后上传）                                                      |
| flag 命名     | `go` / `node`                                                             | `go` / `node`（相同）                                                                  |
| 运行环境      | 同一 CI 镜像                                                              | **同一 CI 镜像**                                                                       |
| 配置文件      | `.github/codecov.yml`（flags / ignore / status / components）             | **无**，配置在 Coveralls 后台                                                          |
| 额外能力      | Bundle Analysis、Test Analytics、Components 面板                          | 无（仅覆盖率 + flag 趋势）                                                             |
| 门禁          | 全 `informational`，不阻塞                                                | 上传 `continue-on-error`，不阻塞                                                       |
| 数据差异      | 受 `codecov.yml` 的 `ignore` 影响（排除 `main.go`、`*_test.go`、mock 等） | 只受 `-coverpkg` 影响，**`cmd/fnmusic-sync/main.go` 等会被计入** → 与 Codecov 略有出入 |

- 两者**互不依赖**：任一方故障不影响另一方。
- 若想让 Coveralls 与 Codecov 数字完全一致，需要在上传前对 `coverage.out` 再做一次与 `codecov.yml` 等价的过滤（当前不做，保持简单）。

## 8. 注意事项

- `flag` 名一旦定了别乱改，改了等于新建一条趋势线，历史会断档。
- 本工作流用 `coverallsapp/github-action@v2`（composite action），镜像里无需额外装 goveralls；若改回 CLI 方式，版本由 `dtapps/golang/fnpack` 的 `GOVERALLS_VERSION` 钉死，升级改那边的 ARG 后重推镜像，**不要**在工作流里 `go install`。
- 文档命名：`docs/*-*-*.md`（双横线）被 `.gitignore` 忽略，本文档用单横线（`docs/coveralls.md`）。
- **Go 端当前基线约 16.5%**：Go 测试文件已全部纳入 git（`.gitignore` 只忽略 `/internal/reqlog/*_test.go`）；若 Coveralls 上显示 0%，先本地 `make cover-go-cobertura` 对照。
