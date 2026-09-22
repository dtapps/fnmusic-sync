-- name: UpsertRunStatus :one
INSERT INTO
  run_status (
    username,
    lastfm_enabled,
    listenbrainz_enabled,
    last_scrobbled_at,
    updated_at
  )
VALUES
  (
    sqlc.arg (username),
    sqlc.arg (lastfm_enabled),
    sqlc.arg (listenbrainz_enabled),
    sqlc.arg (last_scrobbled_at),
    sqlc.arg (updated_at)
  ) ON CONFLICT (username) DO
UPDATE
SET
  lastfm_enabled = excluded.lastfm_enabled,
  listenbrainz_enabled = excluded.listenbrainz_enabled,
  updated_at = excluded.updated_at RETURNING *;

-- name: IncrementScrobble :one
UPDATE run_status
SET
  lastfm_scrobbles = lastfm_scrobbles + CASE
    WHEN sqlc.arg (provider) = 'lastfm' THEN 1
    ELSE 0
  END,
  listenbrainz_scrobbles = listenbrainz_scrobbles + CASE
    WHEN sqlc.arg (provider) = 'listenbrainz' THEN 1
    ELSE 0
  END,
  total_scrobbles = total_scrobbles + 1,
  last_scrobbled_at = sqlc.arg (last_scrobbled_at),
  updated_at = sqlc.arg (updated_at)
WHERE
  username = sqlc.arg (username) RETURNING *;

-- name: GetRunStatus :one
SELECT
  *
FROM
  run_status
WHERE
  username = sqlc.arg (username);

-- name: ListRunStatus :many
SELECT
  *
FROM
  run_status
ORDER BY
  total_scrobbles DESC,
  username ASC;

-- name: GetRunStatusTotal :one
SELECT
  COALESCE(SUM(total_scrobbles), 0) AS total,
  COALESCE(SUM(lastfm_scrobbles), 0) AS lastfm,
  COALESCE(SUM(listenbrainz_scrobbles), 0) AS listenbrainz,
  COUNT(*) AS user_count
FROM
  run_status;

-- name: UpsertUser :one
INSERT INTO
  users (
    username,
    platform_uid,
    platform_username,
    is_admin,
    token_prefix,
    ua_raw,
    ua_system,
    ua_client,
    first_seen_at,
    last_seen_at,
    created_at,
    updated_at
  )
VALUES
  (
    sqlc.arg (username),
    sqlc.arg (platform_uid),
    sqlc.arg (platform_username),
    sqlc.arg (is_admin),
    sqlc.arg (token_prefix),
    sqlc.arg (ua_raw),
    sqlc.arg (ua_system),
    sqlc.arg (ua_client),
    sqlc.arg (first_seen_at),
    sqlc.arg (last_seen_at),
    sqlc.arg (created_at),
    sqlc.arg (updated_at)
  ) ON CONFLICT (token_prefix) DO
UPDATE
SET
  username = excluded.username,
  platform_uid = COALESCE(excluded.platform_uid, users.platform_uid),
  platform_username = COALESCE(
    excluded.platform_username,
    users.platform_username
  ),
  is_admin = users.is_admin,
  ua_raw = CASE
    WHEN excluded.ua_raw IS NOT NULL
    AND excluded.ua_raw != '' THEN excluded.ua_raw
    ELSE users.ua_raw
  END,
  ua_system = CASE
    WHEN excluded.ua_system IS NOT NULL
    AND excluded.ua_system != '' THEN excluded.ua_system
    ELSE users.ua_system
  END,
  ua_client = CASE
    WHEN excluded.ua_client IS NOT NULL
    AND excluded.ua_client != '' THEN excluded.ua_client
    ELSE users.ua_client
  END,
  last_seen_at = excluded.last_seen_at,
  updated_at = excluded.updated_at RETURNING *;

-- name: GetUser :one
SELECT
  *
FROM
  users
WHERE
  username = sqlc.arg (username);

-- name: ListUsers :many
SELECT
  *
FROM
  users
ORDER BY
  last_seen_at DESC;

-- name: TouchUserSeenByToken :exec
UPDATE users
SET
  last_seen_at = sqlc.arg (last_seen_at),
  updated_at = sqlc.arg (updated_at)
WHERE
  token_prefix = sqlc.arg (token_prefix);

-- name: BindUserPlatform :exec
UPDATE users
SET
  platform_uid = sqlc.arg (platform_uid),
  platform_username = sqlc.arg (platform_username),
  is_admin = sqlc.arg (is_admin),
  updated_at = sqlc.arg (updated_at)
WHERE
  username = sqlc.arg (username);

-- name: ListUsersWithUARaw :many
SELECT
  id,
  ua_raw
FROM
  users
WHERE
  ua_raw IS NOT NULL
  AND ua_raw != '';

-- name: SetUserAgent :exec
UPDATE users
SET
  ua_system = sqlc.arg (ua_system),
  ua_client = sqlc.arg (ua_client)
WHERE
  id = sqlc.arg (id);

-- name: InsertPlayback :one
INSERT INTO
  playback_log (
    username,
    token_prefix,
    guid,
    title,
    artist,
    album,
    duration_ms,
    started_at,
    ended_at,
    created_at
  )
VALUES
  (
    sqlc.arg (username),
    sqlc.arg (token_prefix),
    sqlc.arg (guid),
    sqlc.arg (title),
    sqlc.arg (artist),
    sqlc.arg (album),
    sqlc.arg (duration_ms),
    sqlc.arg (started_at),
    sqlc.arg (ended_at),
    sqlc.arg (created_at)
  ) RETURNING id;

-- name: ListPlaybacksByUser :many
SELECT
  id,
  username,
  token_prefix,
  guid,
  title,
  artist,
  album,
  duration_ms,
  started_at,
  ended_at,
  created_at
FROM
  playback_log
WHERE
  username = sqlc.arg (username)
ORDER BY
  started_at DESC
LIMIT
  sqlc.arg (limit_rows);

-- name: ListRecentPlaybacks :many
SELECT
  id,
  username,
  token_prefix,
  guid,
  title,
  artist,
  album,
  duration_ms,
  started_at,
  ended_at,
  created_at
FROM
  playback_log
ORDER BY
  started_at DESC
LIMIT
  sqlc.arg (limit_rows);

-- name: UpdatePlaybackEndedAt :exec
UPDATE playback_log
SET
  ended_at = sqlc.arg (ended_at)
WHERE
  id = sqlc.arg (id);

-- name: CloseOpenPlaybacks :exec
UPDATE playback_log
SET
  ended_at = sqlc.arg (ended_at)
WHERE
  ended_at IS NULL;

-- name: InsertSyncLog :one
INSERT INTO
  sync_log (
    username,
    provider,
    status,
    trigger,
    playlists,
    tracks,
    message,
    started_at,
    finished_at
  )
VALUES
  (
    sqlc.arg (username),
    sqlc.arg (provider),
    sqlc.arg (status),
    sqlc.arg (trigger),
    sqlc.arg (playlists),
    sqlc.arg (tracks),
    sqlc.arg (message),
    sqlc.arg (started_at),
    sqlc.arg (finished_at)
  ) RETURNING *;

-- name: GetLastSuccessTime :one
SELECT
  finished_at
FROM
  sync_log
WHERE
  username = sqlc.arg (username)
  AND provider = sqlc.arg (provider)
  AND status = 'success'
ORDER BY
  finished_at DESC
LIMIT
  1;

-- name: ListSyncLogsByUser :many
SELECT
  *
FROM
  sync_log
WHERE
  username = sqlc.arg (username)
ORDER BY
  finished_at DESC
LIMIT
  sqlc.arg (limit_rows);

-- name: ListRecentSyncLogs :many
SELECT
  *
FROM
  sync_log
ORDER BY
  finished_at DESC
LIMIT
  sqlc.arg (limit_rows);

-- name: UpsertTrackMBID :one
INSERT INTO
  track_mbid_map (
    feiniu_guid,
    file_path,
    title,
    artist,
    album,
    recording_mbid,
    release_mbid,
    release_group_mbid,
    artist_mbid,
    work_mbid,
    matched_by,
    updated_at
  )
VALUES
  (
    sqlc.arg (feiniu_guid),
    sqlc.arg (file_path),
    sqlc.arg (title),
    sqlc.arg (artist),
    sqlc.arg (album),
    sqlc.arg (recording_mbid),
    sqlc.arg (release_mbid),
    sqlc.arg (release_group_mbid),
    sqlc.arg (artist_mbid),
    sqlc.arg (work_mbid),
    sqlc.arg (matched_by),
    sqlc.arg (updated_at)
  ) ON CONFLICT (feiniu_guid) DO
UPDATE
SET
  file_path = excluded.file_path,
  title = excluded.title,
  artist = excluded.artist,
  album = excluded.album,
  recording_mbid = excluded.recording_mbid,
  release_mbid = excluded.release_mbid,
  release_group_mbid = excluded.release_group_mbid,
  artist_mbid = excluded.artist_mbid,
  work_mbid = excluded.work_mbid,
  matched_by = excluded.matched_by,
  updated_at = excluded.updated_at RETURNING *;

-- name: GetTrackMBID :one
SELECT
  *
FROM
  track_mbid_map
WHERE
  feiniu_guid = sqlc.arg (feiniu_guid);

-- name: GetTrackMBIDByPath :one
SELECT
  *
FROM
  track_mbid_map
WHERE
  file_path = sqlc.arg (file_path);

-- name: ListTrackMBIDs :many
SELECT
  *
FROM
  track_mbid_map
ORDER BY
  updated_at DESC;

-- name: DeleteTrackMBID :exec
DELETE FROM track_mbid_map
WHERE
  feiniu_guid = sqlc.arg (feiniu_guid);
