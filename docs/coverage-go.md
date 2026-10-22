# Go 单元测试覆盖率实现文案（fnmusic-sync 后端实战：Go + CNB）

> 按 fnmusic-sync 项目实际实现整理（2026-10 实测）：无 ent，生成代码是 sqlc 的 `internal/db`、`internal/datadb`；本地基线约 1.5%，红线暂设 `lines: 0` / `diffLines: 0` 只收集不门禁。
> 方案可复制到其他 Go 项目，按项目实际调整包路径与生成代码排除项。

# 任务：为 Go 项目实现单元测试覆盖率上报与红线门禁（CNB 云原生构建）

## 背景与目标

- 构建平台是 CNB（cnb.cool），流水线配置在仓库根的 `.cnb.yml`（include `.cnb/workflows/*.yml`）。
- 使用 CNB 内置任务 `type: testing:coverage` 上报覆盖率：push 事件上报全量覆盖率，pull_request 事件额外计算增量覆盖率（diffLines 仅 PR 类事件生效）。
- 与 Node 侧覆盖率共存于同一仓库：Go 侧用 `key: go` / `name: Go Coverage`，Node 侧用 `key: node` / `name: Node Coverage`，各自写入 meta map 互不覆盖，徽章分别显示。
- 红线采用「棘轮策略」：初始值贴着当前基线往下留余量，防止误伤，后续随覆盖率提升逐步上调。

## 本项目当前实况（定红线前必须知道）

- 测试文件共 3 个，且**均未被 git 跟踪**（`.gitignore` 含 `*_test.go`）：`cmd/fnmusic-sync/goroutine_diag_test.go`、`internal/reqlog/reqlog_test.go`、`internal/webui/handler_upgrade_test.go`。
- 本地实测（含上述测试，范围 `./...`，排除 sqlc 生成目录）：total 约 **1.5%**；CI checkout 无测试文件，total 为 **0.0%**。
- 任何大于 0 的红线在当前基线都会误伤，因此暂设 `lines: 0` / `diffLines: 0`：只收集徽章数据、不门禁；等测试纳入 git 后再按棘轮上调。
- `internal/webui` 包带 `//go:build fpk` 标签，默认 `go test ./...` 不编译它（fpk 构建才参与）。
- 本项目**没有** `internal/ent`（那是其他项目的结构）；生成代码是 sqlc 生成的 `internal/db`、`internal/datadb`。

## 实现步骤

### 第 1 步：先测量当前覆盖率基线（定红线前必须做）

在本地运行并记录 total 覆盖率（范围与排除项按项目实际调整）：

```bash
go test -vet=off -coverpkg=$(go list ./... | grep -vE '/(db|datadb)$' | paste -sd, -) -coverprofile=/tmp/cov.out ./... && go tool cover -func=/tmp/cov.out | tail -1
```

### 第 2 步：确定覆盖率生成命令（直接内联 CI，不新增 make 目标）

覆盖率命令只服务 CI，新增 make 目标纯属间接层（YAML 里写 `make coverage`，看流水线还得再打开 Makefile 才知道跑什么）。直接把命令写进流水线 script：

```bash
go test -vet=off -coverpkg=$(go list ./... | grep -vE '/(db|datadb)$' | paste -sd, -) -coverprofile=coverage.out ./...
go tool cover -func=coverage.out
```

逐段解释（实现时保留为 CI 注释）：

- `-vet=off`：若项目存在「i18n.T() 等函数作 printf 参数」类 vet 告警会导致 go test 失败，需关闭（按项目实际取舍）。
- `-coverpkg` 排除生成代码目录：本项目生成代码是 sqlc 的 `internal/db`、`internal/datadb`，合计约 11% 语句（实测全量 13088 → 排除后 11672），计入分母会稀释指标，与排除 vendor 同理。**排除项按项目实际写，不要照抄其他项目的 ent**。
- `go list ./... | grep -v ... | paste -sd, -`：动态生成排除后的包清单，避免硬编码。
- `go tool cover -func=coverage.out`：终端按函数摘要，便于人工 Review。

注意：Makefile 里必须写 `$$(...)`（make 会转义 `$`）；直接写进 shell 脚本时用单 `$`，把 make 语法照抄进 CI 会炸（`$$` 在 bash 里是 PID）。

### 第 3 步：.gitignore 增加

```
coverage.out
coverage.html
```

### 第 4 步：流水线配置（push 与 pull_request 各用独立并行流水线，不并入既有 CI）

CNB 语义：事件键（如 `'**'`）下的值是 **Pipeline 数组，同事件多个数组条目 = 多条流水线并行执行**（并入同一个 pipeline 的 stages 会变串行，不要这样做）。复用 `.cnb/Dockerfile` 构建环境，挂 Go 构建缓存与模块缓存卷。

```yaml
"**":
  push:
    # ...既有流水线保持不动...
    # 新增：Go 覆盖率流水线（与既有流水线并行）
    - runner:
        tags: cnb:arch:amd64
      docker:
        build: .cnb/Dockerfile # 复用项目构建环境
        volumes:
          - /root/.cache/go-build:copy-on-write # Go 构建缓存
          - /go/pkg/mod:copy-on-write # Go 模块缓存
      stages:
        - name: 生成测试覆盖率
          # 命令按本项目实际编写（非从其他项目照抄）：
          # - 本项目无 internal/ent；生成代码是 sqlc 的 internal/db、internal/datadb
          #   （占约 11% 语句，计入分母会稀释指标，故排除）。
          # - webui 包带 //go:build fpk 标签，默认构建不参与测试。
          # -vet=off：i18n.T() 作 printf 参数会触发 vet non-constant format 报错。
          script:
            - go mod download
            - go test -vet=off -coverpkg=$(go list ./... | grep -vE '/(db|datadb)$' | paste -sd, -) -coverprofile=coverage.out ./...
            - go tool cover -func=coverage.out
        - name: 上报覆盖率（全量红线）
          type: testing:coverage
          options:
            key: go # 多报告数据键：与 Node 侧（key: node）区分，各自写入 meta map 互不覆盖
            name: Go Coverage # 徽章显示名，与 key 一对一对应
            lines: 0 # 当前基线约 1.5%（测试尚未纳入 git，CI 实测 0%），暂只收集不门禁；测试补齐后按棘轮上调
            lang: go # golang 格式报告必须指定，否则计算有误差
            pattern: "coverage.out"
            breakIfNoCoverage: true # 报告缺失直接失败，避免测试挂了却静默跳过
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
        build: .cnb/Dockerfile
        volumes:
          - /root/.cache/go-build:copy-on-write
          - /go/pkg/mod:copy-on-write
      stages:
        - name: 生成测试覆盖率
          # 命令按本项目实际编写（非从其他项目照抄）：
          # - 本项目无 internal/ent；生成代码是 sqlc 的 internal/db、internal/datadb
          #   （占约 11% 语句，计入分母会稀释指标，故排除）。
          # - webui 包带 //go:build fpk 标签，默认构建不参与测试。
          # -vet=off：i18n.T() 作 printf 参数会触发 vet non-constant format 报错。
          script:
            - go mod download
            - go test -vet=off -coverpkg=$(go list ./... | grep -vE '/(db|datadb)$' | paste -sd, -) -coverprofile=coverage.out ./...
            - go tool cover -func=coverage.out
        - name: 上报覆盖率（增量红线）
          type: testing:coverage
          options:
            key: go # 多报告数据键：与 Node 侧（key: node）区分，各自写入 meta map 互不覆盖
            name: Go Coverage # 徽章显示名，与 key 一对一对应
            diffLines: 0 # 增量门禁暂不启用（当前无测试基线，任何 PR 都会低于红线）；测试补齐后上调（建议 50）
            lines: 0 # 全量底线与 push 一致（当前基线约 1.5%，CI 实测 0%），暂只收集不门禁
            lang: go
            pattern: "coverage.out"
            breakIfNoCoverage: true
          exports:
            lines: COVERAGE_LINES
            diff_pct: COVERAGE_DIFF_PCT
        - name: 输出覆盖率
          script: 'echo "coverage: lines=$COVERAGE_LINES diff=$COVERAGE_DIFF_PCT"'
```

## 关键决策（实现时遵循，有冲突按项目实际调整并说明）

1. **排除生成代码，且排除项按项目实际写**：本项目无 ent，生成代码是 sqlc 的 `internal/db`、`internal/datadb`（合计约 11% 语句，实测 13088 → 11672），排除后基线 1.4% → 1.5%。照抄其他项目的 ent 排除项在本项目是空转（`grep -v` 匹配不到任何包）。
2. **红线初始值贴当前基线下沿**：当前基线约 1.5%（CI 因测试未跟踪为 0%），任何 >0 的红线都会每次误伤，故暂设 `lines: 0` / `diffLines: 0` 只收集；测试纳入 git 后按「基线 - 5~7 个百分点」重启门禁，每季度上调一次（棘轮）。
3. **PR 用 diffLines 管增量**：新代码必须过半覆盖（建议 50），这是长期提升覆盖率的主要驱动力；当前无测试基线，暂设 0。
4. **breakIfNoCoverage: true**：防止测试编译失败时覆盖率阶段静默通过造成假绿。注意实测：即使完全没有任何测试文件，`go test -coverprofile` 仍会产出全 0% 的 coverage.out，该选项不会触发——真正卡住流程的是 `lines` 阈值。
5. **独立并行流水线**：与既有 CI 并行，互不阻塞；不要合并进既有 pipeline 的 stages（会串行）。
6. **多报告 key/name**：同一仓库同时上报多语言覆盖率时（如 Go + Node），必须为每个 `testing:coverage` 指定不同 `key`（如 `go`/`node`）与一一对应的 `name`。key 决定数据写入 meta map 的键，重复会互相覆盖、最终只剩一个徽章；`name` 即徽章显示名（如 `Go Coverage`）。
7. **YAML 细节**：script 值含 `": "` 等特殊字符时整体用单引号包裹；`go mod download` 前置覆盖冷缓存场景；长命令行超过 yamlfmt `max_line_length: 120` 会被自动折行（折叠标量，语义不变，本地复现时复制解析后的一条命令即可）。
8. **覆盖率命令内联 CI、不进 Makefile**：仅 CI 使用的命令不走 make 间接层，流水线 script 自解释，本地复现直接复制 CI 里的命令（注意 shell 里是单 `$`，Makefile 里才是 `$$`）。
9. **测试文件必须纳入版本库才能真正门禁**：本项目 `.gitignore` 忽略 `*_test.go`，3 个测试文件均未被 git 跟踪，CI 覆盖率恒 0%。要启用红线，先把测试文件纳入版本库（从 .gitignore 移除 `*_test.go` 或按需 `git add -f`）。
10. **Codecov 并存**：本仓库在 GitHub Actions（`.github/workflows/coverage.yml` + `.github/codecov.yml`）另设 Codecov 上报（Go flag: go），与 CNB `testing:coverage` 的硬性红线门禁相互独立——Codecov 仅做趋势/PR 评论/徽章，其状态检查默认 `informational` 不阻塞。

## 验收标准

- CI 中的 `go test` 命令本地可手动复现，产出 coverage.out，`go tool cover -func` 有 total 且与第 1 步基线一致（本项目约 1.5%）。
- YAML 解析正确；push/pull_request 各新增一条独立流水线。
- 首次推送后确认：仓库「洞察 → 单元测试覆盖率」徽章生成，显示名与 `key` 对应（如 `Go Coverage`），数值为真实覆盖率（当前 CI 为 0%，属预期）。
