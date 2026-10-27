# 更新日志

fnmusic-sync 的版本变更记录。格式参考 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，版本号遵循语义化版本；条目按「新增 / 修复 / 优化 / 重构 / 文档 / 测试 / 构建 / CI / 杂项」归类。

> 尚未打 tag 的变更记在 `[Unreleased]`。打 tag 时 CI 会自动生成 Release 正文（`.cnb/workflows/release.yml` 里的 changelog 插件）；本文件是人工维护的合并视图，冲突时以本文件与源码为准。
>
> 关于早期版本：2026-09 那批 tag 是快速迭代 + 反复 rebase 产生的，同一批提交在相邻 tag 上被重放过多次（例如 v0.1.11 ~ v0.1.21 的提交信息几乎一致，但区间内实际有数千行差异），逐 tag 还原不可靠。因此 `v0.1.21` 及更早合并为一段「区间净变化」，`v0.1.22` 起按版本逐条记录。
>
> 相关：[贡献指南](CONTRIBUTING.md) · [安全策略](SECURITY.md) · [文档索引](docs/README.md)

## [Unreleased]

### 文档

- 补齐 `docs/` 完整文档体系（架构、播放与 scrobble、歌单同步、配置、CLI、Web UI API、数据库、开发、部署、排障、贡献），并在中英文 README 加入文档索引（`f7e597f`）
- 新增 CI 镜像说明文档，梳理双平台镜像差异与升级流程（`90c2ae0`）

### 测试

- 补齐前端 API、trim 适配与后端配置/构建信息的单元测试（`d4e6a1e`）

### 构建

- 升级前端依赖并同步 pnpm 至 12.16.0（`5a49df6`）
- 拆分 CNB 工作流并新增代码评审、安全扫描与 SBOM 流水线（`b23eabe`）
- 在 Codecov 新增按业务模块切分的 Components 覆盖率面板（`0d2dacf`）
- 新增 GitHub Actions 覆盖率工作流并接入 Codecov 上报（`1278e9f`）
- 升级 codecov-action 5 → 7（`54f985f`）

### 杂项

- Vitest JUnit 的 classname 归一化为仓库相对路径，修复 Codecov Tests 跨 run 关联（`2b4c438`）
- 预装 markdownfmt 并启用 Markdown 格式化；CI 镜像仅构建 amd64（`60db16f`）
- Coveralls 上传改用官方 Action 与内置 `GITHUB_TOKEN`，替代 goveralls（`7866467`）
- 新增 Coveralls 覆盖率上报工作流，并在 GitHub CI 镜像预装 goveralls（`0ea6f26`）
- 统一构建预装工具链的 CI 镜像，并复用至 CNB 与 GitHub 工作流（`458e4f0`）

## [v0.1.30] - 2026-10-22

### 构建

- Code Wiki 生成迁移至 tag_push 独立触发，清理 pull 流水线注释（`2907a94`）
- 为 Go 与 Node 覆盖率上报增加独立 key/name，避免徽章互相覆盖（`ecb7cb8`）
- 接入前端 vitest 覆盖率流水线与增量红线门禁（`d53cecf`）
- 新增 Go 单元测试覆盖率流水线与增量红线门禁（`408292c`）
- 新增 CNB 平台 NPC 角色与交互配置（`6bc26eb`）
- 升级 oxlint 1.89.0、vite 8.3.5、modernc.org/sqlite 1.61.0（`2ec7c3e`）

## [v0.1.28] - 2026-10-22

### 构建

- 升级 oxlint 1.89.0、vite 8.3.5、modernc.org/sqlite 1.61.0（`48bf602`）
- 修复代码扫描配置的排除规则写法（`5b843da`）

## [v0.1.27] - 2026-10-19

### 构建

- 升级 oxlint 1.88.0、postcss 8.5.31、pnpm 12.14.0（`8f6a617`）
- 升级 postcss 8.5.29 → 8.5.30（`530109f`）
- 升级 `@trimjs/web-app`（`7e1d21c`）

## [v0.1.26] - 2026-10-10

### 构建

- 升级 prettier 3.9.10，修复 Svelte 全局样式与告警过滤（`28a7859`）

## [v0.1.23] / [v0.1.24] / [v0.1.25] - 2026-10-10

> 三个 tag 指向同一次变更的重放提交，内容一致。

### 新增

- 新增文件路径与用户访问权限接口配置（`dbe0b51`、`4f94d39`、`6b7a2f2`）

## [v0.1.22] - 2026-10-10

### 新增

- 自升级的下载接入中转站，失败自动回退源站（`7139517`）

### 修复

- 修正 users 表迁移的列引用错位，并加固 PRAGMA 查询（`4a73e43`）
- 用 PRAGMA 表值函数参数化索引元数据查询（`bcf0744`）

### 构建

- 升级构建镜像工具链，改用 `go install` 安装 Go 工具（`a91d64d`）
- 批量升级前端与 Go 依赖（postcss、`@types/node`、vite、oxlint、prettier、svelte 插件、modernc.org/sqlite）

### 杂项

- 注释掉 Code Wiki 生成步骤（`c7c5fb7`）
- 更新许可证信息（`a37b352`）

## [v0.0.1] – [v0.1.21] - 2026-09-07 ~ 2026-09-22

> 该区间共 33 个 tag（`v0.0.1` ~ `v0.1.21`，另有初始 tag `0.0.0`），由反复 rebase 重放同一批提交产生，逐 tag 无法可靠还原。以下是区间内的净变化，按能力归类，不标注具体 tag。

### 新增

- 支持飞牛 fnOS fpk 打包与 fpk 运行模式（路径由 `TRIM_*` 环境变量决定）
- 用 SQLite 取代 YAML 状态文件：`state.db`（运营状态）+ `data.db`（MBID 映射）
- 用 Svelte + Tailwind + Vite 重写 Web UI，新增在线用户列表页面
- MBID 解析、本地数据库缓存与多源歌单同步（含歌单封面上传）
- 播放记录（`playback_log`）与歌单同步记录（`sync_log`）
- socket 状态诊断与 goroutine 快照
- 下载走中转、失败自动回退源站（自升级）
- 支持通过向导环境变量开启调试日志；登录响应登记新 token 与用户名
- 日志页最新日志置顶

### 修复

- 修复 PRAGMA 查询字符串拼接带来的 SQL 注入告警
- 修复 users 表迁移的列引用错位
- 用 PRAGMA 表值函数参数化索引元数据查询

### 重构

- 拆分 `main.go`，优化 scrobble 兜底逻辑

### 构建

- 移除 rpm/apk 打包，新增英文 README，升级 UPX 版本
- 接入 golangci-lint 与前端 ESLint 检查
- Go 模块与前端依赖的批量升级（Dependabot）

## 版本号与发布

- 版本号由 `git describe --tags` 决定，通过 `-ldflags -X` 注入 `internal/buildinfo`；本地未打 tag 的构建显示 `dev`。
- 打 tag 后由 CI 构建 deb / fpk 并发布 Release，产物与安装方式见 [部署与运维](docs/deployment.md)。
- 补记条目时按 `type(scope): 描述` 写提交信息，便于归类；具体规范见 [贡献指南](CONTRIBUTING.md)。
