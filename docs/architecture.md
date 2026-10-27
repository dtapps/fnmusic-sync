# 架构与运行原理

fnmusic-sync 是一个以 unix socket 接管的透明反向代理：请求原样转发给官方 trim-music，旁路解析播放事件推送 Last.fm / ListenBrainz，并把两家的歌单同步进飞牛音乐。

> 相关：[播放事件与 scrobble 判定](scrobble-pipeline.md) · [歌单同步机制](playlist-sync.md) · [配置参考](configuration.md) · [命令行](cli.md) · [Web UI API](webui-api.md) · [数据库结构](database.md) · [开发指南](development.md) · [部署](deployment.md) · [排障](troubleshooting.md)

## 定位与读者

本文写给两类人：

- **运维**：需要判断"代理到底有没有接管成功""为什么飞牛音乐 502""重启哪个进程能恢复"。
- **二次开发者**：需要在不破坏透明性的前提下加新能力，因此必须先弄清进程结构、socket 生命周期与数据流。

快速上手流程（安装、授权、常用命令）见 [README](../README.md)；本文只讲内部为什么这样跑。

## 系统边界

```text
                    ┌──────────────────────────────┐
   浏览器 / App ────▶│ fnOS 网关 / nginx (www-data) │
                    └──────────────┬───────────────┘
                                   │ HTTP over unix socket
                                   ▼
                 /var/run/trim_music.socket          ← 本代理 bind（0666 起）
                                   │
                    ┌──────────────┴───────────────┐
                    │ fnmusic-sync 进程             │
                    │                              │
                    │ 1. 透明转发（ReverseProxy）   │
                    │ 2. 旁路捕获 → 播放会话        │
                    │ 3. scrobble → Last.fm / LB    │
                    │ 4. 歌单同步 → 飞牛音乐写入    │
                    │ 5. /_ext/healthz 自查         │
                    └───┬───────────────────┬──────┘
                        │                   │
      unix socket       │                   │ HTTP over unix socket
                        ▼                   ▼
 /var/run/trim_music_upstream.socket   /var/apps/.../app.sock（仅 fpk）
                        │                   │
                        ▼                   ▼
                官方 trim-music        fnOS 统一网关 → Svelte Web UI
```

命令行入口与上面两条链路并列：`fnmusic-sync` 不带子命令即进入代理模式，`--check` / `sockets` / `version` / `service` / `self-upgrade` 是旁路命令，见 [cli.md](cli.md)。

## 进程与启动序列

主入口 `cmd/fnmusic-sync/main.go`，根命令在 `cmd/fnmusic-sync/root_cmd.go`（cobra，`SilenceErrors`/`SilenceUsage` 打开，错误由 `main` 统一打印）。

| 文件                                              | 职责                                                                                                     |
|---------------------------------------------------|----------------------------------------------------------------------------------------------------------|
| `cmd/fnmusic-sync/main.go`                        | 代理模式全程：配置加载、DB 打开、manager/recorder 装配、热加载、单实例锁、接管、启动 HTTP 服务、信号处理 |
| `cmd/fnmusic-sync/runtime.go`                     | 运行模式探测（传统 / fpk）与四类固定路径：config、state、logDir、runLock                                 |
| `cmd/fnmusic-sync/setup.go`                       | 由配置构造每用户 provider 列表、provider 状态查询函数、`server.socket_mode` 解析                         |
| `cmd/fnmusic-sync/root_cmd.go`                    | 根命令与三个 flag：`--check`、`--debug`、`--upstream-wait`                                               |
| `cmd/fnmusic-sync/cmd_sockets.go`                 | `sockets` 子命令，打印两个 socket 的归属/权限/属主/PID 与结论                                            |
| `cmd/fnmusic-sync/cmd_service.go`                 | `service install/uninstall/start/stop/restart/status/debug/nodebug`（`!fpk` 构建标签）                   |
| `cmd/fnmusic-sync/auth.go`                        | Last.fm 授权轮询（180s）与 ListenBrainz 用户名自动补全                                                   |
| `cmd/fnmusic-sync/check.go`                       | `--check` 体检：不接管、不建锁，只探测归属/权限/连通性                                                   |
| `cmd/fnmusic-sync/logging.go`                     | slog logger：stderr + lumberjack 双写，`multiHandler` 扇出                                               |
| `cmd/fnmusic-sync/goroutine_diag.go`              | debug 下的 goroutine 监视器与 SIGUSR1 全量快照                                                           |
| `cmd/fnmusic-sync/webui_fpk.go` / `webui_stub.go` | fpk 启动 Web UI（`!fpk` 为空实现）                                                                       |

运行模式由环境变量决定，程序启动时一次性探测（`detectRuntime`，`var rt = detectRuntime()`）：

| 模式 | 判定              | 配置                            | 状态                             | 日志目录                | 单实例锁                     |
|------|-------------------|---------------------------------|----------------------------------|-------------------------|------------------------------|
| 传统 | 无 `TRIM_APPDEST` | `/etc/fnmusic-sync/config.yaml` | `/var/lib/fnmusic-sync/state.db` | `/var/log/fnmusic-sync` | `/run/fnmusic-sync/run.lock` |
| fpk  | 有 `TRIM_APPDEST` | `$TRIM_PKGETC/config.yaml`      | `$TRIM_PKGVAR/state.db`          | `$TRIM_PKGVAR/logs`     | `$TRIM_PKGVAR/run.lock`      |

`runtime.go` 里没有 `--config` / `--state` 之类的覆盖参数：路径只由模式决定。socket 路径是代码常量（`internal/buildinfo` 的 `DefaultListenSocket` / `DefaultUpstreamSocket`），配置文件也不提供。

启动顺序（`run`，`cmd/fnmusic-sync/main.go`）：

```text
1  打印版本信息（buildinfo 注入的版本/Git/构建时间 + Go 版本 + 平台 + 运行模式）
2  配置文件不存在 → config.SaveDefault 生成默认配置（失败仅 WARN，继续用内置默认值）
3  config.Load(CtrlConfigPath)
4  取固定 socket 路径；server.upstream_wait 覆盖 --upstream-wait 的默认 30s
5  计算日志目录；抓包开关 captureEnabled = --debug || wizard_debug=1/true/on/yes
6  --check → 只做体检（probe socket + 连通性），直接返回
7  打开 state.db（StatePath 换 .db 后缀）与同目录 data.db
8  装配：UserStore、Manager（scrobbled hook → 计数落库；playStart hook → Recorder）、Recorder
9  proxy.New（含 UserCache、Detector、CaptureLogger、ReverseProxy）
10 playlist.NewSyncService（向 UserCache 注册"用户首次识别"回调）
11 applyConfig：重建各用户 provider、设置阈值、设置日志级别、刷新抓包标志、更新歌单服务配置
12 config.Watch：文件变化 → applyConfig
13 对"已启用但缺凭证"的用户起后台补全（Last.fm 授权轮询 / LB 用户名查询）
14 signal.NotifyContext(SIGINT, SIGTERM)
15 acquireRunLock：flock 独占；已被持有 → 直接报错退出（提示 service stop）
16 NewTakeover；defer takeover.Restore()；defer store.Save()
17 waitForTakeover：每 3s 重试 Prepare，直到成功或收到信号
18 takeover.Listen(socket_mode)：在原路径 bind 并修正权限/属主
19 p.WaitUpstream(ctx, upstream_wait)：等官方后端可用，超时则回滚（返回错误 → 走 defer Restore）
20 playlistSync.Start()；defer Stop
21 startWebUI（仅 fpk，监听 $TRIM_APPDEST/app.sock；失败只 WARN）
22 defer p.Close()（抓包日志 + Detector 退出）
23 debug 模式下启动 goroutine 监视器
24 p.Start(ctx, listener)：阻塞服务；ctx 取消 → 10s 内 Shutdown
```

第 17 步的循环是刻意设计：飞牛音乐可能晚于本服务启动，立即失败会让 systemd `Restart=always` 反复重启并撞上 start-limit。

## Socket 接管机制

实现集中在 `internal/proxy/socket.go`，探测在 `ProbeSocket`，接管在 `Takeover.Prepare` / `Listen` / `Restore`，状态展示在 `internal/proxy/socket_status.go`。

### inode 语义：为什么用 os.Rename

unix socket 的监听绑定在 **inode** 上，目录项只是名字。`os.Rename("/var/run/trim_music.socket", "/var/run/trim_music_upstream.socket")` 只改目录项，trim-music 手里的监听句柄不受影响，仍在新名字上 accept 新连接。代理随后才能在原路径 bind 自己的监听。

```text
接管前：nginx → /var/run/trim_music.socket（inode A，trim-music accept）
接管后：nginx → /var/run/trim_music.socket（inode B，代理 accept）
               代理 → /var/run/trim_music_upstream.socket（inode A，trim-music accept）
```

### 归属探测：ProbeSocket

探测结果决定接管动作，四种取值（`SocketKind`）：

| 取值    | 含义                                          | 判定方式                                                                                                 |
|---------|-----------------------------------------------|----------------------------------------------------------------------------------------------------------|
| `none`  | 文件不存在，或文件还在但无人 listen（死文件） | `stat` 非 socket，或 `dial unix`（2s）失败                                                               |
| `proxy` | 本代理持有                                    | GET `/_ext/healthz` 返回 <500 且 body 含 `upstream`                                                      |
| `trim`  | 官方 trim-music 持有                          | GET `/music/api/v1/search/track?keyword=fnmusic_probe`，body 含 `INVALID TOKEN` 或 `99999`（未登录判定） |
| `stale` | 有人 listen 但身份不明                        | 以上都不匹配                                                                                             |

探测先做传输层 `dial`（2s），再发 HTTP（3s）。"文件存在但没人监听"必须是 `none`，否则会把死文件当成官方后端。

### Prepare：幂等的接管状态机

```text
Prepare()
 ├─ 探测 listen、upstream 两个路径
 ├─ upstream == trim（官方已在 upstream，重复启动 / 重启场景）
 │   ├─ listen == none   → 已是接管态（上次是死文件），直接沿用
 │   ├─ listen == trim   → 官方重启后抢回了原路径：删 listen，保留 upstream
 │   └─ 其它             → 删除 listen 上的残留监听者
 ├─ listen == trim     → 正常接管：先记录权限/属主，清掉 upstream 残留，os.Rename(listen → upstream)
 └─ 其它               → 报错"未找到可接管的 trim-music socket"，由 waitForTakeover 重试
 → changed = true
```

只有 `changed = true` 时 `Restore` 才会动手，所以"本来就没接管"的进程退出时不会误改官方 socket。

### 权限与属主

```text
effectiveMode(explicit):
  mode = explicit（server.socket_mode）；为 0 则用接管时记录到的官方权限
  mode == 0 → 0666（DefaultSocketMode）
  mode &= ModePerm
  if mode&0007 == 0 { mode |= 0006 }   // other 完全没权限时补 rw
```

- 必须是 0666 级别：fnOS 的 nginx worker 以 `www-data` 运行，`0660` 会让 `connect()` 拿到 `EACCES`，表现为音乐服务 502。
- 注意补权限的判据是 `mode&0007 == 0`：若官方 socket 是 `0644` 这类"other 可读不可写"，不会被修正。这类非标准环境需要显式配置 `server.socket_mode: 438`（0666）。
- 属主：接管时 `captureMode` 记录官方 socket 的 uid/gid，`Listen` 与原路恢复后都 `applyOwner` 复刻；`chown` 失败只 WARN（"不影响 nginx 连接"）。
- `Listen` 在 bind 前还有一道保险：若 `listen` 上仍是官方 trim-music，直接拒绝绑定并报错。

### 退出还原

`Restore` 覆盖所有退出路径（正常退出、启动失败、SIGTERM），在 `run` 里以 `defer takeover.Restore()` 注册：

```text
Restore()
 ├─ !changed → 直接返回
 ├─ listen 已是官方 trim → 官方自己抢回了路径，天然直连，什么都不做
 ├─ 删除代理 socket
 ├─ upstream 不可连接 → ERROR + 打印手工命令 mv <upstream> <listen>（恢复失败 = 飞牛音乐长期不可用）
 └─ os.Rename(upstream → listen)，再 chmod/chown 复刻权限与属主
```

`SIGKILL` 绕不过：它不执行 defer。

### 崩溃与残留的四种情形

| 情形                            | listen 路径            | upstream 路径        | 现象                                  | 恢复方式                                                                      |
|---------------------------------|------------------------|----------------------|---------------------------------------|-------------------------------------------------------------------------------|
| 正常接管中                      | 代理（活）             | trim（活）           | 一切正常                              | —                                                                             |
| 代理被 `kill -9`                | 死文件（代理的 inode） | trim（活）           | nginx 连 listen → `ECONNREFUSED`，502 | 重启代理：Prepare 命中"upstream 已是官方后端 + listen 是死文件"，直接进接管态 |
| 代理从未启动                    | trim（活）             | 不存在               | 官方直连，正常                        | —                                                                             |
| **代理运行中，trim-music 重启** | trim（活，新 inode）   | 死文件（旧 inode A） | 见下节                                | 重启代理：Prepare 清 upstream 残留并 rename，重新 bind 原路径                 |

### trim-music 重启会旁路代理

这是本项目最容易被踩到的坑，README 里也有一条警告。机制如下：

1. 代理接管时把官方 socket 改名到 `trim_music_upstream.socket`，官方进程仍在 inode A 上 accept。
2. trim-music 重启：新进程按自己的配置 `unlink("/var/run/trim_music.socket")` 再 `bind` 同路径。此时路径上的 socket 文件属于**代理**（inode B），被删掉；官方在 inode C 上重新监听原路径。
3. 结果：
   - nginx 连 `/var/run/trim_music.socket` → 直接到**新 trim-music**（inode C），飞牛音乐完全正常；
   - 代理仍在 inode B 上 accept，但 inode B 已被 unlink，没有任何新连接会进来 → **完全收不到请求，scrobble 静默停止**；
   - `trim_music_upstream.socket` 上留着 inode A 的**死文件**（旧进程已退出，无人 listen）。
4. 恢复：`systemctl restart fnmusic-sync`（或 `fnmusic-sync service restart`）。重启后 `Prepare` 看到 `listen == trim`、`upstream == none`，于是 `captureMode` 记录新 socket 的权限、清掉 upstream 死文件、`os.Rename(listen → upstream)`，再由 `Listen` 重新 bind 原路径。

诊断特征：飞牛音乐**能正常播放**但日志里没有任何请求、`fnmusic-sync sockets` 显示监听路径归属 `trim` 而不是 `proxy`。Web UI 的 socket 面板读的就是同一份 `ProbeSockets` 结果。

## 转发链路

反向代理在 `internal/proxy/proxy.go`，用 `net/http/httputil.ReverseProxy`，只挂四个钩子：`Rewrite`、`Transport`、`ModifyResponse`、`ErrorHandler`。

| 环节          | 行为                                                                                                                                                                | 源码             |
|---------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------|------------------|
| 目标改写      | 只设 `Out.URL.Scheme = "http"`、`Out.URL.Host = "trim-music"`                                                                                                       | `Rewrite`        |
| Host 头       | 刻意不动 `Out.Host`：nginx 用 `proxy_pass` 到 unix socket 时下发 `Host: unix`，trim-music 可能依赖它，必须原样透传                                                  | 同上（注释明确） |
| hop-by-hop 头 | 交给 `httputil.ReverseProxy` 处理（`Connection`/`TE`/`Upgrade` 等剥离、`X-Forwarded-For` 追加），不自行复制全部 header                                              | 标准库行为       |
| 请求体        | `bufferBody` 只读前 1MiB（`maxInspectBody`）用于分析，剩余部分用 `io.MultiReader` 续上，保证 upstream 拿到完整 body                                                 | `bufferBody`     |
| 响应流式      | `FlushInterval: -1`，写入即 flush，音频流 / SSE 不被缓冲                                                                                                            | `New`            |
| 连接管理      | transport 关闭 keep-alive（`DisableKeepAlives`）：上游是本地 unix socket，dial 极廉价，而连接池的空闲关闭路径会在持有 `idleMu` 时阻塞，一旦上游假死会把整个代理拖死 | `New`            |
| 超时          | dial 5s；`ResponseHeaderTimeout` 30s（只限响应头，不影响长 body）；server 端 `ReadHeaderTimeout` 30s、`IdleTimeout` 120s，不设 Read/WriteTimeout                    | `New` / `Start`  |
| 优雅退出      | ctx 取消 → 10s 内 `Shutdown`                                                                                                                                        | `Start`          |
| 响应解码      | 需要解析的 JSON 响应走 `drainJSONBody`：按 `Content-Encoding` 解 gzip/deflate，回填明文 body 并删除 `Content-Encoding`/`Content-Length`，否则客户端会二次解压失败   | `drainJSONBody`  |

错误如何回给客户端（`errorHandler`）：

- 客户端主动断开（`context.Canceled` / `net.ErrClosed`，切歌、拖进度条会触发）→ 只打 DEBUG，不写响应；
- 其它错误 → ERROR 日志 + `http.Error(w, "upstream unavailable", 502)`。

也就是说：**代理不会因为自身逻辑回 502**；只有上游 unix socket 真的不可达时，那一个请求才 502。"整个音乐服务 502"只可能来自 socket 接管态异常（权限不足或没接管），而不是转发逻辑。

自带端点：`/_ext/healthz` 在 `ServeHTTP` 开头短路，不转发上游（`internal/proxy/health.go`）。返回 `{"ok":true,"upstream":"ok","socket":"/var/run/trim_music_upstream.socket"}`，上游探测失败时 503 且 `error` 带原因。探测判据是"能 dial 且能拿到 HTTP 响应"，未登录返回 `INVALID TOKEN` 也算健康。

## 旁路捕获与播放会话

`ServeHTTP` 在转发前后做了三件事，全部对客户端无感：

```text
请求进入
 ├─ 提取 userKey（Cookie music-token → Authorization: Bearer → X-Auth-Token → Cookie fnos-token）
 ├─ UserCache.Resolve：命中缓存直接返回；未命中且来源是 music-token 则异步探测 /user/me（不阻塞）
 ├─ bufferBody：留一份前 1MiB body 供解析
 ├─ isEventReportPath → Detector.HandleEventReport（登记 track_play 候选）
 ├─ 转发
 └─ 响应返回后（ModifyResponse 内）
     ├─ /user/me                → 解析用户名，写 UserCache + users 表
     ├─ 登录/换 token            → 解析新 userToken，登记并剔除旧 token
     ├─ /track/metadata         → 缓存曲目元数据
     ├─ /track/stream|play-url  → 用 Content-Range 推算进度
     └─ /track/hls/*.m3u8|*.m4s → 分片时长 / 分片序号推算进度
```

播放会话由 `internal/playback`（`detector.go` 识别、`manager.go` 会话与阈值、`recorder.go` 落库）与 `internal/model/track.go`（统一曲目模型）实现。判定细节、字段、边界情形见 [scrobble-pipeline.md](scrobble-pipeline.md)。

用户归属的多用户隔离靠 `internal/playback/user.go` 的 `UserCache`：

- `music-token` 是主身份；`fnos-token` 只做兜底标识，且**不用它主动探测**（音乐服务 `/user/me` 不认系统 token，必 401）；
- 探测失败进 `failed` 表，`failedTTL = 5min` 内不重复探测；探测中标记 `probing` 防并发重复；
- token 轮换（客户端重新登录）时用 `/user/auth-login`、`/user/password-login` 的响应体主动 `Register`，再用 `InvalidateToken` 剔除旧 token；
- 活跃时间 `users.last_seen_at` 由 `seenToucher` 按 token 前缀 2 分钟节流、异步刷新，不阻塞请求。

## Scrobble 分发

`internal/scrobbler/scrobbler.go` 只定义接口与兼容别名：

```text
Scrobbler
 ├─ Name() string
 ├─ UpdateNowPlaying(ctx, track) error
 └─ Scrobble(ctx, track, startedAt int64) error
```

实现是 `internal/lastfm`（`track.updateNowPlaying` / `track.scrobble`，POST 表单 + MD5 签名）与 `internal/listenbrainz`（`/1/submit-listens`，`playing_now` / `single`）。`cmd/fnmusic-sync/setup.go` 的 `buildUserProviders` 按 `users.<name>` 构造**每用户独立的 provider 列表**，`Manager` 里以 `map[string][]Scrobbler` 保存，`providersFor(username)` 取用 —— 这就是 A 的记录不会推到 B 账号的原因。

两类推送的差别：

| 维度     | UpdateNowPlaying                                 | Scrobble                                     |
|----------|--------------------------------------------------|----------------------------------------------|
| 触发时机 | 会话开始（`Manager.Play` 内）                    | 达到阈值（`CheckScrobble` 内）               |
| 载荷     | 曲目元数据                                       | 曲目元数据 + `startedAt`（会话开始 Unix 秒） |
| 去重     | 同一 session 只发一次（重复 `Play` 提前 return） | `Session.Scrobbled` 标志，true 后不再发      |
| 并发     | 每 provider 一个 goroutine                       | 每 provider 一个 goroutine                   |
| 失败     | ERROR "正在播放推送失败"，不重试                 | ERROR "scrobble推送失败"，不重试             |

成功推送会触发 `onScrobbled` hook → `UserStore.RecordScrobble` → `run_status` 计数 + `last_scrobbled_at`。所有推送都在 `safego.Go` 起的后台 goroutine 里，HTTP 转发路径从不同步等待外网。

## 歌单同步子系统

一句话：`internal/playlist` 定时或按用户识别事件，把 Last.fm / ListenBrainz 的歌单经 `internal/mbid` 的 MBID 映射与 `internal/feiniu` 的曲库索引匹配成飞牛 GUID，再用飞牛的 playlist API 增量写入。机制、匹配优先级、写入策略见 [playlist-sync.md](playlist-sync.md)。

## Web UI 服务层

`internal/webui` 只在 fpk 构建标签下编译，监听 `$TRIM_APPDEST/app.sock`（`chmod 0666`，网关以 `www-data` 连接），所有路由前缀 `/app/fnmusic-sync`，同时注册带/不带尾斜杠的根路径以避免 301（网关 iframe 里 3xx 会导致重定向循环）。职责边界：

- 静态前端来自 `cmd/frontend`（Svelte + Vite），构建产物 `embed` 进二进制；
- REST API 读写配置、状态、播放记录、同步记录、日志尾部、MBID 扫描、升级；写操作统一走 `internal/config` 的 `Save*`/`Set*`，因此与热加载是同一条路径；
- 鉴权来自网关注入的 Header（`X-Trim-Userid` / `X-Trim-Username` / `X-Trim-Isadmin`），管理接口走 `requireAdmin`；
- 不直接接触 socket 接管逻辑，只读 `proxy.ProbeSockets` 的结果。

接口清单与请求/响应结构见 [webui-api.md](webui-api.md)。

## 持久化与分离

两个 sqlite 库（`modernc.org/sqlite`，纯 Go；单连接 `MaxOpenConns(1)` 规避 `database is locked`）：

| 库                     | 打开位置                  | 表               | 内容                                                |
|------------------------|---------------------------|------------------|-----------------------------------------------------|
| `state.db`（运营状态） | `StatePath` 换 `.db` 后缀 | `run_status`     | 每用户 scrobble 计数、各平台启用标志、最近推送时间  |
|                        |                           | `users`          | 一行一个客户端 token（`token_prefix` 唯一）         |
|                        |                           | `playback_log`   | 播放记录（`started_at` 必填，`ended_at` 常为 NULL） |
|                        |                           | `sync_log`       | 每次歌单同步的结果与触发来源                        |
| `data.db`（数据）      | 与 `state.db` 同目录      | `track_mbid_map` | 飞牛 GUID ↔ 五类 MBID，来源为本地标签解析           |

分离理由：运营状态频繁写、体量小、与用户强绑定；MBID 映射可整表重建、体量大、与用户无关。分开后可以单独备份/清空/迁移 `data.db` 而不动运营数据。字段与索引见 [database.md](database.md)。

`store.Save()` 是历史兼容空实现（数据库实时写入），保留只是为了调用点不用改。

## 并发模型与生命周期

所有后台 goroutine 都经 `internal/safego.Go` 启动：入口即带 `recover`，panic 时打 ERROR + 全进程栈快照但进程存活。未捕获的 panic 会让 goroutine 静默消失，反而骗过基于数量的监视器。

| goroutine                                             | 归属                           | 生命周期                        |
|-------------------------------------------------------|--------------------------------|---------------------------------|
| `proxy.shutdownWatcher`                               | `proxy.Start`                  | ctx 取消后 10s Shutdown 结束    |
| HTTP 连接处理（每连接/每请求）                        | `http.Server`                  | 请求结束                        |
| `playback.expireLoop`（2s tick）                      | `Detector`                     | `Proxy.Close` → `Detector.Stop` |
| `proxy.CaptureLogger.worker`                          | `CaptureLogger`                | `Close` 时先写完队列再退出      |
| `proxy.touchUserSeen`                                 | `seenToucher`（2min 节流）     | 单次触发，写完即退              |
| `playback.UserCache.probe`                            | `UserCache`                    | 单次 5s 探测                    |
| `playback.onIdentified`                               | `UserCache.Register`           | 单次回调                        |
| `playback.UpdateNowPlaying` / `Scrobble`              | `Manager`                      | 单次外网请求（15s 超时）        |
| `playback.scheduleScrobble`                           | `Manager.Play`                 | 阈值定时器到点或 session 关闭   |
| `playlist.SyncService.run`                            | `SyncService.Start`            | `Stop` / 配置热更新             |
| `playlist.mbidScan`                                   | `maybeScanMBID`                | 自带 30min 上下文               |
| `config.Watch`                                        | `config.Watch`（文件未存在时） | 切到 viper 文件 watch 后退出    |
| `webui.Serve`                                         | `webui.Server.Start`           | `Stop`                          |
| `startLastFMAuth` / `startListenBrainzUsernameLookup` | `main`                         | 180s / 30s 超时                 |
| `startGoroutineDumper`                                | `main`（仅 debug）             | 进程存活期                      |

配置热加载（`config.Watch` → `applyConfig`，两者共用同一个函数，保证启动与热更新行为一致）：

| 变更项                        | 生效方式                                                                       |
|-------------------------------|--------------------------------------------------------------------------------|
| `users.*` 凭证                | `Manager.UpdateProviders` 重建 provider 列表（正在进行的会话用新列表推下一次） |
| `playback.scrobble_threshold` | `Manager.SetScrobbleThreshold`（解析失败保留旧值并 WARN）                      |
| `logging.level`               | `slog.LevelVar.Set`                                                            |
| `playlist.*`                  | `SyncService.UpdateConfig`：运行中先停再按新配置起                             |
| `library.*`                   | 下一轮 MBID 扫描时读取（`SyncService.cfg` 指针已换）                           |
| 抓包开关                      | 每次重算 `debug` 与 `wizard_debug` 的或值 —— **不受配置文件控制**              |

日志三处出口：

- 主日志：`log/slog` text，同时写 stderr 与 `<日志目录>/fnmusic-sync.log`（lumberjack 切割/gzip/过期）；`logging.enabled: false` 或目录被设为 `off`/`none`/`-` 时仅控制台；目录不可写时降级为仅控制台并 WARN，不阻断启动。
- 抓包日志：`capture.log`（代理流量，`internal/proxy/capture.go`），`feiniu.log` / `lastfm.log` / `listenbrainz.log`（`internal/reqlog`）。四者共用同一套 `Begin`/`Finish` 异步排队写入、敏感头打码（`Authorization`/`Cookie`/`Set-Cookie`/`X-Auth-Token`/`X-Session`）、body 截断。
- goroutine 快照：`goroutine-dump.txt`（仅 debug，SIGUSR1 手动触发全量）。

## 关键不变量

改动代码时需要守住这些，破坏任何一条都会以"飞牛音乐 502"或"scrobble 静默丢失"的形式暴露：

1. **socket 必须可还原**：任何接管后的退出路径都要 `Restore`；新增退出分支时，`defer` 必须已经注册。
2. **接管必须幂等**：`Prepare` 要在任意中间态（两个路径各是 trim / 死文件 / 代理）都能收敛，因为进程可能被 `kill -9`。
3. **代理 socket 权限至少让 other 可读写**：否则 nginx（`www-data`）连不上。
4. **转发不得改写业务语义**：只改目标 scheme/host，不改 `Host`、不改 body、不额外解压响应（除需要解析的那几个 JSON 接口，且解析后必须回填明文）。
5. **旁路逻辑不得影响转发**：所有识别、推送、落库都在转发路径之外；外网失败只写日志。
6. **播放事件只是候选**：`track_play` 不能被直接当成"开始播放"，必须由真实取流（`end > 0`、HLS 分片）佐证或超时回退，否则预加载会造出幽灵 scrobble。
7. **每用户凭证隔离**：provider 实例按 `username` 查表，任何"全局单例 provider"的改动都会跨账号串数据。
8. **阈值判定以真实播放时长为下界**：取流推算的 position 必须被 `elapsed` 钳制。
9. **单个 session 只 scrobble 一次**：靠 `Session.Scrobbled`，重复的 `Play`/进度事件不能重复推送。
10. **热加载不能丢用户会话**：`UserCache` 与 `Manager.current` 都不随配置重建；`UpdateProviders` 只换 provider 列表。
11. **日志不得泄漏凭证**：敏感头打码、token 只打印前 8 位（`strutil.FirstN8`）。
12. **单实例**：`flock` 占用即拒绝启动，避免两个代理互相 rename socket。

## 模块依赖图

```text
cmd/fnmusic-sync
 ├─ internal/config ──────────────┐
 ├─ internal/proxy ─┬─ internal/playback ─┬─ internal/scrobbler ─┬─ internal/lastfm
 │                  │                    │                      └─ internal/listenbrainz
 │                  │                    └─ internal/model
 │                  ├─ internal/db
 │                  ├─ internal/reqlog
 │                  └─ internal/strutil
 ├─ internal/playlist ─┬─ internal/feiniu
 │                     ├─ internal/mbid ── internal/datadb
 │                     ├─ internal/lastfm
 │                     ├─ internal/listenbrainz
 │                     └─ internal/playback（只取 UserCache）
 ├─ internal/webui（仅 fpk）─┬─ internal/updater
 │                            └─ internal/proxy（只取 ProbeSockets）
 ├─ internal/safego（被各处引用）
 └─ internal/buildinfo（版本 + 两个 socket 常量）
```

依赖方向单向：`playback` 不认识 `proxy`，`webui` 只通过接口（`ActiveUserProvider`、`MBIDScanner`）拿数据，`playlist` 不反向依赖 `proxy` 的类型。
