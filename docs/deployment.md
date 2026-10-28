# 部署与运维

两种安装形态（deb 传统安装 / fpk 飞牛应用包）的落地细节、systemd 单元、权限模型、升级回滚、卸载残留，以及日常运维要看的那几个点。

> 相关：[README](../README.md)（快速上手）、[development.md](development.md)（本地构建）、[troubleshooting.md](troubleshooting.md)（故障排查）、[configuration.md](configuration.md)（配置项全解）、[cli.md](cli.md)（命令行参考）、[database.md](database.md)（库表结构）。

## 两种安装形态对比

同一份 Go 代码，两种打包：deb 交给系统包管理器与 systemd，fpk 交给飞牛应用中心与它自己的进程管理脚本。**两者互斥**，`scripts/install.sh` 一旦检测到另一个形态存在就直接退出。

| 维度       | deb（传统安装）                                           | fpk（飞牛应用包）                                                                    |
|------------|-----------------------------------------------------------|--------------------------------------------------------------------------------------|
| 安装入口   | `dpkg -i` / `apt` / `scripts/install.sh`                  | 飞牛应用中心上传 `.fpk`                                                              |
| 二进制     | `/usr/bin/fnmusic-sync`（`.nfpm.yaml` 的 `contents.dst`） | `$TRIM_APPDEST/fnmusic-sync`，即 `/var/apps/fnmusic-sync/target/fnmusic-sync`        |
| 配置       | `/etc/fnmusic-sync/config.yaml`                           | `$TRIM_PKGETC/config.yaml`，向导值另存 `$TRIM_PKGETC/config.env`                     |
| 状态库     | `/var/lib/fnmusic-sync/state.db`                          | `$TRIM_PKGVAR/state.db`                                                              |
| 数据库     | `/var/lib/fnmusic-sync/data.db`                           | `$TRIM_PKGVAR/data.db`                                                               |
| 日志       | `/var/log/fnmusic-sync/`                                  | `$TRIM_PKGVAR/logs/`                                                                 |
| 运行锁     | `/run/fnmusic-sync/run.lock`                              | `$TRIM_PKGVAR/run.lock`                                                              |
| 进程管理   | systemd（unit 由二进制自己生成）                          | 飞牛调 `fpkg/cmd/main` 的 `start` / `stop` / `status`，PID 落 `$TRIM_PKGVAR/app.pid` |
| 运行身份   | root（unit 无 `User=`）                                   | root（`fpkg/config/privilege` 的 `run-as: root`）                                    |
| Web UI     | **没有**（`internal/webui` 带 `//go:build fpk`）          | `$TRIM_APPDEST/app.sock`，网关前缀 `/app/fnmusic-sync`                               |
| 编译 tag   | 无 tag                                                    | `-tags fpk`（`BUILD_TAG=fpk`）                                                       |
| 可用子命令 | `version` / `self-upgrade` / `service` / `sockets`        | `version` / `sockets`（`self-upgrade`、`service` 被 build tag 排除）                 |
| 升级通道   | `self-upgrade`、`install.sh upgrade`、`apt`               | 应用中心，或 Web UI 的 `POST /api/upgrade`（内部调 `appcenter-cli install-local`）   |
| 卸载       | `apt remove` / `purge`，前进 `scripts/preremove.sh`       | 应用中心，卸载向导 `wizard_keep_data` 决定是否删数据                                 |

fpk 形态下路径全部由三个环境变量推导（`cmd/fnmusic-sync/runtime.go`）：`TRIM_APPDEST` 非空即判定为 fpk，`TRIM_PKGETC`/`TRIM_PKGVAR` 缺失时分别回退到 `<APPDEST>/../etc` 与 `<APPDEST>/../var`。

`fpkg/config/resource` 里还声明了应用需要的飞牛能力：`api-scope` 为 `trim.system.getPlatformConfig`、`trim.file.path`、`trim.file.userAccess`；`data-share` 暴露 `fnmusic-sync` 与 `fnmusic-sync/data` 两个共享名——曲库目录配置（`library.directories`）能否读到，和这些共享/挂载关系直接相关。

## 一键安装脚本 `scripts/install.sh`

这个脚本同时承担 install / upgrade / uninstall 三个动作，`ACTION` 默认是 `install`。完整流程拆解：

1. `set -eu`，定义 `REPO="${FNMUSIC_REPO:-https://cnb.cool/dtapp/fnmusic-sync}"`、`BINARY=fnmusic-sync`、`CONFIG_DIR=/etc/fnmusic-sync`、`LOG_DIR=/var/log/fnmusic-sync`、`STATE_DIR=/var/lib/fnmusic-sync`。
2. `require_root`：`id -u` 不是 0 直接 `die "需要 root 权限，请改用：sudo sh $0 $ORIG_ARGS"`。脚本**不会**自动 sudo。
3. `check_fpk_conflict`：存在 `/var/apps/fnmusic-sync` 或环境里有 `TRIM_APPDEST` 就报「fpk 安装与传统安装方式互斥」并退出。
4. `installed_version`：按 `/usr/bin/fnmusic-sync` → `/usr/local/bin/fnmusic-sync` 顺序找已装二进制，跑 `version` 取第一行第二个字段。
5. 若已装且 `systemctl is-active` 为真：先 `systemctl stop fnmusic-sync`；再用 `pidof` 兜底 `kill` 残留进程并 `sleep 1`。
6. `detect_package_info`：`uname -m` 映射到 `amd64`/`arm64`（其它架构直接 `die`），并要求 `dpkg` 存在，否则 `die "未检测到支持的包管理器（dpkg），无法自动安装。"`。
7. `latest_version`：`curl -fsSL "${REPO}/-/releases"` 抓页面，用正则取第一个 `releases/tag/vX.Y.Z`。抓不到就 `die "无法解析最新版本号，请用 --version vX.Y.Z 指定"`。
8. 拼 URL：`${REPO}/-/releases/download/${VERSION}/fnmusic-sync_${arch}.deb`，`curl` 到 `mktemp -d` 的临时目录（`--connect-timeout 10 --max-time 300`）。
9. `dpkg -i <tmp>.deb`。失败则 `die "包管理器安装失败"`。**脚本不校验任何校验和**——Release 附件里有 `SHA256SUMS`，但脚本既不下载也不比对。
10. 清掉 `/usr/local/bin/fnmusic-sync`（仅当 `/usr/bin/fnmusic-sync` 也存在），避免双份二进制。
11. 打印 `安装完成：fnmusic-sync (x.y.z)`；版本号有变化时打印 `升级前版本：… （配置已保留）` 并在服务已 `enable` 的情况下 `systemctl restart`。
12. 收尾打印配置/数据库/日志三个路径，判断服务是否 active，未运行则提示 `systemctl enable --now fnmusic-sync`。

`--version vX.Y.Z` 只影响第 8 步的下载地址；`--purge` 只影响卸载动作；`-h/--help` 的 `usage()` 用 `sed -n '2,16p'` 打印脚注，因此帮助里看不到 `-h` 与 `FNMUSIC_REPO` 两行说明。

> **当前 HEAD 的硬缺陷**：`scripts/install.sh` 里有 `do_install` 函数最后一条日志少了收尾引号，`}` 被当成字符串的一部分，整个文件解析到 EOF 失败。原文长这样：

```sh
  log "卸载：    sudo apt remove ${BINARY}
}
```

实测 `sh scripts/install.sh --help` 直接输出 `scripts/install.sh: line 304: unexpected EOF while looking for matching '"'` 并以 `2` 退出，**任何动作都不会执行**，所以 README 里那三条 `curl -fsSL <脚本地址> | sudo sh` 现在全部不可用。修法是在那一行的 `${BINARY}` 后面补一个 `"`（让字符串闭合）；在修复合入前，请走「手动 `dpkg -i`」或 `apt` 路径安装，走 `fnmusic-sync self-upgrade` 升级。

## systemd 单元

unit 文件不是仓库里的静态模板，而是运行时由 `github.com/kardianos/service` v1.3.0 生成（`cmd/fnmusic-sync/cmd_service.go` 的 `newService`），落在 `/etc/systemd/system/fnmusic-sync.service`。安装脚本与 deb 的 `postinstall` 都会调用 `fnmusic-sync service install` 触发写入。

代码里设置的选项与 v1.3.0 模板的实际渲染结果并不完全一致，按库源码（`service_systemd_linux.go` 的 `systemdScript` 常量）渲染出来是这样：

```ini
[Unit]
Description=fnmusic-sync
ConditionFileIsExecutable=/usr/bin/fnmusic-sync
After=network.target

[Service]
StartLimitInterval=5
StartLimitBurst=10
ExecStart=/usr/bin/fnmusic-sync
Restart=always
RestartSec=120
EnvironmentFile=-/etc/sysconfig/fnmusic-sync

[Install]
WantedBy=multi-user.target
```

三处必须知道的差异：

| 代码里的期望                       | 实际落地                                                               |
|------------------------------------|------------------------------------------------------------------------|
| `Option["RestartSec"] = "5"`       | 模板把 `RestartSec=120` 写死，选项不生效——崩溃后要等 2 分钟才重启      |
| `Option["KillSignal"] = "SIGTERM"` | 模板里没有该指令；实际停止信号由 systemd 默认 `SIGTERM` 提供，行为一致 |
| 早期的 `SystemdScript` 自定义模板  | 已被移除（`docs/ai.md` 的描述是旧方案），现在不覆盖模板                |

`Dependencies: []string{"After=network.target"}` 渲染成 `After=network.target`，没有 `Wants=`/`Requires=`，所以 unit 不保证网络与飞牛音乐先起来——这就是主进程要自己 `waitForTakeover` 死等 socket 的原因。

`EnvironmentFile=-/etc/sysconfig/fnmusic-sync`（前缀 `-` 表示文件不存在也继续）是唯一可用的环境变量注入点，适合临时塞 `wizard_debug=1`。

### `service` 子命令怎么改 unit

`service debug` / `service nodebug` 不碰配置、不重启进程管理器，而是直接做文件级手术（`toggleDebug`）：

1. 读 `/etc/systemd/system/fnmusic-sync.service`，失败则提示 `请先执行 fnmusic-sync service install`。
2. 找到 `ExecStart=` 行：开启时行尾追加 ` --debug`；关闭时 `strings.ReplaceAll(line, " --debug", "")`。已经是目标状态则打印 `debug 模式已开启，无需重复设置` / `debug 模式未开启，无需关闭` 并直接返回（此时**不会**重启服务）。
3. `systemctl daemon-reload` → `systemctl restart fnmusic-sync`。

副作用：这一行是纯文本追加，所以 `service debug` 之后 `systemctl cat` 能看到 `ExecStart=/usr/bin/fnmusic-sync --debug`；而被 `dpkg` 重装或 `service uninstall`/`install` 重建 unit 后，这个改动会消失。

日常操作与 systemd 完全等价，任选一套：

```bash
systemctl status fnmusic-sync
systemctl restart fnmusic-sync
journalctl -u fnmusic-sync -f
fnmusic-sync service status      # 只打印「运行中 / 已停止 / 未知状态」
fnmusic-sync sockets             # 比 status 更有用：两个 socket 的归属与可连接性
```

## 权限与属主

### socket 为什么必须是 0666

链路是 `nginx(worker 跑 www-data) → /var/run/trim_music.socket → 代理`。socket 文件权限是内核在 `connect()` 时检查的，0660 root:root 会让 nginx 直接 `EACCES`，页面表现为 502。

代理的处理（`internal/proxy/socket.go` 的 `effectiveMode`）：

- `server.socket_mode: 0`（默认）= 沿用官方 socket 的权限与属主（`captureMode` 在 rename 前记录）；
- 若最终权限的 other 位为 0，强制补上 `0o006`；
- 官方 socket 读不到时兜底 `DefaultSocketMode = 0o666`；
- `applyOwner` 把属主 chown 回官方 socket 的 uid/gid，失败只打 `设置 socket 属主失败（不影响 nginx 连接）`。

`--check` 会直接把这层风险喊出来：权限不允许 other 访问时打印 `警告 … 权限不允许 other 访问，nginx(www-data) 将无法连接，会表现为 502`。

### 读曲库

`library.directories` 里的路径必须能被**服务进程身份**读到：两种形态都是 root，所以问题通常不在权限而在于「路径根本没挂进来」。

- NAS 本机曲库：直接填真实路径（如 `/media/音乐`），或者填飞牛共享挂载点。
- 曲库在别的机器：先挂 SMB/NFS 到本机，再填挂载点。注意挂载要在服务启动前就绪；挂载晚到不会自动补扫，靠 `library.scan_interval`（默认 `6h`）的下一次扫描，或在 Web UI 里手动触发 `POST /api/library/scan`。
- fpk 形态下应用对文件系统的可见范围受 `fpkg/config/resource` 的 `api-scope`（`trim.file.path`、`trim.file.userAccess`）与共享定义约束，读不到时先确认目录是否在应用可访问的共享内。

### 日志目录不可写时的降级

`setupLogger` 的行为是：`os.MkdirAll(dir, 0o755)` 失败 → 打 `日志目录创建失败，仅输出到控制台` → 返回纯 stderr 的 logger，**不阻断启动**。所以「服务在跑但没有日志文件」首先查目录权限与属主，其次查配置里 `logging.enabled` 是不是被关掉了（`configs/config.example.yaml` 里默认写的是 `false`，而程序内置默认是 `true`，两者不一致，容易踩）。

## 升级与回滚

### 升级通道一览

| 形态 | 命令                                             | 实际动作                                                                                                                               |
|------|--------------------------------------------------|----------------------------------------------------------------------------------------------------------------------------------------|
| deb  | `sudo fnmusic-sync self-upgrade`                 | 查最新 tag → 拼 deb 下载地址 → 下载到临时文件 → `dpkg -i` → `nohup systemctl restart`                                                  |
| deb  | `sudo sh install.sh upgrade`（脚本修好后）       | 停服务 → 下载 deb → `dpkg -i` → 版本变化时 `systemctl restart`                                                                         |
| deb  | `sudo sh install.sh upgrade --version v0.0.32`   | 同上，但跳过「解析最新版本」一步                                                                                                       |
| fpk  | 应用中心「升级」或 Web UI 的 `POST /api/upgrade` | 下载 `fnmusic-sync-<arch>.fpk` → 解外层 tar.gz → `appcenter-cli install-local -d <dir> -v <vol> --env <config.env>`（setsid 后台执行） |

脚本形式的一键命令（不建议直接 `curl <地址> | sudo sh`：管道给 `sudo` 时 `sh -s --` 的参数传递很容易写错，先落盘更稳）：

```bash
curl -fsSL https://cnb.cool/dtapp/fnmusic-sync/-/git/raw/main/scripts/install.sh -o /tmp/install.sh
sudo sh /tmp/install.sh upgrade
sudo sh /tmp/install.sh upgrade --version v0.0.32
```

`self-upgrade` 的版本来源由编译期注入的 `REPO_SOURCE` 决定（`internal/updater`）：

| `REPO_SOURCE`      | 取 tag 的接口                                                                                              | 下载地址                                                       | 令牌                                           |
|--------------------|------------------------------------------------------------------------------------------------------------|----------------------------------------------------------------|------------------------------------------------|
| `github`           | `api.github.com/repos/dtapps/fnmusic-sync/releases?per_page=20`（列表，为了读到 `prerelease` / `draft`）   | `<repo>/releases/download/<tag>/<file>`                        | `GithubToken` 可选                             |
| `gitlab`           | `gitlab.com/api/v4/projects/dtapps%2Ffnmusic-sync/releases`（`released_at` 倒序，跳过 `upcoming_release`） | `<repo>/-/releases/<tag>/downloads/<file>`（302 到通用包仓库） | `GitlabToken` 可选（私有项目必需，`read_api`） |
| 其它（默认 `cnb`） | `api.cnb.cool/dtapp/fnmusic-sync/-/releases`                                                               | `<repo>/-/releases/download/<tag>/<file>`                      | `CnbToken` **必需**                            |

**CNB 路径要求二进制里带 `CnbToken`**，否则直接返回 `CNB 访问令牌未配置，暂时无法自动升级，请前往 … 手动下载`——自建或改源构建时这是第一个会撞的墙。GitLab 公开项目可匿名读 releases API；私有项目不带 `GitlabToken` 会拿到 404（GitLab 对未认证请求不说 401），提示语会让你去配 token。

下载会先走统计中转 `dl-stats.dtapp.net`（三个来源统一，中转不挑源站：GitHub 的 release asset 与 GitLab 的 Release 直链都实测能转发），失败自动回退源站。`SHA256SUMS`、`GIT_COMMIT`、`BUILD_TIME` 三个元数据文件在任何来源下都不走中转，否则统计会被元数据请求污染。

**预发布怎么排除**：三个平台的信号能力不一样——GitHub 与 CNB 的 releases API 都有 `prerelease` 字段；**GitLab 的 Release 根本没有预发布概念**（只有 `upcoming_release`，语义是「计划发布」，不是预发布）。所以判定取**并集**：平台字段标了 **或** tag 带 semver 后缀（`v1.2.3-rc.1`、`v1.2.4-beta`），任一成立就算预发布，`FetchLatestTag` 优先返回正式版。三个平台只要有一个信号到位就不会漏，这就是“后缀约定 + prerelease 字段”的兼容之处：

| 当前版本       | 会拿到什么                                                       |
|----------------|------------------------------------------------------------------|
| 稳定版         | 最新的**正式版**；只有预发布时退回最新一条（不至于永远升不了级） |
| `-rc` 等预发布 | 最新的预发布（预发布用户能继续往前走）                           |
| `dev`（本地）  | 同上，视为预发布客户端                                           |

这套规则**依赖发布时的 tag 命名**，所以发版流水线里**只有一个参数：版本号**。五个平台（CNB / GitHub / GitLab / Gitee / GitCode）共用同一份判定（`scripts/ci/resolve-prerelease.sh`），没有开关、没有「是否预发布」字段可填：

| 版本号形态               | 判定     | 说明                                                   |
|--------------------------|----------|--------------------------------------------------------|
| `v1.2.3`                 | 正式版   | `PRERELEASE=false`                                     |
| `v1.2.3-rc.1`            | 预发布   | 带 semver 的 `-<prerelease>` 段就是预发布              |
| `v1.2.3-beta.1`          | 预发布   | 同上                                                   |
| `v1.2.3-alpha.1`         | 预发布   | 同上（`-dev` / `-pre` 之类也一样，判法与客户端一致）   |
| 空 / `v` / `1.2` / `abc` | 直接失败 | `+build` 元数据也挡：deb 与 fpk 的版本段没为它做过处理 |

判法与客户端 `internal/updater.isPrereleaseTag` 逐字对齐 —— 两边不一致的后果是「发出去的版本没人能升上去」或者「稳定用户被推到 rc 上」，两种都撤不回来。

为什么把那个开关彻底删掉：它跟版本号会打架，而打架的时候必然是在用户什么都没做错的时候打。CNB 的 `switch` 踩过：`web_trigger.yml` 里写死 `default: 'false'`，实际传进来仍是 `true`，「`v0.1.31` + 预发布」撞上硬失败，red 在第一步，页面上没有任何东西能让用户看出这个 `true` 从哪来。版本号里已经写了 `-rc.1`，再让人勾一遍只是多一个出错的地方。

**deb 的版本号要单独转一道**：`0.1.32-rc.1` 在 dpkg 眼里**比 `0.1.32` 新**——Debian 版本是 `upstream-revision`，同 upstream 时带 revision 的那份更大，用户装了 rc 之后正式版就成了降级。所以 deb 元数据用 tilde：`0.1.32~rc.1`（排序低于空串）。这步在 `Makefile` 的 `DEB_VERSION` 里，只作用于 deb；fpk 的 manifest 与前端 `package.json` 仍用原样 semver。

`self-upgrade` 的重启用意很直白：升级流程本身跑在 systemd 管理的进程里，`systemctl restart` 会先杀掉自己，所以它用 `sh -c "nohup systemctl restart … &"` 脱离当前进程树，让 stop→start 能跑完。

### 升级是否保留配置

保留。deb 的 `postinstall` 除最后打印一段帮助文本外，只做四件事：`mkdir -p` 三个目录（存在则跳过）、`chmod 0755` 二进制、`fnmusic-sync service install`、`systemctl enable --now`。它不写 `config.yaml`，配置文件由程序首次启动时 `config.SaveDefault` 生成。所以 `/etc/fnmusic-sync/config.yaml` 跨版本存活。

fpk 的向导值（`wizard_debug`）由 `fpkg/cmd/{install,upgrade,config}_callback` 持久化到 `$TRIM_PKGETC/config.env`，`upgrade_init`/`upgrade_callback` 在 `config.env` 已有该行时**不覆盖**，避免升级把用户开的 debug 冲成默认 `false`。`appcenter-cli install-local` 走的是「卸载 → 重装」，所以 Web UI 触发的升级会显式 `--env $TRIM_PKGETC/config.env` 把向导值带回去。

### 回滚

```bash
# 1) 备份（升级前做，停服务后拷贝最稳）
sudo systemctl stop fnmusic-sync
sudo mkdir -p /root/fnmusic-backup
sudo cp -a /etc/fnmusic-sync/config.yaml /root/fnmusic-backup/
sudo sqlite3 /var/lib/fnmusic-sync/state.db ".backup '/root/fnmusic-backup/state.db'"
sudo sqlite3 /var/lib/fnmusic-sync/data.db ".backup '/root/fnmusic-backup/data.db'"
sudo systemctl start fnmusic-sync

# 2) 回滚二进制：卸掉新版本，装回旧 deb
sudo dpkg -i /root/fnmusic-sync_amd64.deb.old   # 旧版本包
sudo systemctl restart fnmusic-sync

# 3) 校验
fnmusic-sync version
sudo fnmusic-sync --check
```

数据库兼容性上可以放心一半：`db.Open` 与 `datadb.Open` 执行的是整份 `schema.sql`（全部 `CREATE TABLE IF NOT EXISTS` / `CREATE INDEX IF NOT EXISTS`），没有破坏性迁移；唯一的真实迁移是 `migrateUsersTokenKey`（按 `username` 唯一 → 按 `token_prefix` 唯一，重建表并保留数据），它是幂等的，且旧结构检测不到就直接跳过。回滚到旧版本时，新版本建过的表/列会留着，旧代码不读它们即可。真正不可逆的是**数据内容**：`playback_log`、`sync_log` 在被回滚的时间段里已经写入的行不会消失，但也不会错乱。

## 卸载

| 形态 | 命令                           | 行为                                                                                                                                               |
|------|--------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------|
| deb  | `sudo apt remove fnmusic-sync` | 先跑 `scripts/preremove.sh`：`systemctl stop` + `disable`、`service stop`/`uninstall`、删 unit、`daemon-reload`、`pidof` 兜底 kill；然后移除包文件 |
| deb  | `sudo apt purge fnmusic-sync`  | 同上；`purge` 只清包清单内的文件（实际只有 `/usr/bin/fnmusic-sync`），配置与数据不在清单里                                                         |
| deb  | `sudo sh install.sh uninstall` | `service stop`/`uninstall` → 删 unit → `dpkg -r` → `rm -f /usr/bin` 与 `/usr/local/bin`；`--purge` 再删三个目录                                    |
| fpk  | 应用中心「卸载」               | 卸载向导 `wizard_keep_data`（默认 `true`）决定 `uninstall_callback` 是否 `rm -rf $TRIM_PKGETC $TRIM_PKGVAR`                                        |

会保留什么，取决于包清单里到底有什么。`.nfpm.yaml` 的 `contents` 只有一项——二进制 `src: ./bin/fnmusic-sync-linux-${ARCH}` → `dst: /usr/bin/fnmusic-sync`。`/etc/fnmusic-sync`、`/var/log/fnmusic-sync`、`/var/lib/fnmusic-sync` 是 `postinstall` 用 `mkdir -p` 建的，`config.yaml`、`state.db`、`data.db` 是程序运行时生成的，**都不在包清单里**。因此：

- `apt remove` / `apt purge` 之后，上面三个目录与其中内容**依然在**。「`apt purge` 会连配置一起删」是 README 里的旧说法，与包结构不符——要彻底清掉必须手动执行：

  ```bash
  sudo apt remove fnmusic-sync
  sudo rm -rf /etc/fnmusic-sync /var/log/fnmusic-sync /var/lib/fnmusic-sync
  ```

- `install.sh uninstall --purge` 走的是脚本自己的 `rm -rf "$CONFIG_DIR" "$LOG_DIR" "$STATE_DIR"`，这条路确实会删干净（前提是脚本能执行，见上文缺陷）。
- fpk 卸载且 `wizard_keep_data=true`：保留 `$TRIM_PKGETC`（配置 + `config.env`）与 `$TRIM_PKGVAR`（数据库 + 日志 + pid/lock）；向导选 `false` 时 `uninstall_callback` 直接 `rm -rf` 这两个目录。

**卸载前必须让进程正常退出**。`Takeover.Restore()` 挂在 `main` 的 `defer` 上，靠 SIGTERM 触发；`preremove` 与 fpkg `main stop` 都是先 `TERM` 再 `KILL`（fpkg 等 10 秒）。如果进程被 `kill -9` 或机器掉电，官方 socket 会留在 `/var/run/trim_music_upstream.socket`，此时飞牛音乐会 502，手动恢复：

```bash
sudo mv /var/run/trim_music_upstream.socket /var/run/trim_music.socket
```

## 运维例行

### 日志与磁盘占用

轮转由 lumberjack 负责，参数全部来自 `logging.*`：`max_size`（MB，默认 3，0=不按大小切）、`max_backups`（默认 5，0=不限份数）、`max_age`（默认 7 天，0=不过期）、`compress`（默认 gzip）。历史文件由 lumberjack 命名，形如 `fnmusic-sync-<时间戳>.log` / `.log.gz`。

按默认值估算单份日志上限：

| 文件                              | 何时产生               | 上限估算（3MB × (5+1)） |
|-----------------------------------|------------------------|-------------------------|
| `fnmusic-sync.log`                | 常开                   | ≈ 18 MB                 |
| `capture.log`                     | 仅 `--debug`           | ≈ 18 MB                 |
| `feiniu.log`                      | 仅 `--debug`           | ≈ 18 MB                 |
| `lastfm.log`                      | 仅 `--debug`           | ≈ 18 MB                 |
| `listenbrainz.log`                | 仅 `--debug`           | ≈ 18 MB                 |
| `goroutine-dump.txt`              | 仅 `--debug` + SIGUSR1 | ≈ 18 MB                 |
| `info.log` / `upgrade.log`（fpk） | 常开（飞牛脚本写）     | 无轮转，注意体积        |

也就是说常态约 18 MB，全 debug 约 110 MB。压缩后通常只有几 MB，但**份数与天数才是真实闸门**：把 `max_backups` 或 `max_age` 设成 0 就等于放弃上限，大流量设备上会被 `capture.log` 吃满分区。

日志目录位置：deb `/var/log/fnmusic-sync/`，fpk `$TRIM_PKGVAR/logs/`。快速定位 fpk 的实际目录：

```bash
sudo find /vol* -maxdepth 6 -path '*fnmusic-sync*' -name 'config.yaml' 2>/dev/null
```

### 备份

需要备份的只有三样（fpk 多一个第四样）：

| 文件                | 为什么                                                |
|---------------------|-------------------------------------------------------|
| `config.yaml`       | 用户凭证、阈值、歌单开关，丢了就得重新走 Last.fm 授权 |
| `state.db`          | 推送统计、按 token 的用户列表、播放记录、同步记录     |
| `data.db`           | MBID ↔ 飞牛 GUID 映射，重建要重新扫全库               |
| `config.env`（fpk） | 向导持久化值（如 `wizard_debug`）                     |

sqlite 不要用 `cp` 热备（单连接 + 默认 journal，拷贝可能拿到撕裂页）。用 `.backup` 或先停服务。备份脚本示例：

```bash
sudo systemctl stop fnmusic-sync
D=/root/fnmusic-backup/$(date +%F)
sudo mkdir -p "$D"
sudo cp -a /etc/fnmusic-sync/config.yaml "$D/"
sudo sqlite3 /var/lib/fnmusic-sync/state.db ".backup '$D/state.db'"
sudo sqlite3 /var/lib/fnmusic-sync/data.db ".backup '$D/data.db'"
sudo systemctl start fnmusic-sync
```

### 监控点

| 监控点             | 怎么看                                                                                                           | 正常长什么样                                                                                               |
|--------------------|------------------------------------------------------------------------------------------------------------------|------------------------------------------------------------------------------------------------------------|
| 代理健康           | `curl -s --unix-socket /var/run/trim_music.socket http://localhost/_ext/healthz`                                 | `{"ok":true,"upstream":"ok","socket":"/var/run/trim_music_upstream.socket"}`；上游挂了返回 503 + `"error"` |
| socket 归属        | `fnmusic-sync sockets` 或 fpk 的 `GET /api/sockets`                                                              | `结论：✅ 正常（监听可达、飞牛音乐在线）`；`all_healthy: true`                                             |
| 服务状态           | `systemctl is-active fnmusic-sync` / `systemctl status`                                                          | `active`                                                                                                   |
| Web UI 健康（fpk） | `GET /app/fnmusic-sync/api/health`                                                                               | `{"ok":true,"config":"…","logDir":"…"}`                                                                    |
| 推送量             | `state.db` 的 `run_status`：`total_scrobbles`、`lastfm_scrobbles`、`listenbrainz_scrobbles`、`last_scrobbled_at` | `last_scrobbled_at` 随听歌推进                                                                             |
| 歌单同步           | `state.db` 的 `sync_log`：`status` / `trigger` / `playlists` / `tracks` / `message`                              | `status='success'`，`finished_at` 在 `sync_interval` 内                                                    |
| 播放记录           | `state.db` 的 `playback_log`：`started_at`、`ended_at`                                                           | `ended_at` 常为 NULL（飞牛无 end 事件，仅在下一首或退出时回填）                                            |

只读查库（避免写锁）：

```bash
sudo sqlite3 -readonly /var/lib/fnmusic-sync/state.db \
  "SELECT username,total_scrobbles,lastfm_scrobbles,listenbrainz_scrobbles,last_scrobbled_at FROM run_status ORDER BY total_scrobbles DESC LIMIT 10;"

sudo sqlite3 -readonly /var/lib/fnmusic-sync/state.db \
  "SELECT username,provider,status,trigger,playlists,tracks,finished_at FROM sync_log ORDER BY finished_at DESC LIMIT 10;"

sudo sqlite3 -readonly /var/lib/fnmusic-sync/state.db \
  "SELECT username,title,artist,started_at,ended_at FROM playback_log ORDER BY started_at DESC LIMIT 10;"
```

老版本 `sqlite3` 不认 `-readonly` 时，用 URI 打开：`sudo sqlite3 "file:/var/lib/fnmusic-sync/state.db?mode=ro" "SELECT count(*) FROM run_status;"`。

## fnOS 特有注意事项

1. **不要在应用中心重启「飞牛音乐」。** trim-music 重启时会 `unlink` 再重新 `bind` 到 `/var/run/trim_music.socket`，代理手里的监听 socket 变成「已删除 inode」，nginx 直接连到官方，代理被旁路——页面正常、scrobble 全停。恢复方式是重启代理，让它重新接管：

   ```bash
   sudo systemctl restart fnmusic-sync     # deb
   # fpk：在应用中心「停止 → 启动」本应用
   sudo fnmusic-sync sockets               # 确认 上游=飞牛音乐(官方)
   ```

   代理自己也考虑了这种局面：`Takeover.Prepare()` 会处理「原路径与上游路径都是官方后端」的情况（打 `原路径与上游路径都是官方后端，保留上游并移除原路径`），所以即便顺序颠倒，重启代理通常就能自愈。

2. **fpk 环境变量**。`TRIM_APPDEST`/`TRIM_PKGETC`/`TRIM_PKGVAR` 由飞牛注入到应用脚本与进程；手工在 shell 里跑 fpk 二进制时它们不存在，程序会退化成传统模式（去找 `/etc/fnmusic-sync`）。要在命令行复现应用环境，必须带上这些变量。

3. **应用中心升级与自升级的冲突**。deb 形态的 `self-upgrade` 用 `dpkg -i`；fpk 形态**没有** `self-upgrade`（`//go:build !fpk` 排除了它），升级只能走应用中心或 Web UI 的 `/api/upgrade`（它内部又调 `appcenter-cli install-local`，本质还是应用中心的那套流程）。不要在 fpk 环境里手工 `dpkg -i`——`install.sh` 会直接拦（`检测到已通过 fpk 方式安装`），但裸 `dpkg` 不会。

4. **安装形态互斥的判定依据**：脚本看 `/var/apps/fnmusic-sync` 目录与环境变量 `TRIM_APPDEST`。想换形态必须先把旧形态清干净（含 `/etc/fnmusic-sync`、`/var/lib/fnmusic-sync` 里的数据要么备份要么接受丢失）。

5. **`appcenter-cli` 是 Web UI 升级的硬依赖**。`POST /api/upgrade` 找不到它会直接返回 `未检测到 appcenter-cli 工具，请通过飞牛应用中心手动升级`。
