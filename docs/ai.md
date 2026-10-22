可以。下面这份你直接丢给其他 AI，让它**基于这个目标重新实现，不要再让我补代码**。

# fnOS Music → Go Proxy → Last.fm / ListenBrainz 实现需求文档

## 1. 项目目标

开发一个 **Go 实现的 fnOS Music 扩展代理服务**。

目标架构：

```text
┌─────────────────────┐
│      fnOS Music     │
│       前端 / nginx   │
└──────────┬──────────┘
           │
           │ Unix Socket
           ▼
/var/run/trim_music.socket
           │
           ▼
┌─────────────────────┐
│    Go Proxy 服务     │
│                     │
│  1. HTTP 请求透传    │
│  2. 播放状态识别     │
│  3. Last.fm          │
│  4. ListenBrainz     │
│  5. Playlist 同步    │
└──────────┬──────────┘
           │
           │ Unix Socket
           ▼
/var/run/trim_music_upstream.socket
           │
           ▼
┌─────────────────────┐
│ fnOS 官方 trim-music │
└─────────────────────┘
```

**核心原则：**

> Go Proxy 必须对 fnOS Music 透明。没有被扩展逻辑处理的请求，必须完整、无损地转发给官方 `trim-music`。

参考项目：

`javycoder/fnos_music_ext`

它的核心机制必须参考，但**不要照搬 Python 实现**，最终实现使用 Go。

---

# 2. fnOS Socket 机制

官方 socket：

```text
/var/run/trim_music.socket
```

Go Proxy 接管后：

```text
/var/run/trim_music.socket
```

由 Go Proxy 监听。

官方 `trim-music` 原来的监听 socket 改名为：

```text
/var/run/trim_music_upstream.socket
```

最终：

```text
nginx
  ↓
/var/run/trim_music.socket
  ↓
Go Proxy
  ↓
/var/run/trim_music_upstream.socket
  ↓
trim-music
```

## 非常重要

不能简单理解成：

```bash
mv socket
go程序启动
```

参考项目实际上还有完整的：

- socket 接管
- upstream 检查
- proxy 启动
- socket 权限处理
- health check
- systemd 自动重启
- 失败恢复

因此 Go 版本也必须实现可靠的启动流程。

---

# 3. 当前已经验证的 fnOS 环境

实际设备：

```text
fnOS
```

官方 socket：

```text
/var/run/trim_music.socket
```

执行：

```bash
mv /var/run/trim_music.socket /var/run/trim_music_upstream.socket
```

后，Go Proxy 可以成功创建：

```text
/var/run/trim_music.socket
```

Go 程序日志：

```text
time=2026-09-06T10:51:58.821+08:00 level=INFO msg="proxy listening" socket=/var/run/trim_music.socket upstream=/var/run/trim_music_upstream.socket
```

但是实际 fnOS Music 出现：

```text
502
```

恢复：

```bash
mv /var/run/trim_music_upstream.socket /var/run/trim_music.socket
```

后：

```text
fnOS Music 正常
```

因此：

> 官方 `trim-music` 本身没有问题，问题在当前 Go Proxy 的 HTTP / Unix Socket 代理实现或 socket 接管流程。

**不要再假设 Go Proxy 当前实现是正确的。必须重新检查。**

---

# 4. Go Proxy 必须实现的功能

## 4.1 Unix Socket Listener

监听：

```text
/var/run/trim_music.socket
```

连接 upstream：

```text
/var/run/trim_music_upstream.socket
```

必须支持：

- HTTP/1.1
- keep-alive
- GET
- POST
- PUT
- DELETE
- PATCH
- OPTIONS
- HEAD
- 任意 fnOS Music API

不能只实现某几个 API。

---

# 5. HTTP 透明代理

所有未拦截请求：

```text
Client → Go Proxy → upstream
```

必须做到：

```text
Method       保持
URL          保持
Query        保持
Headers      保持
Body         保持
Status Code  保持
Response     保持
```

特别注意：

### Request Body

不能因为读取 body 进行分析后导致 upstream 收不到 body。

正确思路：

```go
body, _ := io.ReadAll(req.Body)

req.Body = io.NopCloser(bytes.NewReader(body))
```

然后再分析。

不要：

```go
strings.NewReader(string(body))
```

这种无意义的 bytes → string → bytes 转换。

---

# 6. HTTP Header

正确处理：

```text
Host
Content-Type
Content-Length
Authorization
Cookie
User-Agent
Accept
Accept-Encoding
Range
If-Modified-Since
```

同时正确处理 HTTP hop-by-hop headers：

```text
Connection
Keep-Alive
Proxy-Authenticate
Proxy-Authorization
TE
Trailer
Transfer-Encoding
Upgrade
```

不能粗暴复制所有 header 导致 nginx / upstream 行为异常。

---

# 7. 必须实现 health check

Go Proxy 启动以后必须检查 upstream：

```text
/var/run/trim_music_upstream.socket
```

是否能够正常连接。

同时 Go Proxy 自己应该提供：

```text
/_ext/healthz
```

例如：

```json
{
  "status": "ok",
  "upstream": true
}
```

参考项目就是使用 health check 判断代理是否正常。

测试方式：

```bash
curl \
  --unix-socket /var/run/trim_music.socket \
  http://localhost/_ext/healthz
```

---

# 8. Socket 权限

参考项目启动后会设置：

```bash
chmod 666 /var/run/trim_music.socket
```

Go 版本必须保证 nginx 可以访问 socket。

不能简单使用：

```text
0660
```

然后假设 nginx 用户一定有权限。

应该：

```text
创建 socket
↓
chmod 0666
↓
确认 nginx 可以连接
```

---

# 9. 启动失败必须恢复

启动过程：

```text
检查官方 socket
       ↓
确认 trim-music
       ↓
接管 socket
       ↓
启动 Go Proxy
       ↓
创建 /var/run/trim_music.socket
       ↓
检查权限
       ↓
检查 upstream
       ↓
health check
       ↓
成功
```

任何一步失败：

```text
恢复：
/var/run/trim_music_upstream.socket
        ↓
/var/run/trim_music.socket
```

不能让 fnOS Music 长时间进入 502。

---

# 10. 不允许猜测播放事件

当前已知参考项目存在：

```text
/music/api/v1/track/stream
```

但：

> 请求音频 stream ≠ 用户真正播放了这么久。

因此不能简单：

```text
track/stream 请求
→ 立即 Last.fm scrobble
```

这是错误的。

需要通过抓取 fnOS Music 实际 API 请求，找出：

```text
播放
暂停
继续
停止
下一首
上一首
seek
播放进度
```

相关 API。

Go Proxy 在开发阶段必须支持：

```text
DEBUG=true
```

然后记录：

```text
HTTP method
URI
query
headers
body
response status
```

例如：

```text
FNOS API
POST /music/api/...
body=...
```

通过实际 fnOS Music 播放行为确定 API，而不是猜。

---

# 11. Track 数据模型

统一内部模型：

```go
type Track struct {
    GUID          string
    Title         string
    Artist        string
    Album         string
    AlbumArtist   string

    Duration      time.Duration
    TrackNumber   int

    MBID          string
    RecordingMBID string
    ReleaseMBID   string
    ArtistMBID    string

    Path string
}
```

优先从 fnOS Music API 的 metadata 中获取。

---

# 12. Scrobbler 抽象

不要在 Proxy 里直接写 Last.fm / ListenBrainz 逻辑。

定义：

```go
type Scrobbler interface {
    Name() string

    UpdateNowPlaying(
        ctx context.Context,
        track model.Track,
    ) error

    Scrobble(
        ctx context.Context,
        track model.Track,
        startedAt int64,
    ) error
}
```

以后可以增加：

```text
Last.fm
ListenBrainz
其他服务
```

而不修改播放逻辑。

---

# 13. Last.fm

使用官方 API：

```text
https://ws.audioscrobbler.com/2.0/
```

需要：

```text
API Key
API Secret
Session Key
```

配置：

```text
LASTFM_API_KEY
LASTFM_API_SECRET
LASTFM_SESSION_KEY
```

实现：

```text
track.updateNowPlaying
track.scrobble
```

### updateNowPlaying

播放开始时调用：

```text
track.updateNowPlaying
```

参数尽可能提供：

```text
artist
track
album
albumArtist
trackNumber
duration
mbid
```

### scrobble

达到 scrobble 条件后调用：

```text
track.scrobble
```

需要：

```text
artist
track
timestamp
album
albumArtist
trackNumber
duration
mbid
```

Last.fm 请求必须正确生成 API signature。

签名逻辑：

```text
所有参数按 key ASCII 排序
↓
key + value 拼接
↓
末尾追加 API Secret
↓
MD5
```

---

# 14. ListenBrainz

API：

```text
https://api.listenbrainz.org
```

认证：

```http
Authorization: Token <TOKEN>
```

提交：

```text
POST /1/submit-listens
```

支持：

```text
playing_now
single
```

### playing_now

播放开始：

```json
{
  "listen_type": "playing_now"
}
```

### single

达到 scrobble 条件：

```json
{
  "listen_type": "single"
}
```

Track metadata 至少包含：

```text
artist_name
track_name
release_name
```

尽可能加入：

```text
additional_info.duration_ms
additional_info.recording_mbid
additional_info.release_mbid
additional_info.artist_mbids
```

---

# 15. Scrobble 判断

不要播放一开始就提交 scrobble。

使用：

```text
threshold = min(track_duration / 2, 4 minutes)
```

例如：

```text
歌曲 3 分钟
→ 1 分 30 秒

歌曲 5 分钟
→ 2 分 30 秒

歌曲 10 分钟
→ 4 分钟
```

达到 threshold：

```text
Scrobble once
```

同一播放 session 不能重复提交。

---

# 16. Playback Session

设计：

```go
type Session struct {
    Track     model.Track
    StartedAt time.Time

    Scrobbled bool
}
```

需要支持：

```text
Play
Pause
Resume
Stop
Seek
Next
Previous
Progress
```

重要：

### Seek

不能简单根据 wall-clock 时间计算。

例如：

```text
歌曲 5:00
播放到 2:00
seek 到 4:30
```

应该以真实播放 position 判断是否达到 threshold。

---

# 17. Provider 并发

Last.fm 和 ListenBrainz 不应该阻塞 fnOS Music。

错误：

```text
fnOS request
 ↓
调用 Last.fm
 ↓
等待网络
 ↓
返回 fnOS
```

正确：

```text
fnOS request
 ↓
立即返回
 ↓
异步发送 Last.fm
 ↓
异步发送 ListenBrainz
```

外部服务失败：

```text
不能影响 fnOS Music 播放
```

---

# 18. ListenBrainz Playlist → fnOS Music

最终还需要实现：

```text
ListenBrainz Playlist
        ↓
Go Service
        ↓
获取 Playlist tracks
        ↓
根据 MBID / metadata
        ↓
匹配 fnOS Music 本地歌曲
        ↓
创建 / 更新 fnOS Playlist
```

必须先研究 fnOS Music 实际 Playlist API。

不能猜 API。

需要通过参考项目以及实际 fnOS API 找出：

```text
playlist/list
playlist/detail
playlist/create
playlist/delete
playlist/add
playlist/remove
```

以及：

```text
track/playlist-detail/list
```

等相关接口。

---

# 19. Playlist 匹配优先级

优先：

```text
Recording MBID
```

其次：

```text
Artist MBID + Track
```

其次：

```text
Artist + Album + Track
```

最后：

```text
Artist + Track
```

不能只使用歌曲标题。

---

# 20. 配置

建议：

```yaml
server:
  listen_socket: /var/run/trim_music.socket
  upstream_socket: /var/run/trim_music_upstream.socket

providers:
  lastfm:
    enabled: false
    api_key: ""
    api_secret: ""
    session_key: ""

  listenbrainz:
    enabled: false
    token: ""

playback:
  scrobble_threshold: auto

playlist:
  enabled: true
  sync_interval: 30m

logging:
  level: info
  max_size: 10 # MB，超过自动切割
  max_backups: 5 # 保留历史份数
  max_age: 7 # 保留天数
  compress: true # 历史日志 gzip 压缩
```

日志路径固定：

```text
/var/log/fnmusic-sync/fnmusic-sync.log
```

目录与文件名是代码常量（`defaultLogDir` / `defaultLogFile`），**配置文件不提供 dir / file 选项**；
仅 `--log-dir` 可临时覆盖（`off` / `none` / `-` 表示只输出控制台、不写文件）。
日志同时输出到控制台(stderr)与文件；目录不可写时降级为仅控制台。

路径约定（FHS）：

```text
配置   /etc/fnmusic-sync/config.yaml
状态   /var/lib/fnmusic-sync/state.yaml
日志   /var/log/fnmusic-sync/fnmusic-sync.log
```

所有配置均写在配置文件内，不使用环境变量。

---

# 21. CLI

建议：

```bash
fnmusic-sync
```

支持：

```bash
fnmusic-sync --config /etc/fnmusic-sync/config.yaml
```

以及：

```bash
fnmusic-sync --check
```

子命令：

```bash
fnmusic-sync version              # 版本 / Git 提交 / 构建时间 / 平台 / 配置、状态、日志路径
fnmusic-sync self-upgrade         # 升级自身二进制到仓库最新版本
fnmusic-sync service install      # 安装系统服务（需 root）
fnmusic-sync service uninstall    # 卸载系统服务（需 root）
fnmusic-sync service start|stop|restart|status
```

日志参数：

```bash
fnmusic-sync --log-dir /var/log/fnmusic-sync   # 临时改日志目录（off = 不写文件）
```

检查：

```text
config
socket
upstream
Last.fm
ListenBrainz
```

---

# 22. systemd

最终部署应该提供：

```text
fnmusic-sync.service
```

要求：

```ini
Restart=always
RestartSec=5
```

启动前：

```text
检查 trim-music
```

启动后：

```text
health check
```

服务停止：

```text
尽可能恢复官方 socket
```

服务由 Go 库 `github.com/kardianos/service` 管理（`cmd/fnmusic-sync/service.go`），
不手写 unit 文件：

```bash
fnmusic-sync service install    # 写 /etc/systemd/system/fnmusic-sync.service
fnmusic-sync service start|stop|restart|status
```

模板通过 `Option["SystemdScript"]` 覆盖库默认模板（库模板把 `RestartSec` 写死成 120），
生成内容：

```ini
[Unit]
Description=fnmusic-sync (fnOS 飞牛音乐 Last.fm / ListenBrainz scrobble 代理)
After=network.target
StartLimitIntervalSec=0

[Service]
Type=simple
ExecStart=/usr/local/bin/fnmusic-sync --config /etc/fnmusic-sync/config.yaml --state /var/lib/fnmusic-sync/state.yaml
Restart=always
RestartSec=5
KillSignal=SIGTERM

[Install]
WantedBy=multi-user.target
```

---

# 23. 日志

使用 Go `slog`。

正常：

```text
INFO proxy listening
INFO upstream connected
INFO playback started
INFO now playing
INFO scrobble submitted
```

异常：

```text
ERROR upstream request failed
ERROR listenbrainz submit failed
ERROR lastfm scrobble failed
```

DEBUG：

```text
DEBUG fnos request
DEBUG fnos response
DEBUG playback event
```

**不要默认记录 token、API secret、session key。**

## 输出目标

同时输出到：

```text
控制台 stderr
日志文件 <配置目录>/logs/fnmusic-sync.log
```

规则：

```text
路径固定：/var/log/fnmusic-sync/fnmusic-sync.log（--log-dir 可临时覆盖，off = 不写文件）
轮转：max_size(MB) 切割 + compress(gzip) + max_backups(份数) / max_age(天) 过期清理
目录不可写：WARN 后降级为仅控制台，不阻断启动
级别优先级：--debug > logging.level（可热更新）
```

轮转由 `gopkg.in/natefinch/lumberjack.v2` 实现（`lumberjack.Logger` 作为文件 writer）：
超过 `max_size` 切割为 `fnmusic-sync-<时间>.log`，历史文件按 `compress` gzip 压缩，
再按 `max_backups` / `max_age` 清理。

实现：`cmd/fnmusic-sync/logging.go`，`multiHandler` 把一条记录扇出到多个 `slog.Handler`，单个失败不影响其它。

---

# 24. 当前测试流程

第一阶段只测试 Proxy。

fnOS 当前必须保持正常：

```text
/var/run/trim_music.socket
```

然后停止官方 Music 扩展测试操作。

接管后：

```text
/var/run/trim_music.socket
```

应该由 Go Proxy 监听。

upstream：

```text
/var/run/trim_music_upstream.socket
```

应该继续由官方 `trim-music` 使用。

首先执行：

```bash
ls -l /var/run/trim_music*.socket
```

然后：

```bash
ss -xlpn | grep trim_music
```

再：

```bash
curl \
  --unix-socket /var/run/trim_music.socket \
  http://localhost/_ext/healthz
```

必须确认：

```text
Proxy OK
Upstream OK
```

---

# 25. 安装与升级

安装方式（推荐顺序）：

```text
1. scripts/install.sh
2. Release 附件手动安装
3. deb / rpm / apk
4. 源码 make build-linux-*
```

脚本：

```bash
sudo ./scripts/install.sh                      # 安装最新版本
sudo ./scripts/install.sh upgrade              # 升级，保留配置
sudo ./scripts/install.sh upgrade --version v0.0.1
sudo ./scripts/install.sh --no-service         # 无 systemd 环境（fnOS 手动运行）
sudo ./scripts/install.sh --file ./xx.tar.gz   # 用本地包，不联网
sudo ./scripts/install.sh uninstall [--purge]  # 卸载（--purge 连配置、日志、状态一起删）
```

**必须以 root 运行**（写 /usr/local/bin、/etc、/var、systemd 目录）；非 root 直接报错退出，不自动 sudo。

默认路径：

```text
二进制   /usr/local/bin/fnmusic-sync
配置     /etc/fnmusic-sync/config.yaml
状态     /var/lib/fnmusic-sync/state.yaml
日志     /var/log/fnmusic-sync/fnmusic-sync.log
服务     /etc/systemd/system/fnmusic-sync.service
```

一键安装（不需要 clone 仓库）：

```bash
curl -fsSL <仓库>/-/git/raw/main/scripts/install.sh | sudo sh
```

安装脚本**不下载仓库里的任何文件**（只下载 Release 里的二进制包）：
配置文件由程序首次运行时自己生成（SaveDefault）。

约束：

```text
升级绝不覆盖 config.yaml（内含 token）
卸载前停止服务并还原官方 socket
平台探测与 Release 附件名一致：fnmusic-sync-<os>-<arch>.tar.gz
服务安装调用二进制自身：fnmusic-sync service install（kardianos/service）
```

构建矩阵（Linux）：

```text
amd64 / arm64 / mips / mipsle / arm / 386
```

打包：`make package-linux-all` 产出 deb / rpm / apk（不含 OpenWrt ipk，已移除）。

然后才打开 fnOS Music。

---

# 25. 编译

开发机是 Mac M1。

fnOS 如果是 x86_64：

```bash
CGO_ENABLED=0 \
GOOS=linux \
GOARCH=amd64 \
go build \
-trimpath \
-ldflags="-s -w" \
-o fnmusic-sync \
./cmd/fnmusic-sync
```

检查：

```bash
file fnmusic-sync
```

然后：

```bash
scp fnmusic-sync root@FNOS_IP:/home/admin/fnmusic-sync
```

fnOS：

```bash
chmod +x /home/admin/fnmusic-sync
```

运行：

```bash
./fnmusic-sync
```

---

# 26. 最重要的开发要求

**不要直接认为当前已有 Go Proxy 代码正确。**

必须：

1. 重新研究 `javycoder/fnos_music_ext`
2. 对照其 `extend.sh`
3. 对照其 Python proxy
4. 正确实现 Unix Socket takeover
5. 正确实现 HTTP transparent proxy
6. 实现 health check
7. 实现 socket 权限
8. 实现失败恢复
9. 实际抓取 fnOS Music API
10. 确认真正的播放/暂停/进度事件
11. 再实现 Last.fm
12. 再实现 ListenBrainz
13. 最后实现 ListenBrainz Playlist → fnOS Playlist

**尤其不要用 `/music/api/v1/track/stream` 请求直接判断用户已经播放了歌曲，也不要猜 fnOS Music 的播放进度 API。必须通过真实 API 流量确认。**

---

## 推荐项目结构

```text
fnmusic-sync/
├── cmd/
│   └── fnmusic-sync/
│       └── main.go
│
├── internal/
│   ├── config/
│   │   └── config.go
│   │
│   ├── model/
│   │   └── track.go
│   │
│   ├── proxy/
│   │   ├── proxy.go
│   │   ├── socket.go
│   │   ├── health.go
│   │   └── takeover.go
│   │
│   ├── playback/
│   │   ├── manager.go
│   │   └── detector.go
│   │
│   ├── scrobbler/
│   │   ├── scrobbler.go
│   │   ├── lastfm.go
│   │   └── listenbrainz.go
│   │
│   ├── fnos/
│   │   ├── client.go
│   │   ├── playlist.go
│   │   └── track.go
│   │
│   └── playlist/
│       ├── sync.go
│       └── matcher.go
│
├── configs/
│   └── config.example.yaml
│
├── deploy/
│   └── fnmusic-sync.service
│
├── go.mod
└── README.md
```

**最终目标不是做一个“能转发 HTTP 的 Go 程序”，而是做一个对 fnOS Music 无感的中间层：正常请求完全透明，只有确认属于播放事件、scrobble 或 playlist 的请求才由扩展逻辑处理。**
