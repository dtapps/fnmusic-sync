# 歌单同步机制

把 Last.fm / ListenBrainz 的歌单拉下来，在飞牛曲库里匹配成 GUID，再增量写进飞牛音乐的歌单——匹配链路与写入策略是这一节的重点。

> 相关：[架构与运行原理](architecture.md) · [播放事件与 scrobble 判定](scrobble-pipeline.md) · [配置参考](configuration.md) · [数据库结构](database.md) · 快速上手见 [README](../README.md)

## 总开关与节奏

配置在 `playlist` 段（`internal/config/config.go`）：

| 配置项                   | 默认值 | 取值范围 / 说明                                 |
|--------------------------|--------|-------------------------------------------------|
| `playlist.enabled`       | `true` | 总开关                                          |
| `playlist.sync_interval` | `30m`  | 任意 Go duration；解析失败或 `<=0` 一律回退 30m |

服务生命周期：`main` 在官方后端就绪后调 `SyncService.Start()`，`defer Stop()`。

```text
Start()
 ├─ !playlist.enabled → INFO "歌单同步已禁用，跳过启动"（不设 running，不起 loop）
 └─ 起 SyncService.run(ctx, interval)
        ├─ 启动时**不做**任何同步（此刻还没有用户被识别）
        ├─ 每 interval 一次 ticker → syncAll（定时触发）
        └─ ctx 取消 → INFO "歌单同步服务停止"
```

三类触发：

| 触发           | 入口                                  | 记录的 `sync_log.trigger` | 说明                                                                   |
|----------------|---------------------------------------|---------------------------|------------------------------------------------------------------------|
| 定时           | `run` 的 ticker → `syncAll`           | `interval`                | 第一次 tick 在 `Start` 之后满一个 interval 才发生                      |
| 用户首次被识别 | `UserCache` 回调 → `onUserIdentified` | `user_identified`         | 客户端启动调 `/user/me` 后几秒内就会触发，通常是"启动即同步"的实际来源 |
| 手动           | 无                                    | —                         | `db.SyncLogTriggerManual` 常量存在但没有任何调用点                     |

两层去抖/节流：

1. **持久化间隔闸门**（`syncProvider`）：同步前查该用户 + 该 provider 的**上一次成功**时间（`sync_log` 里 `status=success` 的 `finished_at`），未到 `sync_interval` 就直接跳过并打 INFO "同步间隔未到，跳过"。跨重启、跨重复触发都有效——所以重启后即使立刻被识别，也不会重复跑。
2. **同用户互斥**（`syncingUsers`）：只覆盖识别回调路径，避免同一用户被并发同步多次。定时循环 `syncAll` 不参与这个互斥，理论上与识别回调可能并发。

`playlist.enabled` 的实际生效范围需要注意：`Start()` 被无条件调用，`startLocked` 在开关关闭时直接返回，因此**关闭总开关只停掉定时循环**；`onUserIdentified` 不检查该开关，用户被识别时仍会触发一次同步（同样受上面的间隔闸门约束，且用户配置里的子项必须 enabled + 有 username）。`UpdateConfig` 在热更新时的行为是"运行中就停掉再按新配置起"，关闭开关即可让后续 tick 消失。

未启用任何子项时该 provider 返回 `Noop`，**不写 `sync_log`**。

## Last.fm 侧四类歌单

配置路径 `users.<名字>.lastfm.playlist.*`，类型 `LastFMPlaylistSourceConfig`（`enabled` / `name` / `period` / `limit`）。同步前置条件：`lastfm.enabled == true` 且 `lastfm.username != ""`（读取类接口只需 `api_key`，**不需要** `session_key`）。

| 源              | Last.fm API                | 关键参数                            | `limit` 上限处理        | 字段映射                                                                                          |
|-----------------|----------------------------|-------------------------------------|-------------------------|---------------------------------------------------------------------------------------------------|
| `top_tracks`    | `user.getTopTracks`        | `period`（默认 `overall`）、`limit` | `<=0` 或 `>1000` → 1000 | `name` / `mbid` / `playcount` / `artist.name` / `artist.mbid` / `album.name`                      |
| `loved_tracks`  | `user.getLovedTracks`      | `limit`                             | `<=0` 或 `>1000` → 1000 | 同上 + `date.#text`                                                                               |
| `recent_tracks` | `user.getRecentTracks`     | `limit`                             | `<=0` 或 `>200` → 200   | `name` / `mbid` / `artist."#text"` / `artist.mbid` / `album."#text"`；空名的 now-playing 条目跳过 |
| `weekly_charts` | `user.getWeeklyTrackChart` | `limit`                             | `<=0` 或 `>1000` → 1000 | `name` / `mbid` / `artist."#text"` / `playcount`（无专辑字段）                                    |

`period` 合法值：`7day` / `1month` / `3month` / `6month` / `12month` / `overall`（`lastfm.validPeriods`）。默认歌单名与占位符：

| 源              | 默认名（未配 `name`） | 占位符替换                                                                                           |
|-----------------|-----------------------|------------------------------------------------------------------------------------------------------|
| `top_tracks`    | `LF 最常听`           | `{period}` → 7天 / 1个月 / 3个月 / 6个月 / 12个月；`overall` 时替换为空并清掉多余空格（"LF 最常听"） |
| `loved_tracks`  | `LF 红心收藏`         | 无                                                                                                   |
| `recent_tracks` | `LF 最近播放`         | 无                                                                                                   |
| `weekly_charts` | `LF 本周榜单`         | 无                                                                                                   |

四个源各自独立 try：单个源失败只打 ERROR，不中断其它源；返回结果累计到 `syncResult{Playlists, Tracks}`。

## ListenBrainz 侧七类歌单

配置路径 `users.<名字>.listenbrainz.playlist.*`，类型 `PlaylistSourceConfig`（`enabled` / `name` / `limit` / `range`）。前置条件：`listenbrainz.enabled == true` 且 `listenbrainz.username != ""`（`username` 缺失时启动期会用 `/1/validate-token` 自动补全并写回配置）。

前五类是 troi-bot / listenbrainz 生成的推荐歌单，走同一组接口：

| 源                   | 识别依据（`source_patch`）       | 标题兜底关键字             | 额外要求                          |
|----------------------|----------------------------------|----------------------------|-----------------------------------|
| `daily_jams`         | `daily-jams`                     | `daily jams`               | 需在 ListenBrainz 关注 `troi-bot` |
| `weekly_jams`        | `weekly-jams`                    | `weekly jams`              | 无                                |
| `weekly_exploration` | `weekly-exploration`             | `weekly exploration`       | 无                                |
| `year_discoveries`   | 前缀 `top-discoveries-of-`       | `top discoveries of`       | 无（LB 每年生成）                 |
| `year_missed`        | 前缀 `top-missed-recordings-of-` | `top missed recordings of` | 无（LB 每年生成）                 |

它们的取数与选期逻辑（`PlaylistClient.FetchTroiBotPlaylists`）：

```text
GET /1/user/{lb_user}/playlists/createdfor        # 只返回"分享给该用户"的歌单
  ├─ 按 source_patch 归类（识别不出再看标题）
  ├─ 同类多期：未过期优先，其次 last_modified_at / date 最新
  └─ 选中后 GET /1/playlist/{mbid}                # 列表接口的 track 恒为空，必须拉详情
```

后两类是基于收听统计生成的，与 troi-bot 无关：

| 源                | API                               | 参数                                                    | 映射                                                                                            |
|-------------------|-----------------------------------|---------------------------------------------------------|-------------------------------------------------------------------------------------------------|
| `top_recordings`  | `/1/stats/user/{user}/recordings` | `range`（默认 `all_time`）、`count`（仅当 `limit > 0`） | 兼容扁平字段与 `track_metadata` 两种结构；`recording_mbid` / `release_mbid` / `artist_mbids[0]` |
| `recently_played` | `/1/user/{user}/listens`          | `count`（仅当 `limit > 0`）                             | `track_metadata.{artist_name,track_name,release_name}` + `additional_info.*`                    |

`range` 合法值：`week` / `month` / `quarter` / `half_year` / `year` / `all_time` / `this_year`（默认 `all_time`）。

`limit = 0` 的语义：**不传 `count` 参数**，返回条数由 ListenBrainz 决定；代码不做上限钳制，`limit > 0` 时原样透传。

关于 `limit` 的一个事实：**只有 `top_recordings` 与 `recently_played` 会用到 `limit`**。前五类推荐歌单的配置里虽然也有 `limit` 字段，但 `syncPlaylist` 接收的是详情接口返回的完整 `TrackList`，不会按 `limit` 截断。

默认名与占位符：

| 源                   | 默认名              | `{year}` 替换                     |
|----------------------|---------------------|-----------------------------------|
| `daily_jams`         | `LB 每日推荐`       | 无                                |
| `weekly_jams`        | `LB 每周推荐`       | 无                                |
| `weekly_exploration` | `LB 每周探索`       | 无                                |
| `year_discoveries`   | `LB <year>年度发现` | 是                                |
| `year_missed`        | `LB <year>年度遗珠` | 是                                |
| `top_recordings`     | `LB 最常听`         | 无（传 `nil` vars，写了也不替换） |
| `recently_played`    | `LB 最近在听`       | 无                                |

推荐歌单结果按 **ListenBrainz 用户名**缓存 2 分钟（`recommendationCacheTTL`）：用户识别回调与定时同步常常前后脚触发，而 LB 对短时间重复请求会返回 HTML 反爬校验页。缓存只存在内存，进程重启即失效。

## 匹配链路

核心函数 `matchTrack`（`internal/playlist/sync.go`），按精度从高到低：

```text
matchTrack(indexes, artist, title, mbid)
 1. mbid != "" 且 indexes.MBIDToGUID[mbid] 命中           → matchMBID
 2. indexes.ArtistTrackToGUID[BuildArtistTrackKey(artist, title)] → matchArtistTitle
 3. 主标题降级：ArtistTrackToGUID[artist | MainTitle(title)]  → matchArtistTitle
 4. indexes.TitleToGUID[NormalizeForMatch(title)]（曲库内曲名唯一）→ matchTitleOnly
 5. TitleToGUID[NormalizeForMatch(MainTitle(title))]      → matchTitleOnly
 6. 全落空                                                → matchNone
```

归一化规则（`internal/feiniu/types.go`）：

- `NormalizeForMatch`：转小写 + 去掉所有空白字符（应对 `G.E.M.` 与 `gem ` 这类差异）；
- `MainTitle`：截断第一个附注括号及其之后的内容，括号包含半角 `(`、全角 `（`、`［`、`【`。例：`不将就 (Can't Bear It) (电影《何以笙箫默》片尾曲)` → `不将就`；
- `BuildArtistTrackKey`：`normalize(artist) + "|" + normalize(title)`。

### MBID 从哪来

飞牛曲库本身没有 MBID 字段（`feiniu.BuildTrackIndexes` 建的是艺人+曲名与唯一曲名两张表，`MBIDToGUID` 初始为空），**所有 MBID 都来自本地音乐目录扫描落库的 `track_mbid_map`**，再由 `mbid.EnrichIndexes` 注入：

```text
library.directories（递归）
   │  filepath.WalkDir，仅处理 .mp3/.flac/.m4a/.mp4/.ogg/.opus/.wav/.aiff/.ape/.wv/.dsf/.wma
   ▼
dhowden/tag 解析标签 → Title/Artist/Album + 五类 MBID
   │  ID3v2：TXXX 帧（Description 是字段名）；Vorbis/FLAC/MP4：键名即字段名
   │  classifyMBID：releasegroup → albumartist → release|album → recording|track → artist → work
   ▼
按「艺人+曲名 → 唯一曲名 → 文件名（唯一时）」匹配到飞牛 GUID（mbid.buildTrackIndex / matchTrack）
   │
   ├─ 标签里有 MBID → 直接落库（matched_by = tag）
   └─ 标签里没有任何 MBID 且 library.mbid_online_lookup = true
        → 查 MusicBrainz WS/2 /recording?query=artist:"…" AND title:"…"
        → 命中则落库（matched_by = musicbrainz），同一次扫描内按「艺人+曲名」缓存
   ▼
track_mbid_map（data.db）
   ▼
EnrichIndexes：recording / release / release_group / artist / work 五个 MBID 列
               全部映射到同一个 feiniu_guid → TrackIndexes.MBIDToGUID
```

几个必须知道的边界：

| 项                           | 事实                                                                                                                                                                               |
|------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `library.mbid_online_lookup` | 默认 `false`。默认配置下**完全不依赖外网**，MBID 只来自本地标签；打开后才发生 MusicBrainz 请求（`internal/mbid/resolver.go` 的分支）                                               |
| MusicBrainz 限速             | 客户端内置 1.1s 最小间隔（`minRequestInterval`），429/503 时 sleep 2s 后放弃该曲（记为未补全）                                                                                     |
| 在线补全的匹配策略           | 优先取 `artist-credit` 与本地艺人名精确匹配的录音，否则取首个（评分最高）                                                                                                          |
| 扫描触发与去抖               | `maybeScanMBID` 在"用户识别回调"和"每次 `syncAll`"里被调用；受 `library.scan_interval`（默认 6h，钳制在 1m~24h）去抖；扫描在独立 goroutine 里跑，自带 30 分钟超时，不随父 ctx 取消 |
| 扫描前置条件                 | `library.directories` 非空，且能拿到一个活跃用户 token 拉全量曲库；否则只在 `ScanStats.Message` 里说明并返回                                                                       |
| 手动扫描                     | Web UI 的 `POST /app/fnmusic-sync/api/library/scan` → `SyncService.ScanMBID`，不受去抖限制                                                                                         |
| 曲库拉取方式                 | `track/list?page=&size=200` 分页拉全量，每次同步都重新建索引（不落缓存）                                                                                                           |
| 索引的“唯一”含义             | 同一归一化曲名命中多首时不建 `TitleToGUID` 条目，这一步等于放弃匹配，宁可漏也不错配                                                                                                |

`ScanStats` 的字段即扫描可观测性：`scanned` / `matched` / `updated` / `skipped` / `has_mbid` / `online` / `errors` / `elapsed_ms` / `message`。

### 匹配结果与日志

每首歌匹配完按命中方式计数，歌单维度打一条 INFO：

```text
歌单曲目匹配完成  用户=… 歌单名称=… 歌单曲目数=N
                  匹配数=… MBID匹配=… 艺人曲名匹配=… 仅曲名匹配=… 未匹配示例=[最多 5 条 "艺人 - 曲名"]
```

- 同一 GUID 在单个歌单内只保留一次（`seen` map）；
- 全部未匹配 → WARN "没有匹配的曲目，跳过歌单创建"，返回 `(0, nil)`：**不建空歌单**，但该 provider 仍记为 `success`（`playlists=0`），因为函数没返回错误；
- 匹配失败不会重试，也不会写单独的表；要定位未命中曲目只能看这条日志的"未匹配示例"。

## 飞牛侧写入

实现：`internal/feiniu/client.go`（基础请求、索引、歌单 CRUD）、`internal/feiniu/types.go`（结构、`FindOrCreatePlaylist`、差量、批量添加）、`internal/feiniu/cover.go`（封面）。所有请求经 unix socket 打到 `trim-music`，`Host: trim-music`，凭证是 `Cookie: music-token=<该用户的 token>`。

```text
syncPlaylist / syncLastFMPlaylist
 ├─ 匹配 → trackGUIDs
 ├─ 空 → 跳过（不建空歌单）
 ├─ FindOrCreatePlaylist(name, coverId)
 │    ├─ playlist/list?page=1&size=200 逐项比 name（先列表，后搜索）
 │    ├─ 找不到 → search/playlist?q=<name>&page=1&size=50 再比一次
 │    ├─ 仍无 → playlist/create（coverId 为空时用默认 playlist_1acb445b…）
 │    └─ 已有 → playlist/detail 读当前 coverId，与目标不同才 playlist/edit（连 name 一起传）
 ├─ diffTracks：playlist/detail 读现有 trackGUIDs（接口失败或无曲目 → 退回内存里上次同步记录）
 ├─ AddTracksToPlaylist：按 50 首一批 POST playlist/add-track
 └─ PurgeInvalidTracks：GET playlist/purge-track-count?guid=…（清掉曲库已删除的死链）
```

| 行为         | 策略                                                                                                                                                       |
|--------------|------------------------------------------------------------------------------------------------------------------------------------------------------------|
| 增量还是覆盖 | **只增量添加，不整体覆盖**：`add-track` 是追加语义，每轮只加缺失的 GUID；除"失效引用清理"外不删除任何曲目                                                  |
| 曲目顺序     | 追加顺序 ≈ 推荐顺序，但歌单不重排；已存在的曲目不会被挪位                                                                                                  |
| 重名歌单     | 以 `name` 为身份：同名即复用；改配置里的 `name` = 建一个新歌单，旧歌单原样保留（不重命名、不删除）                                                         |
| 默认封面     | 取第一条**匹配成功且有 `coverId`** 的曲目的封面，经 `ResolvePlaylistCover` 下载（600px）后上传成 `playlist_*` 类型封面；任一步失败回退默认封面，不中断同步 |
| 封面去重     | 飞牛服务端对上传图片去重，相同图片复用同一 coverId，因此每次同步重新下载+上传是安全的                                                                      |
| 分批         | `AddTracksToPlaylist` 每批 50 首（`addTracksBatchSize`），避免请求体过大                                                                                   |
| 列表上限     | `playlist/list` 固定取 `page=1&size=200`：同名歌单若不在前 200 个里，只能靠搜索兜底命中                                                                    |

## 失败与观测

`sync_log`（`internal/db/schema.sql`）每用户每 provider 每次同步一行（`Noop` 不写）：

| 字段          | 含义                                                        |
|---------------|-------------------------------------------------------------|
| `username`    | 音乐用户名（飞牛侧身份，不是 Last.fm / LB 用户名）          |
| `provider`    | `lastfm` / `listenbrainz`                                   |
| `status`      | `success` / `failed`                                        |
| `trigger`     | `interval` / `user_identified`（`manual` 常量存在但未使用） |
| `playlists`   | 实际同步成功的歌单数（全未匹配的歌单不计）                  |
| `tracks`      | 本轮**新增**到歌单的曲目数（不是歌单总曲目数）              |
| `message`     | 失败时的错误原文；成功时为空                                |
| `started_at`  | 本次同步开始时刻（RFC3339）                                 |
| `finished_at` | 写入时刻（RFC3339）                                         |

同一张表的 `status + finished_at` 还承担"持久化同步间隔"的职责（`GetLastSuccessTime`），所以**不要手工改写这两列**，否则会改变下次同步的节奏。

常见失败原因：

| 现象（日志 / `message`）                                                | 原因与处理                                                                                                                    |
|-------------------------------------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------|
| `获取飞牛音乐曲目列表失败（用户标识=…）: HTTP 401 … INVALID TOKEN`      | 该客户端的 `music-token` 失效。代码会 `InvalidateToken` 剔除它，并改用该用户其它 token 重试；全部失效则等客户端重新登录       |
| `获取 ListenBrainz 推荐歌单失败: 解析…响应失败（响应片段: <html>`       | LB 反爬返回 200 + HTML。`getJSON` 会重试 3 次（间隔 1s/2s）；仍失败则本轮失败                                                 |
| `last.fm 返回 403 Forbidden（可能被反爬拦截）`                          | Last.fm 侧风控；确认 `api_key` 与来源 IP                                                                                      |
| `同步 daily_jams 失败` / `同步 Last.fm Top Tracks 歌单失败` 等（ERROR） | 单个歌单项失败只打 ERROR，不影响其它歌单；`message` 为空、`status` 仍是 `success`（该字段只承载 `syncPlaylist` 返回的硬错误） |
| `歌单同步跳过：用户未在配置中`（DEBUG）                                 | `/user/me` 的用户名与 `users.<key>` 不一致                                                                                    |
| `歌单同步跳过：用户未启用 ListenBrainz 或 Last.fm`（DEBUG）             | 对应平台未 `enabled`，或 `username` 为空                                                                                      |
| `没有匹配的曲目，跳过歌单创建`（WARN）                                  | 曲库没有对应歌曲，或曲名/艺人差异过大；看同一轮"未匹配示例"                                                                   |
| `读取歌单现有曲目失败，改用上次同步记录去重`（WARN）                    | `playlist/detail` 不可用，退回内存记录；进程重启后该记录为空，可能重复追加                                                    |
| `token 失效，改用该用户的其他 token 重试`（WARN）                       | 多客户端登录时正常降级路径                                                                                                    |

多 token 处理：同一用户名可能有多个客户端 token。`syncAll` 先按用户归集 token（上次成功的 token 排最前），逐个尝试；只有 `HTTP 401` / `INVALID TOKEN` 才换下一个，其它错误（网络、接口异常）直接返回——换 token 也不会更好。

## 新增一种歌单源要改哪里

以"再加一个 ListenBrainz 源"为例，按顺序：

1. **配置结构** `internal/config/config.go`
   - `UserPlaylistConfig` 加字段（如 `FollowedPlaylists PlaylistSourceConfig`）；
   - `userAccountToMap` 的 `lbMap["playlist"]` 里补同样的键，否则 Web UI 保存用户配置时会把该子项抹掉。
2. **API 客户端** `internal/listenbrainz/playlist.go`
   - 新增 `FetchXxx(ctx, count int, …) (*Playlist, error)`，把返回结构映射成 `PlaylistTrack`（字段：`ArtistName` / `TrackName` / `ReleaseName` / `RecordingMBID` / `ReleaseMBID` / `ArtistMBID`）；
   - 空结果返回 `nil`（上游按"歌单不存在"处理），错误原样返回；请求走 `c.getJSON`（自带重试与请求日志）。
3. **同步编排** `internal/playlist/listenbrainz_sync.go`
   - 把新字段加进 `syncUser` 开头的 Noop 判断（否则子项全关时仍会拉曲库）；
   - 仿照 `top_recordings` 段落加一个 `if playlistCfg.Xxx.Enabled { … s.syncPlaylist(...) }`，成功时 `res.Playlists++ / res.Tracks += added`；
   - 默认名用 `resolvePlaylistName(cfg.Name, "<默认名>", vars)`，有 `{year}` 之类占位符就把 vars 传进去。
4. **复用既有能力**：匹配（`matchTrack`）、封面（`ResolvePlaylistCover`）、差量（`diffTracks`）、失效清理（`PurgeInvalidTracks`）都不用改。
5. **配套**：`internal/playlist/sync_test.go` 补用例；若要在 Web UI 暴露开关，改 `cmd/frontend/` 与 `internal/webui/handler.go` 的配置读写；文档与 `configs/config.example.yaml` 同步更新。

Last.fm 侧同理：`internal/lastfm/playlist.go` 加 `GetXxx`（记得按 Last.fm 上限钳制 `limit`），`internal/playlist/lastfm_sync.go` 里 `syncLastFMUser` 的 Noop 判断 + 新段落，`resolvePlaylistName` 传 `{"period": periodToChinese(period)}`。

如果要加"返回专辑/艺人而非曲目"的源，额外需要一步"展开为曲目"，当前代码没有任何展开逻辑（README 的"规划中"一节列的就是这类）。
