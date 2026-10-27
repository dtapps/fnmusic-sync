# 播放事件与 scrobble 判定

代理不改写任何流量，只从转发中的请求/响应里读取"在播什么、听了多久"，再决定何时推 now playing 与 scrobble。

> 相关：[架构与运行原理](architecture.md) · [歌单同步机制](playlist-sync.md) · [配置参考](configuration.md) · [排障](troubleshooting.md) · 快速上手见 [README](../README.md)

## 代理看到的是什么

`ServeHTTP`（`internal/proxy/proxy.go`）与 `modifyResponse` 用子串匹配识别六类路径（常量集中在 `internal/proxy/proxy.go` 顶部，全部 `strings.Contains`，兼容路径带不带 `/music/api/v1` 前缀）：

| 路径                                                   | 读的是               | 交给谁                                   | 证据强度                                                                                                                                                                               |
|--------------------------------------------------------|----------------------|------------------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `/track/metadata`                                      | 响应 body            | `Detector.HandleMetadata`                | `internal/playback/detector.go` 包注释标注"已实机抓包确认"                                                                                                                             |
| `/event/report`                                        | 请求 body            | `Detector.HandleEventReport`             | 同上                                                                                                                                                                                   |
| `/track/stream`                                        | 响应 `Content-Range` | `Detector.HandleStreamProgress`          | 同上                                                                                                                                                                                   |
| `/track/play-url`                                      | 响应 `Content-Range` | `Detector.HandleStreamProgress`          | 代码把它与 `stream` 放进同一分支，但注释只对 `stream` 做过抓包说明 —— **推断**（依据：同一 `guid` + Range 处理；若该接口实际返回 JSON 而非分段流，`parseContentRange` 失败即为无操作） |
| `/track/hls/<guid>/*.m3u8`、`*.m4s`                    | 响应 body / 路径     | `HandleHLSPlaylist` / `HandleHLSSegment` | **推断**（依据：`detector.go` 注释描述"客户端切到 HLS 后没有任何进度信号"这一实机现象，据此按分片序号反推）                                                                            |
| `/user/me`、`/user/auth-login`、`/user/password-login` | 响应 body            | `UserCache.Update` / `Register`          | `/user/me` 字段名为宽松匹配、登录响应结构有注释示例 —— 结构未完全固定                                                                                                                  |

结论性前提（源码多处注释一致声明，但没有在仓库里留下抓包产物）：

- 飞牛音乐**只上报 `track_play`**，没有 pause / stop / seek / end 事件；
- 因此"播完"无法实时获知，`Manager` 把"达到 scrobble 阈值"当作唯一可判定的完成信号，`Recorder` 用"下一首开始 = 上一首结束"回填 `ended_at`。

抓包事实的落地依据：`parseMetadata` 断言 `data.track.{title, artists[].name, album.name, duration, trackNo}`（`internal/playback/parse_metadata_test.go` 用真实形态的 JSON 作为用例）；`parsePlayEvent` 断言请求体 `events[].{eventType=="track_play", payload.trackGUID}`；`parseContentRange` 断言 `bytes <start>-<end>/<total>`。

## Track 数据模型

`internal/model/track.go`，共 12 个字段，`parseMetadata` 实际填 6 个：

| 字段                                                    | 类型            | 必填 | 来源 / 说明                                                                    |
|---------------------------------------------------------|-----------------|------|--------------------------------------------------------------------------------|
| `GUID`                                                  | string          | 是   | 请求 query 的 `guid`，飞牛内部曲目标识                                         |
| `Title`                                                 | string          | 是   | `data.track.title`                                                             |
| `Artist`                                                | string          | 是   | `data.track.artists[].name`，多艺人用 `", "` 拼接，空名跳过                    |
| `Album`                                                 | string          | 否   | `data.track.album.name`                                                        |
| `AlbumArtist`                                           | string          | 否   | 当前未填充，恒为空串（Last.fm 请求会因此省略 `albumArtist`）                   |
| `Duration`                                              | `time.Duration` | 否   | **入参单位毫秒**：`time.Duration(track.duration) * time.Millisecond`；可以为 0 |
| `TrackNumber`                                           | int             | 否   | `data.track.trackNo`（Last.fm 请求未使用该字段）                               |
| `MBID` / `RecordingMBID` / `ReleaseMBID` / `ArtistMBID` | string          | 否   | 当前未填充，恒为空串                                                           |
| `Path`                                                  | string          | 否   | 当前未填充                                                                     |

`Valid()` 的判据是 `Title != "" && Artist != ""`。元数据解析失败（JSON 非法 / 缺标题 / 缺艺人）时 `HandleMetadata` 打 ERROR "曲目元数据解析失败" 并丢弃该条，不建缓存。

时间格式：`Duration` 在进程内是 `time.Duration`；落库 `playback_log.duration_ms` 用 `.Milliseconds()`；`started_at` / `ended_at` / `created_at` 一律 RFC3339 字符串；推给 Last.fm 的 `duration` 与 `timestamp` 是**秒级**十进制字符串。

## 播放会话状态机

实现：`internal/playback/detector.go`（识别与佐证）、`internal/playback/manager.go`（会话、阈值、分发）、`internal/playback/recorder.go`（落库）。

```text
        POST /event/report (track_play)
                    │
                    ▼
            ┌───────────────┐   3s 内无取流佐证，且当前无已佐证播放
            │  candidate    │──────────────┐
            │ (guid,user,at)│              │
            └───────┬───────┘              │
                    │ 取流佐证              │ 超时回退（只开最近到达的那一首）
                    │ end>0 │ HLS 分片      │
                    ▼                      ▼
            ┌────────────────────────────────────┐
            │ Session{Track,Username,StartedAt,   │
            │         Scrobbled=false}            │
            │ confirmed = true / false            │
            └───────────────┬────────────────────┘
                            │ 同一 GUID 再次 Play：只把 confirmed 升级为 true，不重开会话
                            │ 不同 GUID 且当前未被已佐证会话锁定 → 结束旧会话，开新会话
                            ▼
                  position ≥ threshold
                            │
                            ▼
                  Scrobbled = true（终态，不再推第二次）
```

四条规则决定了会话能否建立：

| 规则                         | 代码位置                                          | 作用                                                                     |
|------------------------------|---------------------------------------------------|--------------------------------------------------------------------------|
| `track_play` 只登记候选      | `Detector.HandleEventReport`                      | 网页端打开歌单 / 预加载下一首会抢发 `track_play`，直接开会话会造幽灵记录 |
| 真实取流才能佐证             | `end > 0`（排除 `bytes=0-0` 探针）、任一 HLS 分片 | `streamSeen[guid]` 记录最近佐证时间，窗口 `streamSeenWindow = 30s`       |
| 候选超时回退                 | `expireCandidates`（ticker 2s，超时 3s）          | 音频直连 CDN、不经代理取流的客户端仍能记录（`confirmed=false`）          |
| 已佐证会话不被未佐证候选覆盖 | `Manager.Play` + `CurrentConfirmed()`             | 预加载的 `track_play` 不能把正在听的歌挤掉                               |

关于第三条的细节：`expireCandidates` 先看 `Manager.CurrentConfirmed()`；为真则把所有到期候选直接丢弃（说明用户真的在听，那批 `track_play` 是预加载）。为假则在同批到期候选里只取 `at` 最大的那一首开会话，其余丢弃。

`CurrentConfirmed()` 的判据是"会话存在 && 未推送 && confirmed"，三者缺一不可。这里踩过坑：曾经只判 `current != nil && currentConfirmed`，导致第一首推送成功后标记一直为真，后续只发 `track_play`（无取流佐证）的歌全被丢弃，表现为"还在放歌却再也不推送"。

会话回收：

| 触发              | 行为                                                                                                           |
|-------------------|----------------------------------------------------------------------------------------------------------------|
| 切歌（不同 GUID） | 旧会话 `done` 关闭取消兜底定时器；未推送则打 `播放会话结束 原因=切换歌曲 已推送=false`                         |
| 达到阈值          | `Scrobbled=true` → 关闭会话、取消定时器                                                                        |
| 同一首歌再次播放  | 未推送时视为同一会话（只升级 confirmed）；已推送则开新会话，可再次推送                                         |
| 进程退出          | `Detector.Stop` 停 ticker；`Recorder.CloseOpen` 回填未结束的 `playback_log.ended_at`；未达阈值的会话**不补推** |

`Manager.Stop()` 存在但当前未从 `main` 调用，所以 `logSessionEnded(..., "程序退出")` 这条分支在正常停机路径上不会执行。

## 进度推算的三条通道

| 通道       | 数据                              | 推算公式                                              | 约束                                                          |
|------------|-----------------------------------|-------------------------------------------------------|---------------------------------------------------------------|
| 分段下载   | `Content-Range: bytes a-b/total`  | `position = end / total × Duration`                   | `end` 取历史最大值；`total` 为 `*` 时放弃；必须有已缓存元数据 |
| HLS 分片   | `#EXTINF` 分片时长 + 最大分片序号 | `position = (maxSeg+1) × segDur`                      | 未抓到播单（`segDur=0`）时不算；结果截断到 `Duration`         |
| 兜底定时器 | 真实流逝时间                      | 到 `threshold + 1s` 直接调 `CheckScrobble(threshold)` | 覆盖"预加载整首"与"音频走 CDN 不经代理"                       |

任何通道算出的 position 都会在 `CheckScrobble` 里被**真实流逝时间钳制**：

```text
elapsed = time.Since(session.StartedAt)
if elapsed < position { position = elapsed }
```

原因写在 `manager.go` 注释里：客户端预加载整首、或暂停后继续缓冲，都会让取流推算的 position 虚高；`elapsed` 是不可能作弊的下界。副作用是暂停期间 position 不增、elapsed 增，判定仍以 position 为准（不会因为挂机而误推）。

## Scrobble 阈值

配置项 `playback.scrobble_threshold`（默认 `auto`），解析在 `Manager.SetScrobbleThreshold` / `parseScrobbleThreshold`：

| 取值                                              | 语义                                          | 阈值函数                                     |
|---------------------------------------------------|-----------------------------------------------|----------------------------------------------|
| `auto`（默认，含空串、大小写不敏感）              | Last.fm 官方规则：听满一半或 4 分钟，先到为准 | `min(Duration/2, 4min)`                      |
| 固定时长（`30s`、`2m`、`90s`…）                   | 不管歌多长，听满该时长即推                    | 常量                                         |
| 比例（`50%`、`25%`，或 `0.5` 这类 0~1 小数）      | 按时长比例                                    | `Duration × ratio`                           |
| `off`（同义：`disabled`/`false`/`none`/`no`/`0`） | 关闭阈值判断，会话建立即推送                  | 恒返回 0，且 `noThreshold=true` 跳过全部比较 |

`100%`、`0%`、`abc%`、`1`、`1.5` 都会被判为非法（`0`/负数抛错，`>=1` 的小数落到时长解析后失败），此时**保留旧阈值**并打 WARN "scrobble 阈值解析失败，沿用原阈值"。

样例（`auto`）：3 分钟 → 1:30；5 分钟 → 2:30；10 分钟 → 4:00。

### 边界情形

| 情形                          | 实际行为                                                                                                                                                                                                                                                                      |
|-------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| 拿不到时长（`Duration == 0`） | `auto` 与比例模式算出的阈值为 0，`CheckScrobble` 在 `threshold <= 0` 处直接返回 —— **该曲永不推送**；兜底定时器虽然把 `threshold` 退化为 4 分钟，但它回调的 `CheckScrobble` 会重新计算阈值（仍为 0）后返回。只有固定时长与 `off` 不受影响。这是当前实现的硬边界，不是配置问题 |
| 超短曲（如 20s）              | `auto` 阈值 10s；客户端通常能取流佐证，正常推送                                                                                                                                                                                                                               |
| 超长曲（如 12min）            | `auto` 封顶 4 分钟                                                                                                                                                                                                                                                            |
| 重复播放同一首                | 已推送后再次 `Play` 会开新会话，可再推一次；`off` 模式下每首每次播放都推                                                                                                                                                                                                      |
| 前跳 seek                     | 取流 `end` 与 HLS `maxSeg` 只增不减，但 position 被 elapsed 钳制 → 前跳不会提前触发                                                                                                                                                                                           |
| 回退 seek                     | position 不会回退（历史最大值），会话也不会重启                                                                                                                                                                                                                               |
| 预加载整首后长时间暂停        | position 保持虚高，但被 elapsed 钳制，实际未听够不会推                                                                                                                                                                                                                        |
| `off` 模式                    | 会话建立即推；now playing 与 scrobble 是两个并发 goroutine，到达顺序不保证                                                                                                                                                                                                    |
| 用户未识别（`username` 为空） | 会话照常建立，但 `providersFor("")` 返回 nil → 不推任何平台；`playback_log` 也不写（`Recorder.OnPlay` 要求 username 非空）                                                                                                                                                    |

## 用户归属

链路：连接 → token → 用户名 → 配置里的凭证集合。

```text
Cookie: music-token=<v>            → userKey=<v>, source=music-token
Authorization: Bearer <v>          → userKey=<v>, source=music-token
X-Auth-Token: <v>                  → userKey=<v>, source=music-token
Cookie: fnos-token=<v>             → userKey=<v>, source=fnos-token（仅兜底）
        │
        ▼
UserCache.Resolve(key, source, header)
 ├─ 命中 byKey          → 直接返回用户名（热路径，无锁写）
 ├─ 命中 failed 且 <5min → 返回空，不再探测
 ├─ source != music-token → 返回空（不主动探测系统 token）
 └─ 否则异步 probe /user/me（复用原请求的 Cookie / Authorization / X-Auth-Token / Authx）
        │
        ▼
parseUsername：按 username / user_name / userName / name / account / nickname / login /
               displayName / display_name 依次尝试，顶层取不到再看 data.*
        │
        ▼
UserCache.Register → users 表（token_prefix 前 8 位唯一）+ 触发 onUserIdentified 回调
        │
        ▼
Manager.providersFor(username) → 该用户的 []Scrobbler（Last.fm / ListenBrainz）
```

识别用户名还有两条主动路径（在 `modifyResponse` 里，与 debug 开关无关，始终执行）：

- 客户端自己调 `/user/me` 时拦截响应解析（只认 `music-token` 来源）；
- 客户端 token 过期后重新登录，从 `/user/auth-login`（或 `/user/password-login`）响应里取 `data.userToken` + `data.user.name` 直接 `Register`，并把旧 token `InvalidateToken`。

识别失败时的行为：

- 不推送、不写 `playback_log`，会话状态机照常运转（因此不会因为识别晚到而丢当前这首，只丢识别前的那段）；
- 失败 token 进 `failed` 表，5 分钟内不再探测，避免无效 token 刷屏；
- 用户名 key 与配置 `users.<名字>` 不一致 = 该用户没配凭证 = 静默不推（README 排障表里的第一条）。

另外 `users.last_seen_at` 由 `seenToucher` 每 token 前缀 2 分钟节流刷新，异步写库，不阻塞请求。

## 推送时序与失败处理

```text
会话建立（Play）
 ├─ onPlayStart → Recorder.OnPlay：回填上一首 ended_at，写一行 playback_log（username 非空时）
 ├─ off 模式？→ CheckScrobble（立即）
 ├─ 否则 → 定时器 threshold+1s
 ├─ INFO 播放会话开始
 └─ 每 provider 一个 goroutine：UpdateNowPlaying

取流/HLS 进度到达 → CheckScrobble(position)
 ├─ 已 Scrobbled → 返回
 ├─ position 被 elapsed 钳制
 ├─ 未达阈值 → WARN "scrobble 未到阈值"（含播放位置与阈值）
 └─ 达标 → Scrobbled=true → 每 provider 一个 goroutine：Scrobble(track, StartedAt.Unix())
             └─ 成功 → INFO "scrobble推送成功" + onScrobbled → run_status 计数
             └─ 失败 → ERROR "scrobble推送失败"，不重试
```

| 维度               | 结论                                                                                                                |
|--------------------|---------------------------------------------------------------------------------------------------------------------|
| now playing 触发点 | 会话建立时（取流佐证后或超时回退后），**不是**收到 `track_play` 的瞬间；最坏晚 `candidateConfirmTimeout` 的 3s 量级 |
| scrobble 时间戳    | `Session.StartedAt.Unix()`，即会话建立时刻，与 `track_play` 时刻可能相差数秒                                        |
| 去重               | 按会话；`Scrobbled` 一次性，now playing 每个会话一次                                                                |
| 重试               | 无重试、无离线队列、无持久化待发队列；失败只留日志                                                                  |
| 超时               | provider 的 `http.Client` 均为 15s                                                                                  |
| 对官方客户端       | 全部推送在后台 goroutine，转发路径同步等待的只有"读响应 body 做解析"，失败不改变任何响应                            |
| 顺序               | 多 provider 之间、now playing 与 scrobble 之间都没有顺序保证                                                        |

## 可观测性

`playback_log`（`internal/db/schema.sql`）每首歌一行，由 `Recorder` 写入：

| 字段                         | 含义                           | 备注                                            |
|------------------------------|--------------------------------|-------------------------------------------------|
| `username`                   | 音乐用户名（来自 `/user/me`）  | 未识别时不写行                                  |
| `token_prefix`               | 触发播放的客户端 token 前 8 位 | **当前恒为 NULL**：`Recorder.OnPlay` 未传入该值 |
| `guid`                       | 飞牛曲目 GUID                  | 来自取流 query 的 `guid`                        |
| `title` / `artist` / `album` | 元数据三件套                   | 来自 `track/metadata` 缓存                      |
| `duration_ms`                | 时长（毫秒），可为 0           | 总是写入（`Valid` 恒为 true）                   |
| `started_at`                 | 会话建立时刻（RFC3339）        | 等值于推给 Last.fm 的 timestamp 时刻            |
| `ended_at`                   | 结束时刻；进行中/未知为 NULL   | 只在"下一首开始"或进程退出时回填                |
| `created_at`                 | 入库时刻                       |                                                 |

`--debug`（或 `wizard_debug=1/true/on/yes`）额外可见：

- `capture.log`：每个非取流请求的完整请求/响应（含 `event/report` 请求体、`track/metadata` 响应体），敏感头打码，body 截断到 64KiB；
- `feiniu.log` / `lastfm.log` / `listenbrainz.log`：三个客户端出站请求的请求与响应；
- 主日志的 DEBUG 条目：`已缓存曲目`、`收到播放事件(track_play)，登记候选`、`真实取流佐证播放`、`HLS 分片佐证播放`、`取流进度推算`、`HLS 进度推算`、`开始取流` / `取流结束` / `取流被中断`、`探测用户信息成功`。

常用日志关键字（原文，可直接 grep）：

| 关键字（原文）                                       | 级别         | 含义                                             |
|------------------------------------------------------|--------------|--------------------------------------------------|
| `播放事件(event/report)用户识别`                     | INFO         | 每次 `event/report` 都打，含"是否推送"           |
| `播放会话开始`                                       | INFO         | 会话建立（含标题、艺人）                         |
| `推送scrobble`                                       | INFO         | 达到阈值，即将分发                               |
| `scrobble推送成功` / `scrobble推送失败`              | INFO / ERROR | 每 provider 一条                                 |
| `正在播放推送失败`                                   | ERROR        | now playing 失败                                 |
| `scrobble 未到阈值`                                  | WARN         | 每次进度事件未达标都会打，正常听歌时较频繁       |
| `播放会话结束`                                       | INFO         | 未推送就切歌（`原因=切换歌曲` / `已推送=false`） |
| `当前存在取流佐证的进行中播放，丢弃 track_play 候选` | DEBUG        | 预加载被正确忽略                                 |
| `曲目元数据解析失败`                                 | ERROR        | metadata 结构异常或缺标题/艺人                   |
| `识别到用户`                                         | INFO         | token → 用户名首次成功映射                       |
| `探测用户信息失败`                                   | WARN         | 主动探测 5s 内失败（含状态码与 token 名诊断）    |

## 验证 scrobble 是否工作

```bash
# 1. 先看接管态：监听归属应为 proxy，上游归属应为 trim，权限至少 0666
sudo fnmusic-sync --check
sudo fnmusic-sync sockets

# 2. 代理自身与上游健康
curl -s --unix-socket /var/run/trim_music.socket http://localhost/_ext/healthz

# 3. 前台 debug 起一份（会占单实例锁，先停服务）
sudo systemctl stop fnmusic-sync
sudo fnmusic-sync --debug

# 4. 另一个终端观察主日志（播放一首歌，等过阈值）
sudo tail -f /var/log/fnmusic-sync/fnmusic-sync.log \
  | grep -E '播放事件|播放会话开始|推送scrobble|scrobble推送|scrobble 未到阈值|识别到用户'

# 5. 确认原始流量被解析（event/report 与 metadata 都应出现）
sudo tail -f /var/log/fnmusic-sync/capture.log

# 6. 确认真的发出去了
sudo tail -f /var/log/fnmusic-sync/lastfm.log
sudo tail -f /var/log/fnmusic-sync/listenbrainz.log

# 7. 快速验证阈值：把 playback.scrobble_threshold 改成 30s（热加载生效），再播一首
#    config.yaml 保存后日志出现 "配置热更新"

# 8. 看播放记录与推送计数（fpk 模式）
curl -s --unix-socket /var/apps/fnmusic-sync/target/app.sock \
  'http://localhost/app/fnmusic-sync/api/playlist?limit=5'
curl -s --unix-socket /var/apps/fnmusic-sync/target/app.sock \
  'http://localhost/app/fnmusic-sync/api/state'
```

判读顺序：

1. 有 `播放事件(event/report)用户识别` 且"是否推送=true" → 用户归属没问题；
2. 有 `播放会话开始` → 候选被取流佐证（或超时回退）；
3. 有 `推送scrobble` 但没有 `scrobble推送成功` → 看同一时刻的 ERROR 与 `lastfm.log` / `listenbrainz.log`；
4. 只有 `scrobble 未到阈值` → 要么没听够，要么 `Duration` 为 0（见边界情形）；
5. 连 `播放事件` 都没有 → 代理没接管或被旁路（见 [architecture.md](architecture.md) 的 socket 恢复一节）。
