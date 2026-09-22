# fnmusic-sync

飞牛 OS（fnOS）飞牛音乐的 **Last.fm / ListenBrainz 自动 scrobble 透明代理**。

以 unix socket 接管飞牛音乐（trim-music）的通信链路，解析播放事件并按用户推送 scrobble；官方服务与客户端完全无感，代理退出时自动还原 socket。

## 特性

- **透明接管**：启动时把官方 socket 改名到 upstream 路径、代理占用原路径，任何退出路径都会还原，不留 502。
- **多用户隔离**：每个飞牛用户用各自的 Last.fm / ListenBrainz 凭证，A 的听歌记录不会推到 B 的账号。
- **Last.fm 授权辅助**：缺 `session_key` 时打印授权链接，浏览器点同意后自动写回配置文件并热加载，无需手填。
- **配置热加载**：改 `config.yaml` 立即生效（用户、凭证、阈值、日志级别），不用重启。
- **日志双写**：同时输出到控制台(stderr)与日志文件 `/var/log/fnmusic-sync/fnmusic-sync.log`，自动切割、gzip 压缩、过期清理。
- **一键安装**：安装脚本自动下载系统安装包、注册 systemd 服务、设置开机自启并启动，无需手动操作。
- **自升级**：`fnmusic-sync self-upgrade` 自动检测系统包管理器（dpkg），下载对应安装包并安装，升级后自动重启服务。
- **ListenBrainz 推荐/统计歌单同步**：自动同步 ListenBrainz 的 daily_jams、weekly_jams、weekly_exploration、year_discoveries、year_missed 推荐歌单，以及基于收听统计的 top_recordings（最常听）、recently_played（最近在听）到飞牛音乐。
- **Last.fm 智能歌单同步**：基于 scrobble 数据自动生成 top_tracks（最常听）、loved_tracks（红心收藏）、recent_tracks（最近播放）、weekly_charts（本周榜单）、library（我的曲库）歌单。

## 工作原理

```text
nginx(www-data) ──▶ /var/run/trim_music.socket          ← 本代理
                          │
                          └──▶ /var/run/trim_music_upstream.socket   ← 官方 trim-music
```

socket 绑定在 inode 上，用 `os.Rename`（mv）改名后官方后端仍能在新路径继续 accept；代理在原路径监听并原样转发（保留原始 `Host`，请求/响应头与流式响应都透传），同时旁路解析播放事件做 scrobble。

## 默认路径

| 用途                 | 传统安装                                 | fpk 安装                                     |
| -------------------- | ---------------------------------------- | -------------------------------------------- |
| 二进制               | `/usr/bin/fnmusic-sync`                  | `/var/apps/fnmusic-sync/target/fnmusic-sync` |
| 配置文件             | `/etc/fnmusic-sync/config.yaml`          | `$TRIM_PKGETC/config.yaml`                   |
| 数据库（运营状态）   | `/var/lib/fnmusic-sync/state.db`         | `$TRIM_PKGVAR/state.db`                      |
| 数据库（MBID 数据）  | `/var/lib/fnmusic-sync/data.db`          | `$TRIM_PKGVAR/data.db`                       |
| 日志文件             | `/var/log/fnmusic-sync/fnmusic-sync.log` | `$TRIM_PKGVAR/logs/fnmusic-sync.log`         |
| 代理监听 socket      | `/var/run/trim_music.socket`             | 同左                                         |
| 官方 upstream socket | `/var/run/trim_music_upstream.socket`    | 同左                                         |

> fpk 模式下路径由飞牛 fnOS 环境变量（`TRIM_APPDEST`、`TRIM_PKGETC`、`TRIM_PKGVAR` 等）决定，程序启动时自动检测。

## 快速开始

### 安装（自动注册服务 + 开机自启）

```bash
curl -fsSL https://cnb.cool/dtapp/fnmusic-sync/-/git/raw/main/scripts/install.sh | sudo sh
```

脚本会自动完成：检测包管理器 → 下载 deb 安装包 → 安装 → 注册 systemd 服务 → 设置开机自启 → 启动服务。配置文件由程序首次启动时自动生成。

### 升级

```bash
# 方式一：已安装二进制，直接自升级（自动下载安装包并重启服务）
sudo fnmusic-sync self-upgrade

# 方式二：用安装脚本升级
curl -fsSL https://cnb.cool/dtapp/fnmusic-sync/-/git/raw/main/scripts/install.sh | sudo sh -s -- upgrade

# 指定版本
sudo sh install.sh upgrade --version vX.Y.Z
```

升级不会覆盖 `/etc/fnmusic-sync/config.yaml`，升级后服务自动重启加载新版本。

### 卸载

```bash
# 方式一：安装脚本卸载（自动停止并移除服务）
curl -fsSL https://cnb.cool/dtapp/fnmusic-sync/-/git/raw/main/scripts/install.sh | sudo sh -s -- uninstall

# 方式二：包管理器卸载（卸载前自动停止并移除服务）
sudo apt remove fnmusic-sync          # deb 系（Debian/Ubuntu）

# 连配置、日志、状态一起删除：
sudo sh install.sh uninstall --purge
# 或：sudo apt purge fnmusic-sync
```

### 其他安装方式

<details>
<summary>手动下载 deb</summary>

从 Release 附件下载对应架构的包安装（包内 postinstall 脚本自动创建目录、注册服务并启动）：

```bash
sudo dpkg -i fnmusic-sync_amd64.deb     # Debian / Ubuntu
```

</details>

<details>
<summary>源码构建</summary>

```bash
make build-linux-amd64      # 产物 bin/fnmusic-sync-linux-amd64
make build-linux-all        # 2 个 Linux 架构（带标签 + 不带标签）
make package-linux-all      # deb / fpk
```

构建/发布矩阵：**amd64 / arm64**（CI 打 Release 时交叉编译并上传附件）。

</details>

## 服务管理

安装后服务已自动注册并启动，日常使用 `systemctl` 管理即可：

```bash
systemctl status fnmusic-sync          # 查看状态
systemctl restart fnmusic-sync         # 重启
systemctl stop fnmusic-sync            # 停止
journalctl -u fnmusic-sync -f          # 查看实时日志
```

前台运行（无 systemd 或手动调试）：

```bash
sudo fnmusic-sync                       # 前台运行
sudo nohup fnmusic-sync >/dev/null 2>&1 &   # 后台常驻
```

> 配置、状态、日志路径由运行模式自动决定，无需手动指定参数。

⚠️ 不要在应用中心重启飞牛音乐：trim-music 重启会 unlink 并重新绑定原路径，代理被旁路，需重启代理重新接管。

## 命令行

```bash
fnmusic-sync version          # 查看版本、Git 提交、构建时间
fnmusic-sync --check          # 体检：socket 归属/权限/连通性，不接管
fnmusic-sync --debug          # 前台运行 + 请求/响应详细日志
fnmusic-sync self-upgrade     # 升级到仓库最新版本（需 sudo）
fnmusic-sync service status   # 查看服务运行状态（等价 systemctl status）
fnmusic-sync service restart  # 重启服务（等价 systemctl restart）
fnmusic-sync service stop     # 停止服务
sudo fnmusic-sync service debug    # 开启 debug 模式并重启服务
sudo fnmusic-sync service nodebug  # 关闭 debug 模式并重启服务
```

| 子命令                               | 说明                                                                  |
| ------------------------------------ | --------------------------------------------------------------------- |
| `version`                            | 打印版本、Git 提交、构建时间、Go 版本、平台、配置/状态/日志路径       |
| `self-upgrade`                       | 升级自身到仓库最新版本（自动检测包管理器，下载 deb 安装并重启服务）   |
| `service install`                    | 安装系统服务（systemd，需 root）——安装脚本已自动执行                  |
| `service uninstall`                  | 卸载系统服务（需 root）——卸载脚本已自动执行                           |
| `service start` / `stop` / `restart` | 启停服务                                                              |
| `service status`                     | 查看运行状态                                                          |
| `service debug`                      | 开启 debug 模式（修改 ExecStart 加 --debug，daemon-reload + restart） |
| `service nodebug`                    | 关闭 debug 模式（移除 --debug，daemon-reload + restart）              |

| 参数              | 默认值  | 说明                                  |
| ----------------- | ------- | ------------------------------------- |
| `--upstream-wait` | `30s`   | 等待官方后端就绪超时                  |
| `--check`         | `false` | 只体检，不接管                        |
| `--debug`         | `false` | 打印请求/响应详情（含脱敏后的鉴权头） |

## 配置

配置文件默认 `/etc/fnmusic-sync/config.yaml`，路径由运行模式自动决定（fpk 模式下使用飞牛 fnOS 环境变量 `TRIM_PKGETC`）。修改后**自动热加载**，无需重启。

```yaml
server:
  socket_mode: 0 # 0 = 沿用官方 socket 权限（至少 0666）
  upstream_wait: 30s # 启动时等待官方后端就绪的超时

users:
  admin: # key 必须与飞牛 /user/me 返回的用户名一致
    lastfm:
      enabled: false
      api_key: ""
      api_secret: ""
      session_key: "" # 留空时启动会打印授权链接，点同意后自动写回并生效
      username: "" # Last.fm 用户名（授权后自动写回）
      playlist: # Last.fm 智能歌单同步（基于 scrobble 数据）
        top_tracks:
          enabled: false
          name: "LF {period} 最常听"
          period: overall # 7day/1month/3month/6month/12month/overall
          limit: 50
        loved_tracks:
          enabled: false
          name: "LF 红心收藏"
          limit: 50
        recent_tracks:
          enabled: false
          name: "LF 最近播放"
          limit: 50
    listenbrainz:
      enabled: false
      token: "" # ListenBrainz 用户 Token
      username: "" # 歌单同步时必填（留空时自动获取并写回）
      playlist:
        daily_jams:
          enabled: false
          name: "LB 每日推荐"
          limit: 50 # 0=不限制
        weekly_jams:
          enabled: false
          name: "LB 每周推荐"
          limit: 50
        weekly_exploration:
          enabled: false
          name: "LB 每周探索"
          limit: 50
        year_discoveries:
          enabled: false
          name: "LB {year} 年度发现" # {year} 自动替换
          limit: 50
        year_missed:
          enabled: false
          name: "LB {year} 年度遗珠"
          limit: 50

playback:
  scrobble_threshold: auto # auto / 30s / 2m / 50% / off

playlist:
  enabled: true # 歌单同步总开关
  sync_interval: 30m # 同步间隔

logging:
  level: info # info / debug / warn / error
  max_size: 3 # 单文件超过 3MB 自动切割（0 = 不按大小切割）
  max_backups: 5 # 只保留最近 5 份历史日志（0 = 不限份数）
  max_age: 7 # 历史日志保留 7 天（0 = 不按时间过期）
  compress: true # 历史日志自动 gzip 压缩（.gz）
```

| 段                                                      | 说明                                                                                                                                          |
| ------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------- |
| `server`                                                | socket 路径固定不可配；`socket_mode` 控制 socket 权限，`upstream_wait` 控制等待官方后端超时                                                   |
| `users`                                                 | 按飞牛用户名隔离的推送凭证，可配多个用户                                                                                                      |
| `playback.scrobble_threshold`                           | `auto`=听满 min(时长/2, 4min)（Last.fm 规则）；`30s`/`2m`=固定时长；`50%`=按比例；`off`=收到播放即推（不建议日常用）                          |
| `playlist`                                              | 歌单同步总开关与间隔                                                                                                                          |
| `users.<name>.lastfm.playlist`                          | Last.fm 智能歌单同步（top_tracks / loved_tracks / recent_tracks / weekly_charts / library）                                                   |
| `users.<name>.listenbrainz.playlist`                    | ListenBrainz 推荐/统计歌单同步                                                                                                                |
| `users.<name>.listenbrainz.playlist.daily_jams`         | 每日推荐歌单，需在 ListenBrainz 关注 [troi-bot](https://listenbrainz.org/user/troi-bot/)                                                      |
| `users.<name>.listenbrainz.playlist.weekly_jams`        | 每周推荐歌单                                                                                                                                  |
| `users.<name>.listenbrainz.playlist.weekly_exploration` | 每周探索歌单（发现新音乐）                                                                                                                    |
| `users.<name>.listenbrainz.playlist.year_discoveries`   | 年度发现歌单（{year} 自动替换）                                                                                                               |
| `users.<name>.listenbrainz.playlist.year_missed`        | 年度遗珠歌单（{year} 自动替换）                                                                                                               |
| `users.<name>.listenbrainz.playlist.top_recordings`     | 最常听录音歌单（基于 ListenBrainz 收听统计），可选 `range`：week/month/quarter/half_year/year/all_time/this_year，默认 all_time               |
| `users.<name>.listenbrainz.playlist.recently_played`    | 最近在听歌单（基于 ListenBrainz listens 收听记录）                                                                                            |
| `users.<name>.lastfm.playlist.weekly_charts`            | 本周曲目榜单（Last.fm weeklytrackchart）                                                                                                      |
| `users.<name>.lastfm.playlist.library`                  | 我的曲库歌单（Last.fm library.getTracks，基于 scrobble 历史）                                                                                 |
| `logging`                                               | 日志级别与轮转策略（切割大小、保留份数、过期天数、是否压缩）。日志路径固定为 `/var/log/fnmusic-sync/fnmusic-sync.log`，**目录与文件名不可配** |

### Last.fm 授权

1. `users.<名字>.lastfm` 填 `enabled: true` + `api_key` + `api_secret`，`session_key` 留空；
2. 启动程序，日志里会出现授权链接：
   `Last.fm 需要授权，请在浏览器打开以下链接并点击同意`；
3. 浏览器点「同意」后，程序自动把 `session_key` 和 `username` 写回配置文件并热加载生效（180 秒轮询，超时重启程序可重试）。

ListenBrainz 只需填 `token`，`username` 留空时自动获取并写回。

### ListenBrainz 推荐歌单同步

支持将 ListenBrainz 的推荐歌单同步到飞牛音乐，按用户独立配置。

| 歌单类型             | 说明                                      | 特殊要求                                                                       |
| -------------------- | ----------------------------------------- | ------------------------------------------------------------------------------ |
| `daily_jams`         | 每日推荐                                  | 需在 ListenBrainz 上关注 [`troi-bot`](https://listenbrainz.org/user/troi-bot/) |
| `weekly_jams`        | 每周推荐                                  | 无                                                                             |
| `weekly_exploration` | 每周探索（发现新音乐）                    | 无                                                                             |
| `year_discoveries`   | 年度发现歌单                              | 无（ListenBrainz 每年自动生成）                                                |
| `year_missed`        | 年度遗珠歌单                              | 无（ListenBrainz 每年自动生成）                                                |
| `top_recordings`     | 最常听录音（基于收听统计）                | 可选 `range`（week/month/quarter/half_year/year/all_time/this_year）           |
| `recently_played`    | 最近在听（ListenBrainz listens 收听记录） | 无                                                                             |

**开启条件：**

1. **ListenBrainz 用户名**：必须填写 `users.<名字>.listenbrainz.username`
2. **关注 troi-bot**（daily-jams 必须）：在 ListenBrainz 上关注 [`troi-bot`](https://listenbrainz.org/user/troi-bot/) 机器人，它会基于你的听歌历史生成推荐
3. **曲库里有对应歌曲**：曲目按「录音 MBID → 艺人名+曲名 → 仅曲名」的顺序匹配，
   推荐曲名里的括号附注（如 `不将就 (Can't Bear It)`）会自动截断后再次比对，
   因此**不强要求音乐文件带 MBID 标签**
4. **启用歌单同步**：在 `playlist.enabled: true` 且用户配置中开启对应的歌单类型

**配置示例：**

```yaml
playlist:
  enabled: true # 歌单同步总开关
  sync_interval: 30m

users:
  admin:
    listenbrainz:
      enabled: true
      token: "your-token"
      username: "your-listenbrainz-username" # 留空时自动获取并写回
      playlist:
        daily_jams:
          enabled: true
          name: "LB 每日推荐"
          limit: 50
        weekly_jams:
          enabled: true
          name: "LB 每周推荐"
          limit: 50
        weekly_exploration:
          enabled: true
          name: "LB 每周探索"
          limit: 50
        year_discoveries:
          enabled: true
          name: "LB {year} 年度发现"
          limit: 50
        year_missed:
          enabled: true
          name: "LB {year} 年度遗珠"
          limit: 50
```

> ⚠️ **注意**：歌单同步从 ListenBrainz 的 `createdfor` 接口取"分享给你的"推荐歌单（daily-jams / weekly-jams / weekly-exploration，取最新一期），
> 再按「录音 MBID → 艺人名+曲名 → 仅曲名」在飞牛音乐库中匹配，只**增量添加**缺失的曲目，重复同步不会把歌单撑大。
> 每轮同步会在日志里打印匹配数与未匹配示例，便于排查命中率。

## 日志

- 同时写**控制台(stderr)**与**日志文件**，格式一致（`log/slog` text）。
- 路径固定：**`/var/log/fnmusic-sync/fnmusic-sync.log`**（目录与文件名写在代码常量里，配置文件不提供 `dir`/`file` 选项）。
- **自动轮转**（lumberjack）：单文件超过 `logging.max_size` MB 就切割，历史文件自动 gzip 压缩（`.gz`），超过 `logging.max_backups` 份或 `logging.max_age` 天的自动删除。
- 目录不可写时打印 WARN 并降级为仅控制台，不会导致启动失败。
- 级别优先级：`--debug` > `logging.level`；热加载可运行时调整。

## 检查与排障

```bash
fnmusic-sync --check
curl -s --unix-socket /var/run/trim_music.socket http://localhost/_ext/healthz
# {"ok":true,"upstream":"ok",...}
ls -l /var/run/trim_music*.socket      # 两个都应为 srw-rw-rw-
tail -f /var/log/fnmusic-sync/fnmusic-sync.log
```

| 现象                      | 原因与处理                                                                                                             |
| ------------------------- | ---------------------------------------------------------------------------------------------------------------------- |
| 飞牛音乐 502              | socket 权限不足：nginx 以 `www-data` 运行，代理 socket 必须允许 other 访问（`socket_mode: 0` 继承官方权限，至少 0666） |
| 代理收不到任何请求        | 官方后端重启后重新绑定了原路径，重启代理重新接管                                                                       |
| scrobble 不推送           | 用户名 key 与飞牛 `/user/me` 返回不一致、凭证未填全、或进度未到阈值（先用 `30s` 验证）                                 |
| Last.fm 401 INVALID TOKEN | token 失效，重新登录飞牛或重新走授权流程                                                                               |
| 日志里只有控制台没有文件  | 日志目录不可写，看启动时的 WARN                                                                                        |

## 构建与发布

```bash
make build-linux-amd64     # 单架构
make build-linux-all       # amd64/arm64（带标签 + 不带标签）
make package-linux-all     # deb + fpk
make clean-binaries        # 清理裸二进制，保留 tar.gz/安装包
make clean-archives        # 清理裸二进制 + tar.gz，保留安装包
```

版本号由 `git describe --tags` 决定，通过 `-ldflags -X` 注入到 `internal/buildinfo` 包，所以 `fnmusic-sync version` 能显示真实版本；本地 `go build` 不带 ldflags 时显示为 `dev`。

CI（`.cnb/workflows/release.yml`）在打 tag 时构建并发布 Release，支持 `pre_release` 开关，Release 正文用表格标注版本号、发布类型与是否为预发布。

## 目录结构

```text
cmd/fnmusic-sync/     主程序（main.go / upgrade.go / logging.go / service.go / runtime.go / auth.go）
internal/buildinfo/   构建信息（版本号等，由 Makefile -ldflags 注入）
internal/config/      配置加载、热加载、默认配置生成
internal/feiniu/      飞牛音乐 API 客户端（曲目索引、歌单管理）
internal/playback/    播放事件解析、scrobble 判定、用户识别与统计
internal/playlist/    ListenBrainz / Last.fm 歌单同步服务
internal/proxy/       socket 接管、HTTP 反向代理、健康检查
internal/reqlog/      请求日志记录器
internal/scrobbler/   Last.fm / ListenBrainz 客户端
internal/strutil/     字符串工具
internal/webui/       Web 配置界面（fpk 模式，统一网关）
configs/              配置示例
scripts/              install.sh（安装/升级/卸载）+ postinstall.sh + preremove.sh
fpkg/                 飞牛 fnOS 应用包（fpk）源码
```

## 规划中（考虑开发的歌单源）

以下功能仍在评估，尚未实现：

- **B 类：返回的是专辑/艺人，需额外"展开为曲目"一步（实现成本高、收益一般）**
  - ListenBrainz `top_releases`（最常听专辑）→ 展开为专辑下曲目
  - ListenBrainz `top_artists`（最常听艺人）→ 取每位艺人代表曲
  - Last.fm `top_albums` / `top_artists`（最常听专辑/艺人）→ 展开为曲目
- **A 类：实现成本偏高或价值一般，暂搁置**
  - ListenBrainz `recommendations`（相似用户推荐）—— 仅返回 recording MBID，需额外解析曲名，体验依赖 MBID 曲库
  - Last.fm `friends` 最近在听 —— 需逐个好友拉取，N+1 调用
  - Last.fm `neighbours` 热歌 —— 需逐个邻居拉取，N+1 调用

## 许可

本项目基于 [MIT License](LICENSE) 开源。

Copyright (c) 2026 dtapp
