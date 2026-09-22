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
