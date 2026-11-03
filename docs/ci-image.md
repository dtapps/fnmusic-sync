# CI 镜像（统一镜像 dtapps/golang/fnpack）

本文件说明各平台 CI 用的**同一个镜像**：镜像由 `dtapps/golang/fnpack` 项目统一构建、按平台推到各自的 registry，本仓库只消费，**仓库内不再有任何 CI Dockerfile**。

`.cnb/ci/Dockerfile`、`.github/ci/Dockerfile`、`.gitlab/ci/Dockerfile` 与 `.github/workflows/ci-image.yml` 都已删除（`chore(ci): 统一改用共享 CI 镜像 fnpack，移除各仓库自建 Dockerfile`）。历史版本可从 git 历史取回。

## 1. 三处地址与消费方

| 平台            | 镜像地址                                          | 在哪里引用                                                                                       |
|-----------------|---------------------------------------------------|--------------------------------------------------------------------------------------------------|
| CNB             | `docker.cnb.cool/dtapp/golang/fnpack`             | `.cnb/workflows/*.yml` 的 `docker.image`（push / release / pull-coverage / push-coverage）       |
| GitHub          | `ghcr.io/dtapps/golang/fnpack:latest`             | `push.yml` / `codecov.yml` / `coveralls.yml` / `release.yml` 的 `container.image`                |
| GitLab          | `registry.gitlab.com/dtapps/golang/fnpack:latest` | `.gitlab-ci.yml` 的 `variables.CI_IMAGE` → `default.image`（见 [gitlab-ci.md](gitlab-ci.md) §4） |
| GitCode / Gitee | 无                                                | 平台没有「用自定义镜像跑 job」的能力 → 每次现场装工具链（`scripts/ci/build-linux-packages.sh`）  |

三个地址指向**同一份工具链**，只是分发渠道不同：CNB 用自家 registry（国内链路快），GitHub / GitLab 用平台自带 registry（同实例拉取不需要额外凭据）。

## 2. 镜像里有什么

基础镜像 `golang:1.27.3-trixie`，额外预装：

| 类别     | 工具                                                                                          | 谁用                                        |
|----------|-----------------------------------------------------------------------------------------------|---------------------------------------------|
| 运行时   | Node.js、pnpm                                                                                 | 前端构建、`make deps`                       |
| 打包     | nfpm、fnpack、UPX                                                                             | deb / fpk 打包（`make package-linux-*`）    |
| Go 工具  | sqlc、golangci-lint、govulncheck、shfmt、dockerfmt、yamlfmt、gojq、sql-formatter、markdownfmt | `make tool-deps` 的全部内容，无需 CI 现场装 |
| 测试上报 | gotestsum、gocover-cobertura                                                                  | Codecov 的 JUnit / Cobertura 报告           |
| 基础命令 | bash、make、git、curl、jq                                                                     | 流水线脚本本身                              |

**版本以 `dtapps/golang/fnpack` 项目的 Dockerfile 为准**，本仓库不再声明 `ARG`。要查当前版本：

```bash
docker run --rm --platform linux/amd64 ghcr.io/dtapps/golang/fnpack:latest \
  bash -c 'go version && node -v && pnpm -v && nfpm --version && fnpack --help >/dev/null && golangci-lint version'
```

## 3. 升级工具版本

1. 在 `dtapps/golang/fnpack` 项目里改 Dockerfile 的 `ARG`。
2. 重新构建并推到三个 registry（CNB / GHCR / GitLab Registry），tag 仍是 `latest`。
3. **本仓库不需要改代码**：下一次流水线自动拉新的 `latest`。`latest` 是唯一 tag，没有 tag 级回滚手段——回滚 = 重新推一次旧版本。

各平台生效时机：

| 平台   | 生效方式                                                   |
|--------|------------------------------------------------------------|
| CNB    | 下次运行自动拉新镜像（平台侧有镜像缓存，可能要等缓存过期） |
| GitHub | 下次 workflow 运行 `container.image` 拉新镜像              |
| GitLab | 下次 Run pipeline 拉新镜像（`$CI_IMAGE` 固定为 `latest`）  |

## 4. 与运行环境的已知坑

- **`entrypoint` 必须覆盖**：镜像 `CMD` 是 `bash`。GitHub 侧 `container:` 与 GitLab 侧 `default.image` 都显式写了 `entrypoint: ['']`，删了它 runner 起的 shell 会被镜像接管，`script` 一行都跑不出来。
- **只出 linux/amd64**：Apple Silicon 上 `docker pull` 没问题，`docker run` 会 `exec format error`，要带 `--platform linux/amd64`（走 QEMU，慢）。CI 全跑在 amd64 runner 上，不受影响。
- **私有镜像要授权**：GitLab Registry / GHCR 若是私有项目，得把消费方仓库加进 Token Access，否则拉镜像直接 `unauthorized`。
- **容器内 git 的 dubious ownership**：workspace 属宿主 uid，git 2.35+ 会拒绝操作。这正是 `Makefile` 的 `BUILD_CMD` 加 `-buildvcs=false` 的原因，也是 GitLab 根文件里有 `git config --global --add safe.directory` 的原因。
- **镜像内没有 GTK/WebKit 的额外依赖**：本仓库非 Wails 应用，Go 测试不需要 CGO 链接系统库，不要照抄其他仓库的 `libwebkitgtk`。
- **不要在流水线里 `go install` 工具**：镜像已预装，现场装既拖慢 CI 又破坏可重现性。缺工具就去 `dtapps/golang/fnpack` 里补。

## 5. 本地验证镜像

```bash
# 拉统一镜像（Apple Silicon 记得加 --platform）
docker pull --platform linux/amd64 ghcr.io/dtapps/golang/fnpack:latest

# 验证关键工具可用
docker run --rm --platform linux/amd64 ghcr.io/dtapps/golang/fnpack:latest bash -c \
  'go version && node -v && pnpm -v && sqlc version && golangci-lint version && nfpm --version && fnpack --help >/dev/null && echo ok'
```

镜像不需要把仓库挂进去：它没有任何 `COPY` 仓库文件，`/workspace` 由各平台在运行时挂载/检出。
