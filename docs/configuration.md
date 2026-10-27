# 配置参考

fnmusic-sync 的全部运行参数集中在一份 YAML 里：路径由运行模式决定，键值可热加载，凭证可被程序自己回写。

> 相关：[README](../README.md) · [命令行参考](cli.md) · [数据模型与数据库](database.md) · [部署](deployment.md) · [排障](troubleshooting.md)

> 快速上手见 [README](../README.md) 的「配置」一节；本文写全量键、默认值、热加载边界与回写行为。

## 1. 配置文件位置与解析

### 1.1 位置

配置文件路径**不可配置**，由 `detectRuntime()` 在进程启动时一次性判定（`cmd/fnmusic-sync/runtime.go`）：

| 运行模式 | 配置文件路径                    | 判定条件                     |
|----------|---------------------------------|------------------------------|
| 传统安装 | `/etc/fnmusic-sync/config.yaml` | 环境变量 `TRIM_APPDEST` 为空 |
| fpk 安装 | `$TRIM_PKGETC/config.yaml`      | 环境变量 `TRIM_APPDEST` 非空 |

fpk 模式下若 `TRIM_PKGETC` 缺失，回退为 `$TRIM_APPDEST/../etc/config.yaml`；`TRIM_PKGVAR` 缺失则回退为 `$TRIM_APPDEST/../var`（`cmd/fnmusic-sync/runtime.go:45-75`）。

`fnmusic-sync version` 会打印当前实际生效的配置、状态、日志路径 —— 判断模式不用瞎猜，看它输出即可。

### 1.2 解析规则

配置读取全部走 viper（`internal/config/config.go:162-204`）：

| 环节         | 行为                                                                                                 |
|--------------|------------------------------------------------------------------------------------------------------|
| 实例         | 每次 `Load`/`Watch` 都 `viper.New()`，互不共享状态                                                   |
| 读取         | `SetConfigFile(path)` + `SetConfigType("yaml")` + `ReadInConfig()`                                   |
| 文件不存在   | 不报错，直接用内置默认值（`ConfigFileNotFoundError` 与 `os.IsNotExist` 都归为「用默认值」）          |
| 文件语法错误 | 返回 `读取配置文件 <path> 失败: <err>`，启动即失败                                                   |
| 环境变量     | **不支持**。没有 `AutomaticEnv` / `BindEnv` / `SetEnvPrefix`，`config.yaml` 里的键无法用环境变量覆盖 |
| 未知键       | 静默忽略，不报错（拼错 key 的后果见第 9 节）                                                         |

唯一被读取的环境变量是 `wizard_debug`（`cmd/fnmusic-sync/main.go:38-45`），它不参与配置解析，只决定是否开启请求/响应明细日志（等价 `--debug`）。fpk 安装方式的启动脚本会先 `source $TRIM_PKGETC/config.env`，把该变量注入进程环境（`fpkg/cmd/main`）。

### 1.3 首次启动自动生成

`run()` 在读取配置前先 `os.Stat` 配置文件，不存在则调用 `config.SaveDefault`（`internal/config/config.go:206-219`、`cmd/fnmusic-sync/main.go:91-101`）：

1. `os.MkdirAll` 创建父目录（`0755`）；
2. 新建 viper，只灌 `setDefaults` 的默认值，`WriteConfigAs` 落盘；
3. 新文件权限 `0644`（viper 的 `configPermissions` 默认值，`viper@v1.21.0/viper.go:198`）；
4. 写失败（如权限不足）不退出：打 `未找到配置文件且无法自动创建，使用内置默认配置（纯代理模式，不推送 scrobble）` WARN，继续以默认值运行。

生成的内容**只有默认值里存在的键**。因为 `setDefaults` 没有为 `users`、`server.socket_mode`、`library.directories` 设默认，自动生成的 `config.yaml` 里不会有这三项 —— 想加用户得照 `configs/config.example.yaml` 手写。

## 2. 全量键参考

类型按 `internal/config/config.go` 的 mapstructure 定义，默认值一律取自 `setDefaults`（`internal/config/config.go:559-572`）；`config.example.yaml` 与代码不一致的地方在表中直接标注。

### 2.1 `server`

| 键路径                 | 类型   | 默认值  | 取值 / 可选值                          | 说明                                                                                                                   |
|------------------------|--------|---------|----------------------------------------|------------------------------------------------------------------------------------------------------------------------|
| `server.socket_mode`   | int    | `0`     | `0` 或八进制权限（如 `0o660`、`0666`） | 代理在 `/var/run/trim_music.socket` 上监听时 `chmod` 的权限。`0` = 继承官方 socket 当前权限；两者都取不到时兜底 `0666` |
| `server.upstream_wait` | string | `"30s"` | 任意 Go duration（`10s`、`2m`）        | 启动时等待官方后端就绪的超时；解析失败则沿用 `--upstream-wait` 的值                                                    |

`socket_mode` 不是字面 `chmod`：`Takeover.effectiveMode` 会强制补上 other 的读写位（`internal/proxy/socket.go:360-378`）。写 `0o660` 实际得到 `0666` —— 因为 fnOS 的 nginx worker 以 `www-data` 运行，缺 other 位就是 502。

`server.upstream_wait` 的实际优先级是「配置文件 > 命令行 flag」：`run()` 里只要配置值能被 `time.ParseDuration` 解析就无条件覆盖 `--upstream-wait`，而默认值是合法的 `30s`，所以 `--upstream-wait` 基本只在该键被写成非法值时才有意义（`cmd/fnmusic-sync/main.go:111-113`）。

### 2.2 `library`

本地音乐目录用于解析音频标签里的 MusicBrainz 标签，建立「MBID ↔ 飞牛曲目 GUID」映射，让 scrobble 与歌单匹配优先走精确的录音 MBID。

| 键路径                       | 类型     | 默认值  | 取值 / 可选值                       | 说明                                                                                                                          |
|------------------------------|----------|---------|-------------------------------------|-------------------------------------------------------------------------------------------------------------------------------|
| `library.directories`        | []string | 空      | 绝对路径数组，可多个，递归扫描      | 必须是本进程能直接读到的路径（NAS 本机路径，或已挂载的 SMB/NFS 挂载点）。留空则不解析文件标签，MBID 匹配退化为「艺人名+曲名」 |
| `library.scan_interval`      | string   | `"6h"`  | Go duration，实际限制在 `[1m, 24h]` | MBID 本地扫描的去抖间隔。空或非法回退 `6h` 并打 WARN；小于 `1m` 抬到 `1m`，大于 `24h` 压到 `24h`                              |
| `library.mbid_online_lookup` | bool     | `false` | `true` / `false`                    | 本地标签缺 MBID 时是否在线查询 MusicBrainz 补全录音 MBID（`matched_by=musicbrainz`）                                          |

`library.mbid_online_lookup` 是**代码里有、`config.example.yaml` 与 README 都没写**的键（`internal/config/config.go:36-41`）。开启后扫描会按「艺人名+曲名」请求 MusicBrainz WS/2，客户端自带 1100ms 的最小请求间隔以遵守对方「每秒最多 1 请求」的限速（`internal/mbid/musicbrainz.go:25-26`），单次扫描内相同「艺人+曲名」只查一次（`internal/mbid/resolver.go:103-106`）。默认关闭的原因很直接：它引入外网依赖，且只有在你希望启用「录音 MBID 精确匹配」这一层时才值得付出扫描时长。

Web UI 的 `GET/PUT /api/library` 会返回并接受这个字段（`internal/webui/handler.go:786-819`），所以界面上能改，配置文件里也能手写。

### 2.3 `users.<name>.lastfm`

`<name>` 必须与飞牛 `/user/me` 返回的用户名逐字符一致 —— 它是 scrobble 归属的唯一依据（日志里 `识别到用户 用户名=xxx` 就是它）。

| 键路径                            | 类型   | 默认值  | 取值 / 可选值         | 说明                                                                             |
|-----------------------------------|--------|---------|-----------------------|----------------------------------------------------------------------------------|
| `users.<name>.lastfm.enabled`     | bool   | `false` | `true` / `false`      | 是否向 Last.fm 推送 scrobble                                                     |
| `users.<name>.lastfm.api_key`     | string | `""`    | Last.fm API Key       | 应用凭证，四项齐全才会真正启用（见第 7 节）                                      |
| `users.<name>.lastfm.api_secret`  | string | `""`    | Last.fm Shared Secret | 同上                                                                             |
| `users.<name>.lastfm.session_key` | string | `""`    | 授权后获得            | 用户会话密钥。留空且 `api_key`/`api_secret` 已填时，启动会打印授权链接并自动写回 |
| `users.<name>.lastfm.username`    | string | `""`    | Last.fm 用户名        | 歌单同步必需（`user.getTopTracks` 等读取接口按它取数）；授权成功时自动写回       |
| `users.<name>.lastfm.playlist.*`  | object | 见下    | —                     | Last.fm 智能歌单同步，详见 2.4                                                   |

### 2.4 `users.<name>.lastfm.playlist`

源码里 `weekly_charts` 存在，但 README 的 YAML 示例漏了它 —— 它在 `config.example.yaml` 里有。

| 键路径                                                   | 类型   | 默认值                          | 取值 / 可选值                                         | 说明                                                                       |
|----------------------------------------------------------|--------|---------------------------------|-------------------------------------------------------|----------------------------------------------------------------------------|
| `...playlist.top_tracks.enabled`                         | bool   | `false`                         | `true` / `false`                                      | 最常听歌单                                                                 |
| `...playlist.top_tracks.name`                            | string | `"LF 最常听"`                   | 任意字符串，支持 `{period}`                           | 落库时为空则用代码内默认名；`period=overall` 时 `{period}` 被移除          |
| `...playlist.top_tracks.period`                          | string | `""` → `overall`                | `7day`/`1month`/`3month`/`6month`/`12month`/`overall` | 统计周期。**代码不校验**：非法值原样转发 Last.fm，接口报错即该歌单同步失败 |
| `...playlist.top_tracks.limit`                           | int    | `0`（=不限制）                  | `0` 或 `1..1000`                                      | 上限 1000；`<=0` 或 `>1000` 都按 1000 取                                   |
| `...playlist.loved_tracks.enabled` / `.name` / `.limit`  | —      | `false` / `"LF 红心收藏"` / `0` | 同上                                                  | 红心收藏；`limit` 上限 1000                                                |
| `...playlist.recent_tracks.enabled` / `.name` / `.limit` | —      | `false` / `"LF 最近播放"` / `0` | 同上                                                  | 最近播放；`limit` 上限 **200**                                             |
| `...playlist.weekly_charts.enabled` / `.name` / `.limit` | —      | `false` / `"LF 本周榜单"` / `0` | 同上                                                  | Last.fm `weeklytrackchart`；`limit` 上限 1000                              |

`period` 字段在四个 Last.fm 歌单源的结构体里都存在（共用 `LastFMPlaylistSourceConfig`），但只有 `top_tracks` 读取它：`loved_tracks`、`recent_tracks`、`weekly_charts` 即使写了 `period` 也不生效（`internal/playlist/lastfm_sync.go`）。

### 2.5 `users.<name>.listenbrainz`

| 键路径                                 | 类型   | 默认值  | 取值 / 可选值       | 说明                                                                                           |
|----------------------------------------|--------|---------|---------------------|------------------------------------------------------------------------------------------------|
| `users.<name>.listenbrainz.enabled`    | bool   | `false` | `true` / `false`    | 是否推送到 ListenBrainz                                                                        |
| `users.<name>.listenbrainz.token`      | string | `""`    | 用户 Token          | scrobble 只认 token，不需要 username                                                           |
| `users.<name>.listenbrainz.username`   | string | `""`    | ListenBrainz 用户名 | 歌单同步必需；留空且 `enabled=true`、`token` 已填时，启动自动调 `/1/validate-token` 取回并写回 |
| `users.<name>.listenbrainz.playlist.*` | object | 见下    | —                   | 推荐/统计歌单同步，详见 2.6                                                                    |

### 2.6 `users.<name>.listenbrainz.playlist`

| 键路径                             | 类型   | 默认值            | 取值 / 可选值                                                      | 说明                                                             |
|------------------------------------|--------|-------------------|--------------------------------------------------------------------|------------------------------------------------------------------|
| `...playlist.daily_jams.*`         | object | 关                | `enabled`/`name`/`limit`                                           | 默认名 `LB 每日推荐`；需在 ListenBrainz 关注 `troi-bot` 才有数据 |
| `...playlist.weekly_jams.*`        | object | 关                | 同上                                                               | 默认名 `LB 每周推荐`                                             |
| `...playlist.weekly_exploration.*` | object | 关                | 同上                                                               | 默认名 `LB 每周探索`                                             |
| `...playlist.year_discoveries.*`   | object | 关                | 同上，`name` 支持 `{year}`                                         | 默认名 `LB {year}年度发现`（年份取自歌单标识）                   |
| `...playlist.year_missed.*`        | object | 关                | 同上，`name` 支持 `{year}`                                         | 默认名 `LB {year}年度遗珠`                                       |
| `...playlist.top_recordings.*`     | object | 关                | `enabled`/`name`/`limit`/`range`                                   | 默认名 `LB 最常听`；基于收听统计                                 |
| `...playlist.top_recordings.range` | string | `""` → `all_time` | `week`/`month`/`quarter`/`half_year`/`year`/`all_time`/`this_year` | 统计维度。代码不校验，非法值由 ListenBrainz 接口拒绝             |
| `...playlist.recently_played.*`    | object | 关                | `enabled`/`name`/`limit`                                           | 默认名 `LB 最近在听`；基于 listens 记录                          |

`limit` 在 ListenBrainz 侧语义是「最多几首」：`limit>0` 才作为 `count` 参数发出，`0` 表示不带该参数、由服务端决定（`internal/listenbrainz/playlist.go:477-489`）。

### 2.7 `playback`

| 键路径                        | 类型   | 默认值   | 取值 / 可选值                                                             | 说明                         |
|-------------------------------|--------|----------|---------------------------------------------------------------------------|------------------------------|
| `playback.scrobble_threshold` | string | `"auto"` | `auto`、Go duration（`30s`/`2m`）、百分比（`50%`）、小数（`0.5`）、关闭值 | 触发 scrobble 的播放进度阈值 |

解析规则（`internal/playback/manager.go:394-459`）：

| 写法                                               | 阈值                                         |
|----------------------------------------------------|----------------------------------------------|
| 空串 / `auto`                                      | `min(歌曲时长/2, 4min)`，即 Last.fm 官方规则 |
| `30s` / `2m` 等 duration                           | 固定时长                                     |
| `50%`                                              | 歌曲时长的 50%                               |
| `0.5`（0 与 1 之间的小数）                         | 同百分比语义                                 |
| `off` / `disabled` / `false` / `none` / `no` / `0` | 关掉判断，收到播放事件即推送                 |

非法值不会中断：打 `scrobble 阈值解析失败，沿用原阈值` WARN，保留上一次生效的阈值。

### 2.8 `playlist`

| 键路径                   | 类型   | 默认值  | 取值 / 可选值      | 说明                              |
|--------------------------|--------|---------|--------------------|-----------------------------------|
| `playlist.enabled`       | bool   | `true`  | `true` / `false`   | 歌单同步总开关                    |
| `playlist.sync_interval` | string | `"30m"` | 任意正 Go duration | 同步间隔；非法或 `<=0` 回退 `30m` |

总开关为 `true` 只是允许同步，每个用户、每种歌单源还要各自 `enabled`，并且要满足「已识别到活跃用户 + 该用户配置了 `username`」。

### 2.9 `logging`

| 键路径                | 类型   | 默认值   | 取值 / 可选值                                   | 说明                           |
|-----------------------|--------|----------|-------------------------------------------------|--------------------------------|
| `logging.enabled`     | bool   | `true`   | `true` / `false`                                | 是否写日志文件（不影响控制台） |
| `logging.level`       | string | `"info"` | `debug` / `info` / `warn` / `warning` / `error` | 其余值一律按 `info` 处理       |
| `logging.max_size`    | int    | `3`      | `0` = 不按大小切割                              | 单文件超过该 MB 数就切割       |
| `logging.max_backups` | int    | `5`      | `0` = 不限份数                                  | 历史日志保留份数               |
| `logging.max_age`     | int    | `7`      | `0` = 不按时间过期                              | 历史日志保留天数               |
| `logging.compress`    | bool   | `true`   | `true` / `false`                                | 切割后的历史文件是否 gzip 压缩 |

> `logging.enabled` 的代码默认值是 `true`（`internal/config/config.go:564`），而 `configs/config.example.yaml:167` 写的是 `enabled: false`，README 完全没列这个键。**以代码为准**：不写这个键时文件日志是开的。

日志目录与文件名不可配：`rt.LogDir` + `fnmusic-sync.log`（`cmd/fnmusic-sync/logging.go:53-55`）。同一目录下还有随 `logging.*` 轮转的从属文件：`capture.log`（请求/响应明细，仅 `--debug`/`wizard_debug` 开启时写）、`feiniu.log`、`lastfm.log`、`listenbrainz.log`（`cmd/fnmusic-sync/main.go:194-196`、`internal/proxy/capture.go`）、debug 模式下 `SIGUSR1` 生成的 `goroutine-dump.txt`。

## 3. 热加载矩阵

热加载由 `config.Watch`（viper `WatchConfig`，文件不存在时先监听父目录等 `CREATE` 再切文件级监听，`internal/config/config.go:221-308`）驱动，回调 `applyConfig` 统一处理（`cmd/fnmusic-sync/main.go:209-241`）。

| 配置                                            | 生效方式            | 依据                                                                                   |
|-------------------------------------------------|---------------------|----------------------------------------------------------------------------------------|
| `users.*` 全部键                                | 立即                | `manager.UpdateProviders(buildUserProviders(...))` 重建各用户推送平台实例              |
| `playback.scrobble_threshold`                   | 立即                | `manager.SetScrobbleThreshold`                                                         |
| `logging.level`                                 | 立即                | `levelVar.Set(parseLevel(c))`                                                          |
| `playlist.enabled`                              | 有条件，见下        | `playlistSync.UpdateConfig`                                                            |
| `playlist.sync_interval`                        | 立即（重启 ticker） | 同上                                                                                   |
| `users.*.playlist.*`                            | 立即                | 同上                                                                                   |
| `library.scan_interval`                         | 立即                | 去抖间隔读 `SyncService.cfg`                                                           |
| `server.socket_mode`                            | **需重启**          | 只在启动时喂给 `proxy.Config`，热加载路径不碰 socket                                   |
| `server.upstream_wait`                          | **需重启**          | `run()` 里只解析一次                                                                   |
| `logging.enabled`                               | **需重启**          | 文件 handler 在 `newLogger` 时构造；热加载只改级别                                     |
| `logging.max_size/max_backups/max_age/compress` | **需重启**          | lumberjack 实例在启动时按当时的配置创建（主日志与三个 reqlog、capture 均如此）         |
| `library.directories`                           | **需重启**          | `mbid.Resolver` 持有启动时传入的 `*config.Config` 指针，热加载只替换 `SyncService.cfg` |
| `library.mbid_online_lookup`                    | **需重启**          | 同上，扫描时读 `r.cfg.Library.MBIDOnlineLookup`                                        |

`playlist.enabled` 的 0→1 与 1→0→1 都不完整：`UpdateConfig` 只在「此前正在运行且新配置仍开启」时重启服务（`internal/playlist/sync.go:440-456`），而启动时若总开关是关的，`startLocked` 直接返回、`running` 保持 `false`。所以在运行期把 `playlist.enabled` 从 `false` 改成 `true`，同步不会自己起来 —— 重启进程才生效。改成 `false` 则会立刻停。

热加载失败的两种表现：

- 解析失败（YAML 语法错、类型不匹配）：`reload` 返回错误 → WARN `配置热更新失败，沿用旧配置`，进程继续用上一份配置跑。
- 语义失败（阈值格式、间隔非法）：`applyConfig` 内部各自兜底 —— 阈值 `warn` 后沿用旧值，`playlist.sync_interval` 非法回退 `30m`，`library.scan_interval` 非法回退 `6h`。不会因此退出。

热加载只对「文件被写」这个事件做出反应。viper 用 `WriteConfig` 覆盖写同一 inode，`fsnotify` 的 `WRITE` 事件能触发；用 `mv` 原子替换同样能触发。

## 4. 程序回写配置

程序会自己改 `config.yaml`，共 6 个入口，全部走 `internal/config/config.go` 里同一套「读 → `v.Set` → `WriteConfig`」流程：

| 触发场景                                   | 写入的键                                              | 函数                          |
|--------------------------------------------|-------------------------------------------------------|-------------------------------|
| Last.fm 授权成功（启动轮询）               | `users.<name>.lastfm.session_key`、`.username`        | `SetLastFMCredentials`        |
| Last.fm 授权成功（Web UI 轮询）            | 同上                                                  | 同上                          |
| ListenBrainz `validate-token` 成功（启动） | `users.<name>.listenbrainz.username`                  | `SetListenBrainzUsername`     |
| Web UI 保存用户                            | `users.<name>` 整段替换                               | `SaveUser`                    |
| Web UI 删除用户                            | 从 `users` map 中删掉该键                             | `DeleteUser`                  |
| Web UI 保存全局设置 / 音乐目录             | `playback.*`、`playlist.*`、`logging.*` / `library.*` | `SaveSettings`、`SaveLibrary` |

写回行为的三个后果，必须知道：

1. **注释会丢。** viper 只保留键值，`WriteConfig` 按 yaml encoder 重新序列化，`config.yaml` 里所有注释、空行与原有顺序都不再存在（键按字母序重排）。`configs/config.example.yaml` 里那些说明只在你手写时有用。
2. **默认值会被物化。** 写回时 `AllSettings()` 会把 `setDefaults` 的键一起写出来，于是文件里会突然出现 `logging.enabled: true`、`library.scan_interval: 6h` 这类你没写过的键。
3. **权限不重置。** `WriteConfig` 以 `O_CREATE|O_TRUNC|O_WRONLY` 打开已存在的文件，`0644` 只在文件不存在时生效，所以已有文件保留原权限（`viper@v1.21.0/viper.go:1683-1688`）。首次生成的配置文件是 `0644`。

写回会触发 `fsnotify`，`config.Watch` 随即热加载 —— 这正是「授权完不用重启」的实现方式。

## 5. 路径矩阵

| 用途                 | 传统安装                                                                     | fpk 安装                                        | 决定代码                                                              |
|----------------------|------------------------------------------------------------------------------|-------------------------------------------------|-----------------------------------------------------------------------|
| 二进制               | `/usr/bin/fnmusic-sync`                                                      | `$TRIM_APPDEST/fnmusic-sync`                    | `.nfpm.yaml` 的 `contents.dst`（deb）、`fpkg/app/fnmusic-sync`（fpk） |
| 配置文件             | `/etc/fnmusic-sync/config.yaml`                                              | `$TRIM_PKGETC/config.yaml`                      | `cmd/fnmusic-sync/runtime.go:45-75`                                   |
| 运营状态库 state.db  | `/var/lib/fnmusic-sync/state.db`                                             | `$TRIM_PKGVAR/state.db`                         | 同上（`StatePath`）                                                   |
| 数据库 data.db       | `/var/lib/fnmusic-sync/data.db`                                              | `$TRIM_PKGVAR/data.db`                          | `cmd/fnmusic-sync/main.go:147-159`（state 库同目录 + `data.db`）      |
| 日志目录             | `/var/log/fnmusic-sync`                                                      | `$TRIM_PKGVAR/logs`                             | `runtime.go:58-64`                                                    |
| 主日志               | `/var/log/fnmusic-sync/fnmusic-sync.log`                                     | `$TRIM_PKGVAR/logs/fnmusic-sync.log`            | `cmd/fnmusic-sync/logging.go:53-55`                                   |
| 请求明细日志         | 同日志目录下的 `capture.log`、`feiniu.log`、`lastfm.log`、`listenbrainz.log` | 同左                                            | `internal/proxy/capture.go`、`main.go:194-196`                        |
| 单实例锁             | `/run/fnmusic-sync/run.lock`                                                 | `$TRIM_PKGVAR/run.lock`                         | `runtime.go:29`、`main.go:355-380`                                    |
| 代理监听 socket      | `/var/run/trim_music.socket`                                                 | 同左                                            | `internal/buildinfo/buildinfo.go`（常量）                             |
| 官方 upstream socket | `/var/run/trim_music_upstream.socket`                                        | 同左                                            | 同上                                                                  |
| Web UI socket        | 不启动                                                                       | `$TRIM_APPDEST/app.sock`                        | `cmd/fnmusic-sync/webui_fpk.go:12-30`                                 |
| fpk 进程日志 / PID   | —                                                                            | `$TRIM_PKGVAR/info.log`、`$TRIM_PKGVAR/app.pid` | `fpkg/cmd/main`                                                       |
| fpk 向导值           | —                                                                            | `$TRIM_PKGETC/config.env`（`wizard_debug`）     | `fpkg/cmd/install_callback`                                           |

`state.db` 在日志里叫「数据库」，`data.db` 不会出现在启动日志里 —— 两者的职责与表结构见 [database.md](database.md)。

## 6. 三份最小可用配置

### 6.1 只做 scrobble（Last.fm）

```yaml
users:
  admin:
    lastfm:
      enabled: true
      api_key: "你的 api_key"
      api_secret: "你的 api_secret"
      session_key: ""   # 留空：启动后按日志里的授权链接点同意，自动写回
      username: ""      # 留空：授权成功后自动写回

playlist:
  enabled: false
```

`playlist.enabled` 关掉可省掉一切歌单相关的飞牛 API 调用；只做 scrobble 不需要 `library` 段。

### 6.2 只做 scrobble（ListenBrainz）

```yaml
users:
  admin:
    listenbrainz:
      enabled: true
      token: "你的 token"
      username: ""   # 留空：启动时自动 validate-token 并写回

playlist:
  enabled: false
```

ListenBrainz 推送只认 `token`，`username` 只在歌单同步时用；不写它也能正常 scrobble。

### 6.3 开启歌单同步

```yaml
users:
  admin:
    lastfm:
      enabled: true
      api_key: "你的 api_key"
      api_secret: "你的 api_secret"
      session_key: "你的 session_key"
      username: "你的_lastfm_用户名"
      playlist:
        top_tracks:
          enabled: true
          name: "LF {period} 最常听"
          period: 7day
          limit: 50
    listenbrainz:
      enabled: true
      token: "你的 token"
      username: "你的_lb_用户名"
      playlist:
        daily_jams:
          enabled: true
          name: "LB 每日推荐"
          limit: 50
        top_recordings:
          enabled: true
          name: "LB 最常听"
          limit: 50
          range: month

playlist:
  enabled: true
  sync_interval: 30m

library:
  directories:
    - /vol1/1000/音乐
  scan_interval: 6h
  mbid_online_lookup: false
```

`library.directories` 只影响匹配命中率：不配也能同步，只是退化为「艺人名+曲名 → 仅曲名」两级匹配。

## 7. 常见配置错误

| 错误                                                                         | 现象                                                                                                          | 怎么确认                                                                   |
|------------------------------------------------------------------------------|---------------------------------------------------------------------------------------------------------------|----------------------------------------------------------------------------|
| 键名拼错（`scrobble_threshold` → `scrobbleThreshold`、`api-key` 写成连字符） | 静默无效，程序用默认值跑。没有报错、没有 WARN                                                                 | 改完看日志有没有 `配置热更新` + `用户配置` 两行；阈值类可临时设 `30s` 验证 |
| `users.<name>` 与飞牛 `/user/me` 用户名不一致                                | scrobble 一条都不推，日志里 `识别到用户 用户名=xxx` 的 xxx 与配置 key 不同；歌单同步也不触发                  | 对日志里的用户名逐字符核对（大小写、空格、中文）                           |
| `lastfm.enabled=true` 但 `api_key`/`api_secret`/`session_key` 任一为空       | 启动 WARN `用户 Last.fm 已启用但配置不完整，已跳过`，`已设api_key=false` 这类字段直接指出缺哪项               | 看该 WARN 的字段                                                           |
| `listenbrainz.enabled=true` 但 `token` 为空                                  | 与上一条同理，不注册 provider，静默不推送                                                                     | 看启动日志的 `用户配置` 行里 `ListenBrainz` 是否为 `true`                  |
| `limit` 超上限（Last.fm `recent_tracks` > 200，其余 > 1000）                 | 不报错，被静默钳到上限                                                                                        | 日志里 `数量限制` 字段与实际请求量对比                                     |
| `period` 非法（如 `7days`、`1y`）                                            | 该用户 Last.fm `top_tracks` 同步失败，错误进日志与 `sync_log.message`；其他歌单源不受影响                     | Web UI 同步列表里看 `status=failed` 与 message                             |
| `range` 非法（如 `last_month`）                                              | `top_recordings` 同步失败，ListenBrainz 接口报错                                                              | 同上                                                                       |
| `playback.scrobble_threshold` 非法                                           | WARN `scrobble 阈值解析失败，沿用原阈值`，沿用上一次生效值（首次启动时是 `auto`）                             | 看 WARN                                                                    |
| `library.directories` 用相对路径                                             | Web UI 保存时被静默丢弃（只收绝对路径）；手写进配置文件则扫描时按当前工作目录解析为绝对路径，很可能扫不到东西 | `GET /api/library` 看实际存下来的 `directories`                            |
| `logging.max_size: 0` 与 `max_backups: 0` 同时使用                           | 日志永不切割，单文件无限增长                                                                                  | 看日志目录文件大小                                                         |
| 改 `server.socket_mode`（任何安装方式）                                      | 运行期不生效，需重启                                                                                          | 重启后 `fnmusic-sync sockets` 看权限                                       |

## 8. 与 README / `config.example.yaml` 的差异汇总

| 项                                                         | README / 示例文件                             | 实际实现                                                    |
|------------------------------------------------------------|-----------------------------------------------|-------------------------------------------------------------|
| `logging.enabled` 默认值                                   | 示例写 `false`，README 未列                   | `setDefaults` 为 `true`                                     |
| `library.mbid_online_lookup`                               | 两处都未出现                                  | 真实存在，默认 `false`，Web UI 可读写                       |
| `library` 段                                               | README 配置示例未出现                         | 真实存在 `directories`/`scan_interval`/`mbid_online_lookup` |
| `lastfm.playlist.weekly_charts`                            | README 的 YAML 示例缺该项，只在表格里提过一句 | 代码与 `config.example.yaml` 都有该歌单源                   |
| `listenbrainz.playlist.top_recordings` / `recently_played` | README YAML 示例缺这两项                      | 代码与 `config.example.yaml` 都有                           |
| 自动生成的默认配置内容                                     | 未说明                                        | 只含默认值键，不含 `users` / `socket_mode` / `directories`  |
