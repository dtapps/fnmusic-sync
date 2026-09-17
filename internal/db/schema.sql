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
