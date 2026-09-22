-- fnmusic-sync 持久化 schema（sqlc + sqlite3）
-- 引擎：sqlite（运行时驱动用 modernc.org/sqlite，纯 Go、无 CGO，适配 fpk/fnOS 部署）
-- ============================================================
-- 表 1：运行状态（run_status）
-- 每行一个音乐用户，记录其 Scrobble 推送统计。
-- total_scrobbles 由 IncrementScrobble 维护。
-- ============================================================
CREATE TABLE IF NOT EXISTS run_status (
  username TEXT NOT NULL PRIMARY KEY,
  lastfm_scrobbles INTEGER NOT NULL DEFAULT 0,
  listenbrainz_scrobbles INTEGER NOT NULL DEFAULT 0,
  total_scrobbles INTEGER NOT NULL DEFAULT 0,
  lastfm_enabled INTEGER NOT NULL DEFAULT 0,
  listenbrainz_enabled INTEGER NOT NULL DEFAULT 0,
  last_scrobbled_at TEXT,
  updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_run_status_total ON run_status (total_scrobbles DESC);

-- ============================================================
-- 表 2：用户列表（users）
-- 一行 = 一个【客户端 token】：同一音乐账号可在多个客户端登录，
-- 每个客户端持有各自的 music-token，故按 token 分别记录、互不覆盖，
-- 这样"已识别用户"列表就能一个客户端一行（各自保留设备与首/末时间）。
--
-- 字段说明：
--   · token_prefix：music-token 的脱敏前缀（前 8 位），本表唯一键，
--     既是区分各客户端会话的依据，也是列表展示的 "Token 标识"。
--   · username：【音乐用户】名，由 /user/me 解析（scrobble 归属主体）。
--   · 平台用户：fnOS 网关在打开 Web UI 时通过 Header 注入
--     （X-Trim-Userid / X-Trim-Username / X-Trim-Isadmin），用于区分管理员 / 平台用户；
--     平台绑定按【音乐用户名】记忆（BindUserPlatform WHERE username），
--     对同一账号的所有 token 行统一生效。
-- ============================================================
CREATE TABLE IF NOT EXISTS users (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  username TEXT NOT NULL,
  platform_uid TEXT,
  platform_username TEXT,
  is_admin INTEGER NOT NULL DEFAULT 0,
  token_prefix TEXT NOT NULL UNIQUE,
  ua_raw TEXT,
  ua_system TEXT,
  ua_client TEXT,
  first_seen_at TEXT NOT NULL,
  last_seen_at TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_users_last_seen ON users (last_seen_at DESC);

CREATE INDEX IF NOT EXISTS idx_users_admin ON users (is_admin);

-- 平台绑定按用户名 UPDATE（BindUserPlatform WHERE username），需要该索引。
CREATE INDEX IF NOT EXISTS idx_users_username ON users (username);

-- ============================================================
-- 表 3：播放记录（playback_log）
-- 通过代理流量检测用户"在播什么"，落库并关联【音乐用户】。
-- 一首歌开始播放时插入一行（started_at）；无真实"播完"事件（飞牛只上报
-- track_play，没有 pause/stop/end），故 ended_at 通常为空，仅在"下一首开始"
-- 或"进程退出"时回填为上一首的结束时刻。
-- ============================================================
CREATE TABLE IF NOT EXISTS playback_log (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  username TEXT NOT NULL, -- 音乐用户名（来自 /user/me）
  token_prefix TEXT, -- 触发该次播放的客户端 token 前缀（多端区分）
  guid TEXT, -- 曲目 guid（飞牛内部标识）
  title TEXT NOT NULL, -- 标题
  artist TEXT, -- 艺人
  album TEXT, -- 专辑
  duration_ms INTEGER, -- 时长（毫秒）
  started_at TEXT NOT NULL, -- 开始播放时刻（RFC3339）
  ended_at TEXT, -- 结束时刻；未知/进行中为 NULL
  created_at TEXT NOT NULL -- 入库时刻（RFC3339）
);

CREATE INDEX IF NOT EXISTS idx_playback_user_started ON playback_log (username, started_at DESC);

CREATE INDEX IF NOT EXISTS idx_playback_started ON playback_log (started_at DESC);

-- ============================================================
-- 表 4：同步记录（sync_log）
-- 每次 ListenBrainz / Last.fm 歌单同步（成功 / 失败）落库，
-- 关联【音乐用户】与 provider（listenbrainz / lastfm）。
-- 用于 Web UI「同步列表」展示；并在每次同步前查询该用户+provider 的
-- 上一次成功时间，据此跨重启 / 跨重复触发地约束同步间隔。
-- ============================================================
CREATE TABLE IF NOT EXISTS sync_log (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  username TEXT NOT NULL,
  provider TEXT NOT NULL,
  status TEXT NOT NULL,
  trigger TEXT NOT NULL DEFAULT 'interval',
  playlists INTEGER NOT NULL DEFAULT 0,
  tracks INTEGER NOT NULL DEFAULT 0,
  message TEXT,
  started_at TEXT NOT NULL,
  finished_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_sync_log_user_provider ON sync_log (username, provider, finished_at DESC);

CREATE INDEX IF NOT EXISTS idx_sync_log_finished ON sync_log (finished_at DESC);

-- ============================================================
-- 表 5：曲目 MBID 映射（track_mbid_map）
-- 把外部 MusicBrainz ID（录音 / 专辑 / 艺人 / 作品 MBID）关联到飞牛曲目 GUID，
-- 让 scrobble / 歌单匹配优先走精确的「录音 MBID」(优于「艺人名+曲名」模糊匹配)。
--
-- 数据来源（matched_by）：
--   · tag：解析本地音乐文件标签（ID3/FLAC/M4A 中的 MusicBrainz 标签）获取，零网络、无频限；
--   · musicbrainz：本地无标签时，用「艺人+曲名(+专辑)」查询 MusicBrainz API 补全；
--   · manual：手动维护。
--
-- 解析器（mbid_resolver）流程：
--   1. 扫描 library.directories 得到 文件绝对路径 → 标签中的 MBID；
--   2. 按飞牛曲目 AudioSpec.Path（路径匹配，优先）或 元数据（艺人+曲名，兜底）匹配到 feiniu_guid；
--   3. 以 feiniu_guid 为主键写入本表；BuildTrackIndexes 加载时优先取 MBID 填 MBIDToGUID。
--
-- feiniu_guid 为主键（NOT NULL）：一行 = 一个飞牛曲目与其 MBID；未匹配到飞牛曲目的文件
-- 在解析器内用「路径→MBID」内存映射暂存，确定 feiniu_guid 后再落库，避免无主键的孤立行。
-- ============================================================
CREATE TABLE IF NOT EXISTS track_mbid_map (
  feiniu_guid TEXT NOT NULL PRIMARY KEY, -- 飞牛曲目 GUID（关联主体）
  file_path TEXT, -- 来源文件绝对路径（路径匹配用，索引便于反查）
  title TEXT, -- 曲目标题（元数据匹配备用）
  artist TEXT, -- 艺人名
  album TEXT, -- 专辑名
  recording_mbid TEXT, -- 录音 MBID（recording，匹配优先级最高）
  release_mbid TEXT, -- 专辑发行 MBID（release）
  release_group_mbid TEXT, -- 专辑组 MBID（release-group）
  artist_mbid TEXT, -- 艺人 MBID（artist）
  work_mbid TEXT, -- 作品 MBID（work）
  matched_by TEXT NOT NULL DEFAULT 'tag', -- tag | musicbrainz | manual
  updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_mbid_map_path ON track_mbid_map (file_path);

CREATE INDEX IF NOT EXISTS idx_mbid_map_meta ON track_mbid_map (title, artist);
