# 命令行参考

`fnmusic-sync` 只有一个二进制：不带子命令就是前台跑代理，带子命令则做安装、升级、体检这类一次性动作。

> 相关：[README](../README.md) · [配置参考](configuration.md) · [部署](deployment.md) · [排障](troubleshooting.md)

## 1. 命令总览

```text
fnmusic-sync                          # 根命令：前台运行代理（无子命令）
├── version                           # 打印版本 / Git 提交 / 构建时间 / 平台 / 各路径
├── sockets                           # 查看两个固定 socket 的状态，并给"是否正常"结论
├── self-upgrade                      # 下载 deb 安装包并升级（build tag !fpk）
└── service                           # systemd 服务管理（build tag !fpk）
    ├── install                       # 注册 systemd 单元
    ├── uninstall                     # 移除 systemd 单元
    ├── start
    ├── stop
    ├── restart
    ├── status
    ├── debug                         # ExecStart 追加 --debug + daemon-reload + restart
    └── nodebug                       # 移除 --debug + daemon-reload + restart
```

关键机制：`self-upgrade` 与整个 `service` 家族所在的文件带 `//go:build !fpk`（`cmd/fnmusic-sync/cmd_upgrade.go:1`、`cmd/fnmusic-sync/cmd_service.go:1`），而 fpk 包是用 `-tags fpk` 构建的（`Makefile` 的 `build_binary` 传 `-tags "$(4)"`，`BUILD_TAG = fpk`）。所以**应用中心装的那份二进制里没有这两个子命令**，`fnmusic-sync service status` 会被 cobra 判为未知子命令：

```bash
$ fnmusic-sync service status
❌ unknown command "service" for "fnmusic-sync"
$ echo $?
1
```

`version` 与 `sockets` 两种构建都有，且都是只读命令，不需要 root。

## 2. 全局 flag

三个 flag 都注册在根命令的 `PersistentFlags()` 上（`cmd/fnmusic-sync/root_cmd.go:35-44`），因此任何子命令语法上都接受它们；只有根命令（代理模式）真正读取。

| flag              | 类型     | 默认值  | 作用                                                                                             |
|-------------------|----------|---------|--------------------------------------------------------------------------------------------------|
| `--check`         | bool     | `false` | 只体检，不接管 socket、不启动代理                                                                |
| `--debug`         | bool     | `false` | 打开请求/响应明细日志（`capture.log` 及三个 reqlog 文件），并启用 goroutine 监视器               |
| `--upstream-wait` | duration | `30s`   | 等待官方后端就绪的超时。**实际被配置文件的 `server.upstream_wait` 覆盖**（默认可解析，必然覆盖） |

### 2.1 `--check` 做了什么

走 `check()`（`cmd/fnmusic-sync/check.go`），全程只读：

1. `ProbeSocket` 探测两个 socket 的持有者（`none` / `trim` / `proxy` / `stale`）；
2. `SocketMode` 读权限与属主，`other` 位全 0 时打警告（nginx 以 `www-data` 运行，会 502）；
3. 判断官方后端此刻到底在哪个路径上（未接管时在 listen，接管后在 upstream）；
4. 通过上游 socket 发一次真实请求验证连通性；
5. 两个 socket 都没探测到官方进程时再补一条警告。

输出是终端表格 + 日志结论双写，表格用 `%-45s` 对齐。示意（值取决于实际环境）：

```text
监听   /var/run/trim_music.socket                    归属=proxy
上游   /var/run/trim_music_upstream.socket           归属=trim
权限   /var/run/trim_music.socket                    0666 属主=0:0
权限   /var/run/trim_music_upstream.socket           0666 属主=0:0
官方后端连通性(/var/run/trim_music_upstream.socket)：正常
```

怎么读：

| 输出                                              | 含义                                                     |
|---------------------------------------------------|----------------------------------------------------------|
| `归属=trim` + `归属=proxy`                        | 正常接管态：本代理在监听，官方在 upstream                |
| 出现 `提示：尚未接管，飞牛音乐当前为官方直连`     | 代理没在跑，或刚被官方后端抢回路径                       |
| `警告 ... 权限不允许 other 访问`                  | 一定会 502，检查 `server.socket_mode` 与官方 socket 权限 |
| `官方后端连通性(...)：失败 (...)`                 | 命令返回错误、退出码 1                                   |
| `警告：两个 socket 上都没有探测到官方 trim-music` | 飞牛音乐没启动，或僵死；退出码仍是 0                     |

### 2.2 `--debug` 改了什么

| 影响面                                           | 说明                                                                                                           |
|--------------------------------------------------|----------------------------------------------------------------------------------------------------------------|
| `capture.log`                                    | 请求/响应体、头（鉴权类头已脱敏）写入日志目录（`internal/proxy/capture.go`）                                   |
| `feiniu.log` / `lastfm.log` / `listenbrainz.log` | 三个外部 API 客户端的请求明细（`internal/reqlog`）                                                             |
| goroutine 监视器                                 | 每 30s 检查 goroutine 数，≥60 才输出白名单过滤后的可疑栈（冷却 5 分钟）；`SIGUSR1` 手动落 `goroutine-dump.txt` |
| **slog 级别**                                    | **不受影响**。级别只由 `logging.level` 决定（`cmd/fnmusic-sync/logging.go:36-47`）                             |

`--debug` 与 fpk 安装向导字段 `wizard_debug` 等价：`captureEnabled.Store(debug || isDebugEnv())`（`cmd/fnmusic-sync/main.go:120-124`）。

### 2.3 `--upstream-wait` 的解析

`durationFlag` 包装 `time.Duration`，非法值由 pflag 直接报错退出：

```bash
$ fnmusic-sync --upstream-wait=abc
❌ invalid argument "abc" for "--upstream-wait" flag: time: invalid duration "abc"
```

合法值随后仍会被配置文件覆盖（见 [configuration.md](configuration.md) 的 `server.upstream_wait`）：只有把配置写成非法值时，flag 才真正起作用。

## 3. 根命令（前台运行代理）

```bash
fnmusic-sync                     # 前台运行
fnmusic-sync --check             # 体检
fnmusic-sync --debug             # 前台运行 + 明细日志
sudo nohup fnmusic-sync >/dev/null 2>&1 &   # 后台常驻
```

- 权限：读写 `/var/run/trim_music*.socket`、写状态库与日志目录都需要 root（或对应用户权限）。非 root 时常见降级：日志目录不可写 → 仅控制台；`flock` 拿不到 → 跳过单实例保护。
- 启动顺序：读配置 → 生成默认配置（若缺失）→ 打开 `state.db`、`data.db` → 建代理与歌单同步服务 → 首次 `applyConfig` → 启动配置监听 → Last.fm 授权 / ListenBrainz 用户名补全 → 抢单实例锁 → 接管 socket → 等上游就绪 → 启动歌单同步 → （fpk）启动 Web UI → 进入服务循环（`cmd/fnmusic-sync/main.go:79-349`）。
- 退出：收到 `SIGINT` / `SIGTERM` 后还原 socket、回填未结束的播放记录、关闭数据库。任何退出路径都会 `Restore()`，不会把官方后端留在 upstream 路径上。

典型启动日志（字段名固定，便于 grep）：

```text
版本信息 版本=… Git提交=… 平台=linux/amd64 运行模式=传统安装
启动配置 监听=/var/run/trim_music.socket 上游=/var/run/trim_music_upstream.socket 调试=false 配置=… 数据库=… 日志目录=…
上游已就绪 上游=/var/run/trim_music_upstream.socket
```

常见致命错误（返回后 main 打印 `❌ <err>` 并退出 1）：

| 错误文案                                                                                   | 原因                       |
|--------------------------------------------------------------------------------------------|----------------------------|
| `打开数据库失败 (<path>): …`                                                               | 数据目录不可写 / 文件损坏  |
| `已有实例在运行，拒绝重复启动（lock=<path>）。请先停止现有实例：fnmusic-sync service stop` | flock 已被占用             |
| `等待飞牛音乐 socket 时被信号中断: …`                                                      | 等 socket 期间收到 SIGTERM |
| `上游不可用 (<path>): …`                                                                   | 接管后官方后端没起来       |

## 4. `version`

```bash
fnmusic-sync version
```

无参数。输出为「首行 `二进制名 版本`，其余为对齐的标签-值行」（`cmd/fnmusic-sync/version.go`）。示意：

```text
fnmusic-sync v1.2.3
  版本          v1.2.3
  Git提交       a1b2c3d
  构建时间      2026-01-02 03:04:05 UTC（本地 2026-01-02 11:04:05 +0800 CST）
  Go版本        go1.27.3
  平台          linux/amd64
  运行模式      fpk（飞牛应用中心）
  监听Socket    /var/run/trim_music.socket
  上游Socket    /var/run/trim_music_upstream.socket
  配置          /vol1/@appetc/fnmusic-sync/config.yaml
  数据库        /vol1/@appdata/fnmusic-sync/state.db
  日志          /vol1/@appdata/fnmusic-sync/logs/fnmusic-sync.log
```

- 本地 `go build` 未注入 ldflags 时版本是 `dev`，并多一行 `说明 本地构建（未注入发布版本号）`。
- 退出码恒为 0。

## 5. `sockets`

```bash
fnmusic-sync sockets
```

两个固定 socket 的详细状态 + 结论。数据来自 `proxy.ProbeSockets()`，与 Web UI 的 `GET /api/sockets` 同源。示意：

```text
监听 socket (/var/run/trim_music.socket)  ✅ 正常
  存在       true
  类型       unix socket
  权限       0666
  属主       0:0
  归属       本代理
  可连接     true

上游 socket (/var/run/trim_music_upstream.socket)  ✅ 正常
  存在       true
  类型       unix socket
  权限       0666
  归属       飞牛音乐(官方)
  可连接     true

结论：✅ 正常（监听可达、飞牛音乐在线）
```

异常时结论行给出具体原因：`监听 socket 不存在` / `监听 socket 不可连接（nginx 将无法连接，表现为 502）` / `上游 socket 不存在（飞牛音乐可能未启动）` / `上游 socket 不是飞牛音乐（归属=none）` 等。无论正常与否退出码都是 0。

## 6. `service`

**仅 `!fpk` 构建可用**，需要 root（写 `/etc/systemd/system`、调 `systemctl`）。

### 6.1 与 systemctl 的等价关系

| 命令                | 实际动作                                                                                | 等价 systemctl                       |
|---------------------|-----------------------------------------------------------------------------------------|--------------------------------------|
| `service install`   | kardianos/service 写 `/etc/systemd/system/fnmusic-sync.service`                         | —（`systemctl enable --now` 需另做） |
| `service uninstall` | 删除该 unit 文件                                                                        | `systemctl disable --now` 需另做     |
| `service start`     | `systemctl start fnmusic-sync`                                                          | 同义                                 |
| `service stop`      | `systemctl stop fnmusic-sync`                                                           | 同义                                 |
| `service restart`   | `systemctl restart fnmusic-sync`                                                        | 同义                                 |
| `service status`    | 打印 `运行中` / `已停止` / `未知状态`                                                   | `systemctl is-active` 的语义         |
| `service debug`     | 在 unit 的 `ExecStart=` 行尾追加 ` --debug`，然后 `systemctl daemon-reload` + `restart` | 手工改 unit + reload + restart       |
| `service nodebug`   | 删掉 `ExecStart=` 行里的 ` --debug`，再 reload + restart                                | 同上                                 |

`service install` 之后**不会**自动 `enable`，安装脚本另做这件事（`scripts/postinstall.sh` 调 `service install`，随后 `systemctl enable --now`）。

### 6.2 生成的 unit 长什么样

`newService()` 只给了名字、描述与几个 systemd option（`cmd/fnmusic-sync/cmd_service.go:28-42`）：

```text
Name         = fnmusic-sync
DisplayName  = fnmusic-sync
Description  = fnOS 飞牛音乐 Last.fm / ListenBrainz scrobble 代理
Dependencies = After=network.target
Option       = Restart=always, RestartSec=5, KillSignal=SIGTERM
```

实际落盘由 kardianos/service 的 systemd 模板决定（`kardianos/service@v1.3.0/service_systemd_linux.go`）。模板里被引用的只有 `Restart`；`RestartSec` 与 `KillSignal` 未被模板使用，unit 里固定是 `RestartSec=120`，停止信号走 systemd 默认的 `SIGTERM`。`ExecStart=` 指向安装时二进制的绝对路径、无附加参数（`--debug` 由 `service debug` 追加）。

### 6.3 `service debug` / `nodebug` 的边界

直接做文件级操作，逻辑很死（`cmd/fnmusic-sync/cmd_service.go:180-234`）：

- unit 文件固定为 `/etc/systemd/system/fnmusic-sync.service`（`unitFilePath()`）；
- 找不到文件 → `读取 unit 文件失败 <path>: …（请先执行 fnmusic-sync service install）`；
- 找不到 `ExecStart=` 行 → `unit 文件中未找到 ExecStart 行: <path>`；
- 已开启时再执行 `debug` → 打印 `debug 模式已开启，无需重复设置`，直接返回 0，**不重启**；
- 已关闭时再执行 `nodebug` → 同理；
- 写回权限固定 `0644`；
- reload 或 restart 失败 → 返回错误、退出码 1。

因为改的是 unit 文件而不是配置，`logging.level` 不受影响：`--debug` 只开明细日志，见 2.2。

## 7. `self-upgrade`

**仅 `!fpk` 构建可用**。流程（`cmd/fnmusic-sync/cmd_upgrade.go`）：

```text
① 检测包管理器（exec.LookPath("dpkg")）——只有 dpkg，其他发行版直接提示手动升级
② 取最新 tag（internal/updater.FetchLatestTag）——都只挑正式版，tag 带 -rc/-beta 后缀的跳过
      构建来源 github → api.github.com/repos/dtapps/fnmusic-sync/releases?per_page=20（取列表以读 prerelease/draft）
      构建来源 gitlab → gitlab.com/api/v4/projects/dtapps%2Ffnmusic-sync/releases（倒序取首条正式版）
      构建来源 cnb（默认） → api.cnb.cool/dtapp/fnmusic-sync/-/releases?page=1&page_size=20
③ 版本比对（updater.HasUpdate）：tag 与当前版本不同即升级；当前版本是 dev 时强制升级
④ 拼下载地址：<repo>/releases/download/<tag>/fnmusic-sync_<arch>.deb
      gitlab 源为 <repo>/-/releases/<tag>/downloads/<file>（Release 直链，302 到通用包仓库）
      下载优先走中转域名 dl-stats.dtapp.net（三个来源统一），失败自动回退原始地址
⑤ dpkg -i <临时文件>
⑥ 若服务在跑：nohup systemctl restart fnmusic-sync 异步重启（当前进程会被 SIGTERM 杀掉，所以必须脱离）
```

| 维度         | 行为                                                                                                                     |
|--------------|--------------------------------------------------------------------------------------------------------------------------|
| 权限         | 需要 root；`dpkg -i` 权限不足时额外打印 `💡 提示：请尝试使用 'sudo fnmusic-sync self-upgrade' 运行`                      |
| 版本来源     | 见②；`RepoSource`/`CnbToken` 由 Makefile 用 ldflags 注入到 `internal/buildinfo`                                          |
| Token 缺失   | CNB 构建且 `CnbToken` 为空 → `CNB 访问令牌未配置，暂时无法自动升级，请前往 https://cnb.cool/dtapp/fnmusic-sync 手动下载` |
| 配置文件     | 不覆盖 `/etc/fnmusic-sync/config.yaml`（deb 包不含该文件）                                                               |
| 回滚         | **没有自动回滚**。`dpkg -i` 失败时返回错误、旧版本二进制仍在原位；下载成功但安装失败不会降级                             |
| 重启失败提示 | 异步重启启动失败 → `✨ 升级成功！请手动重启服务：fnmusic-sync service restart`                                           |

失败的分级很容易误判，因为大部分「失败」都以成功退出码结束：

| 情形                     | 输出                                                | 退出码 |
|--------------------------|-----------------------------------------------------|--------|
| 没装 dpkg                | `❌ 未检测到支持的包管理器（dpkg），无法自动升级。` | 0      |
| 取版本失败（网络/Token） | `❌ <错误>`                                         | 0      |
| 已是最新                 | `✅ 已是最新版本 (x.y.z)，无需升级。`               | 0      |
| 下载失败 / 安装失败      | `❌ 升级失败: …`                                    | 1      |

脚本化判断升级结果时，别只看退出码，要 grep 输出里的 ✅/❌。

## 8. 退出码与错误文案

main 统一处理：`rootCmd.Execute()` 返回非 nil 就 `fmt.Fprintf(os.Stderr, "❌ %v\n", err)` 并 `os.Exit(1)`（`cmd/fnmusic-sync/main.go:50-55`）。**只有 0 和 1 两种退出码**，没有细分码。

| 文案前缀 / 形态                                                                                                                            | 来源                                                    |
|--------------------------------------------------------------------------------------------------------------------------------------------|---------------------------------------------------------|
| `❌ unknown command "x" for "fnmusic-sync"`                                                                                                | cobra，未知子命令（含 fpk 下的 service / self-upgrade） |
| `❌ unknown flag: --x` / `❌ invalid argument "v" for "--f" flag: …`                                                                       | pflag，flag 解析失败                                    |
| `❌ 读取配置文件 <path> 失败: …`                                                                                                           | `config.Load`                                           |
| `❌ 打开数据库失败 (<path>): …`                                                                                                            | `db.Open` / `datadb.Open`                               |
| `❌ 已有实例在运行，拒绝重复启动（lock=<path>）。…`                                                                                        | `run()` 单实例锁                                        |
| `❌ 上游不可用 (<path>): …`                                                                                                                | `WaitUpstream` 超时                                     |
| `❌ 等待飞牛音乐 socket 时被信号中断: …`                                                                                                   | `waitForTakeover` 被信号打断                            |
| `❌ 创建服务失败: …` / `❌ 安装失败: …` / `❌ 卸载失败: …` / `❌ 启动失败: …` / `❌ 停止失败: …` / `❌ 重启失败: …` / `❌ 查询状态失败: …` | `service` 家族                                          |
| `❌ 读取 unit 文件失败 <path>: …（请先执行 fnmusic-sync service install）`                                                                 | `service debug/nodebug`                                 |
| `❌ 写入 unit 文件失败: …` / `❌ daemon-reload 失败: …` / `❌ 重启服务失败: …` / `❌ unit 文件中未找到 ExecStart 行: <path>`               | 同上                                                    |
| `❌ 升级失败: …`                                                                                                                           | `self-upgrade`                                          |

非致命问题不会进这一栏，它们只写日志：配置热更新失败、日志目录不可写、Web UI 启动失败、`flock` 不可用等，都只在日志里 WARN，进程继续跑。
