# 安全策略

fnmusic-sync 接管飞牛音乐的 unix socket，持有 Last.fm / ListenBrainz 的长期凭证，还带一个能改配置、能触发升级的管理界面。它的安全边界基本等于「能登录这台 NAS 的人」，所以下面把边界写清楚，而不是只说一句「请负责任地披露」。

## 支持范围

| 版本                  | 是否接受安全修复                     |
|-----------------------|--------------------------------------|
| 最新 release（0.1.x） | 是，修复后发新版本                   |
| 更早的 release        | 否，请先升级到最新版本               |
| `main` 分支           | 跟随最新 release，不单独维护安全分支 |

单人维护、best-effort：不承诺 SLA，但会尽快确认与修复。

## 报告漏洞

**不要开公开 Issue，也不要在 PR 里贴 PoC。**

- 首选 GitHub 私密通道：[Security Advisories](https://github.com/dtapps/fnmusic-sync/security/advisories/new)
- 或邮件：dtapp 13553695467@139.com（与包元数据 `.nfpm.yaml` 中的维护者地址一致）

报告里请带上：

1. 版本（`fnmusic-sync version` 的输出）与部署形态（deb / fpk）；
2. 复现步骤或最小 PoC；
3. 影响面判断（能读到什么、能改什么、是否需要本机访问）；
4. 是否希望公开致谢、以及可接受的最晚披露时间。

预期节奏：7 天内确认收到；确认有效后修复并发布，在 advisory 中说明影响版本与修复版本，并按你的意愿致谢或匿名。

不在本项目范围内：fnOS 本身、飞牛音乐（trim-music）官方服务、Last.fm / ListenBrainz / MusicBrainz 平台，以及第三方 Go / npm 依赖的漏洞（请报给上游，本站会在依赖升级中跟进）。

## 威胁模型

默认假设：**部署在内网 NAS 上，能被登录这台机器的人访问；不直接暴露到公网。**

| 资产              | 位置                                                       | 泄露/被改的后果                                     |
|-------------------|------------------------------------------------------------|-----------------------------------------------------|
| Last.fm / LB 凭证 | `config.yaml`（明文 `api_secret`、`session_key`、`token`） | 他人可冒用账号推送 scrobble、读写歌单               |
| 播放与同步记录    | `state.db`、`data.db`                                      | 泄露收听习惯；`data.db` 可重建，`state.db` 会丢统计 |
| 播放事件通道      | `/var/run/trim_music.socket`                               | 可伪造「正在播放」事件，污染 scrobble 与歌单        |
| 管理接口          | fpk 的 `app.sock`（Web UI + API）                          | 可改配置、读日志、写用户凭证、触发升级              |

## 已知设计边界

这些不是「将来要修的 bug」，而是当前实现的事实，部署前请确认你能接受：

1. **unix socket 是 0666。** 代理监听 socket 默认 `0o666`（`internal/proxy/socket.go`，因为 fnOS 的 nginx worker 以 `www-data` 运行），Web UI 的 `app.sock` 同样 `chmod 0666`（`internal/webui/handler.go`）。**本机任意用户/进程都能连**，也就能伪造播放事件、调用 API。

2. **Web UI 的鉴权只依赖飞牛网关转发的 Header。** 用户身份来自 `X-Trim-Userid`、`X-Trim-Username`、`X-Trim-Isadmin`（`readGatewayUser`），**不校验来源、不验签**。更关键的是：**请求里没有这些 Header 时，一律按管理员处理**（本地调试的刻意设计）。因此任何能直连 `app.sock` 的本机进程，等价于管理员。

3. **fpk manifest 声明 `disable_authorization_path = true`。** 也就是这个应用的访问授权由应用自己负责，不要假设飞牛会在网关层拦未授权用户 —— 真正的信任起点是「谁能登录这台 NAS」。

4. **配置文件是明文凭证。** 首次自动生成的 `config.yaml` 使用 viper 默认权限 `0644`，同机其他用户可读；程序回写凭证时不会改权限。建议手动 `chmod 600`。

5. **`/api/upgrade` 等于一个安装入口。** 它会下载 fpk 并调用系统级 `appcenter-cli` 完成重装；deb 形态下进程由 systemd 以 root 运行（安装脚本未指定 `User=`），fpk 形态下由飞牛拉起。能调这个接口，基本等于能在本机安装软件。

6. **升级与下载不校验完整性。** 发布流程目前不产出 `SHA256SUMS`，`install.sh` 也不校验下载物，下载走中转站并会回退源站。请只在你能信任的网络与镜像源上升级；升级前备份 `config.yaml` 与 `state.db`。

7. **出网请求会带上凭证。** Last.fm / ListenBrainz 官方 API、MusicBrainz（仅在 `library.mbid_online_lookup: true` 时）、以及自升级的下载源。没有任何遥测上报。

8. **日志可能含用户数据。** 请求明细日志会对 `Authorization`、`Cookie`、`Set-Cookie` 等敏感键打码（`internal/reqlog`），但 URL、曲目名、用户名仍会落盘。长期开 `--debug` 后分享日志前先自己看一眼。

## 加固建议

```bash
# 1. 收紧配置权限（含 Last.fm secret / session_key / LB token）
sudo chmod 600 /etc/fnmusic-sync/config.yaml
# fpk：sudo chmod 600 "$TRIM_PKGETC/config.yaml"

# 2. 确认 socket 没有对外暴露
ls -l /var/run/trim_music*.socket          # 0666 是预期，但只应存在于本机 /var/run
sudo ss -xlp | grep trim_music

# 3. 备份再升级
sudo cp /etc/fnmusic-sync/config.yaml /root/config.yaml.bak
sqlite3 /var/lib/fnmusic-sync/state.db ".backup '/root/state.db.bak'"
```

- 不要给不可信账号 fnOS 登录权限 —— Web API 的信任链从这里开始。
- 不要把 `app.sock` / `trim_music.socket` 挂进容器或共享给其他机器。
- 不需要 Web UI 升级按钮时，可在飞牛侧限制 `appcenter-cli` 的可用性，缩小攻击面。
- 关注 CI 的 `govulncheck` 与 SCA / SBOM 流水线结果（见 [贡献指南](CONTRIBUTING.md)）；依赖升级走 Dependabot。

## 相关文档

- 鉴权与端点细节：[Web UI API](docs/webui-api.md)
- socket 接管机制与残留情形：[架构与运行原理](docs/architecture.md)
- 权限相关的排障：[故障排查](docs/troubleshooting.md)
- 部署位置与权限推导：[部署与运维](docs/deployment.md)
