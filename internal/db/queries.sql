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
  last_scrobbled_at = excluded.last_scrobbled_at,
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
  ua_raw = COALESCE(excluded.ua_raw, users.ua_raw),
  ua_system = COALESCE(excluded.ua_system, users.ua_system),
  ua_client = COALESCE(excluded.ua_client, users.ua_client),
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

-- name: TouchUserSeen :one
UPDATE users
SET
  last_seen_at = sqlc.arg (last_seen_at),
  updated_at = sqlc.arg (updated_at)
WHERE
  username = sqlc.arg (username) RETURNING *;

-- name: BindUserPlatform :exec
UPDATE users
SET
  platform_uid = sqlc.arg (platform_uid),
  platform_username = sqlc.arg (platform_username),
  is_admin = sqlc.arg (is_admin),
  updated_at = sqlc.arg (updated_at)
WHERE
  username = sqlc.arg (username);
