// Package datadb 封装 fnmusic-sync 的「数据」类持久化（MBID 映射），独立于 state.db。
//
// 与 internal/db（运营状态：run_status / users / playback_log / sync_log）分离，
// 本库只存「数据」：由本地音乐标签 / MusicBrainz 解析得到、关联到飞牛曲目 GUID 的
// MBID 映射（track_mbid_map）。驱动同样用 modernc.org/sqlite（纯 Go、无 CGO）。
package datadb

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaFS embed.FS

// Store 是 MBID 数据持久化层的统一入口，包裹 *sql.DB 与 sqlc 生成的 *Queries。
type Store struct {
	db *sql.DB
	q  *Queries
}

// Open 打开（或创建）data.db 并执行建表（幂等）。
func Open(path string) (*Store, error) {
	sqldb, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("打开数据库 %s 失败: %w", path, err)
	}
	// sqlite 单写者模型，限制单连接可避免 "database is locked"。
	sqldb.SetMaxOpenConns(1)
	sqldb.SetMaxIdleConns(1)

	schemaBytes, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		_ = sqldb.Close()
		return nil, fmt.Errorf("读取内置 schema 失败: %w", err)
	}
	if _, err := sqldb.ExecContext(context.Background(), string(schemaBytes)); err != nil {
		_ = sqldb.Close()
		return nil, fmt.Errorf("初始化表结构失败: %w", err)
	}

	return &Store{db: sqldb, q: New(sqldb)}, nil
}

// Close 关闭底层数据库连接。
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Queries 返回 sqlc 生成的查询接口。
func (s *Store) Queries() *Queries { return s.q }

// Raw 返回底层 *sql.DB（迁移等高级用法直接使用）。
func (s *Store) Raw() *sql.DB { return s.db }

// TrackMBID 描述一条「飞牛曲目 GUID ↔ MBID」映射的写入意图。
// 对应 schema 的 track_mbid_map 表，查询返回直接用 sqlc 生成的 TrackMbidMap 结构。
type TrackMBID struct {
	FeiniuGUID       string
	FilePath         string
	Title            string
	Artist           string
	Album            string
	RecordingMBID    string // 录音 MBID（匹配优先级最高）
	ReleaseMBID      string // 专辑发行 MBID
	ReleaseGroupMBID string // 专辑组 MBID
	ArtistMBID       string // 艺人 MBID
	WorkMBID         string // 作品 MBID
	MatchedBy        string // tag | musicbrainz | manual
}

// SaveTrackMBID 写入/更新一条曲目 MBID 映射（按 feiniu_guid 幂等）。
// feiniu_guid 为空时直接跳过（映射必须关联到具体飞牛曲目）。
func (s *Store) SaveTrackMBID(m TrackMBID) error {
	if s == nil || s.q == nil || m.FeiniuGUID == "" {
		return nil
	}
	if m.MatchedBy == "" {
		m.MatchedBy = "tag"
	}
	now := time.Now().Format(time.RFC3339)
	_, err := s.q.UpsertTrackMBID(context.Background(), UpsertTrackMBIDParams{
		FeiniuGuid:       m.FeiniuGUID,
		FilePath:         nullStr(m.FilePath),
		Title:            nullStr(m.Title),
		Artist:           nullStr(m.Artist),
		Album:            nullStr(m.Album),
		RecordingMbid:    nullStr(m.RecordingMBID),
		ReleaseMbid:      nullStr(m.ReleaseMBID),
		ReleaseGroupMbid: nullStr(m.ReleaseGroupMBID),
		ArtistMbid:       nullStr(m.ArtistMBID),
		WorkMbid:         nullStr(m.WorkMBID),
		MatchedBy:        m.MatchedBy,
		UpdatedAt:        now,
	})
	return err
}

// GetTrackMBID 按飞牛 GUID 查询映射。
func (s *Store) GetTrackMBID(ctx context.Context, guid string) (TrackMbidMap, error) {
	if s == nil || s.q == nil {
		return TrackMbidMap{}, nil
	}
	return s.q.GetTrackMBID(ctx, guid)
}

// GetTrackMBIDByPath 按来源文件路径查询映射（反查 / 调试用）。
func (s *Store) GetTrackMBIDByPath(ctx context.Context, path string) (TrackMbidMap, error) {
	if s == nil || s.q == nil {
		return TrackMbidMap{}, nil
	}
	return s.q.GetTrackMBIDByPath(ctx, nullStr(path))
}

// ListTrackMBIDs 返回全部映射（按更新时间降序），供 EnrichIndexes 注入 MBIDToGUID。
func (s *Store) ListTrackMBIDs(ctx context.Context) ([]TrackMbidMap, error) {
	if s == nil || s.q == nil {
		return nil, nil
	}
	return s.q.ListTrackMBIDs(ctx)
}

// DeleteTrackMBID 删除某飞牛曲目的 MBID 映射。
func (s *Store) DeleteTrackMBID(ctx context.Context, guid string) error {
	if s == nil || s.q == nil {
		return nil
	}
	return s.q.DeleteTrackMBID(ctx, guid)
}

// nullStr 把 Go 字符串转成可空的 sql.NullString（空串视为 NULL）。
func nullStr(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}
