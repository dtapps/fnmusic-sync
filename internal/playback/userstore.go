package playback

import (
	"context"
	"database/sql"
	"log/slog"

	"cnb.cool/dtapp/fnmusic-sync/internal/db"
)

// UserState 单个用户的运行状态（Scrobble 统计 + 启用标志）。
// 持久化在 run_status 表（每个音乐用户一行）。
type UserState struct {
	LastFMEnabled       bool
	ListenBrainzEnabled bool
	Scrobbles           int
	LastScrobbledAt     string
}

// UserStore 用户统计的持久化层。底层为 sqlite（见 internal/db）。
// 原先基于 YAML 文件，现改为数据库，调用方接口（Ensure/RecordScrobble/Save）保持不变。
type UserStore struct {
	db     *db.Store
	logger *slog.Logger
}

// NewUserStore 用已有的 db.Store 构造持久化层。
func NewUserStore(store *db.Store, logger *slog.Logger) *UserStore {
	return &UserStore{db: store, logger: logger}
}

// Get 读取某用户的运行状态；不存在时返回零值。
func (s *UserStore) Get(name string) UserState {
	if s.db == nil {
		return UserState{}
	}
	rs, err := s.db.Queries().GetRunStatus(context.Background(), name)
	if err != nil {
		return UserState{}
	}
	return UserState{
		LastFMEnabled:       rs.LastfmEnabled != 0,
		ListenBrainzEnabled: rs.ListenbrainzEnabled != 0,
		Scrobbles:           int(rs.TotalScrobbles),
		LastScrobbledAt:     rs.LastScrobbledAt.String,
	}
}

// Ensure 写入/刷新用户启用标志与最近推送时间，并返回当前状态。
func (s *UserStore) Ensure(name string, lastfm, listenbrainz bool) UserState {
	if s.db == nil {
		return UserState{LastFMEnabled: lastfm, ListenBrainzEnabled: listenbrainz}
	}
	_ = s.db.UpsertRunStatus(name, lastfm, listenbrainz, sql.NullString{})
	return s.Get(name)
}

// RecordScrobble 累加某用户在某平台的推送次数。provider 为 "lastfm"/"listenbrainz"。
func (s *UserStore) RecordScrobble(name, provider string) {
	if s.db == nil {
		return
	}
	if err := s.db.IncrementScrobble(name, provider); err != nil && s.logger != nil {
		s.logger.Warn("记录 scrobble 失败", "user", name, "provider", provider, "error", err)
	}
}

// UpsertUser 持久化用户身份（音乐用户名 + 平台绑定 + UA + 时间）。
func (s *UserStore) UpsertUser(u db.UserUpsert) error {
	if s.db == nil {
		return nil
	}
	return s.db.UpsertUser(u)
}

// Save 历史兼容：数据库为实时写入，无需退出时集中落盘。
func (s *UserStore) Save() {}
