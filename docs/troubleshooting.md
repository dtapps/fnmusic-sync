# 故障排查

按症状组织的排障手册：每个条目固定按「现象 → 快速定位 → 日志关键字 → 根因 → 处理」走一遍，命令可直接粘贴。

> 相关：[deployment.md](deployment.md)（路径、systemd、升级回滚）、[configuration.md](configuration.md)（配置项）、[cli.md](cli.md)（命令行）、[scrobble-pipeline.md](scrobble-pipeline.md)（推送链路）、[playlist-sync.md](playlist-sync.md)（歌单同步链路）。

## 症状速查表

| 症状                     | 先跑这一条                                                                    | 大概率根因                                               |
|--------------------------|-------------------------------------------------------------------------------|----------------------------------------------------------|
| 飞牛音乐页面打不开 / 502 | `fnmusic-sync sockets`                                                        | socket 权限不是 0666、代理已退出、双进程抢占             |
| 接口超时 / 偶发 502      | `curl --unix-socket /var/run/trim_music.socket http://localhost/_ext/healthz` | 上游 trim-music 未就绪或已挂                             |
| 代理完全不转发           | `ls -l /var/run/trim_music*.socket`                                           | 飞牛音乐重启后抢回原路径，代理被旁路                     |
| scrobble 不推送          | `grep '识别到用户' /var/log/fnmusic-sync/*.log`                               | 用户 key 不匹配、凭证不全、阈值未到                      |
| Last.fm 授权卡住         | `grep 'Last.fm 需要授权' /var/log/fnmusic-sync/fnmusic-sync.log`              | 没点同意、轮询超时、配置目录只读                         |
| 歌单不同步               | `sqlite3 -readonly /var/lib/fnmusic-sync/state.db "select * from sync_log"`   | 总开关关着、间隔未到、无活跃用户、MBID 缺失              |
| 改了配置没反应           | `fnmusic-sync version`（确认读的是哪个文件）                                  | 改错文件、YAML 语法错、键不支持热加载                    |
| 服务起不来               | `journalctl -u fnmusic-sync -n 100`                                           | 单实例锁被占、DB 打不开、日志目录权限                    |
| 日志暴涨 / CPU 高        | `du -sh /var/log/fnmusic-sync`                                                | debug 没关、`library.directories` 指向超大目录           |
| 升级失败 / 升级后异常    | `fnmusic-sync version`                                                        | token 缺失导致无法查版本、`appcenter-cli` 不存在、包未换 |

## 飞牛音乐页面打不开 / 502 / 接口超时

### 页面 502：socket 权限或归属不对

**现象**：飞牛音乐 Web/App 打开报 502，或接口一直转圈；`systemctl status fnmusic-sync` 却是 `active`。

**快速定位**：

```bash
sudo fnmusic-sync sockets
ls -l /var/run/trim_music*.socket
ss -xlpn | grep trim_music
sudo fnmusic-sync --check
```

`fnmusic-sync sockets` 会直接给出结论行，比 `systemctl status` 有用得多：

```text
监听 socket (/var/run/trim_music.socket)  ✅ 正常
  存在       true
  类型       unix socket
  权限       0666
  属主       0:0
  归属       本代理
  可连接     true

上游 socket (/var/run/trim_music_upstream.socket)  ✅ 正常
  归属       飞牛音乐(官方)
```

**日志关键字**：`socket 权限不允许 other 访问，nginx(www-data) 会 502`、`代理已监听`（带 `权限=0666`）、`socket状态`（`阶段=接管前`，含 `归属` 与 `权限`）。

**根因**：nginx worker 以 `www-data` 运行，`connect()` 时内核检查 socket 文件权限，other 位为 0 就 `EACCES`。或代理根本没起来（归属是 `none`/`stale`），nginx 拿不到监听者。

**处理**：

```bash
# 1) 权限层面：让 socket 至少 0666，并把配置恢复为「沿用官方权限」
sudo sed -i 's/^\(\s*socket_mode:\s*\).*/\10/' /etc/fnmusic-sync/config.yaml
sudo systemctl restart fnmusic-sync

# 2) 归属层面：确认两个 socket 分别是「本代理」与「飞牛音乐(官方)」
sudo fnmusic-sync sockets

# 3) 代理没接管成功时，看它卡在哪一步
sudo journalctl -u fnmusic-sync -n 50 --no-pager
```

### 页面正常但 scrobble 全停：飞牛音乐重启把 socket 抢回去了

**现象**：网页能正常听歌，但之后再没有新 scrobble；`sockets` 里「监听 socket」归属变成 `飞牛音乐(官方)`。

**快速定位**：

```bash
sudo fnmusic-sync sockets
sudo tail -50 /var/log/fnmusic-sync/fnmusic-sync.log | grep -E 'socket|接管|恢复'
```

**日志关键字**：`原路径与上游路径都是官方后端，保留上游并移除原路径`、`官方后端已回到原路径，无需恢复`。

**根因**：从飞牛应用中心重启「飞牛音乐」时，trim-music 会 `unlink` 再 `bind` `/var/run/trim_music.socket`。代理手里的监听 socket 成为已删除 inode（仍能 `accept`，但没人连得到），nginx 直连官方，代理被旁路。

**处理**：重启代理重新接管，之后**不要**再从应用中心重启飞牛音乐。

```bash
sudo systemctl restart fnmusic-sync
sudo fnmusic-sync sockets     # 期望：监听=本代理，上游=飞牛音乐(官方)
```

### 接口超时 / 偶发 502：上游没就绪

**现象**：`/_ext/healthz` 返回 503，或页面加载一半卡住。

**快速定位**：

```bash
curl -s -i --unix-socket /var/run/trim_music.socket http://localhost/_ext/healthz
sudo systemctl status trim-music 2>/dev/null || ps -ef | grep -i trim-music | grep -v grep
```

**日志关键字**：`健康检查失败`、`等待上游就绪`（带 `第几次`）、`上游不可用 (/var/run/trim_music_upstream.socket): …`。

**根因**：健康检查只做「能连上 + 拿到 HTTP 响应」，连不上就是上游不可用。常见于飞牛音乐还在启动、或刚被应用中心停掉。

**处理**：等飞牛音乐起来（代理启动时会按 `server.upstream_wait`，默认 30s 轮询等待）；持续不可用就重启飞牛音乐，再重启代理。

### 双进程抢占 / 重复启动

**现象**：日志里出现拒绝启动；或两个进程都试图接管 socket，页面时好时坏。

**快速定位**：

```bash
ps -ef | grep fnmusic-sync | grep -v grep
sudo cat /run/fnmusic-sync/run.lock        # 里面是持锁 PID
sudo lsof /run/fnmusic-sync/run.lock 2>/dev/null
```

**日志关键字**：`已有实例在运行，拒绝重复启动（lock=/run/fnmusic-sync/run.lock）`；锁拿不到时是 `无法获取运行锁，跳过单实例保护（可能权限不足）`。

**根因**：互斥靠 `flock`。已有一个实例持锁时新实例直接退出；但锁目录不可写（本地非 root）时只告警放行，此时两个实例可能同时存在。

**处理**：

```bash
sudo systemctl stop fnmusic-sync
sudo pkill -f '/fnmusic-sync($| )'    # 清掉手工起的前台进程
sudo systemctl start fnmusic-sync
```

## `--check` 各字段怎么读

`--check` 不接管、不启动代理，只做三件事：探测两个 socket 的归属、读权限与属主、验证官方后端连通性。输出固定为「标签 + 路径 + 结论」四类行。

正常输出（已接管）：

```text
监听   /var/run/trim_music.socket                        归属=proxy
上游   /var/run/trim_music_upstream.socket               归属=trim
权限   /var/run/trim_music.socket                        0666 属主=0:0
权限   /var/run/trim_music_upstream.socket               0666 属主=0:0
官方后端连通性(/var/run/trim_music_upstream.socket)：正常
```

正常输出（未接管，官方直连）：

```text
监听   /var/run/trim_music.socket                        归属=trim
上游   /var/run/trim_music_upstream.socket               归属=none
权限   /var/run/trim_music.socket                        0666 属主=0:0
权限   /var/run/trim_music_upstream.socket               (不存在)
提示：尚未接管，飞牛音乐当前为官方直连
官方后端连通性(/var/run/trim_music.socket)：正常
```

字段含义与异常读法：

| 输出                                                                     | 含义                                                                          | 该怎么办                                                   |
|--------------------------------------------------------------------------|-------------------------------------------------------------------------------|------------------------------------------------------------|
| `归属=trim`                                                              | 该路径上是官方 trim-music（靠 `PROBE` 请求返回 `INVALID TOKEN`/`99999` 识别） | 正常；接管态下应出现在上游                                 |
| `归属=proxy`                                                             | 该路径上是本代理（`/_ext/healthz` 返回体含 `upstream` 识别）                  | 正常；接管态下应出现在监听                                 |
| `归属=none`                                                              | 文件不存在，或存在但无人监听（死文件）                                        | 飞牛音乐没起来，或代理已退出                               |
| `归属=stale`                                                             | 有人监听但认不出身份                                                          | 通常是残留进程，在 `ss -xlpn` 输出里筛 `trim_music` 找 PID |
| `警告 … 权限不允许 other 访问，nginx(www-data) 将无法连接，会表现为 502` | other 位为 0                                                                  | 改 `server.socket_mode: 0` 后重启                          |
| `官方后端连通性(<path>)：失败 (…)`                                       | 连不上或拿不到 HTTP 响应                                                      | 先起飞牛音乐；再看 `<path>` 是否指对了                     |
| `警告：两个 socket 上都没有探测到官方 trim-music`                        | 两个路径都不是官方后端                                                        | 飞牛音乐未安装/未启动，或已被别的进程占位                  |
| 进程退出码非 0，末行 `❌ 上游检查失败`                                   | 体检失败（`check()` 返回 error）                                              | 按上面的具体行处理                                         |

本地（Mac/无 fnOS）跑 `--check` 的形态就是最后一行——两个路径都 `(不存在)`，连通性失败。这不是程序问题。

## scrobble 不推送

排查顺序固定为「用户识别 → 凭证 → 阈值 → 平台侧」。前一步没过，后一步不用看。

### 第 1 步：用户有没有被识别

**现象**：日志里完全没有 `推送scrobble`，也没有 `scrobble 未到阈值`。

**快速定位**：

```bash
sudo grep -E '识别到用户|探测用户信息失败|未能从 /user/me 解析出用户名' /var/log/fnmusic-sync/fnmusic-sync.log | tail -20
sudo sqlite3 -readonly /var/lib/fnmusic-sync/state.db "SELECT username,token_prefix,last_seen_at FROM users ORDER BY last_seen_at DESC LIMIT 10;"
```

**日志关键字**：`识别到用户`（带 `用户标识` 前 8 位与 `用户名`）、`探测用户信息失败`（带 `状态码`、`响应体`）、`未能从 /user/me 解析出用户名`。

**根因**：`users` 段配置的 key 是**飞牛 `/user/me` 返回的音乐用户名**，不是飞牛登录名、不是设备名。key 不匹配时该用户没有任何 provider，推送静默跳过。

**处理**：用日志里 `识别到用户 用户名=xxx` 的原文去改配置的 key。

```bash
sudo grep '识别到用户' /var/log/fnmusic-sync/fnmusic-sync.log | tail -3
sudo vi /etc/fnmusic-sync/config.yaml     # users: 下的 key 与上面完全一致
```

配置热加载会立即生效，不用重启。

### 第 2 步：凭证是不是配全了

**现象**：用户识别正常，但从不推送。

**快速定位**：

```bash
sudo grep -E '用户 Last.fm 已启用但配置不完整' /var/log/fnmusic-sync/fnmusic-sync.log | tail -5
```

**日志关键字**：`用户 Last.fm 已启用但配置不完整，已跳过`，附带 `已设api_key` / `已设api_secret` / `已设session_key` 三个布尔。

**根因**：Last.fm 侧要 `enabled + api_key + api_secret + session_key` 四项齐全才会注册 provider（`cmd/fnmusic-sync/setup.go`）；ListenBrainz 侧要 `enabled + token`。

**处理**：补齐配置或先走授权流程（见下一节）。ListenBrainz 只缺 `username` 不影响 scrobble，只影响歌单同步。

### 第 3 步：阈值到没到

**现象**：有 `播放会话开始`，但每次都没推送。

**快速定位**：

```bash
sudo grep -E '播放会话开始|播放会话结束|scrobble 未到阈值|推送scrobble' /var/log/fnmusic-sync/fnmusic-sync.log | tail -20
```

**日志关键字**：

```text
level=WARN msg="scrobble 未到阈值" 用户=admin 播放位置=18s 阈值=1m30s
level=INFO msg="推送scrobble" 用户=admin 标题=… 艺人=… 已播放=1m31s 时长=3m2s
level=INFO msg="播放会话结束" 原因=程序退出 已推送=false
```

**根因**：`playback.scrobble_threshold` 默认 `auto`，规则是 `min(时长/2, 4min)`。另外飞牛只上报 `track_play`，没有 pause/stop/end 事件，所以「播放完成」只能等价于「达到阈值」，没到阈值就切歌只会留下一条 `播放会话结束 已推送=false`。

**处理**：先用 `30s` 验证链路是否通，通了再调回 `auto`。

```bash
sudo sed -i 's/^\(\s*scrobble_threshold:\s*\).*/\130s/' /etc/fnmusic-sync/config.yaml
# 热加载生效；听完一首 30 秒以上，确认出现 "scrobble推送成功"
```

阈值写错会被明确点名：`scrobble 阈值解析失败，沿用原阈值`（带 `配置值`）。支持 `auto`、`30s`/`2m`、`50%`/`0.5`、`off`。

### 第 4 步：平台侧拒绝

**现象**：出现 `推送scrobble`，但紧跟 `scrobble推送失败`。

**快速定位**：

```bash
sudo grep -E 'scrobble推送失败|正在播放推送失败' /var/log/fnmusic-sync/fnmusic-sync.log | tail -20
sudo tail -100 /var/log/fnmusic-sync/lastfm.log      # 需 --debug
sudo tail -100 /var/log/fnmusic-sync/listenbrainz.log
```

**日志关键字**：

| 关键字                                                 | 平台         | 含义                                                      |
|--------------------------------------------------------|--------------|-----------------------------------------------------------|
| `scrobble推送失败 平台=Last.fm` + `last.fm error 9: …` | Last.fm      | session key 无效/被吊销（错误码原样来自 Last.fm 响应）    |
| `last.fm error 13: …`                                  | Last.fm      | 方法签名不合法——通常是 `api_secret` 填错或含多余空格/引号 |
| `last.fm error 29: …`                                  | Last.fm      | 触发速率限制，等一段时间或降低推送频率                    |
| `last.fm HTTP status: 5xx`                             | Last.fm      | 服务端临时故障，重试即可                                  |
| `listenbrainz HTTP status: 401 …`                      | ListenBrainz | `token` 失效                                              |
| `ListenBrainz token 无效`                              | ListenBrainz | `validate-token` 阶段就失败，token 填错或过期             |

**根因**：凭证问题占绝大多数；签名错误的根因是密钥不匹配。Last.fm 的签名是「参数按 key 排序拼接 + `api_secret` 求 MD5」，密钥错一个字符就全错。

**处理**：

- session_key 无效：重新走授权（删掉配置里的 `session_key`，重启程序，日志会给新链接）。
- api_secret 错：Last.fm API 账号页复制原始值，注意别带引号外的空格。
- ListenBrainz token 失效：换新 token，重启或等热加载。

### Last.fm 时间戳与签名

**现象**：推送成功、日志无错，但 Last.fm 网页上没有这条记录，或时间对不上。

**根因**：时间戳取的是 `started_at`（播放开始时刻的 Unix 秒）。系统时钟漂移几个小时后，Last.fm 会认为是过期/未来数据而丢弃。

**快速定位 / 处理**：

```bash
date -u
timedatectl status | sed -n '1,8p'
sudo timedatectl set-ntp true        # 开启 NTP 校时后重听一首验证
```

## Last.fm 授权卡住

**现象**：日志里有授权链接，浏览器点完「同意」，配置里的 `session_key` 还是空的。

**快速定位**：

```bash
sudo grep -E 'Last.fm 需要授权|Last.fm 授权|Last.fm 申请授权|Last.fm 换取|Last.fm 凭证写入' /var/log/fnmusic-sync/fnmusic-sync.log | tail -20
sudo ls -l /etc/fnmusic-sync/config.yaml
sudo grep -A2 'lastfm:' /etc/fnmusic-sync/config.yaml | head -20
```

**日志关键字与根因**：

| 关键字                                                       | 根因                                                                                                          |
|--------------------------------------------------------------|---------------------------------------------------------------------------------------------------------------|
| `Last.fm 需要授权，请在浏览器打开以下链接并点击同意`         | 正常提示，链接形如 `https://www.last.fm/api/auth/?api_key=…&token=…`；说明流程已启动                          |
| `Last.fm 申请授权 token 失败`                                | `api_key`/`api_secret` 无效，或出网失败                                                                       |
| `Last.fm 换取 session_key 失败`（每 5 秒一条）               | 用户还没点同意（正常现象）；持续出现则是 `api_secret` 错误                                                    |
| `Last.fm 授权轮询超时（60秒），重启程序可重试`               | 轮询窗口内没等到同意。注意文案里的「60秒」是旧残留，实际超时常量是 180 秒（`auth.go` 的 `lastFMAuthTimeout`） |
| `Last.fm 凭证写入配置失败`                                   | 配置文件所在目录/文件只读（fpk 下常见于 `$TRIM_PKGETC` 属主不对）                                             |
| `Last.fm 授权成功，session_key 和用户名已写入配置并自动生效` | 成功                                                                                                          |

**处理**：

```bash
# 1) 确认能出网到 Last.fm
curl -sS -o /dev/null -w '%{http_code}\n' https://ws.audioscrobbler.com/2.0/

# 2) 让程序有写配置的权限（deb 下通常没问题，fpk 下重点查）
sudo ls -ld /etc/fnmusic-sync
sudo chown root:root /etc/fnmusic-sync/config.yaml && sudo chmod 600 /etc/fnmusic-sync/config.yaml

# 3) 重新发起授权：清掉 session_key 后重启
sudo sed -i 's/^\(\s*session_key:\s*\).*/\1""/' /etc/fnmusic-sync/config.yaml
sudo systemctl restart fnmusic-sync
sudo journalctl -u fnmusic-sync -n 20 --no-pager | grep '授权链接'

# 4) 浏览器点同意后确认已写回
sudo grep -n 'session_key\|username' /etc/fnmusic-sync/config.yaml
```

一处易踩的语义：只有 `enabled: true` **且** `api_key`/`api_secret` 都非空、`session_key` 为空时，程序才会发起授权轮询（`main.go` 里的判断顺序）。缺 `api_secret` 时不会给链接，只会静默不注册 provider。

## 歌单不同步

按「总开关 → 间隔 → 用户/provider → 匹配」四层排。

**现象**：飞牛音乐里看不到「LB 每日推荐」「LF 最常听」这类歌单，或歌单停更。

**快速定位**：

```bash
sudo grep -E '歌单同步' /var/log/fnmusic-sync/fnmusic-sync.log | tail -30
sudo sqlite3 -readonly /var/lib/fnmusic-sync/state.db \
  "SELECT username,provider,status,trigger,playlists,tracks,message,finished_at FROM sync_log ORDER BY finished_at DESC LIMIT 10;"
sudo sqlite3 -readonly /var/lib/fnmusic-sync/data.db \
  "SELECT count(*) AS mapped, sum(recording_mbid IS NOT NULL AND recording_mbid<>'') AS with_mbid FROM track_mbid_map;"
```

**日志关键字 → 根因 → 处理**：

| 关键字                                                        | 根因                                                                              | 处理                                                             |
|---------------------------------------------------------------|-----------------------------------------------------------------------------------|------------------------------------------------------------------|
| `歌单同步已禁用，跳过启动`                                    | `playlist.enabled: false`                                                         | 改成 `true`，热加载生效                                          |
| `同步间隔未到，跳过`（带 `上次成功`、`已过去`、`间隔`）       | 距上次成功不足 `playlist.sync_interval`（默认 30m），这是持久化间隔，重启也照样拦 | 等间隔过去，或临时调小 `sync_interval`                           |
| `当前没有活跃用户，跳过歌单同步`                              | 还没有用户被识别（没人打开过飞牛音乐 / 没有有效 token）                           | 先用客户端播一首触发识别，见 `识别到用户`                        |
| `歌单同步跳过：用户未在配置中`                                | 用户名与配置 key 不一致                                                           | 对齐 key                                                         |
| `歌单同步跳过：用户未启用 ListenBrainz 或 Last.fm`            | 该用户没开对应平台，或歌单子项全关                                                | 打开 `users.<name>.listenbrainz.playlist.<类型>.enabled`         |
| `用户 token 已失效，已移出同步列表`                           | 上游返回 401 `INVALID TOKEN`（用户退出登录/被踢下线）                             | 重新登录飞牛音乐客户端，让新 token 被识别                        |
| `没有匹配的曲目，跳过歌单创建`                                | 推荐列表与本地曲库匹配为 0                                                        | 见下方「匹配不上」                                               |
| `歌单内容无变化，跳过添加`                                    | 增量同步的保护逻辑，不是错误                                                      | 无需处理                                                         |
| `歌单同步失败`（带 `错误`）                                   | 飞牛侧 API 失败（曲目索引、歌单 CRUD）或平台侧拉取失败                            | 结合 `feiniu.log` / `lastfm.log` / `listenbrainz.log` 看具体请求 |
| `清理失效曲目失败（不影响已同步内容）`                        | 清理阶段失败，不影响已同步内容                                                    | 可忽略；反复出现时看飞牛侧歌单接口                               |
| `加载 MBID 索引失败（不影响艺人+曲名匹配）`                   | `data.db` 打不开或表缺失                                                          | 检查 `data.db` 权限，必要时重建（删除后由程序重新建表并扫描）    |
| `MBID 本地扫描失败` / `library.scan_interval 非法，回退到 6h` | 曲库路径读不到 / 间隔写法非法                                                     | 修正 `library.directories` 与 `scan_interval`                    |

### 匹配不上（曲库路径 / MBID）

匹配顺序是「录音 MBID → 艺人名+曲名 → 仅曲名」，推荐曲名里的括号附注（如 `不将就 (Can't Bear It)`）会被截断后再比一次，所以**不强求**音乐文件带 MBID。但 `library.directories` 指不到真实曲库、或曲库是别的机器没挂载时，后两层也会因索引为空而失败。

```bash
# 曲库到底读不读得到（用服务身份测，deb/fpk 都是 root）
sudo ls -ld /path/to/music
sudo find /path/to/music -maxdepth 2 -type f \( -name '*.flac' -o -name '*.mp3' \) | head -3
# 手动触发一次扫描（fpk 的 Web UI 才有这个接口）
curl -s -X POST --unix-socket /var/apps/fnmusic-sync/target/app.sock \
  http://localhost/app/fnmusic-sync/api/library/scan
```

飞牛侧歌单重名时，同步会更新同名歌单而不是新建；想保留两份就改配置里的 `name`（如 `LB 每日推荐` → `LB 每日推荐 2`）。

## 配置改了不生效

**现象**：改了 `config.yaml`，日志级别/用户/阈值没变化。

按下面顺序查，命中即停：

```bash
# 1) 改的是不是程序真正读的那个文件
sudo fnmusic-sync version            # 看「配置」那一行：deb 是 /etc/fnmusic-sync/config.yaml，fpk 是 $TRIM_PKGETC/config.yaml
sudo ls -l /etc/fnmusic-sync/config.yaml

# 2) YAML 有没有语法错
sudo python3 -c 'import yaml,sys; yaml.safe_load(open("/etc/fnmusic-sync/config.yaml"))' && echo OK

# 3) 热加载有没有触发
sudo grep -E '配置热更新|配置热更新失败|配置监听启动失败' /var/log/fnmusic-sync/fnmusic-sync.log | tail -10
```

**日志关键字**：

| 关键字                                                                          | 根因                                                            |
|---------------------------------------------------------------------------------|-----------------------------------------------------------------|
| `配置热更新`（INFO）                                                            | 正常触发，随后的 `用户配置` 行会打印每个用户的开关              |
| `配置热更新失败，沿用旧配置` + `配置热更新解析失败`                             | YAML 语法错误，旧配置继续生效——这就是「改了没反应」最常见的原因 |
| `配置监听启动失败，将不会热更新`                                                | inotify 不可用（容器/受限环境），任何修改都必须重启             |
| `未找到配置文件且无法自动创建，使用内置默认配置（纯代理模式，不推送 scrobble）` | 配置目录不可写；此时跑的是内置默认值，改哪儿都没用              |

**不支持热加载的键**：`server.socket_mode`（启动时生效，改了要重启）。日志目录与文件名是代码常量，不在配置里。其余用户凭证、`playback.scrobble_threshold`、`playlist.*`、`logging.*` 都走 `applyConfig`，改完即生效。

**处理**：

```bash
# YAML 写坏了：先修语法，再确认热加载成功
sudo vi /etc/fnmusic-sync/config.yaml
sudo grep -c '配置热更新' /var/log/fnmusic-sync/fnmusic-sync.log
# 实在不确定就重启（会重新读一遍全量配置）
sudo systemctl restart fnmusic-sync
```

注意 `configs/config.example.yaml` 里 `logging.enabled` 写的是 `false`，而程序内置默认是 `true`。照抄示例文件会得到「只有控制台、没有日志文件」，这不是故障。

## 服务起不来

**现象**：`systemctl start fnmusic-sync` 后立刻 failed，或反复重启后 `start-limit-hit`。

**快速定位**：

```bash
sudo systemctl status fnmusic-sync --no-pager -l
sudo journalctl -u fnmusic-sync -n 100 --no-pager
sudo journalctl -u fnmusic-sync -p err -n 50 --no-pager
```

**日志关键字 → 根因 → 处理**：

| 关键字                                                                                   | 根因                                              | 处理                                                                                                        |
|------------------------------------------------------------------------------------------|---------------------------------------------------|-------------------------------------------------------------------------------------------------------------|
| `已有实例在运行，拒绝重复启动（lock=…）`                                                 | 残留进程握着 `/run/fnmusic-sync/run.lock`         | 停服务 → `pkill -f fnmusic-sync` → 再启动                                                                   |
| `打开数据库失败 (/var/lib/fnmusic-sync/state.db): …`                                     | 目录不存在/不可写，或 sqlite `database is locked` | `sudo mkdir -p /var/lib/fnmusic-sync && sudo chown root:root /var/lib/fnmusic-sync`；确认没有第二个进程在写 |
| `日志目录创建失败，仅输出到控制台`                                                       | `/var/log/fnmusic-sync` 权限或只读文件系统        | 修权限；只读分区时把 `logging.enabled: false` 容忍                                                          |
| `读取配置文件 … 失败` / `解析配置失败`                                                   | YAML 语法错或权限不足                             | 修 YAML；`sudo chmod 600`                                                                                   |
| `等待飞牛音乐 socket 可用，稍后重试`（每 3 秒）                                          | 飞牛音乐没启动（这是等待，不是崩溃）              | 启动飞牛音乐；进程会一直等到 socket 出现                                                                    |
| `上游不可用 (…): …`                                                                      | 接管成功但上游连不上，超过 `upstream_wait` 后退回 | 起飞牛音乐，或把 `server.upstream_wait` 调大                                                                |
| `未找到可接管的 trim-music socket (listen=none upstream=none)，请确认飞牛音乐应用已启动` | 两个路径都没有官方后端                            | 装/起飞牛音乐                                                                                               |
| `监听 /var/run/trim_music.socket 失败: … bind: address already in use`                   | 有别的进程占着该路径，或死 socket 未清理          | 在 `ss -xlpn` 输出里筛 `trim_music` 找 PID；`sudo rm` 死文件后重启                                          |
| `设置 socket 属主失败（不影响 nginx 连接）`                                              | 非 root 或有 chown 限制                           | 只影响属主，权限够就能用；要彻底解决用 root 跑                                                              |

**systemd 层面还要看这几处**：

```bash
systemctl cat fnmusic-sync            # ExecStart 是否被 service debug 加了 --debug
systemctl is-enabled fnmusic-sync     # 是否开机自启
systemctl show -p Restart -p RestartSec -p ExecMainStatus fnmusic-sync
```

`RestartSec` 实际是 120（kardianos 模板写死，代码里的 `RestartSec: 5` 不生效），所以崩溃后要等两分钟才会被拉起——别以为是卡死了。

## 日志暴涨 / CPU 高 / goroutine 泄漏

**现象**：日志几分钟几十 MB；或 `top` 里 CPU 持续偏高；或内存/goroutine 单调上涨。

**快速定位**：

```bash
du -sh /var/log/fnmusic-sync; ls -lhS /var/log/fnmusic-sync | head
sudo grep -c 'level=DEBUG' /var/log/fnmusic-sync/fnmusic-sync.log
sudo grep -E 'goroutine 达到阈值|可疑 goroutine' /var/log/fnmusic-sync/fnmusic-sync.log | tail -10
```

**日志关键字**：`goroutine 达到阈值，输出可疑清单`（带 `goroutines`、`threshold=60`、`suspicious`）、`可疑 goroutine <id> [<state>]（阻塞 <时长>）`、`goroutine 快照器已启动（常态静默，超阈值过滤输出；SIGUSR1 手动全量落盘）`。

**根因**：

1. `--debug` 或 `wizard_debug` 忘了关：`capture.log` + 三个客户端日志同时记全量请求体，单份上限约 18 MB，五份并跑约 90 MB。
2. `logging.max_backups` / `max_age` 设成 0：等于放弃上限。
3. `library.directories` 指向超大目录（几十万文件），每次扫描把 CPU 打满；`library.scan_interval` 太小会放大这一点。
4. 真有 goroutine 泄漏：项目没有 pprof，靠自研 watchdog 输出。

**处理**：

```bash
# 1) 关 debug（systemd 层）
sudo fnmusic-sync service nodebug      # fpk 形态没有这个子命令，改成应用设置里关 wizard_debug

# 2) 收紧轮转
sudo sed -i -E 's/^(\s*max_size:\s*).*/\13/; s/^(\s*max_backups:\s*).*/\15/; s/^(\s*max_age:\s*).*/\17/' \
  /etc/fnmusic-sync/config.yaml

# 3) 缩小曲库范围 / 拉长扫描间隔
sudo sed -i -E 's/^(\s*scan_interval:\s*).*/\112h/' /etc/fnmusic-sync/config.yaml

# 4) 抓一次 goroutine 现场（仅 debug 模式生效）
sudo kill -USR1 "$(pidof fnmusic-sync)"
sudo tail -300 /var/log/fnmusic-sync/goroutine-dump.txt
```

watchdog 的判据：每 30 秒读一次 goroutine 数，越过 60 才抓栈；按白名单剔除常驻待命的 goroutine；只报「阻塞 ≥ 5 分钟」和「`persistConn`/`http2.` 长连接空闲 ≥ 5 分钟」的栈；两次实际 dump 之间至少 5 分钟。所以「日志里没有可疑清单」不代表没泄漏，只是没越过阈值。

## 升级失败 / 升级后行为异常

**现象**：`self-upgrade` 报错；或升级后版本没变、服务起不来、行为变了。

**快速定位**：

```bash
fnmusic-sync version                  # 版本、Git 提交、构建时间、运行模式、各路径
fnmusic-sync self-upgrade             # 看它卡在哪一步
sudo dpkg -l fnmusic-sync             # deb 是否装上了
```

**日志/报错关键字 → 根因 → 处理**：

| 关键字                                                      | 根因                                                                                | 处理                                                             |
|-------------------------------------------------------------|-------------------------------------------------------------------------------------|------------------------------------------------------------------|
| `CNB 访问令牌未配置，暂时无法自动升级，请前往 … 手动下载`   | 二进制由 CNB 构建但未注入 `CnbToken`                                                | 从 Release 手动下载 deb/fpk 安装                                 |
| `GitHub API 返回 403 …`                                     | GitHub 匿名速率限制                                                                 | 稍后重试，或改用 Release 手动下载                                |
| `未检测到支持的包管理器（dpkg），无法自动升级。`            | 非 deb 系环境（fpk 形态根本没有 `self-upgrade` 子命令）                             | fpk 走应用中心                                                   |
| `💡 提示：请尝试使用 'sudo fnmusic-sync self-upgrade' 运行` | 权限不足                                                                            | 加 `sudo`                                                        |
| `升级失败: exit status 1`（`dpkg` 输出在上方）              | 依赖/架构不匹配，或包冲突                                                           | 看 `dpkg` 原始输出；`dpkg` 的 `--force-*` 选项只在明确原因时使用 |
| `未检测到 appcenter-cli 工具，请通过飞牛应用中心手动升级`   | fpk 的 Web UI 升级路径缺依赖                                                        | 用应用中心升级                                                   |
| 升级后 `version` 没变                                       | 新进程没起来（旧进程还在），或装的是另一个路径的二进制                              | `systemctl restart fnmusic-sync`；`which -a fnmusic-sync` 查双份 |
| 升级后 scrobble 停                                          | 升级过程重绑了 socket，代理没接管回来                                               | `fnmusic-sync sockets`，然后重启代理                             |
| 升级后配置被重置                                            | 改的不是运行模式对应的那份配置（如 fpk 模式下改了 `/etc/fnmusic-sync/config.yaml`） | `fnmusic-sync version` 看「配置」行，改对路径                    |

自己构建时最常见的「版本号不对」：`Makefile` 的 `VERSION` 来自 `git describe --tags --always --dirty`，本地直接 `go build` 不带 `-ldflags` 则显示 `dev`。`fnmusic-sync version` 里出现 `说明 本地构建（未注入发布版本号）` 就是这种情况。

## 权限类错误速查

| 报错文本                                           | 出现位置                                  | 含义与处理                                                                                 |
|----------------------------------------------------|-------------------------------------------|--------------------------------------------------------------------------------------------|
| `bind: address already in use`                     | `Takeover.Listen`、上游 dial              | 路径被占。在 `ss -xlpn` 输出里筛 `trim_music` 找 PID，清掉残留/死 socket 后重启            |
| `EACCES` / `permission denied`（nginx 侧表现 502） | socket 的 `connect()`                     | socket other 位为 0。设 `server.socket_mode: 0` 并重启                                     |
| `operation not permitted`（`chown`/`rename`）      | `Takeover.applyOwner`、`Takeover.Prepare` | 非 root 跑。用 root 或 systemd 托管                                                        |
| `read-only file system`                            | 写 `state.db`、日志、`config.yaml`        | 分区只读（常见于容器/挂载异常）。把状态与日志迁到可写分区，或临时 `logging.enabled: false` |
| `打开数据库失败 (…)`                               | `db.Open` / `datadb.Open`                 | 目录不存在或不可写；`database is locked` 说明还有别的进程在使用同一份库                    |
| `日志目录创建失败，仅输出到控制台`                 | `setupLogger`                             | 目录权限；不影响代理功能，只是没有文件日志                                                 |
| `Last.fm 凭证写入配置失败`                         | `config.SetLastFMCredentials`             | 配置文件只读或属主不对，授权拿到的 session_key 无法落盘                                    |

通用检查命令：

```bash
namei -l /var/lib/fnmusic-sync/state.db
stat -c '%A %U:%G %n' /etc/fnmusic-sync /etc/fnmusic-sync/config.yaml /var/log/fnmusic-sync
id
getfacl /var/run/trim_music.socket 2>/dev/null || stat -c '%A %U:%G %n' /var/run/trim_music*.socket
```

## 一键收集诊断信息

把下面整段贴进真机 shell 即可产出一份可外发的诊断报告（**不打印任何 session_key / token**）。

```bash
{
  echo "===== 1. 版本与运行模式 ====="
  fnmusic-sync version 2>&1

  echo; echo "===== 2. 服务状态 ====="
  systemctl status fnmusic-sync --no-pager -l 2>&1 | head -30
  systemctl show -p Restart -p RestartSec -p ExecMainStatus -p ExecMainStartTimestamp fnmusic-sync 2>&1
  systemctl cat fnmusic-sync 2>&1 | sed -n '1,40p'
  ps -ef | grep -E '[f]nmusic-sync' 2>&1

  echo; echo "===== 3. socket 状态 ====="
  ls -l /var/run/trim_music*.socket 2>&1
  ss -xlpn 2>/dev/null | grep trim_music
  fnmusic-sync sockets 2>&1

  echo; echo "===== 4. 代理健康 ====="
  curl -s -m 5 --unix-socket /var/run/trim_music.socket http://localhost/_ext/healthz 2>&1; echo

  echo; echo "===== 5. 配置摘要（脱敏） ====="
  cfg=$(fnmusic-sync version 2>/dev/null | awk '/配置/{print $2}')
  echo "配置文件: ${cfg:-未知}"
  if [ -r "$cfg" ]; then
    sed -E 's/((api_key|api_secret|session_key|token):[[:space:]]*).*/\1<REDACTED>/' "$cfg" \
      | grep -vE '^\s*#' | grep -vE '^\s*$'
  else
    echo "(无权限读取，请用 sudo 重跑本段)"
  fi

  echo; echo "===== 6. 数据库摘要 ====="
  st=/var/lib/fnmusic-sync/state.db
  [ -r "$st" ] || st=$(find /vol* -maxdepth 6 -name state.db -path '*fnmusic-sync*' 2>/dev/null | head -1)
  echo "state.db: ${st:-未找到}"
  if [ -n "$st" ] && [ -r "$st" ]; then
    sqlite3 -readonly "$st" "SELECT username,total_scrobbles,lastfm_scrobbles,listenbrainz_scrobbles,last_scrobbled_at FROM run_status;" 2>&1
    sqlite3 -readonly "$st" "SELECT username,provider,status,trigger,finished_at FROM sync_log ORDER BY finished_at DESC LIMIT 5;" 2>&1
  fi

  echo; echo "===== 7. 最近日志（脱敏） ====="
  for f in /var/log/fnmusic-sync/fnmusic-sync.log $(find /vol* -maxdepth 8 -path '*fnmusic-sync/logs/fnmusic-sync.log' 2>/dev/null | head -1); do
    [ -r "$f" ] || continue
    echo "--- $f ---"
    tail -200 "$f" | sed -E 's/(session_key|token|api_secret|api_key)(=|":")[^&" ]*/\1\2<REDACTED>/g'
  done
} > /tmp/fnmusic-diag-$(date +%Y%m%d-%H%M%S).txt 2>&1

echo "已生成：$(ls -t /tmp/fnmusic-diag-*.txt | head -1)"
```

脱敏覆盖了三处：配置摘要里按 `api_key|api_secret|session_key|token` 键名整行替换；日志尾部按 `session_key=…` / `"token":"…"` 形式替换；数据库摘要只查统计列，不查任何凭证。发出去之前仍然建议自己扫一眼 `grep -iE 'token|secret' /tmp/fnmusic-diag-*.txt`。
