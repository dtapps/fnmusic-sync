-- fnmusic-sync「数据」持久化 schema（sqlc + sqlite3）
-- 本文件只放「数据」类表：由本地音乐标签 / MusicBrainz 解析得到、
-- 关联飞牛曲目 GUID 的 MBID 映射。与 state.db（运营状态）分离，便于独立维护。
-- 引擎：sqlite（运行时驱动用 modernc.org/sqlite，纯 Go、无 CGO，适配 fpk/fnOS 部署）

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
