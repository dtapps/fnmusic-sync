# fnmusic-sync

A **transparent proxy for Last.fm / ListenBrainz auto-scrobbling** on the fnOS (飞牛 OS) Feiniu Music (trim-music) app.

It takes over Feiniu Music's (trim-music) communication link via a unix socket, parses playback events and pushes scrobbles per user. Neither the official service nor the client notices anything — the proxy automatically restores the socket on exit.

## Features

- **Transparent takeover**: On startup it renames the official socket to the upstream path and binds the proxy to the original path. Every exit path restores it, leaving no 502.
- **Multi-user isolation**: Each Feiniu user uses their own Last.fm / ListenBrainz credentials, so user A's listening history is never pushed to user B's account.
- **Last.fm auth helper**: When `session_key` is missing, it prints an authorization link; once you approve it in the browser, the key is written back to the config file automatically and hot-reloaded — no manual entry needed.
- **Config hot-reload**: Changes to `config.yaml` take effect immediately (users, credentials, thresholds, log level) — no restart required.
- **Dual log output**: Simultaneously writes to the console (stderr) and the log file `/var/log/fnmusic-sync/fnmusic-sync.log`, with automatic rotation, gzip compression and expiry cleanup.
- **One-click install**: The install script auto-downloads the system package, registers the systemd service, enables boot-time autostart and starts it — no manual steps.
- **Self-upgrade**: `fnmusic-sync self-upgrade` auto-detects the system package manager (dpkg), downloads the matching package and installs it, then auto-restarts the service after upgrade.
- **ListenBrainz recommended-playlist sync**: Auto-syncs ListenBrainz's daily_jams, weekly_jams, weekly_exploration, year_discoveries and year_missed recommended playlists to Feiniu Music.
- **Last.fm smart-playlist sync**: Auto-generates top_tracks (most played), loved_tracks (red-heart favorites) and recent_tracks (recently played) playlists based on scrobble data.

## How it works

```text
nginx(www-data) ──▶ /var/run/trim_music.socket          ← this proxy
                          │
                          └──▶ /var/run/trim_music_upstream.socket   ← official trim-music
```

A socket is bound to an inode. After renaming with `os.Rename` (mv), the official backend keeps accepting on the new path; the proxy listens on the original path and forwards verbatim (preserving the original `Host`, with request/response headers and streaming responses passed through), while snooping playback events on the side to scrobble.

## Default paths

| Purpose                  | Traditional install                      | fpk install                                  |
| ------------------------ | ---------------------------------------- | -------------------------------------------- |
| Binary                   | `/usr/bin/fnmusic-sync`                  | `/var/apps/fnmusic-sync/target/fnmusic-sync` |
| Config file              | `/etc/fnmusic-sync/config.yaml`          | `$TRIM_PKGETC/config.yaml`                   |
| State file               | `/var/lib/fnmusic-sync/state.yaml`       | `$TRIM_PKGVAR/state.yaml`                    |
| Log file                 | `/var/log/fnmusic-sync/fnmusic-sync.log` | `$TRIM_PKGVAR/logs/fnmusic-sync.log`         |
| Proxy listen socket      | `/var/run/trim_music.socket`             | same as left                                 |
| Official upstream socket | `/var/run/trim_music_upstream.socket`    | same as left                                 |

> In fpk mode, paths are determined by fnOS environment variables (`TRIM_APPDEST`, `TRIM_PKGETC`, `TRIM_PKGVAR`, etc.) and detected automatically at startup.

## Quick start

### Install (auto-register service + boot autostart)

```bash
curl -fsSL https://cnb.cool/dtapp/fnmusic-sync/-/git/raw/main/scripts/install.sh | sudo sh
```

The script automatically: detect package manager → download deb package → install → register systemd service → enable boot autostart → start service. The config file is generated automatically on first launch.

### Upgrade

```bash
# Method 1: already-installed binary, self-upgrade directly (auto download package and restart service)
sudo fnmusic-sync self-upgrade

# Method 2: upgrade via install script
curl -fsSL https://cnb.cool/dtapp/fnmusic-sync/-/git/raw/main/scripts/install.sh | sudo sh -s -- upgrade

# Specify version
sudo sh install.sh upgrade --version vX.Y.Z
```

Upgrade will not overwrite `/etc/fnmusic-sync/config.yaml`; the service auto-restarts and loads the new version after upgrade.

### Uninstall

```bash
# Method 1: uninstall via install script (auto stop and remove service)
curl -fsSL https://cnb.cool/dtapp/fnmusic-sync/-/git/raw/main/scripts/install.sh | sudo sh -s -- uninstall

# Method 2: uninstall via package manager (auto stop and remove service before uninstall)
sudo apt remove fnmusic-sync          # deb-based (Debian/Ubuntu)

# Remove config, logs and state together as well:
sudo sh install.sh uninstall --purge
# or: sudo apt purge fnmusic-sync
```

### Other install methods

<details>
<summary>Download deb manually</summary>

Download the package for your architecture from the Release attachments and install (the postinstall script inside auto-creates directories, registers the service and starts it):

```bash
sudo dpkg -i fnmusic-sync_amd64.deb     # Debian / Ubuntu
```

</details>

<details>
<summary>Build from source</summary>

```bash
make build-linux-amd64      # output bin/fnmusic-sync-linux-amd64
make build-linux-all        # 2 Linux architectures (tagged + untagged)
make package-linux-all      # deb / fpk
```

Build/release matrix: **amd64 / arm64** (CI cross-compiles and uploads attachments when cutting a Release).

</details>

## Service management

After install, the service is auto-registered and started; just manage it with `systemctl` day-to-day:

```bash
systemctl status fnmusic-sync          # view status
systemctl restart fnmusic-sync         # restart
systemctl stop fnmusic-sync            # stop
journalctl -u fnmusic-sync -f          # view live logs
```

Run in foreground (no systemd or manual debugging):

```bash
sudo fnmusic-sync                       # run in foreground
sudo nohup fnmusic-sync >/dev/null 2>&1 &   # run in background
```

> Config, state and log paths are determined automatically by the run mode — no manual flag needed.

⚠️ Do not restart Feiniu Music from the app center: restarting trim-music will unlink and re-bind the original path, bypassing the proxy — you need to restart the proxy to take over again.

## Command line

```bash
fnmusic-sync version          # view version, git commit, build time
fnmusic-sync --check          # health check: socket ownership/permissions/connectivity, without takeover
fnmusic-sync --debug          # foreground run + verbose request/response logging
fnmusic-sync self-upgrade     # upgrade to the latest repo version (needs sudo)
fnmusic-sync service status   # view service run state (equivalent to systemctl status)
fnmusic-sync service restart  # restart service (equivalent to systemctl restart)
fnmusic-sync service stop     # stop service
sudo fnmusic-sync service debug    # enable debug mode and restart service
sudo fnmusic-sync service nodebug  # disable debug mode and restart service
```

| Subcommand                           | Description                                                                                                        |
| ------------------------------------ | ------------------------------------------------------------------------------------------------------------------ |
| `version`                            | Print version, git commit, build time, Go version, platform, config/state/log paths                                |
| `self-upgrade`                       | Upgrade itself to the latest repo version (auto-detect package manager, download deb, install and restart service) |
| `service install`                    | Install system service (systemd, needs root) — already done by install script                                      |
| `service uninstall`                  | Uninstall system service (needs root) — already done by uninstall script                                           |
| `service start` / `stop` / `restart` | Start/stop the service                                                                                             |
| `service status`                     | View run state                                                                                                     |
| `service debug`                      | Enable debug mode (modify ExecStart to add --debug, daemon-reload + restart)                                       |
| `service nodebug`                    | Disable debug mode (remove --debug, daemon-reload + restart)                                                       |

| Flag              | Default | Description                                                 |
| ----------------- | ------- | ----------------------------------------------------------- |
| `--upstream-wait` | `30s`   | Timeout for waiting for the official backend to be ready    |
| `--check`         | `false` | Only health-check, no takeover                              |
| `--debug`         | `false` | Print request/response details (with redacted auth headers) |

## Configuration

The config file defaults to `/etc/fnmusic-sync/config.yaml`; the path is determined automatically by the run mode (fpk mode uses the fnOS env var `TRIM_PKGETC`). Changes are **hot-reloaded automatically** — no restart needed.

```yaml
server:
  socket_mode: 0 # 0 = inherit official socket permissions (at least 0666)
  upstream_wait: 30s # timeout for waiting for the official backend to be ready at startup

users:
  admin: # key must match the username returned by fnOS /user/me
    lastfm:
      enabled: false
      api_key: ""
      api_secret: ""
      session_key: "" # when empty, an auth link is printed at startup; approve it and it is written back and takes effect automatically
      username: "" # Last.fm username (written back automatically after auth)
      playlist: # Last.fm smart-playlist sync (based on scrobble data)
        top_tracks:
          enabled: false
          name: "LF {period} Most Played"
          period: overall # 7day/1month/3month/6month/12month/overall
          limit: 50
        loved_tracks:
          enabled: false
          name: "LF Loved Tracks"
          limit: 50
        recent_tracks:
          enabled: false
          name: "LF Recently Played"
          limit: 50
    listenbrainz:
      enabled: false
      token: "" # ListenBrainz user Token
      username: "" # required for playlist sync (auto-fetched and written back when empty)
      playlist:
        daily_jams:
          enabled: false
          name: "LB Daily Jams"
          limit: 50 # 0 = no limit
        weekly_jams:
          enabled: false
          name: "LB Weekly Jams"
          limit: 50
        weekly_exploration:
          enabled: false
          name: "LB Weekly Exploration"
          limit: 50
        year_discoveries:
          enabled: false
          name: "LB {year} Year of Discoveries" # {year} auto-replaced
          limit: 50
        year_missed:
          enabled: false
          name: "LB {year} Year of Missed" # {year} auto-replaced
          limit: 50

playback:
  scrobble_threshold: auto # auto / 30s / 2m / 50% / off

playlist:
  enabled: true # master switch for playlist sync
  sync_interval: 30m # sync interval

logging:
  level: info # info / debug / warn / error
  max_size: 3 # rotate when a single file exceeds 3MB (0 = no size-based rotation)
  max_backups: 5 # keep only the latest 5 historical logs (0 = unlimited)
  max_age: 7 # keep historical logs for 7 days (0 = no time-based expiry)
  compress: true # auto gzip-compress historical logs (.gz)
```

| Section                                                 | Description                                                                                                                                                                                                   |
| ------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `server`                                                | socket path is fixed and not configurable; `socket_mode` controls socket permissions, `upstream_wait` controls the wait timeout for the official backend                                                      |
| `users`                                                 | Per-Feiniu-username isolated push credentials; multiple users supported                                                                                                                                       |
| `playback.scrobble_threshold`                           | `auto`=listen until min(duration/2, 4min) (Last.fm rule); `30s`/`2m`=fixed duration; `50%`=proportional; `off`=push on play (not recommended for daily use)                                                   |
| `playlist`                                              | Master switch and interval for playlist sync                                                                                                                                                                  |
| `users.<name>.lastfm.playlist`                          | Last.fm smart-playlist sync (top_tracks / loved_tracks / recent_tracks)                                                                                                                                       |
| `users.<name>.listenbrainz.playlist`                    | ListenBrainz recommended-playlist sync                                                                                                                                                                        |
| `users.<name>.listenbrainz.playlist.daily_jams`         | Daily recommended playlist; requires following [troi-bot](https://listenbrainz.org/user/troi-bot/) on ListenBrainz                                                                                            |
| `users.<name>.listenbrainz.playlist.weekly_jams`        | Weekly recommended playlist                                                                                                                                                                                   |
| `users.<name>.listenbrainz.playlist.weekly_exploration` | Weekly exploration playlist (discover new music)                                                                                                                                                              |
| `users.<name>.listenbrainz.playlist.year_discoveries`   | Year of discoveries playlist ({year} auto-replaced)                                                                                                                                                           |
| `users.<name>.listenbrainz.playlist.year_missed`        | Year of missed playlist ({year} auto-replaced)                                                                                                                                                                |
| `logging`                                               | Log level and rotation policy (rotation size, backups kept, expiry days, whether to compress). Log path is fixed to `/var/log/fnmusic-sync/fnmusic-sync.log`, **directory and filename are not configurable** |

### Last.fm authorization

1. In `users.<name>.lastfm`, set `enabled: true` + `api_key` + `api_secret`, leave `session_key` empty;
2. Start the program; an authorization link appears in the log:
   `Last.fm needs authorization, please open the following link in your browser and click agree`;
3. After clicking "Agree" in the browser, the program auto-writes `session_key` and `username` back to the config file and hot-reloads them (180s polling; if it times out, restart the program to retry).

ListenBrainz only needs `token`; when `username` is empty it is auto-fetched and written back.

### ListenBrainz recommended-playlist sync

Syncs ListenBrainz recommended playlists to Feiniu Music, configured independently per user.

| Playlist type        | Description                             | Special requirement                                                               |
| -------------------- | --------------------------------------- | --------------------------------------------------------------------------------- |
| `daily_jams`         | Daily recommendation                    | Must follow [`troi-bot`](https://listenbrainz.org/user/troi-bot/) on ListenBrainz |
| `weekly_jams`        | Weekly recommendation                   | None                                                                              |
| `weekly_exploration` | Weekly exploration (discover new music) | None                                                                              |
| `year_discoveries`   | Year of discoveries playlist            | None (auto-generated by ListenBrainz every year)                                  |
| `year_missed`        | Year of missed playlist                 | None (auto-generated by ListenBrainz every year)                                  |

**Enabling conditions:**

1. **ListenBrainz username**: `users.<name>.listenbrainz.username` must be filled in
2. **Follow troi-bot** (required for daily-jams): follow the [`troi-bot`](https://listenbrainz.org/user/troi-bot/) bot on ListenBrainz; it generates recommendations based on your listening history
3. **Corresponding tracks in your library**: tracks are matched in order of "recording MBID → artist name + track title → track title only",
   bracketed annotations in recommended track titles (e.g. `不将就 (Can't Bear It)`) are auto-truncated and re-compared,
   so **music files are not required to carry MBID tags**
4. **Enable playlist sync**: set `playlist.enabled: true` and enable the corresponding playlist type in the user config

**Config example:**

```yaml
playlist:
  enabled: true # master switch for playlist sync
  sync_interval: 30m

users:
  admin:
    listenbrainz:
      enabled: true
      token: "your-token"
      username: "your-listenbrainz-username" # auto-fetched and written back when empty
      playlist:
        daily_jams:
          enabled: true
          name: "LB Daily Jams"
          limit: 50
        weekly_jams:
          enabled: true
          name: "LB Weekly Jams"
          limit: 50
        weekly_exploration:
          enabled: true
          name: "LB Weekly Exploration"
          limit: 50
        year_discoveries:
          enabled: true
          name: "LB {year} Year of Discoveries"
          limit: 50
        year_missed:
          enabled: true
          name: "LB {year} Year of Missed"
          limit: 50
```

> ⚠️ **Note**: Playlist sync reads the "shared-with-you" recommended playlists (daily-jams / weekly-jams / weekly-exploration, latest issue) from ListenBrainz's `createdfor` endpoint,
> then matches them against the Feiniu Music library by "recording MBID → artist name + track title → track title only", only **incrementally adding** missing tracks — repeating sync will not bloat the playlist.
> Each sync round prints the match count and unmatched examples in the log for easy hit-rate troubleshooting.

## Logging

- Writes to both **console (stderr)** and **log file**, with consistent format (`log/slog` text).
- Fixed path: **`/var/log/fnmusic-sync/fnmusic-sync.log`** (directory and filename are written into code constants; the config file does not provide `dir`/`file` options).
- **Automatic rotation** (lumberjack): when a single file exceeds `logging.max_size` MB it is rotated; historical files are auto gzip-compressed (`.gz`) and deleted automatically when exceeding `logging.max_backups` files or `logging.max_age` days.
- When the directory is not writable, a WARN is printed and it degrades to console-only — it will not cause startup failure.
- Level priority: `--debug` > `logging.level`; adjustable at runtime via hot-reload.

## Check & troubleshooting

```bash
fnmusic-sync --check
curl -s --unix-socket /var/run/trim_music.socket http://localhost/_ext/healthz
# {"ok":true,"upstream":"ok",...}
ls -l /var/run/trim_music*.socket      # both should be srw-rw-rw-
tail -f /var/log/fnmusic-sync/fnmusic-sync.log
```

| Symptom                         | Cause & handling                                                                                                                                              |
| ------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Feiniu Music 502                | Insufficient socket permissions: nginx runs as `www-data`, the proxy socket must allow other access (`socket_mode: 0` inherits official perms, at least 0666) |
| Proxy receives no requests      | The official backend re-bound the original path after restart — restart the proxy to take over again                                                          |
| Scrobble not pushed             | Username key doesn't match fnOS `/user/me` return, credentials incomplete, or progress hasn't reached the threshold (verify with `30s` first)                 |
| Last.fm 401 INVALID TOKEN       | Token expired — re-login to Feiniu or redo the auth flow                                                                                                      |
| Log shows only console, no file | Log directory not writable — check the WARN at startup                                                                                                        |

## Build & release

```bash
make build-linux-amd64     # single architecture
make build-linux-all       # amd64/arm64 (tagged + untagged)
make package-linux-all     # deb + fpk
make clean-binaries        # clean bare binaries, keep tar.gz/packages
make clean-archives        # clean bare binaries + tar.gz, keep packages
```

The version number is determined by `git describe --tags` and injected into the `internal/buildinfo` package via `-ldflags -X`, so `fnmusic-sync version` shows the real version; a local `go build` without ldflags shows `dev`.

CI (`.cnb/workflows/release.yml`) builds and publishes a Release when a tag is cut, supports a `pre_release` switch, and the Release body uses a table to mark version number, release type and whether it is a pre-release.

## Directory structure

```text
cmd/fnmusic-sync/      main program (main.go / upgrade.go / logging.go / service.go / runtime.go / auth.go)
internal/buildinfo/   build info (version, etc., injected by Makefile -ldflags)
internal/config/      config loading, hot-reload, default config generation
internal/feiniu/      Feiniu Music API client (track index, playlist management)
internal/playback/    playback event parsing, scrobble decision, user identification and stats
internal/playlist/    ListenBrainz / Last.fm playlist sync service
internal/proxy/       socket takeover, HTTP reverse proxy, health check
internal/reqlog/      request logger
internal/scrobbler/   Last.fm / ListenBrainz client
internal/strutil/     string utilities
internal/webui/       Web config UI (fpk mode, unified gateway)
configs/              config examples
scripts/              install.sh (install/upgrade/uninstall) + postinstall.sh + preremove.sh
fpkg/                 fnOS app package (fpk) source
```

## License

This project is open-sourced under the [MIT License](LICENSE).

Copyright (c) 2026 dtapp
