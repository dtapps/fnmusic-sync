# Go 单元测试覆盖率实现文案（fnmusic-sync 后端实战：Go + CNB）

> 按 fnmusic-sync 项目实际实现整理（2026-11 更新）：无 ent，生成代码是 sqlc 的 `internal/db`、`internal/datadb`；测试已全部纳入 git，本地基线约 16.5%，红线暂设 `lines: 0` / `diffLines: 0` 只收集不门禁。 覆盖率命令统一收敛到 Makefile 目标（`make cover-go` / `make junit-go` / `make cover-go-cobertura`），CNB 与 GitHub 侧共用同一份口径。 方案可复制到其他 Go 项目，按项目实际调整包路径与生成代码排除项。

# 任务：为 Go 项目实现单元测试覆盖率上报与红线门禁（CNB 云原生构建）

## 背景与目标

- 构建平台是 CNB（cnb.cool），流水线配置在仓库根的 `.cnb.yml`（include `.cnb/workflows/*.yml`）。
- 使用 CNB 内置任务 `type: testing:coverage` 上报覆盖率：push 事件上报全量覆盖率，pull_request 事件额外计算增量覆盖率（diffLines 仅 PR 类事件生效）。
- 与 Node 侧覆盖率共存于同一仓库：Go 侧用 `key: go` / `name: Go Coverage`，Node 侧用 `key: node` / `name: Node Coverage`，各自写入 meta map 互不覆盖，徽章分别显示。
- 红线采用「棘轮策略」：初始值贴着当前基线往下留余量，防止误伤，后续随覆盖率提升逐步上调。

## 本项目当前实况（定红线前必须知道）

- 测试文件共 21 个，**均已纳入 git**（`.gitignore` 只忽略 `/internal/reqlog/*_test.go`）： `cmd/fnmusic-sync/goroutine_diag_test.go`、`internal/{buildinfo,config,feiniu,lastfm,listenbrainz,mbid,model,playback,playlist,proxy,safego,strutil,updater}/*_test.go`、 `internal/webui/handler_upgrade_test.go` 等。
- 本地实测（范围 `./...`，排除 sqlc 生成目录）：total 约 **16.5%**（2026-11 实测，`make cover-go`）。
- 红线目前**未启用**（`lines` / `diffLines` 在 CNB 工作流里注释掉），只收集徽章数据；按「基线下沿 ≈ 10%」启用后再棘轮上调。
- `internal/webui` 包带 `//go:build fpk` 标签，默认 `go test ./...` 不编译它（fpk 构建才参与）。
- 本项目**没有** `internal/ent`（那是其他项目的结构）；生成代码是 sqlc 生成的 `internal/db`、`internal/datadb`。

## 实现步骤

### 第 1 步：先测量当前覆盖率基线（定红线前必须做）

在本地运行并记录 total 覆盖率（范围与排除项按项目实际调整）：

```bash
go test -vet=off -coverpkg=$(go list ./... | grep -vE '/(db|datadb)$' | paste -sd, -) -coverprofile=/tmp/cov.out ./... && go tool cover -func=/tmp/cov.out | tail -1
```

### 第 2 步：确定覆盖率生成命令（收敛到 Makefile 目标，CI 只调 make）

覆盖率命令同时被 CNB 流水线、GitHub Actions（Codecov / Coveralls）三处复用，**口径唯一定义在 Makefile 的「测试覆盖率」节**（扫描范围、排除清单、生成命令都写在那里），流水线只写 `make cover-go`，避免三处各抄一份命令日后漂移：

```make
# 清单配置（Makefile「测试覆盖率」节）：新增目录 / 生成代码只改这里
GO_COVER_SCAN = ./...
GO_COVER_EXCLUDE = /internal/db /internal/datadb
GO_COVER_TEST_FLAGS = -vet=off
GO_COVER_PKGS = $$(go list $(GO_COVER_SCAN) | grep -v $(GO_COVER_EXCLUDE_ARGS) | paste -sd, -)
GO_COVER_ARGS = $(GO_COVER_TEST_FLAGS) -coverpkg=$(GO_COVER_PKGS) -coverprofile=coverage.out $(GO_COVER_SCAN)

cover-go:
	go test $(GO_COVER_ARGS)
	go tool cover -func=coverage.out
```

逐段解释（实现时保留为 Makefile 注释）：

- `-vet=off`：若项目存在「i18n.T() 等函数作 printf 参数」类 vet 告警会导致 go test 失败，需关闭（按项目实际取舍）。
- `-coverpkg` 排除生成代码目录：本项目生成代码是 sqlc 的 `internal/db`、`internal/datadb`，合计约 11% 语句（实测全量 13088 → 排除后 11672），计入分母会稀释指标，与排除 vendor 同理。**排除项按项目实际写，不要照抄其他项目的 ent**。
- `go list ./... | grep -v ... | paste -sd, -`：动态生成排除后的包清单，避免硬编码；`$$(...)` 让 go list 在配方执行时才跑，平时任何 make 目标都不会触发包扫描。
- `go tool cover -func=coverage.out`：终端按函数摘要，便于人工 Review。

对应的三个目标：

| 目标                      | 产物                                  | 使用方                                                                            |
|---------------------------|---------------------------------------|-----------------------------------------------------------------------------------|
| `make cover-go`           | `coverage.out`                        | CNB `testing:coverage`、Codecov 覆盖率上传                                        |
| `make junit-go`           | `coverage.out` + `go-test-report.xml` | Codecov Test Analytics（gotestsum）                                               |
| `make cover-go-cobertura` | `coverage.xml`（Cobertura）           | Coveralls（gocover-cobertura，绕开其 golang parser 把包前缀压成 basename 的问题） |

注意：Makefile 里必须写 `$$(...)`（make 会转义 `$`）；直接写进 shell 脚本时用单 `$`，把 make 语法照抄进 CI 会炸（`$$` 在 bash 里是 PID）。

### 第 3 步：.gitignore 增加

```
coverage.out
coverage.html
coverage.xml
```

### 第 4 步：流水线配置（push 与 pull_request 各用独立并行流水线，不并入既有 CI）

CNB 语义：事件键（如 `'**'`）下的值是 **Pipeline 数组，同事件多个数组条目 = 多条流水线并行执行**（并入同一个 pipeline 的 stages 会变串行，不要这样做）。镜像统一用 `docker.cnb.cool/dtapp/golang/fnpack`（本仓库已无 Dockerfile，见 [ci-image.md](ci-image.md)），挂 Go 构建缓存与模块缓存卷。

```yaml
"**":
  push:
    # ...既有流水线保持不动...
    # 新增：Go 覆盖率流水线（与既有流水线并行）
    - runner:
        tags: cnb:arch:amd64
      docker:
        image: docker.cnb.cool/dtapp/golang/fnpack # 统一预置镜像（本仓库无 Dockerfile）
        volumes:
          - /root/.cache/go-build:copy-on-write # Go 构建缓存
          - /go/pkg/mod:copy-on-write # Go 模块缓存
      stages:
        - name: 安装所有依赖
          script: make deps
        - name: 生成测试覆盖率
          # 命令口径唯一定义在 Makefile 的 cover-go（-coverpkg 排除 sqlc 生成目录
          # internal/db、internal/datadb；-vet=off 规避 i18n.T() 的 vet 报错），
          # 与 GitHub 侧 codecov.yml / coveralls.yml 共用同一份实现。
          script: make cover-go
        - name: 上报覆盖率（全量红线）
          type: testing:coverage
          options:
            key: go # 多报告数据键：与 Node 侧（key: node）区分，各自写入 meta map 互不覆盖
            name: Go Coverage # 徽章显示名，与 key 一对一对应
            lines: 0 # 当前基线约 16.5%（测试已纳入 git），暂只收集不门禁；按 基线-10 棘轮上调
            lang: go # golang 格式报告必须指定，否则计算有误差
            pattern: "coverage.out"
            breakIfNoCoverage: false # 报告缺失只缺数据、不阻断流水线；低于 lines/diffLines 红线仍会拦截
          exports:
            lines: COVERAGE_LINES
            diff_pct: COVERAGE_DIFF_PCT
        - name: 输出覆盖率
          script: 'echo "coverage: lines=$COVERAGE_LINES diff=$COVERAGE_DIFF_PCT"'
  pull_request:
    # ...既有 PR 流水线保持不动...
    - runner:
        tags: cnb:arch:amd64
      docker:
        image: docker.cnb.cool/dtapp/golang/fnpack
        volumes:
          - /root/.cache/go-build:copy-on-write
          - /go/pkg/mod:copy-on-write
      stages:
        - name: 安装所有依赖
          script: make deps
        - name: 生成测试覆盖率
          # 命令口径唯一定义在 Makefile 的 cover-go（与 push 流水线同一份实现）。
          script: make cover-go
        - name: 上报覆盖率（增量红线）
          type: testing:coverage
          options:
            key: go # 多报告数据键：与 Node 侧（key: node）区分，各自写入 meta map 互不覆盖
            name: Go Coverage # 徽章显示名，与 key 一对一对应
            diffLines: 0 # 增量门禁暂不启用（当前无增量基线）；按 50 上调
            lines: 0 # 全量底线与 push 一致（当前基线约 16.5%），暂只收集不门禁
            lang: go
            pattern: "coverage.out"
            breakIfNoCoverage: false
          exports:
            lines: COVERAGE_LINES
            diff_pct: COVERAGE_DIFF_PCT
        - name: 输出覆盖率
          script: 'echo "coverage: lines=$COVERAGE_LINES diff=$COVERAGE_DIFF_PCT"'
```

## 关键决策（实现时遵循，有冲突按项目实际调整并说明）

1. **排除生成代码，且排除项按项目实际写**：本项目无 ent，生成代码是 sqlc 的 `internal/db`、`internal/datadb`（合计约 11% 语句，实测 13088 → 11672），排除后基线 1.4% → 1.5%。照抄其他项目的 ent 排除项在本项目是空转（`grep -v` 匹配不到任何包）。
2. **红线初始值贴当前基线下沿**：当前基线约 16.5%（测试已全部纳入 git），`lines` / `diffLines` 目前注释掉（等于 0，只收集徽章数据）；启用时取「基线 - 5~7 个百分点」，之后每季度上调一次（棘轮）。
3. **PR 用 diffLines 管增量**：新代码必须过半覆盖（建议 50），这是长期提升覆盖率的主要驱动力；当前无增量基线，暂设 0。
4. **breakIfNoCoverage: false（当前取值）**：报告缺失只缺数据、不阻断流水线；真正的红线卡点仍是 `lines` 阈值。注意实测：即使完全没有任何测试文件，`go test -coverprofile` 仍会产出全 0% 的 coverage.out，`breakIfNoCoverage` 不会触发。
5. **独立并行流水线**：与既有 CI 并行，互不阻塞；不要合并进既有 pipeline 的 stages（会串行）。
6. **多报告 key/name**：同一仓库同时上报多语言覆盖率时（如 Go + Node），必须为每个 `testing:coverage` 指定不同 `key`（如 `go`/`node`）与一一对应的 `name`。key 决定数据写入 meta map 的键，重复会互相覆盖、最终只剩一个徽章；`name` 即徽章显示名（如 `Go Coverage`）。
7. **YAML 细节**：script 值含 `": "` 等特殊字符时整体用单引号包裹；长命令行超过 yamlfmt `max_line_length: 120` 会被自动折行（折叠标量，语义不变，本地复现时复制解析后的一条命令即可）。
8. **覆盖率命令收敛到 Makefile 目标**：CNB 与 GitHub Actions（Codecov / Coveralls）三处复用同一份命令，写在 Makefile「测试覆盖率」节（`make cover-go` / `make junit-go` / `make cover-go-cobertura`），流水线只调 make，避免三处各抄一份日后漂移；清单（扫描范围、排除项）也在同一节，改目录只改一处。注意 Makefile 里 `$(...)` 要写成 `$$(...)`。
9. **测试文件必须纳入版本库才能真正门禁**：本项目测试文件已全部纳入 git（`.gitignore` 只忽略 `/internal/reqlog/*_test.go`），CI 覆盖率不再恒 0%，具备启用红线的前提。
10. **Codecov 并存**：本仓库在 GitHub Actions（`.github/workflows/codecov.yml` + `.github/codecov.yml`）另设 Codecov 上报（Go flag: go），与 CNB `testing:coverage` 的硬性红线门禁相互独立——Codecov 仅做趋势/PR 评论/徽章，其状态检查默认 `informational` 不阻塞。

## 验收标准

- `make cover-go` 本地可手动复现，产出 coverage.out，`go tool cover -func` 有 total 且与第 1 步基线一致（本项目约 16.5%）。
- YAML 解析正确；push/pull_request 各新增一条独立流水线。
- 首次推送后确认：仓库「洞察 → 单元测试覆盖率」徽章生成，显示名与 `key` 对应（如 `Go Coverage`），数值为真实覆盖率。
