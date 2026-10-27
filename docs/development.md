# 开发指南

从一台干净的机器搭出可编译、可测试、可打包的开发环境：工具从哪来、每个 Make 目标做什么、前端与数据层怎么改、哪些东西只能在真机验证。

> 相关：[README](../README.md)（快速上手）、[deployment.md](deployment.md)（安装与运维）、[contributing.md](contributing.md)（提交与 CI 门禁）、[database.md](database.md)（表结构与 sqlc）、[architecture.md](architecture.md)（模块划分）、[ci-image.md](ci-image.md)（CI 镜像里的工具链）。

## 环境要求

版本号不是拍脑袋写的，全部来自仓库里的三处硬证据：`go.mod`、`cmd/frontend/package.json`、CI 镜像 `Dockerfile` 的 `ARG`。

| 组件   | 仓库内要求的版本               | 依据                                                                           |
|--------|--------------------------------|--------------------------------------------------------------------------------|
| Go     | 1.27.3                         | `go.mod` 的 `go 1.27.3`；CI 基础镜像 `golang:1.27.3-trixie`                    |
| Node   | 26.12.1（CI）／25.9.0（nvmrc） | `.cnb/ci/Dockerfile` 的 `ARG NODE_VERSION`；`cmd/frontend/.nvmrc`              |
| pnpm   | 12.16.0（CI）／12.15.0（锁定） | `.cnb/ci/Dockerfile` 的 `ARG PNPM_VERSION`；`package.json` 的 `packageManager` |
| sqlc   | v1.31.1                        | `.cnb/ci/Dockerfile` 的 `ARG SQLC_VERSION`；生成代码头部同样记录该版本         |
| nfpm   | 2.48.0                         | `.cnb/ci/Dockerfile` 的 `ARG NFPM_VERSION`，仅 deb 打包需要                    |
| fnpack | 1.2.3                          | `.cnb/ci/Dockerfile` 的 `ARG FNPACK_VERSION`，仅 fpk 打包需要                  |
| UPX    | 5.2.1                          | `.cnb/ci/Dockerfile` 的 `ARG UPX_VERSION`，可选压缩                            |

Node 与 pnpm 的版本在仓库里有**两份互相不一致的记录**：CI 镜像用 `ARG NODE_VERSION=26.12.1` / `PNPM_VERSION=12.16.0`，而 `cmd/frontend/.nvmrc` 写 `25.9.0`、`cmd/frontend/package.json` 的 `packageManager` 写 `pnpm@12.15.0`。本地装哪个都不会立刻出错，但遇到「本地过、CI 不过」时，先按 CI `Dockerfile` 的 `ARG` 对齐。

### 工具链从哪来

仓库里没有任何 `tools.go` 或 `go:generate` 清单，工具只有两条来源：

| 来源                             | 覆盖的工具                                                                       | 版本策略                 |
|----------------------------------|----------------------------------------------------------------------------------|--------------------------|
| `make tool-deps`（`Makefile`）   | sqlc、golangci-lint、govulncheck、gojq、yamlfmt、shfmt、sql-formatter、dockerfmt | 全部 `@latest`，不锁版本 |
| 手工 `go install` / 系统包管理器 | markdownfmt、nfpm、fnpack、upx                                                   | 自行决定                 |
| CI 镜像 `.cnb/ci/Dockerfile`     | 上述全部（含 markdownfmt、nfpm、fnpack、upx）+ Node + pnpm                       | 全部 `ARG` 锁死，可复现  |

`make tool-deps` 的实际安装清单（逐条对应 `Makefile` 的 `tool-deps` 目标）：

| 工具          | 安装命令                                                                       | 用途                                  |
|---------------|--------------------------------------------------------------------------------|---------------------------------------|
| sqlc          | `go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest`                          | 生成 `internal/db`、`internal/datadb` |
| golangci-lint | `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest`     | `make lint-go` / `make check`         |
| govulncheck   | `go install golang.org/x/vuln/cmd/govulncheck@latest`                          | 漏洞扫描（CI 的安全流水线另行处理）   |
| gojq          | `go install github.com/itchyny/gojq/cmd/gojq@latest`                           | `format-json` 格式化 fpk 的 JSON      |
| yamlfmt       | `go install github.com/google/yamlfmt/cmd/yamlfmt@latest`                      | `format-yaml`                         |
| shfmt         | `go install mvdan.cc/sh/v3/cmd/shfmt@latest`                                   | `format-shell`（只覆盖 `fpkg/cmd/*`） |
| sql-formatter | `go install github.com/wasilibs/go-sql-formatter/v15/cmd/sql-formatter@latest` | `format-sql`                          |
| dockerfmt     | `go install github.com/reteps/dockerfmt@latest`                                | `format-docker`                       |

三个容易踩的点：

1. **`markdownfmt` 不在 `tool-deps` 里。** `format-markdown` 检测不到可执行文件时只打印 `⚠️ markdownfmt 未安装，跳过 Markdown 格式化。` 然后继续（`exit 0`），所以漏装不会被发现。手工装：

   ```bash
   go install github.com/Kunde21/markdownfmt/v3/cmd/markdownfmt@latest
   ~/go/bin/markdownfmt -w docs/development.md
   ```

2. **`nfpm`、`fnpack`、`upx` 也不在 `tool-deps` 里。** 它们决定 `package-linux-*` 能不能跑；而 `make check` 依赖 `package-linux`（见下节），所以缺 `fnpack` 时 `make check` 会直接失败，而不是只报一个 lint 错。

3. **`tool-deps` 用 `@latest`，CI 镜像用固定 `ARG`。** 本地 `golangci-lint` 比 CI 新时会多出一批告警。看到只在本地出现的 lint 报错，先比版本：CI 是 `GOLANGCI_LINT_VERSION=v2.15.0`。`sqlc` 同理——生成文件头写着 `sqlc v1.31.1`，用更新版本重新 `make sqlc` 会把这行改掉，产出与仓库现有生成代码不一致的 diff。

## Make 目标参考

`Makefile` 顶部集中定义了几个全局变量，读目标之前先知道它们：`VERSION`（`git describe --tags --always --dirty`，剥掉 `refs/tags/` 前缀）、`PKG_VERSION`（去掉 `v` 前缀，进 deb/fpk 元数据）、`COMMIT`、`BUILDTIME`、`REPO_SOURCE`（默认 `cnb`）、`BUILD_TAG`（固定 `fpk`）、`ENABLE_UPX` / `ENABLE_ARCHIVE`（默认 1）。

| 目标                                | 做什么                                                                                                 | 产物                                                                                   | 依赖                                                                     |
|-------------------------------------|--------------------------------------------------------------------------------------------------------|----------------------------------------------------------------------------------------|--------------------------------------------------------------------------|
| `tool-deps`                         | 逐个 `go install ...@latest` 装 8 个工具                                                               | `$(go env GOPATH)/bin/*`                                                               | Go、网络                                                                 |
| `deps`                              | `go mod tidy`、`go mod download`、`pnpm --dir cmd/frontend install --frozen-lockfile --prefer-offline` | module 缓存、`cmd/frontend/node_modules`                                               | Go、pnpm                                                                 |
| `update-deps`                       | `go get -u ./...`、`go mod tidy`、`pnpm self-update`、`pnpm update --latest`                           | 改写 `go.mod`、`go.sum`、`pnpm-lock.yaml`                                              | 同上                                                                     |
| `sqlc`                              | 分别进 `internal/db`、`internal/datadb` 执行 `sqlc generate`                                           | `db.go`、`models.go`、`querier.go`、`queries.sql.go`                                   | sqlc                                                                     |
| `dev`                               | 只打印双终端启动说明，不启动任何进程                                                                   | 无                                                                                     | 无                                                                       |
| `dev-server`                        | `go run ./cmd/devserver`，监听 `:8080`                                                                 | `tests/configs/config.yaml`、`tests/configs/state.db`、`tests/logs/`                   | `cmd/devserver/main.go` 存在（见下文）                                   |
| `dev-frontend`                      | `cd cmd/frontend && pnpm dev`，Vite `:5173`，把 `/app/fnmusic-sync/api` 代理到 `:8080`                 | 无（内存热更新）                                                                       | `pnpm install`                                                           |
| `build-frontend`                    | `sed` 把 `PKG_VERSION` 写进 `cmd/frontend/package.json`，再 `pnpm build`                               | `internal/webui/www/app.js`、`app.css`、`chunks/`、`images/`                           | pnpm                                                                     |
| `build-linux-amd64`                 | 编译两份 amd64 二进制：不带 tag / 带 `fpk` tag；各自 UPX 压缩 + `tar.gz`                               | `bin/fnmusic-sync-linux-amd64{,.tar.gz}`、`bin/fnmusic-sync-fpk-linux-amd64{,.tar.gz}` | Go                                                                       |
| `build-linux-arm64`                 | 同上，arm64                                                                                            | `bin/fnmusic-sync-linux-arm64{,.tar.gz}`、`bin/fnmusic-sync-fpk-linux-arm64{,.tar.gz}` | Go                                                                       |
| `build-linux-all`                   | `backup-version` → `build-frontend` → `build-linux-amd64` → `build-linux-arm64` → `restore-version`    | 上面全部                                                                               | Go、pnpm                                                                 |
| `package-linux-deb`                 | 用**不带 tag** 的二进制 + `.nfpm.yaml` 打两次 `nfpm package -p deb`                                    | `bin/fnmusic-sync_amd64.deb`、`bin/fnmusic-sync_arm64.deb`                             | nfpm、`build-linux-all`                                                  |
| `package-linux-fpk`                 | 把**带 tag** 的二进制拷进 `fpkg/app/fnmusic-sync`，替换 manifest 占位符后 `fnpack build`               | `bin/fnmusic-sync-amd64.fpk`、`bin/fnmusic-sync-arm64.fpk`                             | fnpack、`build-linux-all`                                                |
| `package-linux-all`                 | `package-linux-deb` + `package-linux-fpk`                                                              | `bin/` 下 deb + fpk 共 4 个包                                                          | 上述两者                                                                 |
| `package-linux`                     | `package-linux-all` 的别名                                                                             | 同上                                                                                   | 同上                                                                     |
| `format`                            | 顺序执行 8 个 `format-*`：go、frontend、yaml、json、markdown、shell、sql、docker                       | 就地改写源文件                                                                         | gofmt、pnpm、gojq、yamlfmt、markdownfmt、shfmt、sql-formatter、dockerfmt |
| `check`                             | `lint-go` + `lint-go-fix` + `lint-frontend` + `package-linux`                                          | 安装包（顺带校验打包链路）                                                             | 全部格式化/lint 工具 + nfpm + fnpack                                     |
| `lint-go`                           | `go vet ./...` + `golangci-lint run ./...`                                                             | 无                                                                                     | golangci-lint                                                            |
| `lint-go-fix`                       | `golangci-lint run --fix ./...`                                                                        | 就地修复                                                                               | golangci-lint                                                            |
| `lint-frontend`                     | 要求 `node_modules` 存在，再 `pnpm run lint`（`tsc --noEmit` + `oxlint`）与 `pnpm run format:check`    | 无                                                                                     | pnpm                                                                     |
| `clean`                             | `rm -rf bin && mkdir bin`                                                                              | 空 `bin/`                                                                              | 无                                                                       |
| `clean-binaries`                    | 删 4 个裸二进制，保留 `.tar.gz` 与安装包                                                               | —                                                                                      | `bin/` 已有产物                                                          |
| `clean-archives`                    | 删裸二进制 + `.tar.gz`，只留安装包                                                                     | —                                                                                      | 同上                                                                     |
| `backup-version`                    | 把 `cmd/frontend/package.json` 复制成 `bin/package.json.ver.bak`（已存在则沿用）                       | 备份文件                                                                               | 无                                                                       |
| `restore-version`                   | 用备份覆盖回 `package.json` 并删备份                                                                   | —                                                                                      | 有备份                                                                   |
| `sync` / `sync-cnb` / `sync-github` | 多远程 `fetch` + `merge --ff-only`                                                                     | —                                                                                      | 配好 `origin` / `github` remote                                          |
| `push` / `push-force`               | 依次推 `origin`、`gitee`、`gitcode`、`github`、`gitlab`                                                | —                                                                                      | 上述 remote 全部存在                                                     |

`format` 的覆盖面有明确边界，别以为跑完就万事大吉：

| 子目标            | 覆盖范围                                                                                                 | 备注                                                 |
|-------------------|----------------------------------------------------------------------------------------------------------|------------------------------------------------------|
| `format-go`       | 仓库内全部 Go：`gofmt -w -s .`、`go fmt ./...`、`go fix ./...`                                           | 含 `cmd/frontend` 之外的目录                         |
| `format-frontend` | `cmd/frontend` 的 `src/**` 以及 `internal/webui/www/**`（排除 `www/app.css`、`www/app.js`）              | 走 `prettier` + `prettier-plugin-svelte`；失败只警告 |
| `format-yaml`     | `.cnb`、`.github`、`.gitlab`、`.cnb.yml`、`.nfpm.yaml`、`internal/db`、`.golangci.yml`、`.gitlab-ci.yml` | 用 `.yamlfmt` 配置                                   |
| `format-json`     | `fpkg/wizard/*`、`fpkg/app/ui/config`、`fpkg/config/privilege`、`fpkg/config/resource`                   | 用 `gojq .` 重排                                     |
| `format-markdown` | 全仓 `*.md`（排除 `node_modules`、`.git`、`.vscode`、`.idea`、`.codebuddy`、`.trae`、`.mimocode`）       | 缺 `markdownfmt` 时静默跳过                          |
| `format-shell`    | **只有** `fpkg/cmd/*`，参数 `shfmt -w -i 2 -ci -bn -s -ln bash`                                          | `scripts/*.sh` 不在范围内                            |
| `format-sql`      | `internal/db/*.sql`、`internal/datadb/*.sql`，`sql-formatter --fix -l sql`                               | 格式化会改写 SQL 排版                                |
| `format-docker`   | `.cnb`、`.github`、`.gitlab` 下的 `Dockerfile*`                                                          | 先找 `dockerfmt`，找不到就跳过                       |

### 新人最常用的 5 条命令

```bash
make deps                                   # 拉 Go 与前端依赖（每次切分支后跑一次）
make sqlc                                   # 改过 schema.sql / queries.sql 后必跑
make dev-server                             # 终端 1：Go API :8080
make dev-frontend                           # 终端 2：Vite :5173（热更新）
make build-linux-amd64                      # 只验编译，产物在 bin/
```

提交前把最后一步换成 `make format && make check`（顺序与原因见 [contributing.md](contributing.md)）。

## 本地运行：没有 fnOS 环境怎么办

先把能力边界划清，省得在 Mac 上折腾一天发现方向不对。

| 想在本地做                             | 可行 | 说明                                                                                            |
|----------------------------------------|------|-------------------------------------------------------------------------------------------------|
| 跑单元测试                             | ✅   | `go test ./...`、`pnpm --dir cmd/frontend test`，不依赖任何 socket                              |
| 调前端页面与 API                       | ✅   | `make dev-server` + `make dev-frontend`，`cmd/devserver` 在 `:8080` 复刻了 webui 的全部 handler |
| 验打包链路                             | ✅   | `make package-linux-all`，但需要 `nfpm` + `fnpack`（Linux amd64 宿主）                          |
| `--check` 体检                         | ⚠️   | 只能看到「socket 不存在」；没有真实 socket 时结论必然是失败                                     |
| socket 接管 / 502 复现                 | ❌   | 依赖 `/var/run/trim_music.socket` 与 `www-data` 的 nginx，只能在 fnOS 真机验证                  |
| 播放事件识别（track_play / HLS 分片）  | ❌   | 事件来自飞牛 App 的真实请求，必须真机抓                                                         |
| 歌单创建 / 曲库索引                    | ❌   | 依赖 trim-music 的内部 API，必须真机                                                            |
| fpk 网关（`app.sock` + `X-Trim-*` 头） | ❌   | 只有飞牛统一网关会注入这些头                                                                    |

### `--check` 与 `sockets` 子命令

`--check` 走的是 `cmd/fnmusic-sync/check.go`，输出四类行：`监听` / `上游` 的归属（`trim`、`proxy`、`stale`、`none`）、两个 socket 的权限与属主（`%#o` + uid:gid）、权限不允许 other 访问时的 `警告` 行、以及 `官方后端连通性(<path>)：正常|失败`。它**不接管** socket，也不会启动代理。

`fnmusic-sync sockets` 是同一套探测的详细版（`internal/proxy/socket_status.go`），逐项打印 `存在`、`类型`、`权限`、`属主`、`归属`、`可连接`、`说明`，最后给一句 `结论：✅ 正常（监听可达、飞牛音乐在线）` 或 `结论：❌ 异常 —— <message>`。判定规则写得很直白：监听 socket 只要求「存在 + 是 socket + 可连接」；上游 socket 还额外要求归属是 `trim`。

本地（无 fnOS）跑 `--check` 的实际形态：两个路径都 `归属=none`，`权限` 行打印 `(不存在)`，随后 `官方后端连通性(/var/run/trim_music_upstream.socket)：失败 (dial unix ...: connect: no such file or directory)`，进程以 `上游检查失败` 退出。这不是 bug，是缺环境。

### 前台跑代理需要什么

直接 `./fnmusic-sync`（不带子命令）会依次做：读配置（缺则生成）→ 打开 `state.db` 与 `data.db` → 拿 `flock` 单实例锁 → `Takeover.Prepare()`（把官方 socket `rename` 到 upstream）→ 在原路径 `net.Listen("unix")` → 上游健康检查 → 启歌单同步 → 启 Web UI（仅 fpk 构建）。

非 root 会在这些地方失败或降级：

| 动作                                   | 位置                          | 非 root 结果                                           |
|----------------------------------------|-------------------------------|--------------------------------------------------------|
| `os.Rename` 官方 socket                | `internal/proxy/socket.go`    | `接管 socket 失败 (/var/run/trim_music.socket -> ...)` |
| 在 `/var/run` 建 socket                | `Takeover.Listen`             | `监听 /var/run/trim_music.socket 失败: ...`            |
| `chown` socket 到官方属主              | `Takeover.applyOwner`         | 只打 WARN，不致命                                      |
| 创建 `/run/fnmusic-sync/run.lock`      | `main.go` 的 `acquireRunLock` | 打 WARN `无法获取运行锁，跳过单实例保护` 后继续        |
| 写 `/var/lib/fnmusic-sync`、`/var/log` | `db.Open`、`setupLogger`      | 前者致命（`打开数据库失败`），后者降级为仅控制台       |

结论：**真机调试一律 `sudo`**。本地想跑通进程骨架，最小代价是给一个能写的 `TRIM_APPDEST`（见下一条），让它走 fpk 路径分支。

### socket 路径能不能改

不能。两个路径是编译期常量（`internal/buildinfo/buildinfo.go` 的 `DefaultListenSocket` / `DefaultUpstreamSocket`），`server` 段只有 `socket_mode`（权限）和 `upstream_wait`（等待超时）两个键。`server.socket_mode: 0` 的语义是「沿用官方 socket 权限」，不是「路径 0」。

同理，当前 CLI 只有 `--check`、`--debug`、`--upstream-wait` 三个 flag（`cmd/fnmusic-sync/root_cmd.go`）。早期设计文档 `docs/ai.md` 里出现的 `--config`、`--log-dir` **在现有代码中不存在**，配置/状态/日志路径完全由运行模式推导（`cmd/fnmusic-sync/runtime.go`）。

运行模式只有两种，判定条件很粗暴：环境变量 `TRIM_APPDEST` 非空即 fpk 模式。

| 路径   | 传统模式                         | fpk 模式                                              |
|--------|----------------------------------|-------------------------------------------------------|
| 配置   | `/etc/fnmusic-sync/config.yaml`  | `$TRIM_PKGETC/config.yaml`（缺省 `<APPDEST>/../etc`） |
| 状态   | `/var/lib/fnmusic-sync/state.db` | `$TRIM_PKGVAR/state.db`（缺省 `<APPDEST>/../var`）    |
| 日志   | `/var/log/fnmusic-sync`          | `$TRIM_PKGVAR/logs`                                   |
| 运行锁 | `/run/fnmusic-sync/run.lock`     | `$TRIM_PKGVAR/run.lock`                               |

所以在 Mac/Linux 上把 `TRIM_APPDEST` / `TRIM_PKGETC` / `TRIM_PKGVAR` 指到临时目录，就能把配置、数据库、日志都留在本地，不影响系统的 `/var`。socket 那两个路径仍然是硬编码的。

### `cmd/devserver`：本地 Web UI 后端

`make dev-server` 跑的是 `cmd/devserver/main.go`，行为要点：

- 监听 `:8080`，所有路由前缀与生产一致（`/app/fnmusic-sync/api/...`）。
- 参数全带默认值：`-addr :8080`、`-config tests/configs/config.yaml`、`-state tests/configs/state.db`、`-logdir tests/logs`。
- 配置文件不存在时用 `config.SaveDefault` 生成一份；日志文件不存在时写两行模拟日志。
- 打开的是 `state.db` 的真实 sqlite（路径由 `-state` 换扩展名得来），所以用户列表 / 运行状态是真数据。
- 没有网关头（`X-Trim-Userid` / `X-Trim-Username` / `X-Trim-Isadmin`）时，`readGatewayUser` 返回 `{UID:"0", Username:"local", IsAdmin:true}`——默认就是管理员，方便测全部功能。
- 静态文件用 `os.DirFS("internal/webui/www")` 从磁盘读，因此**必须在仓库根目录启动**；`index.html` 由它用 `html/template` 填入 `{{.BaseURL}}`。

最大的坑：**这个目录没有被 git 跟踪**。`.gitignore` 里有一行 `/cmd/dev*`。新克隆的仓库里没有 `cmd/devserver/`，`go run ./cmd/devserver` 会因找不到该目录而失败。要么找原作者要这个文件，要么直接 `make dev-frontend` 之后用任意静态服务器，要么把 `.gitignore` 的这一行去掉再提交（这会改变仓库约定，需要先说清楚）。

另外它**不是** `internal/webui` 的薄壳：`webui` 包带 `//go:build fpk`，devserver 导不进来，所以它把 handler 逻辑复制了一份。新增/修改 API 时必须同时改两处，否则本地页面和真机行为会分叉（见 [contributing.md](contributing.md) 的「新增功能 checklist」）。

## 前端开发

技术栈：Svelte 5（runes）+ Vite 8 + Tailwind 4（走 `@tailwindcss/postcss`）+ `svelte-i18n` 4 + vitest 5 + `oxlint` + `prettier`（含 `prettier-plugin-svelte`）。包管理只有 pnpm。

```
cmd/frontend/
├── src/App.svelte            根组件：tab 切换、主题/语言初始化、飞牛 SDK 接入
├── src/app.ts                Vite 入口（rollupOptions.input 指向它）
├── src/pages/*.svelte        8 个页面：Users / UserList / Settings / State / Logs / Socket / Playlist / SyncLog
├── src/lib/api.ts            fetch 封装，BASE_URL 取自 window.BaseURL
├── src/lib/stores.ts         全局 store
├── src/lib/types.ts          与 Go 侧 JSON 结构对齐的类型
├── src/lib/trim.ts           飞牛宿主 SDK（主题、语言、平台配置）
├── src/lib/i18n/{zh-CN,en}.json + index.ts
├── src/lib/components/       Button / Card / Input / Select / Switch / Badge / Toast / Dialog / Collapsible / PasswordInput
├── src/test/*.test.ts        vitest 用例
├── dev.sh / build.sh         薄封装脚本
└── vite.config.ts / vitest.config.ts / svelte.config.js / tsconfig.json
```

两个脚本与 Make 目标的关系需要分清：

| 入口                    | 做什么                                                                                                             |
|-------------------------|--------------------------------------------------------------------------------------------------------------------|
| `make dev-frontend`     | `cd cmd/frontend && pnpm dev`，不装依赖                                                                            |
| `cmd/frontend/dev.sh`   | 先 `pnpm install --frozen-lockfile`（失败则退回 `pnpm install`），再 `pnpm dev`                                    |
| `make build-frontend`   | 只做 `sed` 改版本 + `pnpm build`，**不走** `build.sh`                                                              |
| `cmd/frontend/build.sh` | 清 `app.js` / `app.css` / `app.js.map` / `chunks/` / `assets/`，`pnpm build`，缺 `index.html` 时补一份 Go 模板版本 |

构建产物落到 `internal/webui/www/`，由 `internal/webui/handler.go` 的 `//go:embed www` 嵌进二进制。这里有两个必须记住的事实：

1. **`internal/webui` 整包带 `//go:build fpk`。** 也就是说只有 `-tags fpk` 构建出来的 fpk 二进制里有 Web UI；deb 二进制走的是 `cmd/fnmusic-sync/webui_stub.go`，`startWebUI` 直接返回 `(nil, nil)`。`go test ./...` 默认也不编译它（覆盖率流水线因此显式排除了这个包）。
2. **`index.html` 是手写维护的，不是 Vite 产物。** `vite.config.ts` 用 `rollupOptions.input = 'src/app.ts'`，输出文件名固定（`app.js`、`chunks/[name].js`、`[name][extname]`），不生成 `index.html`。`internal/webui/www/*` 被 `.gitignore` 整体忽略，只 `!index.html` 例外。

### i18n 维护规则

- 两份语言文件：`src/lib/i18n/zh-CN.json`（默认/`fallbackLocale`）与 `src/lib/i18n/en.json`，在 `src/lib/i18n/index.ts` 里 `addMessages` 注册。
- 语言映射是白名单式的：`zh-TW` / `zh-HK` 都降级到 `zh-CN`，`en-*` 一律归 `en`，其它语言回退 `zh-CN`（`mapLanguage`）。
- 键必须两边同时加、同名同层级。仓库里**没有**自动的键对齐测试（`src/test/i18n.test.ts` 只覆盖 `mapLanguage`），漏键不会在 CI 报错，表现是页面上直接显示键名。加键时用 `grep -c` 或肉眼比对两份 JSON。
- 仓库里的中文文案偏好全角标点与「飞牛」而非「fnOS」的地方，改动时跟着同文件既有风格走。

### 新增页面/组件往哪放

| 要加的东西  | 落点                                                                                                     |
|-------------|----------------------------------------------------------------------------------------------------------|
| 新页面      | `src/pages/XxxPage.svelte`，在 `App.svelte` 里 import 并加进 tab 分支                                    |
| 复用组件    | `src/lib/components/Xxx.svelte`，并在 `src/lib/components/index.ts` 里 `export { default as Xxx }`       |
| 新 API 调用 | `src/lib/api.ts` 加 `apiCall<T>('/xxx')`；返回结构在 `src/lib/types.ts` 定义                             |
| 新 store    | `src/lib/stores.ts`                                                                                      |
| 新翻译      | `src/lib/i18n/zh-CN.json` + `src/lib/i18n/en.json`                                                       |
| 新单元测试  | `src/test/*.test.ts`（`vitest.config.ts` 的 `include` 是 `src/**/*.ts`，`.svelte` 组件不在覆盖率分母里） |

## 数据层与代码生成

两个 sqlite 库，两套 sqlc 配置，别搞混：

| 库         | 目录              | 内容                                                                                     | 生成命令                              |
|------------|-------------------|------------------------------------------------------------------------------------------|---------------------------------------|
| `state.db` | `internal/db`     | 运营状态：`run_status`（推送统计）、`users`（按 token 一行）、`playback_log`、`sync_log` | `cd internal/db && sqlc generate`     |
| `data.db`  | `internal/datadb` | 数据类：`track_mbid_map`（飞牛 GUID ↔ MBID 映射）                                        | `cd internal/datadb && sqlc generate` |

`make sqlc` 就是把上面两条串起来。两边的 `sqlc.yaml` 除了 `package` 名不同，其余一致：`engine: sqlite`、`sql_package: database/sql`、`emit_json_tags`、`emit_interface`、`emit_empty_slices`。

schema 改动流程：

1. 改 `internal/<db|datadb>/schema.sql`（表结构注释放这里）。
2. 改 `internal/<db|datadb>/queries.sql`。**不能写注释**——`Makefile` 的 `sqlc` 目标注释明确写着 sqlc v1.31 在多查询文件上有解析 bug。
3. `make sqlc`。
4. `git diff` 检查生成文件：`db.go`、`models.go`、`querier.go`、`queries.sql.go`。`db.go` 头部带 `sqlc v1.31.1`，工具版本不同会污染 diff。
5. 需要在业务里用到新查询时，走 `internal/*/store.go` 的封装，不要直接在调用方拿 `*Queries`——现有代码（`db.Store`、`datadb.Store`）已经把所有 SQL 使用收在 store 里。

运行期两个行为值得知道：`db.Open` 用 `//go:embed schema.sql` 在建库时执行整份 schema（全部 `CREATE TABLE IF NOT EXISTS` + `CREATE INDEX IF NOT EXISTS`，幂等），随后跑一次 `migrateUsersTokenKey`（旧版按 `username` 唯一 → 新版按 `token_prefix` 唯一，重建表并保留数据）与 `ReparseUserAgents`（best-effort 回填 UA）。连接池被钉成单连接（`SetMaxOpenConns(1)`），就是为了避开 sqlite 的 `database is locked`。

## 测试

| 范围        | 命令                                             | 产物/说明                                          |
|-------------|--------------------------------------------------|----------------------------------------------------|
| Go 全量     | `go test ./...`                                  | 默认不编译 `internal/webui`（`fpk` tag）           |
| Go 单包     | `go test ./internal/playback/ -v`                | 定位时最常用                                       |
| Go 单个用例 | `go test ./internal/playback/ -run TestParse -v` | `-run` 支持正则                                    |
| 覆盖率      | 见下方 CI 口径命令                               | `coverage.out`（仓库根，已 gitignore）             |
| 前端全量    | `pnpm --dir cmd/frontend test`（= `vitest run`） | 用例在 `src/test/`                                 |
| 前端覆盖率  | `pnpm --dir cmd/frontend test:coverage`          | `cmd/frontend/coverage/lcov.info` + 终端 text 报告 |

CI 用的 Go 覆盖率命令与本地一致（原始出处 `.cnb/workflows/push-coverage.yml`，`docs/coverage-go.md` 有完整复盘）：

```bash
go test -vet=off \
  -coverpkg=$(go list ./... | grep -vE '/(db|datadb)$' | paste -sd, -) \
  -coverprofile=coverage.out ./...
go tool cover -func=coverage.out | tail -1
```

三个细节：`-vet=off` 是因为 `i18n.T()` 当 printf 实参时会触发 vet 的 non-constant format 报错；`grep -vE '/(db|datadb)$'` 排除 sqlc 生成的包（约占 11% 语句）；`internal/webui` 带 `fpk` tag，默认不参与。

测试组织约定：

- Go：与被测文件同包同目录的 `*_test.go`；重逻辑（解析、阈值、socket 判定、updater）都有对应用例，新增纯函数顺手补一个。
- 单测不要碰真实 `/var/run` 与网络：`internal/proxy/socket_test.go` 用 `t.TempDir()` 造假 socket 是现成范式。
- 前端：`src/test/` 下按模块名命名，只测 `.ts` 逻辑；`.svelte` 组件暂无测试，`vitest.config.ts` 的 `environment` 是 `node`（不是 jsdom），写组件测试前要先改配置并加 `@testing-library/svelte`。
- 覆盖率文档索引：`docs/coverage-go.md`、`docs/coverage-node.md`、`docs/codecov.md`、`docs/coveralls.md`。

## 调试技巧

### 日志

| 手段                     | 效果                                                                                                       |
|--------------------------|------------------------------------------------------------------------------------------------------------|
| `logging.level: debug`   | 主日志（slog）降级到 DEBUG，热加载即时生效，不用重启                                                       |
| `--debug`                | 打开请求明细日志：`capture.log`、`feiniu.log`、`lastfm.log`、`listenbrainz.log`                            |
| `wizard_debug=1`         | fpk 模式下的等价开关（`main.go` 的 `isDebugEnv`，接受 `1/true/on/yes`），由 `$TRIM_PKGETC/config.env` 注入 |
| `logging.enabled: false` | 只写控制台不写文件；命令行没有 `--log-dir`，想不落盘只能改配置                                             |

注意 `--debug` 与 `logging.level` 是**两条独立通道**：前者只控制请求明细文件，不改变 slog 级别；后者只控制主日志。想同时看两者，两个都要开。

`sudo fnmusic-sync service debug` 的做法是直接改 unit 文件里的 `ExecStart` 行追加 ` --debug`，然后 `daemon-reload` + `restart`；`service nodebug` 反向操作。它不读配置文件、也不持久化到配置里——这是 systemd 层的开关。

### goroutine 诊断

`cmd/fnmusic-sync/goroutine_diag.go` 提供的是**自研 watchdog，不是 pprof**。全仓没有 `net/http/pprof`，也没有任何 profiling 端点（`grep -rn pprof` 为空）。它的行为：

- 只在 `--debug` 或 `wizard_debug` 打开时才启动（`main.go` 里 `startGoroutineDumper`）。
- 每 30s 读一次 `runtime.NumGoroutine()`；低于阈值 60 什么都不做。
- 越过阈值才抓全量栈，按白名单过滤（`lumberjack`、`os/signal`、`fsnotify`、`viper.WatchConfig`、`database/sql.connectionOpener`、`net/http.Server.Serve`、`*UnixListener.Accept`、`reqlog`/`CaptureLogger` worker、`playback.Detector.expireLoop`、`playlist.SyncService.run` 等），只把「阻塞 ≥ 5 分钟」的、以及「`persistConn` / `http2.` 长连接空闲 ≥ 5 分钟」的栈打 WARN。
- 两次实际 dump 至少间隔 5 分钟，防日志风暴。
- `kill -USR1 <pid>` 是手动全量通道：把完整快照追加写到 `<日志目录>/goroutine-dump.txt`（lumberjack 轮转，参数同主日志），不受阈值与冷却限制。
- SIGUSR1 由这个 goroutine 独占，主流程只监听 SIGINT/SIGTERM，互不冲突。

```bash
# 真机抓一次现场
sudo kill -USR1 "$(pidof fnmusic-sync)"
sudo tail -200 /var/log/fnmusic-sync/goroutine-dump.txt
```

### 建议的断点位置

不需要 pprof 就能定位九成问题，按「请求进—事件出—推送发」三段下断：

| 现象方向       | 文件与函数                                                                                                                      |
|----------------|---------------------------------------------------------------------------------------------------------------------------------|
| 接管失败 / 502 | `internal/proxy/socket.go` 的 `Takeover.Prepare`、`Takeover.Listen`、`effectiveMode`                                            |
| 请求转发异常   | `internal/proxy/proxy.go` 的 `ServeHTTP`（上游失败打 `上游请求失败`）、`internal/proxy/health.go` 的 `CheckUpstream`            |
| 播放事件识别   | `internal/playback/detector.go`（`track_play` 候选、HLS 分片佐证）、`internal/playback/manager.go` 的 `Start` / `CheckScrobble` |
| 用户识别       | `internal/playback/user.go` 的 `Resolve` / `probe`（日志关键词 `识别到用户`）                                                   |
| 推送失败       | `internal/scrobbler/*`、`internal/lastfm/client.go`、`internal/listenbrainz/client.go`                                          |
| 歌单同步       | `internal/playlist/sync.go` 的 `syncProvider`、`internal/playlist/{lastfm,listenbrainz}_sync.go`                                |
| 配置热加载     | `internal/config/config.go` 的 `Watch` / `reload`                                                                               |

## 代码风格与门禁

`golangci-lint` 配置在 `.golangci.yml`（`version: '2'`），只启用 12 个 linter，全部围绕「真 bug」和「并发/安全」：

| 类别       | linter                                            |
|------------|---------------------------------------------------|
| Bug 预防   | `errcheck`、`govet`、`staticcheck`、`ineffassign` |
| 代码质量   | `misspell`、`unconvert`、`nilerr`、`errorlint`    |
| 并发与安全 | `bodyclose`、`contextcheck`、`noctx`、`gosec`     |

排除规则有三层，改动 lint 配置前先读一遍：`exclusions.paths` 排除 `cmd/devserver`、`cmd/frontend`、`vendor`（注意是**子串匹配**，写裸词会误伤同名文件）；`exclusions.rules` 按文本压掉 gosec 的文件权限/`exec.Command`/md5 告警，以及 errcheck 的 `Error return value of`、contextcheck 的 `should pass the context parameter`、noctx 的 `must not be called`；`internal/lastfm/client.go` 与 `internal/listenbrainz/client.go` 另有按路径的定点豁免（md5 是 Last.fm 签名强制要求，`validateTokenEndpoint` 是 URL 不是凭据）。

其他格式化器与其配置：

| 工具                      | 配置来源                             | 覆盖                                                                           |
|---------------------------|--------------------------------------|--------------------------------------------------------------------------------|
| `gofmt`/`go fmt`/`go fix` | 标准                                 | 全部 Go                                                                        |
| `prettier`                | `package.json` 的 `format` 脚本参数  | `cmd/frontend` + `internal/webui/www`                                          |
| `oxlint`                  | `cmd/frontend/.oxlintrc.json`        | `no-unused-vars`（`_` 前缀豁免）、`no-explicit-any` 为 warn，`no-console` 关闭 |
| `yamlfmt`                 | `.yamlfmt`（2 空格、120 列、单引号） | `.cnb`、`.github`、`.gitlab` 等 YAML                                           |
| `markdownfmt`             | 无配置文件                           | 全仓 Markdown                                                                  |
| `shfmt`                   | `-i 2 -ci -bn -s -ln bash`           | 仅 `fpkg/cmd/*`                                                                |
| `sql-formatter`           | `--fix -l sql`                       | `internal/{db,datadb}/*.sql`                                                   |
| `dockerfmt`               | 无配置文件                           | 三个 CI 目录下的 `Dockerfile*`                                                 |

CI 会跑什么、失败怎么读、覆盖率为何分 Go/Node 两个 key，统一写在 [contributing.md](contributing.md)，本文不重复。
