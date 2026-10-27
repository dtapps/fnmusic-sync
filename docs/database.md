# 数据模型与数据库

fnmusic-sync 用两个独立的 sqlite 文件分开「运营状态」与「曲目数据」：`state.db` 记的是不可再生的历史与统计，`data.db` 记的是可重建的 MBID 映射。

> 相关：[配置参考](configuration.md) · [Web UI API](webui-api.md) · [歌单同步](playlist-sync.md) · [排障](troubleshooting.md)

## 1. 两个库的分工

| 维度     | `state.db`（`internal/db`）                              | `data.db`（`internal/datadb`）                                    |
|----------|----------------------------------------------------------|-------------------------------------------------------------------|
| 职责     | 运营状态：用户、推送统计、播放记录、同步记录、运行状态   | 曲目 MBID 映射：本地标签 / MusicBrainz 解析结果                   |
| 表       | `run_status`、`users`、`playback_log`、`sync_log`        | `track_mbid_map`                                                  |
| 数据来源 | 代理运行时产生（识别用户、推送成功、播放事件、同步结果） | 本地音乐目录扫描 + 可选 MusicBrainz 在线补全                      |
| 可重建性 | **不可重建**（历史统计与播放流水丢了就是丢了）           | 可重建（重扫音乐目录即可，前提是 `library.directories` 仍配置着） |
| 写入频率 | 每次识别/推送/播放/同步                                  | 每次扫描（默认 6h 去抖或手动触发），逐文件 upsert                 |
| 删除影响 | 丢失累计推送数、播放历史、同步历史、已识别用户与平台绑定 | 歌单匹配退化为「艺人名+曲名 → 仅曲名」，scrobble 不受影响         |

分开的理由写在两边 `schema.sql` 的注释里：`state.db` 的注释指出 `track_mbid_map`「已拆分到独立的 data.db / internal/datadb，与『运营状态』表分离」；`datadb/schema.sql` 的注释是「本文件只放『数据』类表……与 state.db（运营状态）分离，便于独立维护」。落到运维上，就是**备份策略可以不一样**：`state.db` 要留，`data.db` 可丢。

`track_mbid_map` 的唯一读写方是 `internal/mbid` 与 `internal/playlist`；`internal/db` 里没有任何代码引用它。两个包各自 `//go:embed schema.sql`，互不依赖。

## 2. 驱动与连接参数

| 项       | 值                                                                                                              |
|----------|-----------------------------------------------------------------------------------------------------------------|
| 驱动     | `modernc.org/sqlite`（纯 Go，无 CGO）—— fpk/fnOS 部署不能依赖 CGO 工具链                                        |
| 注册名   | `sql.DB` 的 `driverName` 是 `"sqlite"`（`sql.Open("sqlite", path)`，`internal/db/store.go:31`）                 |
| 访问方式 | `database/sql` + sqlc 生成的类型安全查询                                                                        |
| 连接池   | `SetMaxOpenConns(1)` + `SetMaxIdleConns(1)`，注释写明「sqlite 单写者模型，限制单连接可避免 database is locked」 |
| PRAGMA   | **没有任何自定义 PRAGMA**。`journal_mode`、`busy_timeout`、`synchronous`、`foreign_keys` 全走驱动默认值         |
| 建表     | 每次 `Open` 都把嵌入的 `schema.sql` 整段 `ExecContext` 一遍，全部语句带 `IF NOT EXISTS`，幂等                   |
| 迁移     | 没有版本号、没有迁移表；只有两处启动期特判（见 2.1）                                                            |

并发写入策略就是「串行化」：单连接池 + 应用侧节流（播放记录写入有锁，`users.last_seen_at` 每 token 2 分钟最多一次，MBID 扫描整轮在独立 goroutine）。Web UI 的读请求同样排在同一条连接上，所以高峰期接口可能等待，但不会出现锁冲突。

### 2.1 启动期的两处特判

`db.Open`（`internal/db/store.go:30-65`）在建表之后依次做两件事：

1. **`migrateUsersTokenKey`**：早期 `users` 表以 `username` 为唯一键，同账号多端登录会把多条 token 挤成一行。现在要求 `token_prefix` 唯一。函数先查 `pragma_index_list` 找有没有「含 `token_prefix` 的唯一索引」，没有就重建表（新建 `users_new` → 回填带 token 的行 → 删旧表 → 改名 → 重建三个索引），全程在一个事务里。sqlite 删不掉列上的 `UNIQUE` 约束，所以只能重建。
2. **`ReparseUserAgents`**：用最新的 `ParseUserAgent` 规则重算所有行的 `ua_system` / `ua_client`（`ua_raw` 保留原值），用于解析规则升级后回填历史数据。

`data.db` 没有迁移逻辑，`datadb.Open` 只有建表。

## 3. 表结构

类型即 sqlite 的声明类型；时间是 `TEXT`，格式统一为 RFC3339（`time.Now().Format(time.RFC3339)`）。

### 3.1 `run_status` —— 每用户一行推送统计

| 字段                     | 类型    | 约束 / 默认值      | 含义                                 |
|--------------------------|---------|--------------------|--------------------------------------|
| `username`               | TEXT    | PRIMARY KEY        | 音乐用户名（飞牛 `/user/me` 的名字） |
| `lastfm_scrobbles`       | INTEGER | NOT NULL DEFAULT 0 | 累计推送到 Last.fm 的次数            |
| `listenbrainz_scrobbles` | INTEGER | NOT NULL DEFAULT 0 | 累计推送到 ListenBrainz 的次数       |
| `total_scrobbles`        | INTEGER | NOT NULL DEFAULT 0 | 两者之和（任何一次推送都 +1）        |
| `lastfm_enabled`         | INTEGER | NOT NULL DEFAULT 0 | 0/1，识别该用户时的 Last.fm 启用快照 |
| `listenbrainz_enabled`   | INTEGER | NOT NULL DEFAULT 0 | 0/1，同上                            |
| `last_scrobbled_at`      | TEXT    | 可空               | 最近一次成功推送时刻                 |
| `updated_at`             | TEXT    | NOT NULL           | 本行最后更新时间                     |

索引：`idx_run_status_total (total_scrobbles DESC)`，用于「按推送量排名」的列表。

### 3.2 `users` —— 一行一个客户端 token

同一音乐账号可在多个客户端登录，每个客户端有自己的 `music-token`，因此按 token 分行，列表里一个客户端一行。

| 字段                | 类型    | 约束 / 默认值             | 含义                                                                                |
|---------------------|---------|---------------------------|-------------------------------------------------------------------------------------|
| `id`                | INTEGER | PRIMARY KEY AUTOINCREMENT | 自增主键                                                                            |
| `username`          | TEXT    | NOT NULL                  | 音乐用户名（scrobble 归属主体）                                                     |
| `platform_uid`      | TEXT    | 可空                      | 飞牛平台 UID（来自 `X-Trim-Userid`）                                                |
| `platform_username` | TEXT    | 可空                      | 飞牛平台用户名（来自 `X-Trim-Username`）                                            |
| `is_admin`          | INTEGER | NOT NULL DEFAULT 0        | 0/1，平台管理员标记（来自 `X-Trim-Isadmin`），仅记录用                              |
| `token_prefix`      | TEXT    | NOT NULL **UNIQUE**       | `music-token` 前 8 位，脱敏标识，也是去重键                                         |
| `ua_raw`            | TEXT    | 可空                      | 原始 User-Agent                                                                     |
| `ua_system`         | TEXT    | 可空                      | 解析出的系统：iOS / Android / Windows / macOS / Linux / HarmonyOS / ChromeOS / 其他 |
| `ua_client`         | TEXT    | 可空                      | 解析出的客户端：Flutter x.y / okhttp / libmpv / curl / 浏览器 …                     |
| `first_seen_at`     | TEXT    | NOT NULL                  | 该 token 首次出现时间                                                               |
| `last_seen_at`      | TEXT    | NOT NULL                  | 最近活跃时间（心跳刷新）                                                            |
| `created_at`        | TEXT    | NOT NULL                  | 入库时间                                                                            |
| `updated_at`        | TEXT    | NOT NULL                  | 更新时间                                                                            |

索引：`idx_users_last_seen (last_seen_at DESC)` 供列表排序、`idx_users_admin (is_admin)`、`idx_users_username (username)` —— 后者是 `BindUserPlatform` 按用户名 UPDATE 用的。

### 3.3 `playback_log` —— 播放流水

飞牛只上报 `track_play`（开始播放），没有 pause/stop/end 事件，所以一首歌的结束时间靠「下一首开始」或「进程退出」回填。

| 字段           | 类型    | 约束 / 默认值             | 含义                                        |
|----------------|---------|---------------------------|---------------------------------------------|
| `id`           | INTEGER | PRIMARY KEY AUTOINCREMENT | 自增主键，用于回填 `ended_at`               |
| `username`     | TEXT    | NOT NULL                  | 音乐用户名                                  |
| `token_prefix` | TEXT    | 可空                      | 触发该次播放的客户端 token 前缀（多端区分） |
| `guid`         | TEXT    | 可空                      | 飞牛曲目 GUID                               |
| `title`        | TEXT    | NOT NULL                  | 曲名                                        |
| `artist`       | TEXT    | 可空                      | 艺人                                        |
| `album`        | TEXT    | 可空                      | 专辑                                        |
| `duration_ms`  | INTEGER | 可空                      | 时长（毫秒）                                |
| `started_at`   | TEXT    | NOT NULL                  | 开始播放时刻（RFC3339）                     |
| `ended_at`     | TEXT    | 可空                      | 结束时刻；`NULL` = 进行中或结束未明         |
| `created_at`   | TEXT    | NOT NULL                  | 入库时刻                                    |

索引：`idx_playback_user_started (username, started_at DESC)` 服务「某用户最近播放」，`idx_playback_started (started_at DESC)` 服务「全站最近播放」。

### 3.4 `sync_log` —— 歌单同步记录

| 字段          | 类型    | 约束 / 默认值                 | 含义                                      |
|---------------|---------|-------------------------------|-------------------------------------------|
| `id`          | INTEGER | PRIMARY KEY AUTOINCREMENT     | 自增主键                                  |
| `username`    | TEXT    | NOT NULL                      | 音乐用户名                                |
| `provider`    | TEXT    | NOT NULL                      | `lastfm` / `listenbrainz`                 |
| `status`      | TEXT    | NOT NULL                      | `success` / `failed`                      |
| `trigger`     | TEXT    | NOT NULL DEFAULT `'interval'` | `interval` / `user_identified` / `manual` |
| `playlists`   | INTEGER | NOT NULL DEFAULT 0            | 本次同步的歌单数                          |
| `tracks`      | INTEGER | NOT NULL DEFAULT 0            | 本次新增曲目数                            |
| `message`     | TEXT    | 可空                          | 失败原因（`status=failed` 时是错误原文）  |
| `started_at`  | TEXT    | NOT NULL                      | 同步开始时刻                              |
| `finished_at` | TEXT    | NOT NULL                      | 同步结束（= 落库）时刻                    |

索引：`idx_sync_log_user_provider (username, provider, finished_at DESC)` —— 这条索引同时服务两个高频查询：取 `status='success'` 的最近一条（同步间隔判定）与「某用户同步历史」；`idx_sync_log_finished (finished_at DESC)` 服务全站最近记录。

`sync_log` 不只是日志，还是**持久化的同步间隔**：每次同步前查该用户 + provider 的上一次成功时间，未到 `playlist.sync_interval` 就跳过。这意味着删掉 `sync_log` 会让下一轮定时同步立刻重跑一遍。

### 3.5 `track_mbid_map` —— 曲目 MBID 映射（`data.db`）

| 字段                 | 类型 | 约束 / 默认值            | 含义                                                   |
|----------------------|------|--------------------------|--------------------------------------------------------|
| `feiniu_guid`        | TEXT | PRIMARY KEY              | 飞牛曲目 GUID（关联主体）                              |
| `file_path`          | TEXT | 可空                     | 来源音频文件的绝对路径                                 |
| `title`              | TEXT | 可空                     | 曲名（元数据匹配备用）                                 |
| `artist`             | TEXT | 可空                     | 艺人                                                   |
| `album`              | TEXT | 可空                     | 专辑                                                   |
| `recording_mbid`     | TEXT | 可空                     | 录音 MBID（匹配优先级最高）                            |
| `release_mbid`       | TEXT | 可空                     | 专辑发行 MBID                                          |
| `release_group_mbid` | TEXT | 可空                     | 专辑组 MBID                                            |
| `artist_mbid`        | TEXT | 可空                     | 艺人 MBID                                              |
| `work_mbid`          | TEXT | 可空                     | 作品 MBID                                              |
| `matched_by`         | TEXT | NOT NULL DEFAULT `'tag'` | `tag`（本地标签）/ `musicbrainz`（在线补全）/ `manual` |
| `updated_at`         | TEXT | NOT NULL                 | 本行最后更新时间                                       |

索引：`idx_mbid_map_path (file_path)` 供按路径反查，`idx_mbid_map_meta (title, artist)` 供人工核对。

注意 `feiniu_guid` 是主键 —— 一首曲目只能有一条映射，重扫会覆盖上一轮结果（包括 `matched_by`）。

## 4. 写入时机

| 表               | 写入路径                                                                                                                                                                               | 频率                                                                |
|------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|---------------------------------------------------------------------|
| `run_status`     | `UserStore.Ensure` → `UpsertRunStatus`（`internal/playback/userstore.go:50-56`），由 `UserCache.Register` 在**首次识别用户 / 用户名变化**时调用（`internal/playback/user.go:206-214`） | 每个用户每次识别，通常一次                                          |
| `run_status`     | `UserStore.RecordScrobble` → `IncrementScrobble`（`userstore.go:59-66`），挂在 `manager.SetScrobbledHook`（`cmd/fnmusic-sync/main.go:164-170`）                                        | 每成功推送一首到任一平台一次                                        |
| `users`          | `Proxy.recordUserIdentity` → `UpsertUser`（`internal/proxy/proxy.go:598-614`）                                                                                                         | 每次识别到 token（`ON CONFLICT(token_prefix)` 合并，UA 非空才覆盖） |
| `users`          | `seenToucher.touch` → `TouchUserSeenByToken`（`proxy.go:660-700`）                                                                                                                     | 每 token 最多 2 分钟一次，异步写                                    |
| `users`          | Web UI `GET /api/me` → `BindUserPlatform`（`internal/webui/handler.go:365-371`）                                                                                                       | 每次打开 Web UI 且带 `music-token` Cookie 时                        |
| `users`          | `ReparseUserAgents` → `SetUserAgent`                                                                                                                                                   | 每次进程启动（全表扫描，只更新 UA 两个字段）                        |
| `playback_log`   | `Recorder.OnPlay` → `RecordPlayback`（`internal/playback/recorder.go:38-66`），挂在 `manager.SetPlaybackStartHook`                                                                     | 每次开始播放一首歌一行                                              |
| `playback_log`   | `Recorder.OnPlay` 前一步 → `ClosePlayback`（回填上一首 `ended_at`）                                                                                                                    | 同一用户换歌时                                                      |
| `playback_log`   | `Recorder.CloseOpen` → `CloseOpenPlaybacks`（`main.go:176` 的 defer）                                                                                                                  | 进程退出一次，把 `ended_at IS NULL` 的行统一收尾                    |
| `sync_log`       | `SyncService.syncProvider` → `RecordSyncLog`（`internal/playlist/sync.go:249-278`）                                                                                                    | 每次非 Noop 的同步尝试（成功/失败都记）                             |
| `track_mbid_map` | `Resolver.Scan` → `saveMBID` → `SaveTrackMBID`（`internal/mbid/resolver.go:150-186`、`internal/datadb/store.go:74-104`）                                                               | 每轮扫描，逐文件 upsert                                             |

MBID 扫描的触发点有两个：定时同步与用户识别回调都会调 `maybeScanMBID`（去抖，间隔 `library.scan_interval`，默认 6h，限制在 `[1m, 24h]`），以及 Web UI 的手动 `POST /api/library/scan`（不受去抖限制）。

## 5. 读取时机

| 查询                                          | 用途                                                                |
|-----------------------------------------------|---------------------------------------------------------------------|
| `ListRunStatus`                               | `GET /api/state`                                                    |
| `GetRunStatus`                                | `UserStore.Get`，识别用户后回读状态                                 |
| `ListUsers`                                   | `GET /api/users`；歌单同步挑选管理员 token（`adminUsernames`）      |
| `ListPlaybacksByUser` / `ListRecentPlaybacks` | `GET /api/playlist`                                                 |
| `ListSyncLogsByUser` / `ListRecentSyncLogs`   | `GET /api/synclog`                                                  |
| `GetLastSuccessTime`                          | 同步前的间隔判定，每次 `syncProvider` 一次                          |
| `ListTrackMBIDs`                              | `mbid.EnrichIndexes`，注入 `MBIDToGUID` 后让歌单匹配优先走精确 MBID |

生成代码里有几个查询目前没有调用方：`GetRunStatusTotal`、`GetUser`、`GetTrackMBID` / `GetTrackMBIDByPath` / `DeleteTrackMBID`（后者只在 `datadb.Store` 里包了一层，没有业务调用）。它们是可用的调试入口，不是死代码风险点。

## 6. 数据生命周期

| 问题                | 结论                                                                                                                                                                                                               |
|---------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| 有没有清理/保留策略 | **没有**。`playback_log` 与 `sync_log` 只增不删，没有 TTL、没有定期 DELETE、没有 VACUUM 任务                                                                                                                       |
| 增长速率            | `playback_log` ≈ 每天播放曲目数；`sync_log` ≈ `2 × 用户数 × 每天同步轮数`；`users` ≈ 每客户端登录一次一行                                                                                                          |
| 重复写入去重        | `run_status` 按 `username` 主键 upsert；`users` 按 `token_prefix` 唯一键 upsert；`track_mbid_map` 按 `feiniu_guid` 主键 upsert；`playback_log` **不去重**（同一首歌播两次就是两行）；`sync_log` 不去重（每轮一条） |
| 扫描去抖            | 6h（`library.scan_interval`），且两次扫描不并发：`lastMBIDScan` 有互斥保护                                                                                                                                         |
| 空库启动            | 完全可行。所有表都是 `IF NOT EXISTS`，启动即建；没有数据就是「零统计、零历史」                                                                                                                                     |
| 删 `state.db`       | 丢累计推送数（`run_status`）、播放历史、同步历史、已识别用户与平台绑定。**推荐先备份再删**                                                                                                                         |
| 删 `data.db`        | 只丢 MBID 映射，下次扫描（定时或手动）重建；若 `library.directories` 为空则永远不会重建                                                                                                                            |
| 手工 VACUUM         | 安全，但需要停服务（单写者），且对纯删表后的空间回收才有意义                                                                                                                                                       |

### 6.1 备份与恢复

sqlite 单文件，没有 WAL（未设置 `journal_mode=WAL`），所以只在写事务进行中存在 `state.db-journal` 这类临时文件。选择其一：

```bash
# 停服务后直接复制（最稳）
systemctl stop fnmusic-sync
cp /var/lib/fnmusic-sync/state.db{,.bak}
cp /var/lib/fnmusic-sync/data.db{,.bak}
systemctl start fnmusic-sync

# 或在线一致性备份（sqlite3 CLI 的备份 API，不需要停）
sqlite3 /var/lib/fnmusic-sync/state.db ".backup '/tmp/state-$(date +%F).db'"
```

fpk 安装把两条路径换成 `$TRIM_PKGVAR/state.db` 与 `$TRIM_PKGVAR/data.db`。恢复就是停服务 → 覆盖文件 → 启动；旧版本库文件同样能被新版本打开（建表幂等 + `migrateUsersTokenKey` 兜底）。

## 7. sqlc 工作流

两个包各有一份 `sqlc.yaml`，内容一致（`internal/db/sqlc.yaml`、`internal/datadb/sqlc.yaml`）：

```yaml
version: '2'
sql:
  - engine: 'sqlite'
    queries: 'queries.sql'
    schema: 'schema.sql'
    gen:
      go:
        package: 'db'          # datadb 包里是 datadb
        out: '.'
        sql_package: 'database/sql'
        emit_json_tags: true
        emit_interface: true
        emit_empty_slices: true
```

| 文件             | 来源                                                | 手改？ |
|------------------|-----------------------------------------------------|--------|
| `schema.sql`     | 人写，建表与索引；也是 `//go:embed` 的建表脚本      | 改这里 |
| `queries.sql`    | 人写，`-- name: X :one/:many/:exec` 注解            | 改这里 |
| `queries.sql.go` | sqlc 生成                                           | 不改   |
| `models.go`      | sqlc 生成（每表一个结构体，含 json tag）            | 不改   |
| `querier.go`     | sqlc 生成（`Querier` 接口）                         | 不改   |
| `db.go`          | sqlc 生成（`DBTX`、`New`、`WithTx`）                | 不改   |
| `store.go`       | 人写，业务封装（nil 安全、时间格式化、空串转 NULL） | 改这里 |

生成命令：

```bash
make sqlc          # 依次在 internal/db 与 internal/datadb 里执行 sqlc generate
```

`make sqlc` 只调 `sqlc generate`，不负责装工具；首次用 `make tool-deps`（`go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest`）。生成文件头部的注释记录了产生它的 sqlc 版本（当前是 `v1.31.1`），跨版本重新生成会产生大量无意义 diff。

### 7.1 改 schema 的正确顺序

1. 改 `schema.sql`（建表/索引语句保持 `IF NOT EXISTS` 形式，便于空库与旧库共用同一段脚本）；
2. 若要新增查询，改 `queries.sql`。**这个文件里不能写注释** —— Makefile 注释说明 `sqlc v1.31` 对多查询文件里的注释有解析 bug，表结构注释只能放 `schema.sql`；
3. `make sqlc`；
4. 在 `store.go` 里加包装方法（保持「nil 接收者安全 + 空串转 `sql.NullString`」的既有惯例）；
5. 如果改动涉及**已存在的表结构**（加列、改约束），仅靠 `IF NOT EXISTS` 不会生效 —— 必须像 `migrateUsersTokenKey` 那样在 `Open` 里写一段幂等的检测 + 迁移代码，并写测试。

改表结构时别忘了一个约束：`internal/db` 的 schema 会被打进二进制，旧库升级时只会「补建缺的表」，不会自动 `ALTER`。

## 8. 手工排障 SQL

以下全部是**只读**查询，可直接 `sqlite3 /var/lib/fnmusic-sync/state.db` 执行（fpk 用 `$TRIM_PKGVAR` 下的路径）。写操作前先停服务。

```sql
-- 最近 20 次播放
SELECT started_at, username, title, artist, ended_at
FROM playback_log
ORDER BY started_at DESC
LIMIT 20;

-- 各用户推送量排名
SELECT username,
       lastfm_scrobbles,
       listenbrainz_scrobbles,
       total_scrobbles,
       last_scrobbled_at
FROM run_status
ORDER BY total_scrobbles DESC;

-- 某用户近 14 天的每日播放数（scrobble 是否真的在发生）
SELECT substr(started_at, 1, 10) AS day, count(*) AS plays
FROM playback_log
WHERE username = 'admin'
GROUP BY day
ORDER BY day DESC
LIMIT 14;

-- 推送配置是否和实际推送数对得上（enabled=1 但 0 次 = 配置或凭证问题）
SELECT username, lastfm_enabled, lastfm_scrobbles,
       listenbrainz_enabled, listenbrainz_scrobbles
FROM run_status
WHERE (lastfm_enabled = 1 AND lastfm_scrobbles = 0)
   OR (listenbrainz_enabled = 1 AND listenbrainz_scrobbles = 0);

-- 最近 20 条同步记录（失败原因直接看 message）
SELECT finished_at, username, provider, status, trigger, playlists, tracks, message
FROM sync_log
ORDER BY finished_at DESC
LIMIT 20;

-- 每个用户 / provider 的上次成功时间（同步间隔判定的输入）
SELECT username, provider, max(finished_at) AS last_success
FROM sync_log
WHERE status = 'success'
GROUP BY username, provider;

-- 已识别客户端（一行一个 token）
SELECT username, token_prefix, is_admin, ua_system, ua_client, first_seen_at, last_seen_at
FROM users
ORDER BY last_seen_at DESC;

-- 未收尾的播放记录（进程被 kill -9 时会残留，正常退出会被 CloseOpenPlaybacks 清掉）
SELECT count(*) AS open_rows FROM playback_log WHERE ended_at IS NULL;
```

`data.db` 侧的命中率：

```sql
-- MBID 来源分布（tag 多说明本地标签质量好，musicbrainz 多说明在线补全在工作）
SELECT matched_by, count(*) AS rows
FROM track_mbid_map
GROUP BY matched_by
ORDER BY rows DESC;

-- 有录音 MBID 的曲目占比（决定歌单能否走精确匹配这一层）
SELECT sum(CASE WHEN recording_mbid IS NOT NULL AND recording_mbid <> '' THEN 1 ELSE 0 END) AS with_recording,
       count(*) AS total
FROM track_mbid_map;

-- 抽查若干条映射，核对曲名与 MBID 是否对应
SELECT title, artist, recording_mbid, file_path
FROM track_mbid_map
WHERE recording_mbid IS NOT NULL
LIMIT 10;

-- 某曲目 GUID 的映射详情
SELECT * FROM track_mbid_map WHERE feiniu_guid = '<guid>';
```
