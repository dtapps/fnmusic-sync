# GitLab 发布流水线（自成一套）

本文件说明 GitLab 侧的发布流水线：在 GitLab 上**发布**（不是跑覆盖率）。

与 CNB / GitHub 两侧的语义完全对齐——手动触发、填版本号、`make package-linux-all` 产出 deb/fpk、建 Release 挂附件——但实现上自成一套：GitLab 自己的 runner、GitLab registry 里由 `.gitlab/ci/Dockerfile` 构建的镜像、产物只留在 GitLab，不与任何其它平台共用引用。

| 平台   | 配置文件                        | 版本输入                 | 产物去向                  |
|--------|---------------------------------|--------------------------|---------------------------|
| CNB    | `.cnb/workflows/release.yml`    | 手动触发 + `VERSION`     | CNB Release 附件          |
| GitHub | `.github/workflows/release.yml` | `workflow_dispatch`      | GitHub Release 附件       |
| GitLab | `.gitlab-ci.yml`                | Run pipeline + `VERSION` | 通用包仓库 + Release 链接 |

## 0. 三个文件怎么分工

```
.gitlab-ci.yml                    91 行  workflow / stages / variables / default + include
.gitlab/workflows/image.yml      146 行  check-image → build-image → merge-image
.gitlab/workflows/release.yml    149 行  package-release
.gitlab/ci/Dockerfile                    CI 镜像本身（release 的工具链全烤在里面）
```

三个平台各有一个 workflows 目录，读起来对得上：`.cnb/workflows/`、`.github/workflows/`、`.gitlab/workflows/`。区别在于前两个是平台**强制**的路径，`workflows/` 这三个字是约定不是规范——GitLab 只认仓库根的 `.gitlab-ci.yml`，这个目录纯粹是给人看的。

Dockerfile 故意**不**搬进 `workflows/`：`build-image` 里 `--local dockerfile=.gitlab/ci` 指定的是 Dockerfile 的上下文目录，把流水线 YAML 和构建现场混在一起，等于每次构建都在给 dockerfile 上下文塞无关文件。`.gitlab/ci/` 因此只放镜像构建物料，`.gitlab/workflows/` 只放流水线描述，一个目录一件事。

**入口不要挪**：GitLab 允许在项目设置里把 CI 配置路径改成别处（比如 `.gitlab/workflows/main.yml`），但那个设置藏在网页里，换 fork、换镜像仓库就失效，读仓库的人根本看不出流水线从哪来。根 `.gitlab-ci.yml` 保持成一个只放 `include` + 全局段的壳。

`include: local` 是**编译期展开**：流水线创建时 GitLab 把三个文件合并成一份配置，不存在运行时模块加载，语义与单文件完全一致（拆完做过逐字段等价校验，见 §8）。

两条纪律，破坏了会出怪事：

1. **一个 job 只在一个文件里定义。** 同名 job 跨文件是深合并（key 逐个 merge），不是整体覆盖。在 A 里删掉一条 `script`，B 里同名的还留着，最后跑出什么命令谁都想不到。
2. **`workflow` / `stages` / `variables` / `default` 只写在根文件。** 这些段跨文件也会 merge，但分散后无法推理——最典型的是 `default.before_script` 会被 job 级 `before_script` **整体替换**（不是追加），这条规则藏进被 include 的文件，读另一个文件的人就看不见了。

`include` 放文件最前面（先读入的先应用）。路径一律**相对仓库根**，不是相对当前文件——所以 `image.yml` 里 `sha256sum .gitlab/ci/Dockerfile` 不需要改。

## 1. 流水线结构

| stage     | job               | 文件                            | 干什么                                                            |
|-----------|-------------------|---------------------------------|-------------------------------------------------------------------|
| `image`   | `check-image`     | `.gitlab/workflows/image.yml`   | 算 `.gitlab/ci/Dockerfile` 内容哈希，查 registry 里有没有对应 tag |
| `image`   | `build-image`     | `.gitlab/workflows/image.yml`   | 缺就构建（amd64 原生，rootless BuildKit），有就十几秒跳过         |
| `image`   | `merge-image`     | `.gitlab/workflows/image.yml`   | 把该镜像挂到 `ci:latest`（`default.image` 只用这个）              |
| `release` | `package-release` | `.gitlab/workflows/release.yml` | 校验版本 → 打包 → 上传安装包 → 创建 Release                       |

`image.yml` 里还有两个复用片段（点号开头，不会被当作 job 执行）：

| 片段           | 内容                                                   | 谁用                         |
|----------------|--------------------------------------------------------|------------------------------|
| `.docker-cli`  | `docker:cli` 镜像 + `before_script` 里的 registry 登录 | `check-image`、`merge-image` |
| `.image-rules` | 三个 job 共用的 4 条 `rules`                           | 三个 job 全部                |

用 `extends` 而不是 `!reference`：规则列表要整段继承，`extends` 语义更贴；`!reference` 适合精确取一条 `script`。也不用 YAML 锚点 `&`/`*`——锚点只在同文件内有效，跨 include 就断了。

`release` stage 只有一个 job，故意不拆：CNB 那边拆成 5 步是为了让每一步在页面上可点、可重跑，代价是产物在步骤间搬运；这里同一个容器里 curl / jq / git 都有，构建产物当场就能上传并拼出 Release 正文，拆开只剩开销。

## 2. 怎么发布

1. CI/CD → Pipelines → **Run pipeline**（分支选默认分支）
2. 填 `VERSION`，形如 `0.1.31` 或 `v0.1.31`（带不带 v 都行，脚本会归一化）
3. 预发布填 `PRE_RELEASE=true`
4. Run

`VERSION` 留空时 `package-release` 不创建，流水线只走 `image` stage——相当于「刷一下镜像」，不会误发版，也不会因为「所有 job 都被 rules 过滤掉」报错。

版本处理与另外两个平台一致：

| 变量          | 值                               | 用途                                      |
|---------------|----------------------------------|-------------------------------------------|
| `VERSION`     | 用户在 Run pipeline 里填的原始值 | 输入                                      |
| `TAG_NAME`    | `v` + 去 v 后的版本              | git tag / Release 名 / 包仓库里的版本目录 |
| `PKG_VERSION` | 去 v 后的版本                    | 进二进制、进 deb/fpk 的元数据             |

`VERSION` 会先过一遍正则（`^\d+\.\d+\.\d+` 起头），打错直接红在第一步，不会建出乱七八糟的 tag。

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

所以 `coverage` stage、`go-coverage`、`node-coverage` 全部删除。要恢复的话，从 git 历史里取回那两个 job 与 `coverage` stage 即可；注意 `package-release` 与它们共用同一份 CI 镜像。

## 4. CI 镜像：GitLab 自己构建（`.gitlab/ci/Dockerfile`）

镜像不跨平台拉，GitLab 侧自成一套：

| 项     | 值                                                                                                              |
|--------|-----------------------------------------------------------------------------------------------------------------|
| 源文件 | `.gitlab/ci/Dockerfile`（本平台专用；Dockerfile 本身兼容 aarch64，只是流水线不再构 arm）                        |
| 构建   | `check-image` → `build-image`（amd64 原生）→ `merge-image`，都在 `.gitlab/workflows/image.yml` 的 stage `image` |
| 引擎   | `moby/buildkit:rootless` + `buildctl-daemonless.sh`，**不需要 privileged、不需要 dind**，不用 QEMU              |
| 产物   | `ci:latest`（linux/amd64 单平台）；内容寻址 tag `ci:sha-<Dockerfile sha256 前 12 位>`                           |
| 缓存   | registry cache：`ci:buildcache`                                                                                 |
| 触发   | 默认分支 push 自动判断（tag 在不在 registry 里）；`CI_FORCE_REBUILD_IMAGE=true` 强制重建；其它分支不建          |

**为什么只出 amd64**：唯一消费方 `package-release` 跑在 amd64 runner 上，arm64 那份建出来没人拉。多建一份 = compute minutes 翻倍（每次 Dockerfile 变更约 2×10 分钟）。

| 架构  | runner tag               | 说明                           |
|-------|--------------------------|--------------------------------|
| amd64 | `saas-linux-small-amd64` | 默认免费 runner，2 vCPU / 8 GB |

**需要 arm64 时怎么加回来**（比如要在 Apple Silicon 上本地 `docker run` 这个镜像调试）：

1. 复制 `build-image` 一份改名 `build-image-arm64`，`tags: [saas-linux-small-arm64]`，tag/cache 后缀改 `-arm64`；
2. `merge-image.needs` 加一条 `- job: build-image-arm64`（`optional: true`，没权限时不阻塞）；
3. `merge-image` 的 `imagetools create` 把两个 `sha-` ref 一起列上，就又能合成 manifest list。

**注意单平台**：`ci:latest` 是 linux/amd64 单平台，Apple Silicon 上 `docker pull` 没问题，`docker run` 会 `exec format error`，要带 `--platform linux/amd64`（走 QEMU，慢）。CI 里 `default.image: $CI_IMAGE` 不受影响。

构建引擎的选择依据是 GitLab 官方文档《Use BuildKit to build Docker images》：

| 方法              | 安全要求                   | 使用的命令               |
|-------------------|----------------------------|--------------------------|
| BuildKit rootless | **不需要 privileged 容器** | `buildctl-daemonless.sh` |
| Docker Buildx     | 需要 `docker:dind`         | `docker buildx`          |
| Native BuildKit   | 需要 `docker:dind`         | `buildctl`               |

原文写明 rootless 是 Kaniko 的替代品，且 "Hosted runners on GitLab.com permit these calls and need no extra configuration"——GitLab.com 上开箱可用，不用自备 runner。

`build-image` / `merge-image` 的必要细节（写错就挂）：

- `entrypoint: ['']` **必须覆盖**——`moby/buildkit:rootless` 默认启动常驻 daemon，不覆盖就一直挂到超时。
- `BUILDKITD_FLAGS: --oci-worker-no-process-sandbox` 是 rootless 的必需参数。
- 认证手写 `~/.docker/config.json`（rootless 不会自动用 `CI_JOB_TOKEN`）。
- job 级 `before_script` 会**整体替换** `default.before_script`，所以 `check-image` / `merge-image`（走 `.docker-cli`）和 `build-image` 里都没有 `git safe.directory`——它们不读 git。
- `check-image` / `merge-image` 的镜像与登录块只有 `.docker-cli` 一份：`extends` 是深合并，job 里若再写 `before_script` 会把那三行**整体换掉**，不是追加。
- **`--output` 只推一个 tag**：`name=$IMAGE_REF`。写 `name=repo:a,repo:b` 会直接失败——值按逗号切成多段 `key=value`，第二段不是 `key=value`，报 `error: invalid value ...`。可读 tag 交给 `merge-image` 的 `imagetools create -t`。
- **不写 `--opt platform`**：job 跑在 amd64 runner 上，目标天然是本机架构；显式写死反而会在别的架构 runner 上退化成 QEMU。

**什么时候会重建**（判断依据是「内容哈希对应的 tag 在不在 registry 里」，不是 commit 差分）：

| 场景                                          | 会怎样                                                                                                   |
|-----------------------------------------------|----------------------------------------------------------------------------------------------------------|
| 默认分支 push，Dockerfile 变了                | `sha-<hash>` 变 → 重建（有 registry cache 时几分钟）→ 指向新 `latest`                                    |
| 默认分支 push，Dockerfile 没变、镜像在        | 查到 tag → `build-image` 十几秒跳过；`merge-image` 仍跑一遍把 `latest` 重新指（顺带修好被误删的 latest） |
| 默认分支 push，Dockerfile 没变、镜像**不在**  | tag 查不到 → **自动补建**，这就是自愈                                                                    |
| Run pipeline 传 `CI_FORCE_REBUILD_IMAGE=true` | 无视 tag，强制重建（刷基础镜像、上次失败时用）                                                           |

**为什么不用 `rules:changes` 判断要不要重建**（`manifest unknown` 那次的根因）：`changes` 比的是「这次 commit 相对上次有没有改 Dockerfile」，而不是「registry 里到底有没有能用的镜像」。镜像一次都没建成过时，只改 `.gitlab-ci.yml` 推上来 → Dockerfile 没变 → build job 不创建 → 拉 `ci:latest` 直接 `manifest unknown`，**下一条流水线还是同样结果**，死循环。而且 `changes` 在 schedule / web / API 这类没有 push 事件的流水线里恒为 true。

现在用 `sha256sum .gitlab/ci/Dockerfile | cut -c1-12` 算内容哈希，拿 `ci:sha-<hash>` 去 registry 里查——判断的是客观事实。

**代价**：默认分支每次 push 多出 `check-image`（约 20 秒）+ 空跑的 `build-image`（约 20 秒）+ `merge-image`（约 20 秒），约 1 分钟固定开销，换来「镜像缺了自动补」「不白烧 10 分钟」。

**Dockerfile 归属**：`.gitlab/ci/Dockerfile` 是 GitLab 专用的一份，其它平台各自也有一份，互相不引用——某个平台的 registry / runner 出问题不拖累别的平台。代价是升 Go / Node / pnpm 版本时每份都要改。

## 5. 前置条件

1. **项目要开 CI/CD。** pull mirror 仓库默认可能是关的：Settings → CI/CD → General pipelines；镜像仓库还要勾 **Run pipelines for mirrored repositories**。
2. **要开 Container Registry。** 镜像存在 `$CI_REGISTRY_IMAGE/ci`，需 Settings → General → Visibility, project features → **Container Registry 打开**。不需要 secret：同实例 registry，Runner 用 `CI_JOB_TOKEN` 自动认证。
3. **推送分支**：`workflow.rules` 只允许默认分支 push 与 Run pipeline 建流水线，其它分支 / MR / tag 一律不建。

## 6. 只有 `package-release` 会读的变量

| 变量           | 默认           | 说明                                                                                                                                                |
|----------------|----------------|-----------------------------------------------------------------------------------------------------------------------------------------------------|
| `VERSION`      | 空             | Run pipeline 表单里填；空 = 不发布                                                                                                                  |
| `PRE_RELEASE`  | `false`        | 只影响正文横幅与「发布类型」行；GitLab Release 本身没有预发布标记                                                                                   |
| `REPO_SOURCE`  | `gitlab`       | 进二进制 `buildinfo.RepoSource`。updater 只区分 `github` 与其它（走 CNB 链路），所以 `gitlab` 与 `cnb` 运行时等价，但 UI「构建来源」如实显示 gitlab |
| `PACKAGE_NAME` | `fnmusic-sync` | 包仓库里的包名                                                                                                                                      |
| `GIT_DEPTH`    | `0`            | 发布要 `git describe` / `git log` 取 CHANGELOG，浅克隆拿不到 tag                                                                                    |

要支持「从 GitLab API 检查更新」，得改 `internal/updater`（现在只实现了 GitHub 与 CNB 两条链路），与 CI 无关。

## 7. GitLab 特有的坑

| 现象                                                  | 原因 / 处理                                                                                                             |
|-------------------------------------------------------|-------------------------------------------------------------------------------------------------------------------------|
| 流水线根本没触发                                      | 项目没开 CI/CD，或镜像仓库没勾 "Run pipelines for mirrored repositories"，或 push 的不是默认分支（见 `workflow.rules`） |
| `jobs config should contain at least one visible job` | 该事件下所有 job 都被 `rules` 过滤。根文件的 `workflow.rules` 只在默认分支 push / web 时建流水线，就是为了避开这条      |
| MR 里什么都不跑                                       | 故意的：发布流水线不该在 MR 上占额度。要发布走 Run pipeline                                                             |
| 拉镜像 `unauthorized` / `manifest unknown`            | ① 没开 Container Registry；② 镜像确实还没建。默认分支 push 会自动补建；急着用就带 `CI_FORCE_REBUILD_IMAGE=true`         |
| `build-image` 一直挂到超时、无日志                    | 忘了 `entrypoint: ['']`，容器里起的是 BuildKit 常驻 daemon                                                              |
| `error: invalid value <registry>/ci:<sha>`            | `--output` 写了两个 tag（逗号被当属性分隔符）。只留一个 `name=`                                                         |
| 本地 `docker run` 报 `exec format error`              | `ci:latest` 只出 amd64，Apple Silicon 上要 `--platform linux/amd64`                                                     |
| 上传安装包 403 / 401                                  | `CI_JOB_TOKEN` 对包仓库要有写权限（默认有）；项目若关了 Package Registry 功能则上传失败                                 |
| 私有项目 Release 链接点开要登录                       | 包仓库下载对私有项目要求认证，见 §2 末尾                                                                                |
| Release 已存在、job 红                                | 已做兜底：409 时自动改 `PUT` 更新；别的 4xx 会把响应体打出来                                                            |
| 正文里没有 CHANGELOG（永远显示"首次发布"）            | `GIT_DEPTH` 被改小或 tag 没拉下来，`git describe --tags --abbrev=0 HEAD^` 取不到上一个 tag                              |
| 变量后面紧跟中文导致 `unbound variable`               | bash 在部分 locale 下把多字节字符当成变量名的一部分。`$var` 后面接全角字符时一律写 `${var}`                             |
| 缓存命中但依赖还是全量装                              | `cache.paths` 只能写**项目目录内**路径，故 `GOMODCACHE`/`GOBIN`/pnpm store 全在 `$CI_PROJECT_DIR/.cache/`               |
| 拆文件后某个 job 的命令“凭空”变了                     | 同名 job 跨文件是**深合并**。`rg -n '^ name:'` 之类的 grep 确认过：一个 job 只能出现在一个文件里                        |
| `include` 里写 `./image.yml` 找不到文件               | `include:local` 路径相对**仓库根**，不相对当前文件。写 `.gitlab/workflows/image.yml`                                    |
| `extends` 之后 `before_script` 少了几行               | 深合并里数组是**整体替换**：job 自己写了 `before_script` 就盖掉模板的全部内容，不会追加                                 |

## 8. 怎么验证

本地能验的部分（不碰 registry，也不需要 token）：

```bash
# 1. 合并后的结构 = include 展开 + extends 解析（等同于 Pipeline Editor 的 Full configuration）
python3 - <<'PY'
import yaml
cfg = {}
for f in ['.gitlab/workflows/image.yml', '.gitlab/workflows/release.yml', '.gitlab-ci.yml']:
    cfg.update(yaml.safe_load(open(f)) or {})
print(cfg['stages'], sorted(k for k in cfg if k not in ('include','workflow','stages','variables','default')))
PY

# 2. 版本号正则
V=0.1.31; printf 'ok %s\n' "${V#v}" | grep -qE '^ok [0-9]+\.[0-9]+\.[0-9]+' && echo pass

# 3. 复现打包（本机需要 nfpm / fnpack / upx）
VERSION=v0.1.31 make package-linux-all && make clean-archives && ls -lh bin
```

拆文件那次做过一次更严的校验：用脚本模拟 `include` 展开 + `extends` 深合并，把合并结果与拆之前的单文件**逐字段比对**——4 个 job 的生效配置、`workflow`/`stages`/`variables`/`default` 全部一致，hidden job 没有泄漏成可执行 job。以后再拆/再抽公共片段，照这个思路验一遍就行。

线上验证：

1. 默认分支 push 一次：`image` stage 三个 job 绿；Deployments → Container Registry 里有 `ci:latest` 与 `ci:sha-<hash>`。
2. Run pipeline 填 `VERSION=v0.1.31`（先 `PRE_RELEASE=true` 试一次）：`package-release` 绿，日志里能看到四个文件的上传记录与 `✓ Release v0.1.31 已发布`。
3. 打开 Deployments → Releases：正文有下载表，四个文件都能点开；`/-/releases/v0.1.31/downloads/fnmusic-sync_amd64.deb` 这个稳定路径也通。
4. 同版本再跑一次：日志出现「Release ... 已存在，改为更新」，仍然绿。
