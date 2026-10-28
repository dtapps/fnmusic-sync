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
.gitlab-ci.yml                   125 行  include:rules / workflow / stages / variables / default
.gitlab/workflows/image.yml      163 行  check-image → build-image → merge-image（按需引入）
.gitlab/workflows/release.yml    175 行  package-release
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

**什么事件会带出哪些 job**（两张表合起来看：`include:rules` 决定镜像那组在不在，`workflow:rules` 决定流水线在不在）：

| 事件                      | 条件                                   | 流水线   | 镜像三个 job | `package-release`   |
|---------------------------|----------------------------------------|----------|--------------|---------------------|
| 默认分支 push             | 这次 push 动过 `.gitlab/ci/Dockerfile` | 建       | ✅ 且重建    | ❌                  |
| 默认分支 push             | 没动 Dockerfile                        | **不建** | —            | —                   |
| Run pipeline（`web`）     | 填了 `VERSION`                         | 建       | ❌           | ✅                  |
| Run pipeline（`web`）     | 勾了 `CI_FORCE_REBUILD_IMAGE=true`     | 建       | ✅ 且重建    | 填了 `VERSION` 才有 |
| Run pipeline（`web`）     | `VERSION` 留空、也没勾 force           | **不建** | —            | —                   |
| 定时流水线（`schedule`）  | 目标分支是默认分支                     | 建       | ✅ 且重建    | ❌                  |
| MR / 其它分支 / tag / API | —                                      | **不建** | —            | —                   |

一句话：**发布流水线里没有镜像 job**。镜像只在「Dockerfile 变了」「定时」「手工勾 force」三件事上出现。

`image.yml` 里还有一个复用片段（点号开头，不会被当作 job 执行）：

| 片段          | 内容                                                   | 谁用                         |
|---------------|--------------------------------------------------------|------------------------------|
| `.docker-cli` | `docker:cli` 镜像 + `before_script` 里的 registry 登录 | `check-image`、`merge-image` |

三个 job 自己**没有 `rules`**：`image.yml` 被引入 = 就该跑。判断只写在根文件，一处。

**三层判断分清楚**，这是理解整套镜像逻辑的关键：

| 层               | 在哪                | 回答什么                | 判断依据                   |
|------------------|---------------------|-------------------------|----------------------------|
| `include:rules`  | 根 `.gitlab-ci.yml` | 镜像那组 job **在不在** | commit 差分 / 流水线来源   |
| `workflow:rules` | 根 `.gitlab-ci.yml` | 这条流水线**存不存在**  | 同上                       |
| `check-image`    | job 内部            | 镜像**要不要重建**      | 内容哈希 + registry 存在性 |

为什么不能只靠 job 上的 `rules`：那只能把 job 过滤掉，流水线照样建，于是又回到「有流水线但一个 job 都没有」的报错。`include:rules` 是把**定义本身**挡在门外，这是唯一干净的写法。`changes` 只在这两处做准入判断，从不承担「该不该重建」——原因见 §4 末尾那段。

用 `extends` 而不是 `!reference`：规则列表要整段继承，`extends` 语义更贴；`!reference` 适合精确取一条 `script`。也不用 YAML 锚点 `&`/`*`——锚点只在同文件内有效，跨 include 就断了。

`release` stage 只有一个 job，故意不拆：CNB 那边拆成 5 步是为了让每一步在页面上可点、可重跑，代价是产物在步骤间搬运；这里同一个容器里 curl / jq / git 都有，构建产物当场就能上传并拼出 Release 正文，拆开只剩开销。

## 2. 怎么发布

1. CI/CD → Pipelines → **Run pipeline**（分支选默认分支）
2. 填 `VERSION`，形如 `0.1.31` 或 `v0.1.31`（带不带 v 都行，脚本会归一化）
3. 预发布把版本号写成 `0.1.32-rc.1`（后缀是唯一信号；没有第二个参数）
4. Run

`VERSION` 留空、也没勾 `CI_FORCE_REBUILD_IMAGE` 时**什么都不建**——没有事可做，建一条零 job 的流水线只会报错。

要刷镜像（不发布）就走另一个组合：`VERSION` 留空 + 勾 `CI_FORCE_REBUILD_IMAGE=true`。这时只有 `image` stage 三个 job，不会误发版。

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
| 触发   | 默认分支 push 且动过 Dockerfile；Run pipeline；每周定时强制重建；`CI_FORCE_REBUILD_IMAGE=true` 强制重建         |

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

| 场景                                          | 会怎样                                                                                               |
|-----------------------------------------------|------------------------------------------------------------------------------------------------------|
| 默认分支 push，Dockerfile 变了                | 三个 job 出现；`sha-<hash>` 变 → 重建（有 registry cache 时几分钟）→ 指向新 `latest`                 |
| 默认分支 push，Dockerfile 没变                | **流水线根本不建**，GitLab 页面里不会多出一条空转记录                                                |
| Run pipeline（发布，没勾 force）              | **镜像 job 不存在**——`image.yml` 没被引入，`package-release` 直接用现成的 `ci:latest`                |
| Run pipeline 勾 `CI_FORCE_REBUILD_IMAGE=true` | 三个 job 出现，且无视 tag 强制重建（上次失败、要临时刷基础镜像、镜像被删时用）                       |
| 定时流水线                                    | 无论哈希在不在，**一律重建并丢掉 layer cache**（刷基础镜像安全补丁与 apt 包版本），然后重指 `latest` |

**准入判断和重建判断分开，别让 `changes` 越权**（`manifest unknown` 那次的根因）：

| 层                        | 判断什么        | 依据                                    |
|---------------------------|-----------------|-----------------------------------------|
| `include:rules`（根文件） | 镜像 job 在不在 | `changes`：这次 push 动过 Dockerfile 吗 |
| `check-image`             | 镜像要不要重建  | 内容哈希 + registry 里 tag 在不在       |

当年**只**用 `changes` 当裁判：镜像一次都没建成过，改个 `.gitlab-ci.yml` 推上来 → Dockerfile 没变 → build job 不创建 → 拉 `ci:latest` 直接 `manifest unknown`，**下一条流水线还是同样结果**，死循环。另外 `changes` 在 web / schedule / API 这类没有 push 事件的流水线里恒为 true，拿它当唯一依据等于在两个极端上都没判断力。

现在 `changes` 只用在准入层，重建的权威判断留在 `check-image`：`sha256sum .gitlab/ci/Dockerfile | cut -c1-12` 拿 `ci:sha-<hash>` 去 registry 里查——判断的是客观事实。

**这里换过一次取舍，说清楚**：

| 方案                                 | 发布流水线里的镜像 job | 镜像丢了能不能自愈           |
|--------------------------------------|------------------------|------------------------------|
| 每次 Run pipeline 都带三个 job（旧） | 3 个（约 1 分钟空转）  | 能，下一次发布顺手补上       |
| `include:rules` 按需引入（现在）     | 0 个                   | 不自动，靠每周定时或勾 force |

选了后者：发布流水线干净，代价是「镜像没了」不再被发布路径顺手救回来。兜底是**每周定时流水线**（不管哈希对不对一律重建，最迟 7 天自愈）+ 勾一次 `CI_FORCE_REBUILD_IMAGE`（立刻）。要退回旧行为，只需在根文件 `include:rules` 里加回一条 `- if: $CI_PIPELINE_SOURCE == "web"`。

**代价**：现在镜像开销只在「改动 Dockerfile 的 push」「定时」「勾 force」上产生。发布流水线零镜像开销。

### 4.1 定时刷新（Pipeline schedules）

镜像里的东西有两类**哈希看不见**的腐化，只有重新构建才吃得到：

- 基础镜像 `golang:1.27.3-trixie` 被上游推了新的 digest（apt 安全补丁）；
- Dockerfile 里 `apt-get install` 那一步，在固定 ARG 版本下拿到的**包版本**随镜像源变化。

所以 CI/CD → **Schedules** 里建一条周期性流水线（GitLab 的位置：Build → Pipeline schedules → New schedule）：

| 字段          | 填什么                                                                       |
|---------------|------------------------------------------------------------------------------|
| Description   | `weekly ci image refresh`                                                    |
| Interval      | Weekly，或 Custom 写 `0 0 * * 1`（每周一 00:00 UTC，GitHub 侧同款）          |
| Cron timezone | `UTC`                                                                        |
| Target branch | 默认分支（`main`）——**别的分支上建 schedule 不会生效**，两道闸都只认默认分支 |
| Variables     | **留空**。填了 `VERSION` 也不会发版——`package-release` 只认 web 源           |

定时流水线只跑 `image` stage 三个 job：`check-image` 检测到 `CI_PIPELINE_SOURCE == "schedule"` 就把 `IMAGE_NEEDED` 强制置 `true`，`build-image` 重建，`merge-image` 把 `latest` 重新指过去。

**光强制重建不够，还得丢缓存。** BuildKit 的 layer cache 键里没有「今天是几号」：base digest 没变、命令没变，`apt-get install` 那层就是命中的，重建出来的镜像和上周一模一样。所以：

| 位置                              | 做了什么                                                                        |
|-----------------------------------|---------------------------------------------------------------------------------|
| `.gitlab/ci/Dockerfile` 第 1.5 节 | `ARG CACHE_BUST=static` + `RUN echo "cache-bust=$CACHE_BUST"`，一个可失效的锚点 |
| `build-image`（仅 schedule 源）   | `--opt build-arg:CACHE_BUST=$CI_PIPELINE_IID`，让锚点层失效 → 之后全重建        |

默认值 `static` 意味着普通 push / 手动构建完全不传这个参数，锚点层的 cache key 不变，缓存照旧全命中。用 `CI_PIPELINE_IID` 而不是日期：不需要在容器里做 ISO 周算术（busybox 的 `date` 未必支持 `%V`），而且每条定时流水线唯一——语义就是「这次重来一遍」。

代价是定时那一次是**真·全量重建**（10 分钟左右，不是缓存命中的几分钟）。每周一次，值。

**重建会覆盖同一个 `ci:sha-<hash>` tag**。标签的含义是「这份 Dockerfile 对应的镜像」，不是「某个字节快照」——所以定时重建后 tag 内容变了是预期行为，`latest` 和 `sha-` 两条永远指向同一份。

定时任务本身**不能靠代码配**：它存在 GitLab 数据库里，不在 `.gitlab-ci.yml` 里。仓库侧能做的就是把 `schedule` 源放行（根文件的 `include:rules` 与 `workflow:rules` 各有一条），剩下的那一下在网页上点。换 fork / 换实例要重建一次 schedule——这是它和配置文件最大的区别，也是它不该被遗忘的原因：漏建了不会报错，只是镜像悄悄停在某天的状态。

**Dockerfile 归属**：`.gitlab/ci/Dockerfile` 是 GitLab 专用的一份，其它平台各自也有一份，互相不引用——某个平台的 registry / runner 出问题不拖累别的平台。代价是升 Go / Node / pnpm 版本时每份都要改。反过来，GitLab 这份多出的 `CACHE_BUST` 两行是 GitLab 独有的（只有这边有定时重建 + registry cache 的组合），跟另外两版对齐工具版本时**不要**把它同步过去。

## 5. 前置条件

1. **项目要开 CI/CD。** pull mirror 仓库默认可能是关的：Settings → CI/CD → General pipelines；镜像仓库还要勾 **Run pipelines for mirrored repositories**。
2. **要开 Container Registry。** 镜像存在 `$CI_REGISTRY_IMAGE/ci`，需 Settings → General → Visibility, project features → **Container Registry 打开**。不需要 secret：同实例 registry，Runner 用 `CI_JOB_TOKEN` 自动认证。
3. **推送分支**：`workflow.rules` 只允许「默认分支 push 且动过 `.gitlab/ci/Dockerfile`」「定时流水线」「Run pipeline（填了 `VERSION` 或勾了 force）」建流水线，其它分支 / MR / tag 一律不建。
4. **首次接入必须先建一次镜像**。发布流水线不再自带镜像 job（§4），所以新建的 fork / 迁移项目第一次发布前，先跑一次 Run pipeline 并勾 `CI_FORCE_REBUILD_IMAGE=true`，把 `ci:latest` 建出来。否则 `package-release` 会拉不到镜像，报 `manifest unknown`。
5. **建议建一条 weekly schedule**（见 §4.1）。不建不会报错，但镜像就停在构建当天的状态（基础镜像的安全补丁、`apt-get install` 拿到的包版本都不会自己更新），而且它是「镜像被删」这条路上唯一的自动兜底。

## 6. 只有 `package-release` 会读的变量

| 变量              | 默认           | 说明                                                                                                                                                |
|-------------------|----------------|-----------------------------------------------------------------------------------------------------------------------------------------------------|
| `VERSION`         | 空             | Run pipeline 表单里填；空 = 不发布                                                                                                                  |
| ~~`PRE_RELEASE`~~ | —              | **已删除**：预发布由 `VERSION` 的后缀决定（`1.2.3-rc.1`），GitLab Release 本身也没有预发布标记                                                      |
| `REPO_SOURCE`     | `gitlab`       | 进二进制 `buildinfo.RepoSource`。updater 只区分 `github` 与其它（走 CNB 链路），所以 `gitlab` 与 `cnb` 运行时等价，但 UI「构建来源」如实显示 gitlab |
| `PACKAGE_NAME`    | `fnmusic-sync` | 包仓库里的包名                                                                                                                                      |
| `GIT_DEPTH`       | `0`            | 发布要 `git describe` / `git log` 取 CHANGELOG，浅克隆拿不到 tag                                                                                    |

`REPO_SOURCE=gitlab` 现在是有消费方的：`internal/updater` 会走 GitLab 的 releases API 取最新 tag， 下载链接用 Release 的 `/-/releases/<tag>/downloads/<file>`（302 到通用包仓库），下载先走 `dl-stats.dtapp.net` 中转、失败回退源站（与 CNB 同域）。 公开项目匿名可读，私有项目需要 `GitlabToken`（`read_api`），由构建时的 `GITLAB_TOKEN` 环境变量注入—— 本流水线只在项目 CI/CD 变量里配了它的时候才编进二进制，留空即是匿名。

GitLab 的 Release **没有预发布标记**（见 [deployment.md](deployment.md) §升级）：API 里看不出来，`FetchLatestTag` 只能靠 tag 名过滤。所以发布预发布版本时 **`VERSION` 必须带 semver 后缀**（`0.1.32-rc.1`），稳定的用户不会被推到 rc 上；没有后缀的版本一律按正式版发 —— 这里也没有字段能把它标成预发布。

## 7. GitLab 特有的坑

| 现象                                                  | 原因 / 处理                                                                                                                                                                                                                                 |
|-------------------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| 流水线根本没触发                                      | 项目没开 CI/CD；或镜像仓库没勾 "Run pipelines for mirrored repositories"；或 push 的不是默认分支；或 push 没动 `.gitlab/ci/Dockerfile`（`changes` 故意拦掉，见 §4）；或 Run pipeline 里 `VERSION` 留空且没勾 force（没有任何事可做，见 §2） |
| `jobs config should contain at least one visible job` | 该事件下所有 job 都被过滤。根文件的两道闸（`include:rules` / `workflow:rules`）已经把「有没有 job」算在准入里，正常情况下撞不到这条                                                                                                         |
| MR 里什么都不跑                                       | 故意的：发布流水线不该在 MR 上占额度。要发布走 Run pipeline                                                                                                                                                                                 |
| 拉镜像 `unauthorized` / `manifest unknown`            | ① 没开 Container Registry；② 镜像确实还没建（首次接入 / 被删 / 新 fork）。勾 `CI_FORCE_REBUILD_IMAGE=true` 跑一次 Run pipeline 建出来，之后靠定时流水线保活                                                                                 |
| 定时流水线跑完镜像没变                                | `CI_PIPELINE_SOURCE` 不是 `schedule`（用 Run now 手点时它是 `schedule`，但用 web 手点不是）；或 schedule 建在别的分支上                                                                                                                     |
| `build-image` 一直挂到超时、无日志                    | 忘了 `entrypoint: ['']`，容器里起的是 BuildKit 常驻 daemon                                                                                                                                                                                  |
| `error: invalid value <registry>/ci:<sha>`            | `--output` 写了两个 tag（逗号被当属性分隔符）。只留一个 `name=`                                                                                                                                                                             |
| 本地 `docker run` 报 `exec format error`              | `ci:latest` 只出 amd64，Apple Silicon 上要 `--platform linux/amd64`                                                                                                                                                                         |
| 上传安装包 403 / 401                                  | `CI_JOB_TOKEN` 对包仓库要有写权限（默认有）；项目若关了 Package Registry 功能则上传失败                                                                                                                                                     |
| 私有项目 Release 链接点开要登录                       | 包仓库下载对私有项目要求认证，见 §2 末尾                                                                                                                                                                                                    |
| Release 已存在、job 红                                | 已做兜底：409 时自动改 `PUT` 更新；别的 4xx 会把响应体打出来                                                                                                                                                                                |
| 正文里没有 CHANGELOG（永远显示"首次发布"）            | `GIT_DEPTH` 被改小或 tag 没拉下来，`git describe --tags --abbrev=0 HEAD^` 取不到上一个 tag                                                                                                                                                  |
| 变量后面紧跟中文导致 `unbound variable`               | bash 在部分 locale 下把多字节字符当成变量名的一部分。`$var` 后面接全角字符时一律写 `${var}`                                                                                                                                                 |
| 缓存命中但依赖还是全量装                              | `cache.paths` 只能写**项目目录内**路径，故 `GOMODCACHE`/`GOBIN`/pnpm store 全在 `$CI_PROJECT_DIR/.cache/`                                                                                                                                   |
| 拆文件后某个 job 的命令“凭空”变了                     | 同名 job 跨文件是**深合并**。`rg -n '^ name:'` 之类的 grep 确认过：一个 job 只能出现在一个文件里                                                                                                                                            |
| `include` 里写 `./image.yml` 找不到文件               | `include:local` 路径相对**仓库根**，不相对当前文件。写 `.gitlab/workflows/image.yml`                                                                                                                                                        |
| `extends` 之后 `before_script` 少了几行               | 深合并里数组是**整体替换**：job 自己写了 `before_script` 就盖掉模板的全部内容，不会追加                                                                                                                                                     |

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

1. 改一行 `.gitlab/ci/Dockerfile` 推到默认分支：`image` stage 三个 job 绿；Deployments → Container Registry 里有 `ci:latest` 与 `ci:sha-<hash>`。
2. 再改一行 **Go 代码**推到默认分支：**不应该**产生新的流水线（`changes` 拦住了）。这一条是判定「改动生效了」的关键。
3. Run pipeline 填 `VERSION=v0.1.31`（要试预发布就填 `v0.1.32-rc.1`）：**流水线里只有 `package-release` 一个 job，`image` stage 是空的**。这一条是判定 `include:rules` 生效的关键。
4. Run pipeline 留空 `VERSION`、不勾 force：**不应该产生流水线**；勾上 `CI_FORCE_REBUILD_IMAGE=true` 再跑：只有三个 image job，`build-image` 十几秒跳过（哈希没变）。
5. 打开 Deployments → Releases：正文有下载表，四个文件都能点开；`/-/releases/v0.1.31/downloads/fnmusic-sync_amd64.deb` 这个稳定路径也通。
6. 同版本再跑一次：日志出现「Release ... 已存在，改为更新」，仍然绿。
7. Pipeline schedules 里点某条 schedule 的 **Run now**：`check-image` 日志出现「定时流水线：强制重建」，`build-image` 日志出现 `CACHE_BUST=<pipeline iid>` 且真的跑完整套安装（十分钟量级，不是十几秒跳过），`latest` 指向新的 digest。
