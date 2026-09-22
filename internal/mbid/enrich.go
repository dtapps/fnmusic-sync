package mbid

import (
	"context"
	"database/sql"
	"log/slog"

	"cnb.cool/dtapp/fnmusic-sync/internal/datadb"
	"cnb.cool/dtapp/fnmusic-sync/internal/feiniu"
)

// EnrichIndexes 把已落库的 MBID 映射注入飞牛 TrackIndexes（填充 MBIDToGUID），
// 使 playlist 的 matchTrack 能优先走精确录音 MBID 匹配。
//
// 每个非空 MBID 字段都映射到同一 feiniu_guid；录音 MBID 是推荐歌单匹配的主键，
// 其余 MBID 作为补充（命中概率低但无害）。
func EnrichIndexes(ctx context.Context, store *datadb.Store, indexes *feiniu.TrackIndexes) error {
	if store == nil || indexes == nil {
		return nil
	}

	rows, err := store.ListTrackMBIDs(ctx)
	if err != nil {
		return err
	}

	for _, row := range rows {
		guid := row.FeiniuGuid
		if guid == "" {
			continue
		}
		add := func(m sql.NullString) {
			if m.Valid && m.String != "" {
				indexes.MBIDToGUID[m.String] = guid
			}
		}
		add(row.RecordingMbid)
		add(row.ReleaseMbid)
		add(row.ReleaseGroupMbid)
		add(row.ArtistMbid)
		add(row.WorkMbid)
	}

	if len(rows) > 0 {
		slog.Debug("MBID 索引已注入", "映射行数", len(rows), "MBIDToGUID", len(indexes.MBIDToGUID))
	}
	return nil
}
