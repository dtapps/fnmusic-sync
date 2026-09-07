# fnmusic-sync

飞牛 OS（fnOS）飞牛音乐的 **Last.fm / ListenBrainz 自动 scrobble 透明代理**。

以 unix socket 接管飞牛音乐（trim-music）的通信链路，解析播放事件并按用户推送 scrobble；官方服务与客户端完全无感，代理退出时自动还原 socket。

## 特性

- **透明接管**：启动时把官方 socket 改名到 upstream 路径、代理占用原路径，任何退出路径都会还原，不留 502。
- **多用户隔离**：每个飞牛用户用各自的 Last.fm / ListenBrainz 凭证，A 的听歌记录不会推到 B 的账号。
- **Last.fm 授权辅助**：缺 `session_key` 时打印授权链接，浏览器点同意后自动写回配置文件并热加载，无需手填。
- **配置热加载**：改 `config.yaml` 立即生效（用户、凭证、阈值、日志级别），不用重启。
- **日志双写**：同时输出到控制台(stderr)与日志文件 `/var/log/fnmusic-sync/fnmusic-sync.log`，自动切割、gzip 压缩、过期清理。
- **服务自管理**：内置 `service install/uninstall/start/stop/restart/status`（基于 `kardianos/service`），不用手搓 unit 文件。
- **自升级**：`fnmusic-sync self-upgrade`，一键拉最新版本。
- **ListenBrainz 推荐歌单同步**：自动同步 ListenBrainz 的 daily_jams、weekly_jams、weekly_exploration 推荐歌单到飞牛音乐。

## 工作原理

```text
nginx(www-data) ──▶ /var/run/trim_music.socket          ← 本代理
                          │
                          └──▶ /var/run/trim_music_upstream.socket   ← 官方 trim-music
```

socket 绑定在 inode 上，用 `os.Rename`（mv）改名后官方后端仍能在新路径继续 accept；代理在原路径监听并原样转发（保留原始 `Host`，请求/响应头与流式响应都透传），同时旁路解析播放事件做 scrobble。

## 默认路径

| 用途 | 路径 |
| --- | --- |
| 二进制 | `/usr/local/bin/fnmusic-sync`（deb/rpm/apk 包内为 `/usr/bin/fnmusic-sync`） |
| 配置文件 | `/etc/fnmusic-sync/config.yaml` |
| 状态文件（程序维护） | `/var/lib/fnmusic-sync/state.yaml` |
| 日志文件 | `/var/log/fnmusic-sync/fnmusic-sync.log` |
| 代理监听 socket | `/var/run/trim_music.socket` |
| 官方 upstream socket | `/var/run/trim_music_upstream.socket` |

## 安装

### 方式一：安装脚本（推荐）

> ⚠️ 安装脚本**必须以 root 运行**（要写 `/usr/local/bin`、`/etc`、`/var` 与 systemd 目录），非 root 会直接报错退出，不会自动 sudo。

```bash
curl -fsSL https://cnb.cool/dtapp/fnmusic-sync/-/git/raw/main/scripts/install.sh | sudo sh
```

或下载后再执行：

```bash
curl -fsSLO https://cnb.cool/dtapp/fnmusic-sync/-/git/raw/main/scripts/install.sh
sudo sh install.sh
```

脚本只做四件事：检测 root → 建目录（`/etc/fnmusic-sync`、`/var/log/fnmusic-sync`、`/var/lib/fnmusic-sync`）→ 下载 Release 里的二进制 `fnmusic-sync-<os>-<arch>.tar.gz` → 安装二进制与服务（调用 `fnmusic-sync service install`）。**不 clone 仓库、不下载仓库里的任何文件**；配置文件由程序首次运行时自己生成。

常用变体：

```bash
# 已安装二进制时，直接用它升级：
sudo fnmusic-sync self-upgrade

# 本地有脚本时，也可用（支持更多选项）：
sudo ./install.sh upgrade                      # 升级，保留配置
sudo ./install.sh upgrade --version v0.0.1     # 升级到指定版本
sudo ./install.sh --no-service                 # 不装服务
sudo ./install.sh --dir /opt/bin               # 改安装目录
sudo ./install.sh --file ./fnmusic-sync-linux-amd64.tar.gz   # 用本地包
```

无 systemd 的环境（如直接在 fnOS 上手动跑）脚本会跳过服务安装，直接前台运行即可。

配置文件在**首次启动**时由程序自动生成（`/etc/fnmusic-sync/config.yaml`，含 `users` 等各段），启动后编辑它再重启服务即可；升级时脚本绝不覆盖它。

### 方式二：手动下载 Release

```bash
curl -fsSL -o fnmusic-sync.tar.gz \
  https://cnb.cool/dtapp/fnmusic-sync/-/releases/download/v0.0.1/fnmusic-sync-linux-amd64.tar.gz
tar -xzf fnmusic-sync.tar.gz
sudo install -m 0755 fnmusic-sync-linux-amd64 /usr/local/bin/fnmusic-sync
sudo mkdir -p /etc/fnmusic-sync /var/log/fnmusic-sync /var/lib/fnmusic-sync
```

首次运行会自动生成默认配置文件（改完再重启即可）。

### 方式三：deb / rpm / apk

从 Release 附件下载对应架构的包安装（安装后脚本会创建 `/etc/fnmusic-sync`）：

```bash
sudo dpkg -i fnmusic-sync_0.0.1_amd64.deb     # Debian / Ubuntu / fnOS
sudo rpm -i fnmusic-sync-0.0.1.x86_64.rpm     # RHEL / CentOS / openSUSE
sudo apk add fnmusic-sync_0.0.1_x86_64.apk    # Alpine
```

### 方式四：源码构建

```bash
make build-linux-amd64      # 产物 bin/fnmusic-sync-linux-amd64
make build-linux-all        # 6 个 Linux 架构
make package-linux-all      # deb / rpm / apk
```

构建/发布矩阵：**amd64 / arm64 / mips / mipsle / arm / 386**（CI 打 Release 时逐个交叉编译并上传附件）。

## 升级

| 场景 | 命令 |
| --- | --- |
| 已安装二进制 | `sudo fnmusic-sync self-upgrade`（从仓库拉最新版原子替换自身） |
| 本地有脚本 | `sudo ./install.sh upgrade`（或加 `--version vX.Y.Z`，支持更多选项） |
| 包管理器安装 | 下载新包覆盖安装，配置不会被覆盖 |

升级**不会**覆盖 `/etc/fnmusic-sync/config.yaml`（里面是你的 token）。

## 配置

配置文件默认 `/etc/fnmusic-sync/config.yaml`，可用 `--config` 指定。修改后**自动热加载**，无需重启。

```yaml
server:
  listen_socket: /var/run/trim_music.socket   # 代理监听的 socket（接管官方路径）
  upstream_socket: /var/run/trim_music_upstream.socket
  socket_mode: 0          # 0 = 沿用官方 socket 权限（至少 0666，nginx 是 www-data）
  upstream_wait: 30s      # 启动时等待官方后端就绪的超时

users:
  admin:                  # key 必须与飞牛 /user/me 返回的用户名一致
    lastfm:
      enabled: false
      api_key: ""
      api_secret: ""
      session_key: ""     # 留空时启动会打印授权链接，点同意后自动写回并生效
    listenbrainz:
      enabled: false
      token: ""           # ListenBrainz 用户 Token
      username: ""        # 歌单同步时必填（ListenBrainz 用户名）
      # 歌单同步配置（需要 username）
      # 开启前请确保：1. 关注 troi-bot（daily-jams 必须） 2. 音乐文件有 MBID 标签
      playlist:
        daily_jams:
          enabled: false
          name: "每日推荐"       # 飞牛音乐中的歌单名称
        weekly_jams:
          enabled: false
          name: "每周推荐"
        weekly_exploration:
          enabled: false
          name: "每周探索"

playback:
  scrobble_threshold: auto   # auto / 30s / 50% / off

playlist:
  enabled: true          # 歌单同步总开关
  sync_interval: 30m     # 同步间隔

logging:
  level: info                # info / debug
  max_size: 3                # 单文件超过 3MB 自动切割（0 = 不按大小切割）
  max_backups: 5             # 只保留最近 5 份历史日志（0 = 不限份数）
  max_age: 7                 # 历史日志保留 7 天（0 = 不按时间过期）
  compress: true             # 历史日志自动 gzip 压缩（.gz）
```

| 段 | 说明 |
| --- | --- |
| `server` | socket 路径与权限；`socket_mode: 0` 表示继承官方 socket 权限，一般不用改 |
| `users` | 按飞牛用户名隔离的推送凭证，可配多个用户 |
| `playback.scrobble_threshold` | `auto`=听满 min(时长/2, 4min)（Last.fm 规则）；`30s`/`2m`=固定时长；`50%`=按比例；`off`=收到播放即推（不建议日常用） |
| `playlist` | 歌单同步总开关与间隔 |
| `users.<name>.listenbrainz.playlist` | 每个用户的 ListenBrainz 推荐歌单同步配置 |
| `users.<name>.listenbrainz.playlist.daily_jams` | 每日推荐歌单，需在 ListenBrainz 关注 [troi-bot](https://listenbrainz.org/user/troi-bot/) |
| `users.<name>.listenbrainz.playlist.weekly_jams` | 每周推荐歌单 |
| `users.<name>.listenbrainz.playlist.weekly_exploration` | 每周探索歌单（发现新音乐） |
| `logging` | 日志级别与轮转策略（切割大小、保留份数、过期天数、是否压缩）。日志路径固定为 `/var/log/fnmusic-sync/fnmusic-sync.log`，**目录与文件名不可配** |

### Last.fm 授权

1. `users.<名字>.lastfm` 填 `enabled: true` + `api_key` + `api_secret`，`session_key` 留空；
2. 启动程序，日志里会出现授权链接：
   `Last.fm 需要授权，请在浏览器打开以下链接并点击同意`；
3. 浏览器点「同意」后，程序自动把 `session_key` 写回配置文件并热加载生效（180 秒轮询，超时重启程序可重试）。

ListenBrainz 只需填 `token`。

### ListenBrainz 推荐歌单同步

支持将 ListenBrainz 的推荐歌单同步到飞牛音乐，按用户独立配置。

| 歌单类型 | 说明 | 特殊要求 |
| --- | --- | --- |
| `daily_jams` | 每日推荐 | 需在 ListenBrainz 上关注 [`troi-bot`](https://listenbrainz.org/user/troi-bot/) |
| `weekly_jams` | 每周推荐 | 无 |
| `weekly_exploration` | 每周探索（发现新音乐） | 无 |

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
  enabled: true           # 歌单同步总开关
  sync_interval: 30m

users:
  admin:
    listenbrainz:
      enabled: true
      token: "your-token"
      username: "your-listenbrainz-username"  # 必须填写
      playlist:
        daily_jams:
          enabled: true
          name: "每日推荐"           # 飞牛音乐中显示的歌单名称
        weekly_jams:
          enabled: true
          name: "每周推荐"
        weekly_exploration:
          enabled: true
          name: "每周探索"
```

> ⚠️ **注意**：歌单同步从 ListenBrainz 的 `createdfor` 接口取"分享给你的"推荐歌单（daily-jams / weekly-jams / weekly-exploration，取最新一期），
> 再按「录音 MBID → 艺人名+曲名 → 仅曲名」在飞牛音乐库中匹配，只**增量添加**缺失的曲目，重复同步不会把歌单撑大。
> 每轮同步会在日志里打印匹配数与未匹配示例，便于排查命中率。

## 运行

systemd（脚本安装后会自动装服务，也可手动执行）：

```bash
sudo fnmusic-sync service install      # 写 /etc/systemd/system/fnmusic-sync.service
sudo systemctl enable --now fnmusic-sync
sudo fnmusic-sync service status       # 或 systemctl status fnmusic-sync
journalctl -u fnmusic-sync -f
```

前台运行（无 systemd / 手动部署到 fnOS，如 `/home/admin/fnmusic-sync`）：

```bash
sudo fnmusic-sync \
  --config /etc/fnmusic-sync/config.yaml \
  --state /var/lib/fnmusic-sync/state.yaml

sudo nohup fnmusic-sync >/dev/null 2>&1 &     # 后台常驻
```

⚠️ 不要在应用中心重启飞牛音乐：trim-music 重启会 unlink 并重新绑定原路径，代理被旁路，需重启代理重新接管。

## 命令行

子命令：

| 子命令 | 说明 |
| --- | --- |
| `version` | 打印版本、Git 提交、构建时间、Go 版本、平台、配置/状态/日志路径 |
| `self-upgrade` | 升级自身二进制到仓库最新版本（写安装目录通常要 `sudo`） |
| `service install` | 安装系统服务（systemd，需 root） |
| `service uninstall` | 卸载系统服务（需 root） |
| `service start` / `stop` / `restart` | 启停服务 |
| `service status` | 查看运行状态 |

服务由 `github.com/kardianos/service` 管理（unit 里 `Restart=always`、`RestartSec=5`），不需要手写 unit 文件。

参数：

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `--config` | `/etc/fnmusic-sync/config.yaml` | 配置文件路径 |
| `--state` | `/var/lib/fnmusic-sync/state.yaml` | 用户状态文件（程序维护推送统计） |
| `--listen` | 配置文件 | 覆盖监听 socket |
| `--upstream` | 配置文件 | 覆盖官方 upstream socket |
| `--upstream-wait` | `30s` | 等待官方后端就绪超时 |
| `--check` | `false` | 只体检（socket 归属/权限/连通性），不接管 |
| `--debug` | `false` | 打印请求/响应详情（含脱敏后的鉴权头） |
| `--log-dir` | `/var/log/fnmusic-sync` | 日志目录（仅临时覆盖用），`off` 关闭文件日志 |

```bash
fnmusic-sync version
fnmusic-sync --check
fnmusic-sync --debug
```

## 日志

- 同时写**控制台(stderr)**与**日志文件**，格式一致（`log/slog` text）。
- 路径固定：**`/var/log/fnmusic-sync/fnmusic-sync.log`**（目录与文件名写在代码常量里，配置文件不提供 `dir`/`file` 选项）；仅调试时可用 `--log-dir` 临时改目录，`--log-dir off` 关闭文件日志。
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

| 现象 | 原因与处理 |
| --- | --- |
| 飞牛音乐 502 | socket 权限不足：nginx 以 `www-data` 运行，代理 socket 必须允许 other 访问（`socket_mode: 0` 继承官方权限，至少 0666） |
| 代理收不到任何请求 | 官方后端重启后重新绑定了原路径，重启代理重新接管 |
| scrobble 不推送 | 用户名 key 与飞牛 `/user/me` 返回不一致、凭证未填全、或进度未到阈值（先用 `30s` 验证） |
| Last.fm 401 INVALID TOKEN | token 失效，重新登录飞牛或重新走授权流程 |
| 日志里只有控制台没有文件 | 日志目录不可写，看启动时的 WARN，或换 `--log-dir` |

## 卸载

```bash
# 已安装二进制时，直接用它卸载服务：
sudo fnmusic-sync service uninstall

# 再删除二进制本身：
sudo rm /usr/local/bin/fnmusic-sync

# 可选：连配置、日志、状态一起删除：
sudo rm -rf /etc/fnmusic-sync /var/log/fnmusic-sync /var/lib/fnmusic-sync
```

> 没有 systemd 时（`service uninstall` 可能报错），直接 `kill` 进程再删二进制即可。

卸载前程序若在运行会先停止并还原官方 socket，不会留下 502。

## 构建与发布

```bash
make build-linux-amd64     # 单架构
make build-linux-all       # amd64/arm64/mips/mipsle/arm/386
make package-linux-all     # deb + rpm + apk（6 架构）
make clean-binaries        # 清理裸二进制，保留 tar.gz/安装包
```

版本号由 `git describe --tags` 决定，通过 `-ldflags -X main.Version=...` 注入，所以 `fnmusic-sync version` 能显示真实版本；本地 `go build` 不带 ldflags 时显示为 `dev`。

CI（`.cnb/workflows/build_go_project.yml`）在打 tag 时构建并发布 Release，支持 `pre_release` 开关，Release 正文用表格标注版本号、发布类型与是否为预发布。

## 目录结构

```text
cmd/fnmusic-sync/     主程序（main.go / upgrade.go / logging.go / service.go）
internal/config/      配置加载、热加载、默认配置生成
internal/playback/    播放事件解析、scrobble 判定、用户识别与统计
internal/playlist/    ListenBrainz 推荐歌单同步服务
internal/proxy/       socket 接管、HTTP 反向代理、健康检查
internal/scrobbler/   Last.fm / ListenBrainz 客户端
configs/              配置示例
scripts/              install.sh（安装/升级/卸载）、postinstall.sh、preremove.sh
```

## 许可

MIT
