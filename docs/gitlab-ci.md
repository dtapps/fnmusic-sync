# GitLab 发布流水线（自成一套）

本文件说明 GitLab 侧的发布流水线：在 GitLab 上**发布**（不是跑覆盖率）。

与 CNB / GitHub 两侧的语义完全对齐——手动触发、填版本号、`make package-linux-all` 产出 deb/fpk、建 Release 挂附件——但实现上自成一套：GitLab 自己的 runner、产物只留在 GitLab，不与任何其它平台共用引用。

CI 镜像**不再由本仓库构建**：所有 job 统一拉预置镜像 `registry.gitlab.com/dtapps/golang/fnpack:latest`（见 §4）。原先自建的 `.gitlab/ci/Dockerfile` 与 `.gitlab/workflows/image.yml` 已删除。

| 平台   | 配置文件                        | 版本输入                 | 产物去向                  | CI 镜像                                           |
|--------|---------------------------------|--------------------------|---------------------------|---------------------------------------------------|
| CNB    | `.cnb/workflows/release.yml`    | 手动触发 + `VERSION`     | CNB Release 附件          | `docker.cnb.cool/dtapp/golang/fnpack`             |
| GitHub | `.github/workflows/release.yml` | `workflow_dispatch`      | GitHub Release 附件       | `ghcr.io/dtapps/golang/fnpack:latest`             |
| GitLab | `.gitlab-ci.yml`                | Run pipeline + `VERSION` | 通用包仓库 + Release 链接 | `registry.gitlab.com/dtapps/golang/fnpack:latest` |

## 0. 两个文件怎么分工

```
.gitlab-ci.yml                 include / workflow / stages / variables / default
.gitlab/workflows/release.yml  package-release
```

三个平台各有一个 workflows 目录，读起来对得上：`.cnb/workflows/`、`.github/workflows/`、`.gitlab/workflows/`。区别在于前两个是平台**强制**的路径，`workflows/` 这三个字是约定不是规范——GitLab 只认仓库根的 `.gitlab-ci.yml`，这个目录纯粹是给人看的。

**入口不要挪**：GitLab 允许在项目设置里把 CI 配置路径改成别处（比如 `.gitlab/workflows/main.yml`），但那个设置藏在网页里，换 fork、换镜像仓库就失效，读仓库的人根本看不出流水线从哪来。根 `.gitlab-ci.yml` 保持成一个只放 `include` + 全局段的壳。

`include: local` 是**编译期展开**：流水线创建时 GitLab 把两个文件合并成一份配置，不存在运行时模块加载，语义与单文件完全一致。

两条纪律，破坏了会出怪事：

1. **一个 job 只在一个文件里定义。** 同名 job 跨文件是深合并（key 逐个 merge），不是整体覆盖。在 A 里删掉一条 `script`，B 里同名的还留着，最后跑出什么命令谁都想不到。
2. **`workflow` / `stages` / `variables` / `default` 只写在根文件。** 这些段跨文件也会 merge，但分散后无法推理——最典型的是 `default.before_script` 会被 job 级 `before_script` **整体替换**（不是追加），这条规则藏进被 include 的文件，读另一个文件的人就看不见了。

`include` 放文件最前面（先读入的先应用）。路径一律**相对仓库根**，不是相对当前文件。

## 1. 流水线结构

只有一个 stage、一个 job：

| stage     | job               | 文件                            | 干什么                                      |
|-----------|-------------------|---------------------------------|---------------------------------------------|
| `release` | `package-release` | `.gitlab/workflows/release.yml` | 校验版本 → 打包 → 上传安装包 → 创建 Release |

`stages` 里只留 `release`：镜像 stage 随自建镜像方案一起删掉了，留着会报「未使用的 stage」。

**什么时候创建流水线**（只有一条入口）：

| 事件                                                  | 流水线   | `package-release` |
|-------------------------------------------------------|----------|-------------------|
| Run pipeline（`web`）且填了 `VERSION`                 | 建       | ✅                |
| Run pipeline（`web`）但 `VERSION` 留空                | **不建** | —                 |
| 默认分支 push / 定时 / MR / 其它分支 / tag / 外部 API | **不建** | —                 |

`workflow:rules` 只放行 `$CI_PIPELINE_SOURCE == "web" && $VERSION != ""`，其余一律 `when: never`。以前还有「默认分支 push 且动过 `.gitlab/ci/Dockerfile`」「每周定时刷镜像」两条，它们的唯一目的是维护自建镜像，镜像统一后一并删除。

为什么用 `workflow:rules` 而不是 job 上的 `rules`：job 级 `rules` 只能把 job 过滤掉，流水线照样建，于是会出现「有流水线但一个 job 都没有」，GitLab 直接报 `jobs config should contain at least one visible job`。`workflow:rules` 决定的是**流水线本身存不存在**，这是唯一干净的写法。

`release` stage 只有一个 job，故意不拆：CNB 那边拆成 5 步是为了让每一步在页面上可点、可重跑，代价是产物在步骤间搬运；这里同一个容器里 curl / jq / git 都有，构建产物当场就能上传并拼出 Release 正文，拆开只剩开销。

## 2. 怎么发布

1. CI/CD → Pipelines → **Run pipeline**（分支选默认分支）
2. 填 `VERSION`，形如 `0.1.31` 或 `v0.1.31`（带不带 v 都行，脚本会归一化）
3. 预发布把版本号写成 `0.1.32-rc.1`（后缀是唯一信号；没有第二个参数）
4. Run

`VERSION` 留空时**什么都不建**——没有事可做，建一条零 job 的流水线只会报错。以前那个用来「只刷镜像不发版」的 `CI_FORCE_REBUILD_IMAGE` 开关已随自建镜像方案删除。

版本处理与另外两个平台一致：

| 变量          | 值                               | 用途                                                                                                                         |
|---------------|----------------------------------|------------------------------------------------------------------------------------------------------------------------------|
| `VERSION`     | 用户在 Run pipeline 里填的原始值 | 输入                                                                                                                         |
| `TAG_NAME`    | `v` + 去 v 后的版本              | git tag / Release 名 / 包仓库里的版本目录                                                                                    |
| `PKG_VERSION` | 去 v 后的版本                    | 进 fpk manifest 与前端 package.json；deb 元数据用它的 `~` 变体（`DEB_VERSION`），否则 `0.1.32-rc.1` 在 dpkg 里比 `0.1.32` 新 |

`VERSION` 会先过一遍正则（`^\d+\.\d+\.\d+` 起头），打错直接红在第一步，不会建出乱七八糟的 tag。

**预发布版本必须在版本号里带 semver 后缀**（`0.1.32-rc.1` / `-beta.1` / `-alpha.1`）：GitLab 的 Release 没有 prerelease 字段，tag 里的后缀是客户端 `FetchLatestTag` 判断预发布的唯一依据。判定与另外四个平台共用 `scripts/ci/resolve-prerelease.sh`：带后缀是预发布，不带是正式版 —— 流水线没有「是否预发布」这个入参。

产物（`make clean-archives` 之后剩下的就是这些）：

| 文件                     | 说明             |
|--------------------------|------------------|
| `fnmusic-sync_amd64.deb` | Debian/Ubuntu    |
| `fnmusic-sync_arm64.deb` | Debian/Ubuntu    |
| `fnmusic-sync-amd64.fpk` | 飞牛 fnOS 应用包 |
| `fnmusic-sync-arm64.fpk` | 飞牛 fnOS 应用包 |

四个文件都会：

1. 上传到**通用包仓库**（Generic Package Registry）：`$CI_API_V4_URL/projects/<id>/packages/generic/fnmusic-sync/<tag>/<文件名>`
2. 挂进 Release 的 assets links，并额外给一个稳定路径 `/-/releases/<tag>/downloads/<文件名>`

**为什么用包仓库而不是 job 产物**：job artifacts 会过期（这里留 30 天，纯粹为了排查），Release 的下载链接不能挂在会过期的地址上。包仓库是 GitLab 里唯一「长期可下载」的官方存放点。

⚠️ **私有项目下载需要 token**：包仓库的下载接口对私有项目要求认证（`PRIVATE-TOKEN` 或 Deploy Token）；公开项目可匿名下载。要让私有项目的 Release 链接对外可用，得换对象存储或把项目转公开。

Release 已存在时（重跑同一个版本），脚本会 `PUT` 更新描述与附件，而不是直接红——改个正文重跑是常见操作。

## 3. 为什么不再跑覆盖率

GitLab 内建的覆盖率是两套互不联动的机制（百分比 + Cobertura 行级注释），口径与其它平台不同（语句 vs 行），也没有任何消费方：仓库的多平台覆盖率口径以 `docs/codecov.md` / `docs/coveralls.md` / `docs/coverage-*.md` 为准，GitLab 这侧再看一遍只是白烧 compute minutes。

所以 `coverage` stage、`go-coverage`、`node-coverage` 全部删除。要恢复的话，从 git 历史里取回那两个 job 与 `coverage` stage 即可；它们与 `package-release` 共用同一个 `$CI_IMAGE`。

## 4. CI 镜像：统一预置镜像

| 项       | 值                                                                                                        |
|----------|-----------------------------------------------------------------------------------------------------------|
| 地址     | `registry.gitlab.com/dtapps/golang/fnpack:latest`                                                         |
| 引用位置 | 根 `.gitlab-ci.yml` 的 `variables.CI_IMAGE` + `default.image.name`（`entrypoint: ['']` 保留）             |
| 平台     | linux/amd64 单平台（与 runner 一致）                                                                      |
| 工具链   | Go 1.27.3 基础镜像 + Node / pnpm / nfpm / fnpack / upx + sqlc、golangci-lint 等 Go 工具                   |
| 维护方   | `dtapps/golang/fnpack` 项目自己的 Dockerfile，本仓库只消费                                                |
| 变更方式 | 改该项目的 Dockerfile 并重新推送 `latest`；本仓下次流水线自动拉新的（`latest` 是唯一 tag，无 tag 级回滚） |

同一份工具链也以 `ghcr.io/dtapps/golang/fnpack:latest`（GitHub）与 `docker.cnb.cool/dtapp/golang/fnpack`（CNB）分发，三处指向同一个镜像项目的同一次构建，详见 [ci-image.md](ci-image.md)。

**为什么不再自建**：旧方案要在本仓库维护第三份 Dockerfile（工具版本与 CNB / GitHub 两版手工对齐）、3 个镜像 job、内容哈希 tag、registry cache、每周定时重建和 `CACHE_BUST` 锚点——全部只为「镜像只在本仓 registry 里」这一点收益。统一镜像后这些成本归零，升 Go / Node / pnpm 时也不必再改四处。

**镜像必须满足的契约**（换掉或升级镜像时按这个自检）：`bash`、`make`、`git`、`curl`、`jq`、`pnpm`、`nfpm`、`fnpack` 都在 PATH 上。`package-release` 的每一步都直接依赖它们，缺一个 job 就红在打包或上传阶段。

**为什么保留 `entrypoint: ['']`**：镜像的 `CMD` 是 `bash`，不覆盖 entrypoint 时 GitLab runner 起的 shell 会被它接管，`script` 里的每一行都跑不出来。这条与「镜像好不好」无关，换任何镜像都该留着。

**私有镜像的额外一步**：`registry.gitlab.com/dtapps/golang/fnpack` 若是私有项目，需要在那个项目里把本仓加入 **Token Access**（或在本仓配 `DOCKER_AUTH_CONFIG`），否则拉镜像直接 `unauthorized`。公开项目匿名可拉，不需要任何配置。

**单平台的后果**：Apple Silicon 上 `docker pull` 没问题，`docker run` 会 `exec format error`，要带 `--platform linux/amd64`（走 QEMU，慢）。CI 里 `default.image` 不受影响。

**想退回自建**：`.gitlab/workflows/image.yml` 与 `.gitlab/ci/Dockerfile` 还在 git 历史里（删除它们的那个提交即可 `git checkout <sha>^ -- .gitlab/ci .gitlab/workflows/image.yml`），再补回根文件的 `include:rules`、`image` stage 与两条 `workflow:rules` 即可。

## 5. 前置条件

1. **项目要开 CI/CD。** pull mirror 仓库默认可能是关的：Settings → CI/CD → General pipelines；镜像仓库还要勾 **Run pipelines for mirrored repositories**。
2. **镜像要拉得到。** 用统一的 `registry.gitlab.com/dtapps/golang/fnpack:latest`：公开则匿名可拉，私有需在那边授权本仓（见 §4）。**不再需要给本仓开 Container Registry**——自建镜像已下线。
3. **推送分支**：`workflow.rules` 只允许「Run pipeline 且填了 `VERSION`」建流水线，其它分支 / push / 定时 / MR / tag 一律不建。
4. **不需要预先建镜像**：旧方案要求「首次接入先勾 `CI_FORCE_REBUILD_IMAGE` 跑一次」，这条随自建镜像一起没了。镜像由 `dtapps/golang/fnpack` 统一维护，本仓第一次跑流水线就能直接拉。

## 6. 只有 `package-release` 会读的变量

| 变量              | 默认           | 说明                                                                                                                                                |
|-------------------|----------------|-----------------------------------------------------------------------------------------------------------------------------------------------------|
| `VERSION`         | 空             | Run pipeline 表单里填；空 = 不发布                                                                                                                  |
| ~~`PRE_RELEASE`~~ | —              | **已删除**：预发布由 `VERSION` 的后缀决定（`1.2.3-rc.1`），GitLab Release 本身也没有预发布标记                                                      |
| `CI_IMAGE`        | 见 §4          | 统一 CI 镜像地址；用项目级变量覆盖它即可整体换镜像                                                                                                  |
| `REPO_SOURCE`     | `gitlab`       | 进二进制 `buildinfo.RepoSource`。updater 只区分 `github` 与其它（走 CNB 链路），所以 `gitlab` 与 `cnb` 运行时等价，但 UI「构建来源」如实显示 gitlab |
| `PACKAGE_NAME`    | `fnmusic-sync` | 包仓库里的包名                                                                                                                                      |
| `GIT_DEPTH`       | `0`            | 发布要 `git describe` / `git log` 取 CHANGELOG，浅克隆拿不到 tag                                                                                    |

`REPO_SOURCE=gitlab` 现在是有消费方的：`internal/updater` 会走 GitLab 的 releases API 取最新 tag， 下载链接用 Release 的 `/-/releases/<tag>/downloads/<file>`（302 到通用包仓库），下载先走 `dl-stats.dtapp.net` 中转、失败回退源站（与 CNB 同域）。 公开项目匿名可读，私有项目需要 `GitlabToken`（`read_api`），由构建时的 `GITLAB_TOKEN` 环境变量注入—— 本流水线只在项目 CI/CD 变量里配了它的时候才编进二进制，留空即是匿名。配的时候注意两点：**别勾 Protected**（受保护变量不会注入到非保护 ref 的流水线，Run pipeline 跑在未受保护的默认分支上就一条也拿不到）；脚本里引用一律写 `${GITLAB_TOKEN:-}` —— job 第一段开了 `set -eu`，而 GitLab 把整个 job 的 script 行拼成一个 bash 脚本执行，裸写 `${GITLAB_TOKEN}` 在变量缺失时会直接 `GITLAB_TOKEN: unbound variable` 把 job 打死。

GitLab 的 Release **没有预发布标记**（见 [deployment.md](deployment.md) §升级）：API 里看不出来，`FetchLatestTag` 只能靠 tag 名过滤。所以发布预发布版本时 **`VERSION` 必须带 semver 后缀**（`0.1.32-rc.1`），稳定的用户不会被推到 rc 上；没有后缀的版本一律按正式版发 —— 这里也没有字段能把它标成预发布。

## 7. GitLab 特有的坑

| 现象                                                  | 原因 / 处理                                                                                                                                       |
|-------------------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------|
| 流水线根本没触发                                      | 本流水线唯一的入口是 Run pipeline 且填了 `VERSION`（见 §1）。项目没开 CI/CD、push 的是普通提交、MR / tag 事件都不会建流水线——这些是设计，不是故障 |
| `jobs config should contain at least one visible job` | 该事件下没有 job 可跑。`workflow:rules` 已经把「有没有 job」算在准入里，正常情况下撞不到这条                                                      |
| MR 里什么都不跑                                       | 故意的：发布流水线不该在 MR 上占额度。要发布走 Run pipeline                                                                                       |
| 拉镜像 `unauthorized` / `denied`                      | 统一镜像是私有项目且没授权本仓。去 `dtapps/golang/fnpack` 的 Settings → CI/CD → Token Access 把本仓加进去，或在本仓配 `DOCKER_AUTH_CONFIG`        |
| 拉镜像 `manifest unknown`                             | 地址或 tag 拼错（`latest` 是唯一 tag）；或那个项目的镜像被删了。本仓库不再负责构建，去 `dtapps/golang/fnpack` 查                                  |
| 本地 `docker run` 报 `exec format error`              | 镜像只出 amd64，Apple Silicon 上要 `--platform linux/amd64`                                                                                       |
| 打包阶段报找不到 `nfpm` / `fnpack` / `pnpm`           | 镜像工具链不完整。按 §4 的「镜像契约」自检，缺工具要在 `dtapps/golang/fnpack` 里补，不要在本 job 临时装                                           |
| `script` 一行都不执行、job 直接结束                   | 镜像的 `CMD` 接管了 runner 的 shell。根文件 `default.image.entrypoint: ['']` 不能被删                                                             |
| 上传安装包 403 / 401                                  | `CI_JOB_TOKEN` 对包仓库要有写权限（默认有）；项目若关了 Package Registry 功能则上传失败                                                           |
| 私有项目 Release 链接点开要登录                       | 包仓库下载对私有项目要求认证，见 §2 末尾                                                                                                          |
| Release 已存在、job 红                                | 已做兜底：409 时自动改 `PUT` 更新；别的 4xx 会把响应体打出来                                                                                      |
| 正文里没有 CHANGELOG（永远显示"首次发布"）            | `GIT_DEPTH` 被改小或 tag 没拉下来，`git describe --tags --abbrev=0 HEAD^` 取不到上一个 tag                                                        |
| 变量后面紧跟中文导致 `unbound variable`               | bash 在部分 locale 下把多字节字符当成变量名的一部分。`$var` 后面接全角字符时一律写 `${var}`                                                       |
| 缓存命中但依赖还是全量装                              | `cache.paths` 只能写**项目目录内**路径，故 `GOMODCACHE`/`GOBIN`/pnpm store 全在 `$CI_PROJECT_DIR/.cache/`                                         |
| 拆文件后某个 job 的命令"凭空"变了                     | 同名 job 跨文件是**深合并**。`rg -n '^ name:'` 之类的 grep 确认过：一个 job 只能出现在一个文件里                                                  |
| `include` 里写 `./release.yml` 找不到文件             | `include:local` 路径相对**仓库根**，不相对当前文件。写 `.gitlab/workflows/release.yml`                                                            |
| `extends` 之后 `before_script` 少了几行               | 深合并里数组是**整体替换**：job 自己写了 `before_script` 就盖掉模板的全部内容，不会追加                                                           |

## 8. 怎么验证

本地能验的部分（不碰 registry，也不需要 token）：

```bash
# 1. 合并后的结构 = include 展开（等同于 Pipeline Editor 的 Full configuration）
python3 - <<'PY'
import yaml
cfg = {}
for f in ['.gitlab/workflows/release.yml', '.gitlab-ci.yml']:
    cfg.update(yaml.safe_load(open(f)) or {})
print(cfg['stages'], sorted(k for k in cfg if k not in ('include','workflow','stages','variables','default')))
print(cfg['variables']['CI_IMAGE'])
PY

# 2. 镜像里的工具链是否齐全（Apple Silicon 记得加 --platform）
docker run --rm --platform linux/amd64 registry.gitlab.com/dtapps/golang/fnpack:latest \
  bash -c 'go version && node -v && pnpm -v && nfpm --version && fnpack --help >/dev/null && echo ok'

# 3. 版本号正则
V=0.1.31; printf 'ok %s\n' "${V#v}" | grep -qE '^ok [0-9]+\.[0-9]+\.[0-9]+' && echo pass

# 4. 复现打包（本机需要 nfpm / fnpack / upx）
VERSION=v0.1.31 make package-linux-all && make clean-archives && ls -lh bin
```

线上验证：

1. 改一行 **Go 代码**推到默认分支：**不应该**产生流水线。这一条是判定 `workflow:rules` 生效的关键。
2. Run pipeline 填 `VERSION=v0.1.31`（要试预发布就填 `v0.1.32-rc.1`）：流水线里只有 `package-release` 一个 job，日志开头能看到从 `registry.gitlab.com/dtapps/golang/fnpack:latest` 拉镜像。
3. Run pipeline 留空 `VERSION`：**不应该产生流水线**（没有 `CI_FORCE_REBUILD_IMAGE` 这个开关了）。
4. 打开 Deployments → Releases：正文有下载表，四个文件都能点开；`/-/releases/v0.1.31/downloads/fnmusic-sync_amd64.deb` 这个稳定路径也通。
5. 同版本再跑一次：日志出现「Release ... 已存在，改为更新」，仍然绿。
