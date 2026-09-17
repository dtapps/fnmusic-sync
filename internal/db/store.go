// Package db 封装 fnmusic-sync 的 sqlite 持久化层（sqlc 生成代码 + 运行期封装）。
//
// 驱动使用 modernc.org/sqlite（纯 Go、无 CGO），适配 fpk/fnOS 部署。
// 表结构见 schema.sql，查询见 queries.sql（由 sqlc 生成 queries.sql.go）。
package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaFS embed.FS

// Store 是持久化层的统一入口，包裹 *sql.DB 与 sqlc 生成的 *Queries。
type Store struct {
	db *sql.DB
	q  *Queries
}

// Open 打开（或创建）sqlite 数据库并执行建表（幂等）。
// path 为数据库文件路径，如 /var/lib/fnmusic-sync/state.db。
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

// Queries 返回 sqlc 生成的查询接口（高级用法可直接调用）。
func (s *Store) Queries() *Queries { return s.q }

// Raw 返回底层 *sql.DB（极少需要直接使用）。
func (s *Store) Raw() *sql.DB { return s.db }

// UserUpsert 描述一次用户行的 upsert 意图。
// 平台相关字段（PlatformUID/PlatformUsername/IsAdmin）与 UA 字段均可选：
// 只传音乐侧字段时，平台字段经 COALESCE 保留原有值（见 queries.sql 的 UpsertUser）。
type UserUpsert struct {
	Username         string
	PlatformUID      string
	PlatformUsername string
	IsAdmin          bool
	TokenPrefix      string
	UARaw            string
	UASystem         string
	UAClient         string
}

// UpsertUser 写入/刷新用户行（音乐身份 + 平台绑定 + UA + 时间）。
func (s *Store) UpsertUser(u UserUpsert) error {
	if s == nil {
		return nil
	}
	now := time.Now().Format(time.RFC3339)
	_, err := s.q.UpsertUser(context.Background(), UpsertUserParams{
		Username:         u.Username,
		PlatformUid:      nullStr(u.PlatformUID),
		PlatformUsername: nullStr(u.PlatformUsername),
		IsAdmin:          boolToInt(u.IsAdmin),
		TokenPrefix:      nullStr(u.TokenPrefix),
		UaRaw:            nullStr(u.UARaw),
		UaSystem:         nullStr(u.UASystem),
		UaClient:         nullStr(u.UAClient),
		FirstSeenAt:      now,
		LastSeenAt:       now,
		CreatedAt:        now,
		UpdatedAt:        now,
	})
	return err
}

// BindUserPlatform 把 fnOS 平台身份（uid / 用户名 / 是否管理员）绑定到指定音乐用户。
// 注意：这是 UPDATE，仅在该音乐用户行已存在时生效（由 /user/me 经代理先行创建）。
func (s *Store) BindUserPlatform(username, uid, pusername string, isAdmin bool) error {
	if s == nil {
		return nil
	}
	return s.q.BindUserPlatform(context.Background(), BindUserPlatformParams{
		PlatformUid:      nullStr(uid),
		PlatformUsername: nullStr(pusername),
		IsAdmin:          boolToInt(isAdmin),
		UpdatedAt:        time.Now().Format(time.RFC3339),
		Username:         username,
	})
}

// UpsertRunStatus 写入/刷新某用户的运行状态（启用标志 + 最近推送时间）。
func (s *Store) UpsertRunStatus(username string, lastfm, listenbrainz bool, lastScrobbledAt sql.NullString) error {
	if s == nil {
		return nil
	}
	_, err := s.q.UpsertRunStatus(context.Background(), UpsertRunStatusParams{
		Username:            username,
		LastfmEnabled:       boolToInt(lastfm),
		ListenbrainzEnabled: boolToInt(listenbrainz),
		LastScrobbledAt:     lastScrobbledAt,
		UpdatedAt:           time.Now().Format(time.RFC3339),
	})
	return err
}

// IncrementScrobble 累加某用户在某平台的推送次数（total_scrobbles 同步 +1）。
// provider 取值应与 queries.sql 中 CASE 一致："lastfm" / "listenbrainz"。
func (s *Store) IncrementScrobble(username, provider string) error {
	if s == nil {
		return nil
	}
	now := time.Now().Format(time.RFC3339)
	_, err := s.q.IncrementScrobble(context.Background(), IncrementScrobbleParams{
		Provider:        provider,
		LastScrobbledAt: nullStr(now),
		UpdatedAt:       now,
		Username:        username,
	})
	return err
}

// ListRunStatus 返回全部用户运行状态，按 total_scrobbles 降序。
func (s *Store) ListRunStatus(ctx context.Context) ([]RunStatus, error) {
	if s == nil {
		return nil, nil
	}
	return s.q.ListRunStatus(ctx)
}

// ListUsers 返回全部用户，按 last_seen_at 降序（最近活跃在前）。
func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	if s == nil {
		return nil, nil
	}
	return s.q.ListUsers(ctx)
}

// nullStr 把 Go 字符串转成可空的 sql.NullString（空串视为 NULL）。
func nullStr(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

// boolToInt 把布尔转成 sqlite 的 0/1 int。
func boolToInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// ParseUserAgent 从 User-Agent 中尽力解析出系统（ua_system）与客户端（ua_client）。
// 仅做粗粒度识别，无法识别时回退到 UA 首段（截断 24 字符）。
func ParseUserAgent(ua string) (system, client string) {
	ua = strings.TrimSpace(ua)
	if ua == "" {
		return "", ""
	}

	switch {
	case strings.Contains(ua, "iPhone"), strings.Contains(ua, "iPad"), strings.Contains(ua, "iPod"):
		system = "iOS"
	case strings.Contains(ua, "Android"):
		system = "Android"
	case strings.Contains(ua, "Windows"):
		system = "Windows"
	case strings.Contains(ua, "Macintosh"), strings.Contains(ua, "Mac OS"), strings.Contains(ua, "Mac OS X"):
		system = "macOS"
	case strings.Contains(ua, "CrOS"):
		system = "ChromeOS"
	case strings.Contains(ua, "Linux"):
		system = "Linux"
	default:
		system = "其他"
	}

	switch {
	case strings.Contains(ua, "Flutter"):
		client = "Flutter"
	case strings.Contains(ua, "okhttp"):
		client = "okhttp"
	case strings.Contains(ua, "libmpv"):
		client = "libmpv"
	case strings.Contains(ua, "curl"):
		client = "curl"
	case strings.Contains(ua, "Java"):
		client = "Java"
	case strings.Contains(ua, "Postman"):
		client = "Postman"
	case strings.Contains(ua, "Mozilla"), strings.Contains(ua, "Chrome"), strings.Contains(ua, "Safari"):
		client = "浏览器"
	default:
		client = strings.SplitN(ua, "/", 2)[0]
		if len(client) > 24 {
			client = client[:24]
		}
	}
	return system, client
}
