# 贡献指南

感谢你愿意改这个项目。它是个小工具，但跑在别人的 NAS 上，所以对「别弄坏用户已有的部署」这件事比较较真。

提交信息用中文（与仓库现状一致），Issue / PR 中英文都可以。完整的提交规范、CI 细节、功能落点清单在 [docs/contributing.md](docs/contributing.md)，这里只放最短路径。

## 两条入口

- **报问题**：先在 [Issue](https://github.com/dtapps/fnmusic-sync/issues) 搜一下有没有重复；带上版本（`fnmusic-sync version`）、部署形态（deb / fpk）、相关日志（注意脱敏 `api_secret` / `token` / `session_key`）。排障步骤见 [故障排查](docs/troubleshooting.md)。
- **提代码**：fork → 切分支 → 改 → 本地自检 → PR。小改动（错别字、文档、单行 bug）直接提 PR，不用先开 issue。

安全问题**不要**开公开 Issue，走 [SECURITY.md](SECURITY.md)。

## 本地准备

```bash
git clone https://github.com/dtapps/fnmusic-sync.git
cd fnmusic-sync
make tool-deps     # sqlc、golangci-lint、yamlfmt、shfmt、sql-formatter、dockerfmt 等
make deps          # go mod tidy + 前端依赖
```

要求 Go 1.27.3+（见 `go.mod`）、Node 25.9.0（见 `cmd/frontend/.nvmrc`；CI 镜像里用的是更高版本）与 pnpm 12.15.0（见 `cmd/frontend/package.json` 的 `packageManager`）。没有 fnOS 环境时能开发到什么程度、`make dev` / `dev-server` / `dev-frontend` 各自干什么，见 [开发指南](docs/development.md)。

## 提交规范

格式 `type(scope): 中文描述`，scope 可省略；一条提交只做一件事，描述写清「为什么」。

| type       | 用在                              |
|------------|-----------------------------------|
| `feat`     | 新功能                            |
| `fix`      | 缺陷修复                          |
| `perf`     | 性能优化                          |
| `refactor` | 不改行为的重构                    |
| `docs`     | 文档                              |
| `test`     | 测试                              |
| `build`    | 依赖升级、打包与构建配置、CI 镜像 |
| `ci`       | 流水线本身                        |
| `chore`    | 其他杂务                          |

真实提交长这样：

```text
feat(updater): 下载接入中转并失败自动回退源站
fix(db): 修正 users 表迁移的列引用错位并加固 PRAGMA 查询
build(deps): 升级前端依赖并同步 pnpm 至 12.16.0
```

不要写 `update`、`fix bug`、`优化` 这种空话；破坏性变更把影响面写进描述（仓库不用 `!` 标记）。Dependabot 的英文提交不用管。

## 分支与 PR

```bash
git switch -c feat/lb-playlist-range
```

- 分支名：`feat/` `fix/` `docs/` `chore/` + 短横线英文描述。
- PR 目标分支 `main`；标题用最终 squash 后的提交信息。
- PR 描述四段：**背景**（为什么改）→ **改动**（3-5 条，点出取舍）→ **验证**（本地跑了什么、真机验证了哪条链路）→ **影响面**（是否动到 socket 接管 / 配置键 / DB schema / CI 镜像 / 发布产物）。
- 关联 issue 写 `Closes #12`。合并方式为 squash，`main` 上保持一条提交一件事。

## 合并前自检

```text
[ ] make format 已跑，工作区干净
[ ] make check 通过（golangci-lint / tsc / oxlint / prettier / 打包链路）
[ ] 改过 schema.sql 或 queries.sql：make sqlc 后生成文件一起提交
[ ] 改了配置结构：config.example.yaml、README、docs/configuration.md 同步
[ ] 改了 Web UI API：api.ts、types.ts、页面同步
[ ] 新增/修改的 Markdown 已过 markdownfmt
[ ] 真机验证过受影响链路（socket 接管 / scrobble / 歌单 / 升级）
```

`make check` 依赖 `nfpm` 与 `fnpack`（`make tool-deps` 不装这两个），装不上时至少跑 `go test ./...`、`pnpm --dir cmd/frontend test` 与 `golangci-lint run`。

## CI 门禁

仓库同时挂 CNB、GitHub Actions、GitLab CI 三套流水线，职责不同：

| 平台   | 跑什么                                                      |
|--------|-------------------------------------------------------------|
| CNB    | 构建、测试、代码评审、SCA / 许可证 / SBOM、CodeWiki、覆盖率 |
| GitHub | 构建、Codecov、Coveralls、CI 镜像构建                       |
| GitLab | 构建与打包发布（刻意不跑覆盖率）                            |

分工、失败定位与覆盖率上报方式见 [docs/contributing.md](docs/contributing.md)；CI 镜像的工具版本对应关系见 [docs/ci-image.md](docs/ci-image.md)。

## 代码与文档规范

- **Go**：`golangci-lint` 配置在 `.golangci.yml`；注释写中文，错误用 `%w` 包装，并发任务走 `internal/safego`。
- **前端**：Svelte 5 + Vite + Tailwind，`oxlint` + `prettier` + `vitest`；i18n 的 `zh-CN.json` 与 `en.json` 键必须对齐。
- **SQL**：schema 与查询在 `internal/db`、`internal/datadb`，由 sqlc 生成代码；`queries.sql` 里不写注释。
- **Markdown**：一律过 `markdownfmt`；新增文档放 `docs/` 并同步更新 [docs/README.md](docs/README.md) 索引。
- **不要提交**：`node_modules/`、`bin/`、`logs/`、`coverage.out`、本地 `config.yaml`（`.gitignore` 已覆盖）。

改配置键 / 加 HTTP 接口 / 加 provider / 加歌单源 / 改 schema 分别要动哪几处，[docs/contributing.md](docs/contributing.md) 里有逐项清单。

## 许可

提交即表示同意以仓库的 [MIT 许可证](LICENSE) 分发你的贡献。
