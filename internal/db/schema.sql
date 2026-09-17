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
-- 一行 = 一个"用户绑定"：把【音乐用户】与【平台用户】关联起来，
-- 两边身份独立、必须分别记录，不可混用：
--   · 音乐用户：music-token → /user/me 解析出的音乐用户名（本表主键，scrobble 归属主体）。
--   · 平台用户：fnOS 网关在打开 Web UI 时通过 Header 注入
--     （X-Trim-Userid / X-Trim-Username / X-Trim-Isadmin），用于区分管理员 / 平台用户。
-- 绑定时机：打开 Web UI 的请求同时带 music-token cookie（音乐身份）与 X-Trim-* Header（平台身份），
--           解析出音乐用户名后写进同一行，完成"两个用户"的绑定。
-- ============================================================
CREATE TABLE IF NOT EXISTS users (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  username TEXT NOT NULL UNIQUE,
  platform_uid TEXT,
  platform_username TEXT,
  is_admin INTEGER NOT NULL DEFAULT 0,
  token_prefix TEXT,
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
