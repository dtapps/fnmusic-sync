# CI 镜像说明（fnmusic-sync）

本仓库维护**两份 CI 环境镜像定义**：`.cnb/ci/Dockerfile`（CNB 用）与 `.github/ci/Dockerfile`（GitHub Actions 用）。二者基于同一个基础镜像、装同一批工具、版本号逐一对应，**只在镜像的分发方式和网络加速上有差异**。改动任何一版的工具版本，必须同步改另一版。

## 1. 两种分发模型（最重要的差异）

| 维度     | CNB（`.cnb/ci/Dockerfile`）                    | GitHub（`.github/ci/Dockerfile`）                                     |
|----------|------------------------------------------------|-----------------------------------------------------------------------|
| 分发方式 | **每次运行时构建**，由 CNB 平台自动完成并缓存  | **预构建推送**到 `ghcr.io/<owner>/<repo>/ci:latest`，复用             |
| 构建声明 | 各工作流 `docker: build: .cnb/ci/Dockerfile`   | `.github/workflows/ci-image.yml` 手动触发构建                         |
| 引用方式 | 工作流声明 `build:` 后，步骤直接在该容器内执行 | 工作流 `container: image: ghcr.io/${{ github.repository }}/ci:latest` |
| 多架构   | 由 CNB 运行环境决定                            | `linux/amd64` 原生 + `linux/arm64` QEMU 模拟（buildx）                |
| 首次使用 | 无需准备，随工作流自动构建                     | **必须先手动跑一次 `ci-image.yml`**，否则容器拉不到镜像               |

> 为什么 GitHub 侧要预构建：每次 `push.yml` / `coverage.yml` / `coveralls.yml` 都现装一遍 Go 工具链（`go install` 编译 golangci-lint 等大件）会拖慢每次 CI 且不可重现；CNB 平台自己做了按需构建 + 缓存，无需额外工作流。

### 消费方清单

- **CNB**：`.cnb/workflows/` 里 `push.yml`、`release.yml`、`pull-coverage.yml`、`push-coverage.yml` 共 6 处 `build: .cnb/ci/Dockerfile`。
- **GitHub**：`push.yml`、`coverage.yml`（Codecov）、`coveralls.yml`（Coveralls）三个工作流的 `container:`。
- **GitLab**：`.gitlab-ci.yml` 的 `default.image`（`check-image` → `build-image` → `merge-image` 用 `.gitlab/ci/Dockerfile` 自建，存在 GitLab 自己的 registry；消费方只有 `package-release`）。

## 2. 工具清单（两版一致，版本号逐项对应）

基础镜像 `golang:1.27.3-trixie`（自带 Go 1.27 + git），额外安装：

| 工具             | 版本       | 用途                                                                    |
|------------------|------------|-------------------------------------------------------------------------|
| UPX              | `5.2.1`    | 二进制压缩（release 打包用）                                            |
| nfpm             | `2.48.0`   | 打包 deb/rpm/apk                                                        |
| fnpack           | `1.2.3`    | 飞牛 fnOS fpk 打包                                                      |
| Node.js          | `26.12.1`  | 前端构建                                                                |
| pnpm             | `12.15.0`  | 前端包管理                                                              |
| sqlc             | `v1.31.1`  | 生成 `internal/db`/`datadb`                                             |
| golangci-lint    | `v2.15.0`  | Go lint（`make check`）                                                 |
| govulncheck      | `v1.9.0`   | Go 漏洞扫描                                                             |
| shfmt            | `v3.14.1`  | shell 格式化                                                            |
| dockerfmt        | `v0.5.4`   | Dockerfile 格式化                                                       |
| yamlfmt          | `v0.21.0`  | YAML 格式化                                                             |
| gojq             | `v0.12.19` | JSON 处理                                                               |
| go-sql-formatter | `v15.8.2`  | SQL 格式化                                                              |
| markdownfmt      | `v3.1.0`   | Markdown 格式化（`Kunde21/markdownfmt/v3`，shurcooL 原版的维护中 fork） |

Go 工具经 `go install` 装到 `/go/bin`（`GOPATH=/go`，已在 PATH）。

### 仅 GitHub 版有的工具

| 工具      | 版本      | 用途                                                       |
|-----------|-----------|------------------------------------------------------------|
| gotestsum | `v1.13.0` | 跑测试 + JUnit 报告（`coverage.yml` / `coveralls.yml` 用） |
| goveralls | `v0.0.12` | 上报 Coveralls（`coveralls.yml` 用）                       |

> CNB 侧不需要它们：`.cnb/workflows/*-coverage.yml` 直接用 `go test -coverprofile=coverage.out`，覆盖率上报走 CNB 平台自己的流程，不经 gotestsum / goveralls。

### 仅 CNB 版有的配置

- **网络加速**（CNB 运行环境在国内，GitHub 侧不需要）：
  - `ARG GITHUB_MIRROR="https://files.m.daocloud.io"`：UPX / nfpm 等从 GitHub Releases 下载走镜像。
  - `GOPROXY=https://mirrors.cloud.tencent.com/go/,direct`：`go install` 拉模块。
  - APT 源替换 `deb.debian.org` → `mirrors.cloud.tencent.com`。
  - npm/pnpm registry 替换为腾讯云源，且 `pnpm config set ignore-scripts false`。
- `GO111MODULE=on` 显式声明。

## 3. 升级工具版本的流程

1. **两份 Dockerfile 同步改**：`.cnb/ci/Dockerfile` 与 `.github/ci/Dockerfile` 对应的 `ARG`（两边的版本块逐一对应，找同名的改）。
2. **触发重建**：
   - GitHub：手动跑一次 `ci-image.yml`，推送新的 `ghcr.io/.../ci:latest`。**下一次 CI 才会用新镜像**；`latest` 是唯一 tag，无回滚手段，回滚 = 手动重跑旧版本的 `ci-image.yml`。
   - CNB：无需操作，下次运行自动重建（平台有构建缓存）。
3. **同步检查工作流**：若涉及行为变化（如 goveralls 参数、gotestsum 的 JUnit 路径），检查 `coverage.yml` / `coveralls.yml` / `.cnb/workflows/*-coverage.yml` 是否需要跟进。

> markdownfmt 用 `Kunde21/markdownfmt/v3`（shurcooL 原版已停更，fork 支持 GFM 全语法）；模块路径带 `/v3`，安装路径是 `.../v3/cmd/markdownfmt`。

> gotestsum 的历史教训：`v1.12.0` 在 Go 1.27 下编译失败，必须钉 `v1.13.0`。升级前先在本地 `go install <path>@<new-version>` 验证编译通过。

## 4. 与运行环境的已知坑

- **容器内 git 的 dubious ownership**：GHA `container:` 挂载的 workspace 属宿主 uid，git 2.35+ 会拒绝操作。这正是 `Makefile` 的 `BUILD_CMD` 加 `-buildvcs=false` 的原因（VCS stamping 在容器内拿不到 git 状态会 exit 128）。两份镜像都装了 git，此坑两边都适用。
- **arm64 用 QEMU 模拟**（仅 GitHub 侧 `ci-image.yml`）：构建慢属正常，别因为超时去砍架构——`build-linux-arm64` 产物要在真实 arm64 环境验证。
- **镜像内没有 GTK/WebKit**：本仓库非 Wails 应用，Go 测试不需要 CGO 链接系统库，不要照抄其他仓库往 Dockerfile 里加 `libwebkitgtk`。
- **清理层**：两份 Dockerfile 末尾都 `apt-get purge wget xz-utils` 并清缓存，控制镜像体积；新增依赖时注意是否也要进清理名单。

## 5. 本地验证镜像

```bash
# 构建本地镜像（用 GitHub 版做示例；CNB 版同理）
docker build -t fnmusic-sync-ci -f .github/ci/Dockerfile .github/ci

# 验证关键工具可用
docker run --rm fnmusic-sync-ci bash -c \
  'go version && node -v && pnpm -v && sqlc version && golangci-lint version && gotestsum --version'
```

注意构建上下文是 `.github/ci`（或 `.cnb/ci`）目录本身，不是仓库根——两份 Dockerfile 都没有 COPY 仓库文件，上下文给目录即可。
