# Web UI HTTP API

fpk 安装下，进程在 `$TRIM_APPDEST/app.sock` 上提供一套 HTTP API 与内嵌前端；飞牛统一网关把 `/app/fnmusic-sync` 前缀的请求原样转过来。

> 相关：[README](../README.md) · [配置参考](configuration.md) · [数据库](database.md) · [部署](deployment.md) · [排障](troubleshooting.md)

## 1. 服务与路由

| 维度        | 事实                                                                                                |
|-------------|-----------------------------------------------------------------------------------------------------|
| 可用构建    | 仅 `-tags fpk`。`internal/webui/handler.go` 首行是 `//go:build fpk`，传统安装的二进制里没有这套代码 |
| 监听        | unix socket `$TRIM_APPDEST/app.sock`（`TRIM_APPDEST` 缺失时回退 `/var/apps/fnmusic-sync/target`）   |
| socket 权限 | 启动时 `chmod 0666` —— 网关以 `www-data` 连接（`internal/webui/handler.go:116-119`）                |
| HTTP 参数   | `ReadHeaderTimeout: 30s`、`IdleTimeout: 120s`，无写超时                                             |
| 启动失败    | 只打 WARN `Web UI 启动失败（不影响代理功能）`，代理照常跑                                           |
| 静态资源    | `//go:embed www`，编译进二进制，无需额外文件                                                        |
| 路由前缀    | 常量 `gatewayPrefix = "/app/fnmusic-sync"`（`internal/webui/handler.go:162`），所有路由都带这个前缀 |

### 1.1 `gatewayPrefix` 的匹配规则

网关把完整原始路径（`/app/fnmusic-sync/api/config`）转发到 `app.sock`，服务端不剥离前缀，所以每条路由都按全路径注册。带斜杠与不带斜杠的根路径各注册一次：

```text
/app/fnmusic-sync/  → 静态处理器
/app/fnmusic-sync   → 静态处理器
```

这不是冗余：Go 的 `ServeMux` 对「已注册 `/prefix/`、请求 `/prefix`」会回 301，而在网关 iframe 里任何 3xx 都可能变成重定向循环。静态处理器同样不使用 `http.FileServer`（它会做目录补斜杠与 `/index.html → /` 的 301），而是自己读文件写响应，**任何路径都不发重定向**。

### 1.2 SPA 回退与 `BaseURL` 注入

- 请求 `/app/fnmusic-sync/` → 渲染 `index.html`；
- 请求的路径在嵌入文件里不存在 → 同样返回 `index.html`，状态码 200；
- `index.html` 用 `html/template` 渲染，注入 `BaseURL=/app/fnmusic-sync`，模板里以 `{{.BaseURL}}/app.js` 之类的方式引用资源，同时写成 `window.BaseURL` 供前端拼接 API 前缀；
- 其余静态文件走 `http.ServeContent`，带正确的 `Content-Type` 与 `Range` 支持。

前端 `cmd/frontend/src/lib/api.ts` 的第一行逻辑就是 `const BASE_URL = window.BaseURL || '/app/fnmusic-sync'`，然后所有请求发到 `${BASE_URL}/api/...`。

## 2. 鉴权模型

### 2.1 身份来源：网关 Header

服务端**不校验会话、不发 token、没有 401**。身份全部来自飞牛统一网关在校验用户会话后注入的请求头（`readGatewayUser`，`internal/webui/handler.go:310-336`）：

| Header            | 含义                         | 例值    |
|-------------------|------------------------------|---------|
| `X-Trim-Userid`   | 用户 UID                     | `1000`  |
| `X-Trim-Username` | 用户名                       | `admin` |
| `X-Trim-Isadmin`  | 是否管理员，仅 `"true"` 算真 | `true`  |

> **没有这些 Header 时，一律按管理员处理**（UID 记为 `"0"`，用户名记为 `"local"`）。这是为本地调试留的口子（`cmd/devserver` 正是靠它跑通全部接口），也意味着**能给 `app.sock` 发请求的本地进程拥有完整管理权限**。不要把这个 socket 暴露到网关之外。

### 2.2 `requireAdmin`

`requireAdmin` 是普通中间件，判定来源就是 `X-Trim-Isadmin == "true"`（`internal/webui/handler.go:339-349`），非管理员返回：

```json
{ "error": "仅管理员可执行此操作" }
```

状态码 `403`。

数据库里 `users.is_admin` **不是**鉴权来源：它是 `/api/me` 顺手写入的记录（`BindUserPlatform`），用于「活跃用户列表」展示与歌单同步挑选管理员 token。两者数据同源（都来自网关 Header），但判定只读 Header。

### 2.3 三层权限

| 层级     | 端点                                                                                              | 规则                                                             |
|----------|---------------------------------------------------------------------------------------------------|------------------------------------------------------------------|
| 公开     | `/api/version`、`/api/health`、`/api/sockets`、`/api/me`                                          | 无校验                                                           |
| 用户级   | `/api/config`、`/api/state`、`/api/playlist`、`/api/synclog`、`/api/user/{name}`、`/api/lastfm/*` | 管理员看全部；普通用户只能看/改自己（按 `X-Trim-Username` 匹配） |
| 管理员级 | `/api/logs`、`/api/settings`、`/api/library*`、`/api/users`、`/api/upgrade*`                      | `requireAdmin`；超出范围时 403                                   |

鉴权失败只有两种码：`403`（权限不够）与 `405`（方法不对）。**没有 401。**

### 2.4 统一响应形态

所有响应都是 JSON，`Content-Type: application/json; charset=utf-8`：

```json
{ "error": "错误描述" }
```

错误体固定是这一个键（`writeJSONError`）。方法不允许时额外带 `Allow` 响应头，例如 `Allow: GET`。

## 3. 端点参考

| 方法        | 路径                 | 鉴权   | handler              |
|-------------|----------------------|--------|----------------------|
| GET         | `/api/version`       | 公开   | `handleVersion`      |
| GET         | `/api/health`        | 公开   | `handleHealth`       |
| GET         | `/api/sockets`       | 公开   | `handleSockets`      |
| GET         | `/api/me`            | 公开   | `handleMe`           |
| GET         | `/api/config`        | 用户级 | `handleConfig`       |
| POST        | `/api/user/{name}`   | 用户级 | `handleUser`         |
| DELETE      | `/api/user/{name}`   | 管理员 | `handleUser`         |
| GET         | `/api/state`         | 用户级 | `handleState`        |
| GET         | `/api/playlist`      | 用户级 | `handlePlaylist`     |
| GET         | `/api/synclog`       | 用户级 | `handleSyncLog`      |
| POST        | `/api/lastfm/auth`   | 用户级 | `handleLastFMAuth`   |
| POST        | `/api/lastfm/poll`   | 用户级 | `handleLastFMPoll`   |
| GET         | `/api/logs`          | 管理员 | `handleLogs`         |
| PUT \| POST | `/api/settings`      | 管理员 | `handleSettings`     |
| GET         | `/api/library`       | 管理员 | `handleLibrary`      |
| PUT \| POST | `/api/library`       | 管理员 | `handleLibrary`      |
| POST        | `/api/library/scan`  | 管理员 | `handleLibraryScan`  |
| GET         | `/api/users`         | 管理员 | `handleActiveUsers`  |
| GET         | `/api/upgrade/check` | 管理员 | `handleUpgradeCheck` |
| POST        | `/api/upgrade`       | 管理员 | `handleUpgrade`      |

handler 名都指 `internal/webui/handler.go` 里的方法。

### 3.1 `GET /api/version`

公开 · `handleVersion`。无参数。

```json
{ "version": "v1.2.3", "gitCommit": "a1b2c3d", "buildTime": "2026-01-02 03:04:05 UTC", "binaryName": "fnmusic-sync" }
```

全部 string。非 GET → 405，`Allow: GET`。

### 3.2 `GET /api/health`

公开 · `handleHealth`。

```json
{ "ok": true, "config": "/vol1/@appetc/fnmusic-sync/config.yaml", "logDir": "/vol1/@appdata/fnmusic-sync/logs" }
```

`ok` 恒为 `true`（进程活着才答得出来），另外两个是运行期路径，用来对照配置是否写对了文件。

### 3.3 `GET /api/sockets`

公开 · `handleSockets`。直接返回 `proxy.ProbeSockets()` 的结构：

```json
{
  "listen": {
    "path": "/var/run/trim_music.socket",
    "exists": true,
    "is_socket": true,
    "perm": "0666",
    "uid": 0,
    "gid": 0,
    "kind": "proxy",
    "connectable": true,
    "healthy": true,
    "peer_pid": 1234,
    "detail": ""
  },
  "upstream": {
    "path": "/var/run/trim_music_upstream.socket",
    "exists": true,
    "is_socket": true,
    "perm": "0666",
    "uid": 0,
    "gid": 0,
    "kind": "trim",
    "connectable": true,
    "healthy": true,
    "peer_pid": 987,
    "detail": ""
  },
  "all_healthy": true,
  "message": "正常"
}
```

| 字段       | 类型   | 说明                                                                        |
|------------|--------|-----------------------------------------------------------------------------|
| `kind`     | string | `none` / `trim`（飞牛官方）/ `proxy`（本代理）/ `stale`（有人在听但认不出） |
| `perm`     | string | 八进制字符串，如 `"0666"`                                                   |
| `healthy`  | bool   | 监听侧要求「存在 + 是 socket + 可连接」；上游侧额外要求 `kind=trim`         |
| `peer_pid` | int    | 监听侧是本进程 PID；上游侧是持有者 PID，取不到为 0                          |
| `message`  | string | 首个不满足项的自然语言解释，全好时为 `正常`                                 |

### 3.4 `GET /api/me`

公开 · `handleMe`。

```json
{ "uid": "1000", "username": "admin", "isAdmin": true }
```

副作用：如果请求同时带 `music-token` Cookie，且该 token 在活跃用户缓存里能解析出音乐用户名，则把本次的平台身份写进 `users` 表（`BindUserPlatform`），供「活跃用户列表」展示管理员标记。写失败被忽略。

### 3.5 `GET /api/config`

用户级 · `handleConfig`。管理员拿到完整 `Config`，键与配置文件一致（`internal/config/config.go` 的 json tag）：

```json
{
  "server":   { "socket_mode": 0, "upstream_wait": "30s" },
  "users":    { "admin": { "lastfm": { "...": "..." }, "listenbrainz": { "...": "..." } } },
  "playback": { "scrobble_threshold": "auto" },
  "playlist": { "enabled": true, "sync_interval": "30m" },
  "logging":  { "enabled": true, "level": "info", "max_size": 3, "max_backups": 5, "max_age": 7, "compress": true },
  "library":  { "directories": ["/vol1/1000/音乐"], "scan_interval": "6h", "mbid_online_lookup": false }
}
```

普通用户只拿到自己的 `users.<自己>` 加 `playback`/`playlist`/`logging` 三段，**不返回 `server` 与 `library`**；自己不在配置里时 `users` 为空对象。

读取失败 → 500 `读取配置失败: <err>`。

### 3.6 `POST|DELETE /api/user/{name}`

用户级 · `handleUser`。**没有 GET**（读配置走 `/api/config`）；`PUT` 也不支持，统一用 POST。

POST 请求体是完整的 `config.UserAccount`，字段名与 `config.yaml` 一致：

```json
{
  "lastfm": {
    "enabled": true,
    "api_key": "…",
    "api_secret": "…",
    "session_key": "…",
    "username": "…",
    "playlist": {
      "top_tracks":    { "enabled": false, "name": "LF {period} 最常听", "period": "overall", "limit": 50 },
      "loved_tracks":  { "enabled": false, "name": "LF 红心收藏", "limit": 50 },
      "recent_tracks": { "enabled": false, "name": "LF 最近播放", "limit": 50 },
      "weekly_charts": { "enabled": false, "name": "LF 本周榜单", "limit": 50 }
    }
  },
  "listenbrainz": {
    "enabled": true,
    "token": "…",
    "username": "…",
    "playlist": {
      "daily_jams":         { "enabled": false, "name": "LB 每日推荐", "limit": 50, "range": "" },
      "weekly_jams":        { "enabled": false, "name": "LB 每周推荐", "limit": 50, "range": "" },
      "weekly_exploration": { "enabled": false, "name": "LB 每周探索", "limit": 50, "range": "" },
      "year_discoveries":   { "enabled": false, "name": "LB {year} 年度发现", "limit": 50, "range": "" },
      "year_missed":        { "enabled": false, "name": "LB {year} 年度遗珠", "limit": 50, "range": "" },
      "top_recordings":     { "enabled": false, "name": "LB 最常听", "limit": 50, "range": "all_time" },
      "recently_played":    { "enabled": false, "name": "LB 最近在听", "limit": 50, "range": "" }
    }
  }
}
```

回写只用 `userAccountToMap` 里列出的字段：LB 侧只有 `top_recordings` 带 `range`，其余六个歌单源即使传了 `range` 也不会写进配置文件（`internal/config/config.go:396-478`）。

**整段替换**：`SaveUser` 用 `v.Set("users.<name>", 整体map)`，请求体里没带的字段会被写成零值。想保留旧值就必须先 GET `/api/config`，改完整体回传。

成功返回：

```json
{ "ok": true }
```

DELETE 删除整个 `users.<name>` 段，同样返回 `{ "ok": true }`。

| 情形                | 状态码 | body                                                                |
|---------------------|--------|---------------------------------------------------------------------|
| 路径 `name` 为空    | 400    | `{ "error": "用户名不能为空" }`                                     |
| 请求体不是合法 JSON | 400    | `{ "error": "解析请求体失败: …" }`                                  |
| 普通用户改别人      | 403    | `{ "error": "只能编辑自己的用户配置" }`                             |
| 普通用户 DELETE     | 403    | `{ "error": "仅管理员可删除用户" }`                                 |
| 写配置失败          | 500    | `{ "error": "保存用户失败: …" }` / `{ "error": "删除用户失败: …" }` |
| 其他方法            | 405    | `{ "error": "不支持的请求方法" }`，`Allow: POST, DELETE`            |

### 3.7 `GET /api/state`

用户级 · `handleState`。数据来自 `run_status` 表，按 `total_scrobbles` 降序。

```json
{
  "users": {
    "admin": {
      "scrobbles": 1234,
      "last_scrobbled_at": "2026-01-02T03:04:05Z",
      "lastfm_enabled": true,
      "listenbrainz_enabled": false
    }
  }
}
```

| 字段                   | 类型   | 说明                                         |
|------------------------|--------|----------------------------------------------|
| `scrobbles`            | int    | 累计推送次数（`run_status.total_scrobbles`） |
| `last_scrobbled_at`    | string | RFC3339；从未推送过为 `""`                   |
| `lastfm_enabled`       | bool   | 识别该用户时的平台启用快照                   |
| `listenbrainz_enabled` | bool   | 同上                                         |

普通用户只看到自己那一行。数据库不可用时返回 `{"users":{}}`。

### 3.8 `GET /api/playlist`

用户级 · `handlePlaylist`。查询参数：`user`（仅管理员可指定）、`limit`。

播放记录来自 `playback_log` 表：每首歌开始播放时插一行；飞牛只上报 `track_play`，没有 stop/end 事件，所以 `ended_at` 通常是空串，只有「下一首开始」或「进程退出」时才回填。

```json
{
  "entries": [
    {
      "id": 42,
      "username": "admin",
      "token_prefix": "a1b2c3d4",
      "guid": "…",
      "title": "曲名",
      "artist": "艺人",
      "album": "专辑",
      "duration_ms": 245000,
      "started_at": "2026-01-02T03:04:05Z",
      "ended_at": "2026-01-02T03:08:10Z"
    }
  ]
}
```

| 参数    | 默认 | 上限 | 说明                                                      |
|---------|------|------|-----------------------------------------------------------|
| `limit` | 100  | 1000 | 非法或 `<=0` 取默认；超过 1000 钳到 1000                  |
| `user`  | 空   | —    | 空 = 全部用户（管理员）；普通用户无论传什么都被强制成自己 |

行字段的可空列在 Go 侧是 `sql.NullString`/`sql.NullInt64`，序列化时取 `.String`/`.Int64`，未设置就是零值（`""` / `0`）。查询失败 → 500 `读取播放记录失败: …`。

### 3.9 `GET /api/synclog`

用户级 · `handleSyncLog`。查询参数同上，默认 `limit=200`，上限 1000。

```json
{
  "entries": [
    {
      "id": 7,
      "username": "admin",
      "provider": "listenbrainz",
      "status": "success",
      "trigger": "interval",
      "playlists": 3,
      "tracks": 87,
      "message": "",
      "started_at": "2026-01-02T03:04:05Z",
      "finished_at": "2026-01-02T03:07:41Z"
    }
  ]
}
```

| 字段        | 取值                                                            |
|-------------|-----------------------------------------------------------------|
| `provider`  | `lastfm` / `listenbrainz`                                       |
| `status`    | `success` / `failed`                                            |
| `trigger`   | `interval`（定时）/ `user_identified`（用户识别回调）/ `manual` |
| `playlists` | 本次实际同步的歌单数                                            |
| `tracks`    | 本次实际新增的曲目数                                            |
| `message`   | 失败原因；成功为空串                                            |

失败的记录同样落库，`message` 就是错误原文。

### 3.10 `POST /api/lastfm/auth`

用户级 · `handleLastFMAuth`。发起一次授权，返回浏览器要打开的链接。

```json
{ "api_key": "…", "api_secret": "…", "username": "admin" }
```

`username` 是**飞牛用户名**（配置文件里的 key），不是 Last.fm 用户名。前端拿到 `auth_url` 后 `window.open` 新标签页，然后开始轮询。

```json
{ "auth_url": "https://www.last.fm/api/auth/?api_key=…&token=…", "token": "…" }
```

服务端把 `{token, username, startedAt}` 存进进程内 `sync.Map`（按用户名一条，同用户重复发起会覆盖上一条）。

| 情形                       | 状态码 | body                                             |
|----------------------------|--------|--------------------------------------------------|
| 非 POST                    | 405    | `{ "error": "不支持的请求方法" }`，`Allow: POST` |
| body 解析失败              | 400    | `{ "error": "解析请求体失败: …" }`               |
| `api_key`/`api_secret` 空  | 400    | `{ "error": "api_key 和 api_secret 不能为空" }`  |
| 普通用户为别人授权         | 403    | `{ "error": "只能为自己的账号授权" }`            |
| 向 Last.fm 申请 token 失败 | 500    | `{ "error": "申请授权 token 失败: …" }`          |

### 3.11 `POST /api/lastfm/poll`

用户级 · `handleLastFMPoll`。请求体与 auth 相同（`api_key` / `api_secret` / `username`），前端每 3 秒轮询一次、最多 60 次（约 3 分钟，与服务端会话过期时间对齐；`cmd/frontend/src/lib/components/UserEditCard.svelte`）。

三种响应：

```json
{ "authorized": false }
```

用户还没在浏览器点「同意」（Last.fm 返回错误码 14）。

```json
{ "authorized": false, "expired": true }
```

没有进行中的会话，或会话超过 3 分钟被清理。前端应停止轮询。

```json
{ "authorized": true, "session_key": "…", "lastfm_username": "…" }
```

授权成功，`session_key` 与 `username` 已写回配置文件，写回触发的热加载会立刻让新凭证生效。

还有一种中间态：Last.fm 返回了其它错误时是 `{ "authorized": false, "error": "<错误原文>" }`，状态码仍是 200 —— 前端按 `authorized=false` 继续轮询即可。

| 情形           | 状态码 | body                                    |
|----------------|--------|-----------------------------------------|
| 普通用户查别人 | 403    | `{ "error": "只能查询自己的授权状态" }` |
| 写回配置失败   | 500    | `{ "error": "写入配置失败: …" }`        |

### 3.12 `GET /api/logs`

管理员 · `handleLogs`。读日志文件尾部 200 行。

```json
{ "lines": ["time=… level=INFO msg=…"], "file_enabled": true }
```

| 情形                           | 响应                                     |
|--------------------------------|------------------------------------------|
| 配置里 `logging.enabled=false` | `{"lines":[],"file_enabled":false}`，200 |
| 日志文件不存在                 | 同上                                     |
| 文件存在                       | 最后 200 行；不足 200 行则全给           |
| 读失败（权限等）               | 500 `{ "error": "读取日志失败: …" }`     |

只读 `fnmusic-sync.log`，不读 `capture.log` 等从属文件。

### 3.13 `PUT|POST /api/settings`

管理员 · `handleSettings`。三段的**整体覆盖**。

```json
{
  "playback": { "scrobble_threshold": "auto" },
  "playlist": { "enabled": true, "sync_interval": "30m" },
  "logging":  { "enabled": true, "level": "info", "max_size": 3, "max_backups": 5, "max_age": 7, "compress": true }
}
```

```json
{ "ok": true }
```

结构体是 `config.PlaybackConfig` / `PlaylistConfig` / `LoggingConfig`，缺字段即零值 —— `logging` 少传 `enabled` 会把文件日志关掉。没有 GET（读配置走 `/api/config`）。

| 情形          | 状态码 | body                                                  |
|---------------|--------|-------------------------------------------------------|
| 非 PUT/POST   | 405    | `{ "error": "不支持的请求方法" }`，`Allow: PUT, POST` |
| body 解析失败 | 400    | `{ "error": "解析请求体失败: …" }`                    |
| 写配置失败    | 500    | `{ "error": "保存设置失败: …" }`                      |

### 3.14 `GET|PUT /api/library`

管理员 · `handleLibrary`。

GET：

```json
{
  "directories": ["/vol1/1000/音乐"],
  "scan_interval": "6h",
  "mbid_online_lookup": false
}
```

PUT（也接受 POST）：

```json
{ "directories": ["/vol1/1000/音乐"], "scan_interval": "6h", "mbid_online_lookup": true }
```

```json
{ "ok": true, "directories": ["/vol1/1000/音乐"], "scan_interval": "6h", "mbid_online_lookup": true }
```

目录会经过 `cleanDirectories` 清洗：去空白、丢弃空串、**丢弃相对路径**、`filepath.Clean` 规范化、去重（`internal/webui/handler.go:860-880`）。返回的是清洗后的结果，不是你传进去的原文。

`scan_interval` 传空串表示「不改该字段」（`SaveLibrary` 里判断为空则跳过），`mbid_online_lookup` 始终写入。

| 情形          | 状态码 | body                                                 |
|---------------|--------|------------------------------------------------------|
| 其他方法      | 405    | `{ "error": "不支持的请求方法" }`，`Allow: GET, PUT` |
| body 解析失败 | 400    | `{ "error": "解析请求体失败: …" }`                   |
| 写配置失败    | 500    | `{ "error": "保存音乐目录失败: …" }`                 |

保存后需要重启进程才真正影响下次扫描（`mbid.Resolver` 持有启动时的配置指针），见 [configuration.md](configuration.md) 第 3 节。

### 3.15 `POST /api/library/scan`

管理员 · `handleLibraryScan`。同步触发一次本地音乐 MBID 扫描，扫描完才返回，服务端超时 30 分钟。

```json
{
  "scanned": 1893,
  "matched": 1750,
  "updated": 1702,
  "skipped": 143,
  "has_mbid": 1600,
  "online": 102,
  "errors": 3,
  "elapsed_ms": 41523,
  "message": "扫描完成"
}
```

| 字段         | 含义                                                                        |
|--------------|-----------------------------------------------------------------------------|
| `scanned`    | 解析过的音频文件数                                                          |
| `matched`    | 在飞牛曲库里匹配到曲目的文件数                                              |
| `updated`    | 写入/更新 `track_mbid_map` 的行数                                           |
| `skipped`    | 无 MBID（且未开在线补全）或匹配失败                                         |
| `has_mbid`   | 标签里实际解析到 MBID 的文件数                                              |
| `online`     | 经 MusicBrainz 在线补全成功的数目（`mbid_online_lookup=true` 时才有非零值） |
| `errors`     | 读文件/解析出错数                                                           |
| `elapsed_ms` | 耗时                                                                        |

| 情形                         | 状态码 | body                                                   |
|------------------------------|--------|--------------------------------------------------------|
| 非 POST                      | 405    | `{ "error": "仅支持 POST" }`，`Allow: POST`            |
| 未启用歌单同步（无 scanner） | 503    | `{ "error": "MBID 扫描服务不可用（未启用歌单同步）" }` |
| 扫描报错                     | 500    | `{ "error": "MBID 扫描失败: …" }`                      |

未配 `library.directories` 时扫描会立刻返回 `message: "未配置 library.directories，跳过 MBID 扫描"`，状态码 200。

### 3.16 `GET /api/users`

管理员 · `handleActiveUsers`。已识别到的客户端会话列表，来自 `users` 表（`token_prefix` 反查）。

```json
{
  "users": [
    {
      "token": "a1b2c3d4",
      "username": "admin",
      "platform_username": "admin",
      "is_admin": true,
      "ua_system": "iOS",
      "ua_client": "Flutter 3.10.9",
      "ua_raw": "1.0.1 (com.trim.music.ios; build:101163; iOS 17.7.11) Flutter/3.10.9",
      "first_seen_at": "2026-01-01T10:00:00Z",
      "last_seen_at": "2026-01-02T03:04:05Z"
    }
  ]
}
```

一行 = 一个 `music-token`（同一账号多端登录会各占一行）。`token` 是脱敏前缀（前 8 位），不是完整 token。`platform_*` 与 `is_admin` 只有在用户打开过 Web UI、被 `BindUserPlatform` 绑定后才有值。

数据库不可用时退回内存活跃用户列表，此时只有 `token` 与 `username` 两个字段。

### 3.17 `GET /api/upgrade/check`

管理员 · `handleUpgradeCheck`。

```json
{
  "current_version": "v1.2.3",
  "latest_version": "v1.3.0",
  "has_update": true,
  "repo_source": "cnb"
}
```

`repo_source` 是构建来源（`cnb` / `github` / 空）。取版本失败（网络、Token 缺失）→ 500，body 是错误原文。

### 3.18 `POST /api/upgrade`

管理员 · `handleUpgrade`。fpk 专用：下载 `.fpk` 后交给飞牛官方的 `appcenter-cli install-local` 做「停止 → 卸载 → 重装 → 启动」，因此会刷新应用中心里的版本号。

```json
{ "ok": true, "latest_version": "v1.3.0", "message": "已通过飞牛应用中心执行升级，应用正在重启以完成升级" }
```

响应发出后升级命令才在后台（`setsid` 脱离会话）执行，所以这个 200 只代表「已触发」，不代表装完。

| 情形                     | 状态码 | body                                                                     |
|--------------------------|--------|--------------------------------------------------------------------------|
| 非 POST                  | 405    | `{ "error": "不支持的请求方法" }`，`Allow: POST`                         |
| 找不到 `appcenter-cli`   | 500    | `{ "error": "未检测到 appcenter-cli 工具，请通过飞牛应用中心手动升级" }` |
| 取版本 / 下载 / 解包失败 | 500    | 对应错误原文（`解包 fpk 失败: …` 等）                                    |
| 启动升级命令失败         | 500    | `{ "error": "启动升级失败: …" }`                                         |

升级时会透传向导配置：优先用 `$TRIM_PKGETC/config.env` 作为 `--env`，避免重装把用户开着的 `wizard_debug` 冲回默认值。

## 4. Last.fm 授权时序

```text
浏览器(Web UI)                 fnmusic-sync                      Last.fm
     │                              │                              │
     │ POST /api/lastfm/auth        │                              │
     │ {api_key, api_secret, user}  │                              │
     │─────────────────────────────>│  auth.getToken               │
     │                              │─────────────────────────────>│
     │                              │  {token}                     │
     │  {auth_url, token}           │<─────────────────────────────│
     │<─────────────────────────────│  存内存会话(username→token)   │
     │                              │                              │
     │ 新标签页打开 auth_url ────────┼─────────────────────────────>│  用户点「同意」
     │                              │                              │
     │ POST /api/lastfm/poll (循环)  │                              │
     │─────────────────────────────>│  auth.getSession(token)      │
     │                              │─────────────────────────────>│
     │  {authorized:false}          │  error 14 = 还没同意          │
     │<─────────────────────────────│<─────────────────────────────│
     │  ... 每 3 秒重复 ...          │                              │
     │                              │  {session:{name,key}}        │
     │                              │<─────────────────────────────│
     │                              │  写回 config.yaml            │
     │                              │  session_key = key           │
     │                              │  users.<x>.lastfm.username   │
     │                              │  → fsnotify → 热加载          │
     │ {authorized:true,            │                              │
     │  session_key, lastfm_username}│                             │
     │<─────────────────────────────│                              │
```

关键点：

- 授权会话存在进程内存，**重启即丢**；同一用户重复 `auth` 会覆盖上一条会话。
- 会话 3 分钟过期（`time.Since(startedAt) > 3*time.Minute`），过期后 poll 返回 `expired:true`。
- 写回的是**飞牛用户名**对应的段（`users.<飞牛用户名>.lastfm.*`），不是 Last.fm 用户名。
- 写回触发 `config.Watch` → 新 `session_key` 立即用于推送，不需要重启。
- 同一套流程还有一条无人值守版本：启动时对「`enabled=true` 且填了 `api_key`/`api_secret` 但 `session_key` 为空」的用户，打授权链接到**日志**并每 5 秒轮询，总时长 180 秒（`cmd/fnmusic-sync/auth.go`）。超时后需重启进程重试。该路径的日志文案写的是「60秒」，实际超时是 180 秒。

## 5. 前端如何消费

`cmd/frontend/src/lib/api.ts` 把上面所有端点包成 `getVersion()` / `getCurrentUser()` / `getConfig()` / `saveUser()` / `saveSettings()` / `saveLibrary()` / `startLastFmAuth()` / `pollLastFmAuth()` / `checkUpgrade()` / `doUpgrade()` 等函数，统一走 `apiCall()`：拼 `${window.BaseURL || '/app/fnmusic-sync'}/api` 前缀、`Content-Type: application/json`、非 2xx 时抛 `Error(data.error || 'HTTP <status>')`。联调时对齐这三点即可：前缀、JSON 体、错误键名 `error`。

## 6. curl 示例集

### 6.1 直连 `app.sock`（fpk 机器本地）

不走网关，没有 `X-Trim-*` Header → 自动获得管理员权限，适合排障：

```bash
SOCK="${TRIM_APPDEST:-/var/apps/fnmusic-sync/target}/app.sock"
BASE="http://localhost/app/fnmusic-sync"

curl -s --unix-socket "$SOCK" "$BASE/api/version"
curl -s --unix-socket "$SOCK" "$BASE/api/health"
curl -s --unix-socket "$SOCK" "$BASE/api/sockets"
curl -s --unix-socket "$SOCK" "$BASE/api/me"
curl -s --unix-socket "$SOCK" "$BASE/api/state"
curl -s --unix-socket "$SOCK" "$BASE/api/playlist?limit=20"
curl -s --unix-socket "$SOCK" "$BASE/api/synclog?limit=20"
curl -s --unix-socket "$SOCK" "$BASE/api/users"
curl -s --unix-socket "$SOCK" "$BASE/api/library"
```

写操作：

```bash
# 保存用户（整段替换）
curl -s --unix-socket "$SOCK" -X POST "$BASE/api/user/admin" \
  -H 'Content-Type: application/json' \
  -d '{
        "lastfm": {"enabled": true, "api_key": "k", "api_secret": "s", "session_key": "", "username": ""},
        "listenbrainz": {"enabled": false, "token": "", "username": ""}
      }'

# 删除用户
curl -s --unix-socket "$SOCK" -X DELETE "$BASE/api/user/admin"

# 保存全局设置
curl -s --unix-socket "$SOCK" -X PUT "$BASE/api/settings" \
  -H 'Content-Type: application/json' \
  -d '{
        "playback": {"scrobble_threshold": "30s"},
        "playlist": {"enabled": true, "sync_interval": "30m"},
        "logging":  {"enabled": true, "level": "debug", "max_size": 3, "max_backups": 5, "max_age": 7, "compress": true}
      }'

# 保存音乐目录
curl -s --unix-socket "$SOCK" -X PUT "$BASE/api/library" \
  -H 'Content-Type: application/json' \
  -d '{"directories": ["/vol1/1000/音乐"], "scan_interval": "6h", "mbid_online_lookup": false}'

# 触发 MBID 扫描（可能跑几十秒到几十分钟）
curl -s --unix-socket "$SOCK" -X POST "$BASE/api/library/scan"

# Last.fm 授权两步
curl -s --unix-socket "$SOCK" -X POST "$BASE/api/lastfm/auth" \
  -H 'Content-Type: application/json' \
  -d '{"api_key": "k", "api_secret": "s", "username": "admin"}'

curl -s --unix-socket "$SOCK" -X POST "$BASE/api/lastfm/poll" \
  -H 'Content-Type: application/json' \
  -d '{"api_key": "k", "api_secret": "s", "username": "admin"}'

# 升级
curl -s --unix-socket "$SOCK" "$BASE/api/upgrade/check"
curl -s --unix-socket "$SOCK" -X POST "$BASE/api/upgrade"
```

### 6.2 走飞牛统一网关

网关在 `http://<fnOS-IP>:<端口>/app/fnmusic-sync`，需要浏览器里已登录的会话 Cookie（`fnos-token` / `music-token`），因此通常只用浏览器 devtools 抓，或带 Cookie 手动构造：

```bash
GW="http://192.168.1.10:5666/app/fnmusic-sync"

curl -s "$GW/api/version" \
  -H 'Cookie: fnos-token=<你的会话cookie>'
```

网关会自行注入 `X-Trim-Userid` / `X-Trim-Username` / `X-Trim-Isadmin`，普通用户身份下再访问 `/api/settings` 会得到：

```json
{ "error": "仅管理员可执行此操作" }
```

### 6.3 本地开发服务器

没有飞牛环境时用仓库自带的 dev server（TCP `:8080`，同样带 `/app/fnmusic-sync` 前缀，无网关 Header 即管理员）：

```bash
make dev-server        # 等价 go run ./cmd/devserver

curl -s http://127.0.0.1:8080/app/fnmusic-sync/api/version
curl -s http://127.0.0.1:8080/app/fnmusic-sync/api/settings -X PUT \
  -H 'Content-Type: application/json' \
  -d '{"playback":{"scrobble_threshold":"auto"},"playlist":{"enabled":true,"sync_interval":"30m"},"logging":{"enabled":true,"level":"info","max_size":3,"max_backups":5,"max_age":7,"compress":true}}'
```

注意 `cmd/devserver` 只镜像了部分路由（version / health / me / config / state / user / lastfm / logs / settings / users），**没有** `sockets`、`playlist`、`synclog`、`library*`、`upgrade*`；这几条要验就得用真实的 fpk 环境或直连 `app.sock`。
