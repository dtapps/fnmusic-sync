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

	store := &Store{db: sqldb, q: New(sqldb)}

	// 旧版本 users 表以 username 为唯一键，会把同一账号的多个 token 覆盖成一行；
	// 这里在检测到旧结构时迁移为按 token_prefix 唯一（保留数据，幂等）。
	if err := store.migrateUsersTokenKey(context.Background()); err != nil {
		_ = sqldb.Close()
		return nil, err
	}

	// 升级 UA 解析规则后，用新规则回填历史行的系统/客户端（best-effort，不阻断启动）。
	if _, err := store.ReparseUserAgents(context.Background()); err != nil {
		_ = sqldb.Close()
		return nil, err
	}

	return store, nil
}

// migrateUsersTokenKey 把 users 表从「按 username 唯一」的旧结构迁移为「按 token_prefix 唯一」。
//
// 背景：早期 users 表以 username 为唯一键，同一音乐账号在多个客户端登录时，
// 各 token 会被 upsert 覆盖进同一行，导致「已识别用户」列表只显示一个。
// 现改为一行一个 token（见 schema.sql）。sqlite 无法直接删除列上的 UNIQUE 约束，
// 故在检测到旧结构时重建表并保留数据；已是新结构则直接跳过（幂等）。
func (s *Store) migrateUsersTokenKey(ctx context.Context) error {
	has, err := s.hasUniqueIndexOn(ctx, "users", "token_prefix")
	if err != nil {
		return fmt.Errorf("检测 users 表结构失败: %w", err)
	}
	if has {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("迁移 users 表失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmts := []string{
		// 旧索引随表重命名保留原名，先删除以免与新表索引重名冲突。
		`DROP INDEX IF EXISTS idx_users_last_seen`,
		`DROP INDEX IF EXISTS idx_users_admin`,
		`DROP INDEX IF EXISTS idx_users_username`,
		`ALTER TABLE users RENAME TO users_old`,
		`CREATE TABLE users (
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
		)`,
		// 仅迁移带 token 前缀的行（recordUserIdentity 始终写入 token_prefix）。
		`INSERT INTO users (
			id, username, platform_uid, platform_username, is_admin, token_prefix,
			ua_raw, ua_system, ua_client, first_seen_at, last_seen_at, created_at, updated_at
		)
		SELECT
			id, username, platform_uid, platform_username, is_admin, token_prefix,
			ua_raw, ua_system, ua_client, first_seen_at, last_seen_at, created_at, updated_at
		FROM users_old
		WHERE token_prefix IS NOT NULL`,
		`DROP TABLE users_old`,
		`CREATE INDEX IF NOT EXISTS idx_users_last_seen ON users (last_seen_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_users_admin ON users (is_admin)`,
		`CREATE INDEX IF NOT EXISTS idx_users_username ON users (username)`,
	}

	for _, stmt := range stmts {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("迁移 users 表失败: %w", err)
		}
	}

	return tx.Commit()
}

// hasUniqueIndexOn 判断表上是否存在包含指定列的唯一索引。
func (s *Store) hasUniqueIndexOn(ctx context.Context, table, column string) (bool, error) {
	names, err := s.uniqueIndexNames(ctx, table)
	if err != nil {
		return false, err
	}
	for _, name := range names {
		ok, err := s.indexHasColumn(ctx, name, column)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

// uniqueIndexNames 返回表上全部唯一索引名（含 sqlite_autoindex_*）。
func (s *Store) uniqueIndexNames(ctx context.Context, table string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, "PRAGMA index_list("+quoteIdent(table)+")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		vals, err := scanRow(rows)
		if err != nil {
			return nil, err
		}
		// PRAGMA index_list 列：seq, name, unique, origin, partial（旧版本仅前 3 列）。
		if len(vals) < 3 {
			continue
		}
		name, _ := vals[1].(string)
		if name == "" || !sqliteBool(vals[2]) {
			continue
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

// indexHasColumn 判断索引是否包含指定列。
func (s *Store) indexHasColumn(ctx context.Context, index, column string) (bool, error) {
	rows, err := s.db.QueryContext(ctx, "PRAGMA index_info("+quoteIdent(index)+")")
	if err != nil {
		return false, err
	}
	defer rows.Close()

	for rows.Next() {
		vals, err := scanRow(rows)
		if err != nil {
			return false, err
		}
		// PRAGMA index_info 列：seqno, cid, name。
		if len(vals) < 3 {
			continue
		}
		if name, _ := vals[2].(string); name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

// scanRow 把当前行读成 []any（列数动态）。
func scanRow(rows *sql.Rows) ([]any, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return nil, err
	}
	return vals, nil
}

// sqliteBool 把 sqlite 返回的 0/1 整数或布尔值统一成 bool。
func sqliteBool(v any) bool {
	switch t := v.(type) {
	case int64:
		return t != 0
	case bool:
		return t
	default:
		return false
	}
}

// quoteIdent 用单引号包裹 SQL 标识符，供 PRAGMA 函数参数使用。
func quoteIdent(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
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
		TokenPrefix:      u.TokenPrefix,
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

// TouchUserSeenByToken 按 token 前缀刷新 last_seen_at / updated_at（活跃心跳）。
//
// 用 token_prefix（而非 username）是因为同一音乐账号可能有多个 token 行，
// 按 token 刷新才能精确反映"是哪个客户端"最后一次活动，不会把同账号其它设备一起带动。
// 仅更新已存在的行（UPDATE ... WHERE token_prefix），不负责创建行——
// 创建在 recordUserIdentity → UpsertUser 完成，这里只做"活跃心跳"。
// 查询定义在 queries.sql（TouchUserSeenByToken），由 sqlc 生成。
func (s *Store) TouchUserSeenByToken(tokenPrefix string) error {
	if s == nil || s.q == nil || tokenPrefix == "" {
		return nil
	}
	now := time.Now().Format(time.RFC3339)
	return s.q.TouchUserSeenByToken(context.Background(), TouchUserSeenByTokenParams{
		LastSeenAt:  now,
		UpdatedAt:   now,
		TokenPrefix: tokenPrefix,
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

// PlaybackRecord 描述一次"开始播放"的写入意图。
// 关联音乐用户名（username），并记录曲目元数据与开始时刻；
// 结束时刻（ended_at）由下一首开始或进程退出时回填，进行中的播放为 NULL。
type PlaybackRecord struct {
	Username    string
	TokenPrefix string
	GUID        string
	Title       string
	Artist      string
	Album       string
	Duration    time.Duration
	StartedAt   time.Time
}

// RecordPlayback 写入一条播放记录，返回自增主键 id（用于后续回填 ended_at）。
func (s *Store) RecordPlayback(p PlaybackRecord) (int64, error) {
	if s == nil || s.q == nil {
		return 0, nil
	}
	now := time.Now().Format(time.RFC3339)
	started := now
	if !p.StartedAt.IsZero() {
		started = p.StartedAt.Format(time.RFC3339)
	}
	return s.q.InsertPlayback(context.Background(), InsertPlaybackParams{
		Username:    p.Username,
		TokenPrefix: nullStr(p.TokenPrefix),
		Guid:        nullStr(p.GUID),
		Title:       p.Title,
		Artist:      nullStr(p.Artist),
		Album:       nullStr(p.Album),
		DurationMs:  sql.NullInt64{Int64: p.Duration.Milliseconds(), Valid: true},
		StartedAt:   started,
		EndedAt:     sql.NullString{},
		CreatedAt:   now,
	})
}

// ListPlaybacksByUser 返回某用户的播放记录，按 started_at 降序（最近播放在前）。
func (s *Store) ListPlaybacksByUser(ctx context.Context, username string, limit int64) ([]PlaybackLog, error) {
	if s == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 100
	}
	return s.q.ListPlaybacksByUser(ctx, ListPlaybacksByUserParams{Username: username, LimitRows: limit})
}

// ListRecentPlaybacks 返回全部用户的最近播放记录（管理员总览用）。
func (s *Store) ListRecentPlaybacks(ctx context.Context, limit int64) ([]PlaybackLog, error) {
	if s == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 200
	}
	return s.q.ListRecentPlaybacks(ctx, limit)
}

// ClosePlayback 回填某条播放记录的结束时刻（下一首开始 / 进程退出时调用）。
func (s *Store) ClosePlayback(id int64, endedAt string) error {
	if s == nil || s.q == nil || id <= 0 {
		return nil
	}
	return s.q.UpdatePlaybackEndedAt(context.Background(), UpdatePlaybackEndedAtParams{
		EndedAt: nullStr(endedAt),
		ID:      id,
	})
}

// CloseOpenPlaybacks 回填所有仍未结束（ended_at IS NULL）的播放记录，
// 进程退出时调用，把"进行中"的播放统一标记为在给定时刻结束。
func (s *Store) CloseOpenPlaybacks(endedAt string) error {
	if s == nil || s.q == nil {
		return nil
	}
	return s.q.CloseOpenPlaybacks(context.Background(), nullStr(endedAt))
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
//
// 飞牛音乐 App 的 UA 形如：
//
//	1.0.1 (com.trim.music.ios; build:101163; iOS 17.7.11) Flutter/3.10.9
//
// 其中 com.trim.music.<平台> 与 "; <系统> <版本>)" 均携带系统信息；
// 纯 Dart 客户端（如 Dart/3.12 (dart:io)）不含系统信息，只能回退到「其他」。
// 无法识别时回退到 UA 首段（截断 24 字符）。
// ReparseUserAgents 用最新的 ParseUserAgent 规则重算所有已存用户的
// ua_system / ua_client（ua_raw 仍保留原始 UA）。用于升级解析规则后回填
// 历史数据——UpsertUser 仅在首次写入时落 UA，旧行不会随规则改进自动更正。
// 返回被更新的行数。失败不阻断启动（best-effort）。
// 查询定义在 queries.sql（ListUsersWithUARaw / SetUserAgent），由 sqlc 生成。
func (s *Store) ReparseUserAgents(ctx context.Context) (int, error) {
	if s == nil || s.q == nil {
		return 0, nil
	}
	recs, err := s.q.ListUsersWithUARaw(ctx)
	if err != nil {
		return 0, err
	}
	updated := 0
	for _, r := range recs {
		sys, cli := ParseUserAgent(r.UaRaw.String)
		if err := s.q.SetUserAgent(ctx, SetUserAgentParams{
			UaSystem: nullStr(sys),
			UaClient: nullStr(cli),
			ID:       r.ID,
		}); err != nil {
			return updated, err
		}
		updated++
	}
	return updated, nil
}

func ParseUserAgent(ua string) (system, client string) {
	ua = strings.TrimSpace(ua)
	if ua == "" {
		return "", ""
	}

	// 1) 飞牛音乐 App：com.trim.music.<平台>; ...; <系统> <版本>)
	if idx := strings.Index(ua, "com.trim.music."); idx >= 0 {
		system = parseFnOSPlatform(ua[idx:])
		// 优先用 "; <系统> <版本>)" 里的系统名（更精确，如 iPadOS / HarmonyOS）。
		if os := parseFnOSOSName(ua); os != "" {
			system = os
		}
	}

	// 2) 通用关键字兜底（覆盖浏览器、系统 webview、okhttp 等）。
	if system == "" {
		switch {
		case strings.Contains(ua, "iPhone"), strings.Contains(ua, "iPad"),
			strings.Contains(ua, "iPod"), strings.Contains(ua, "iOS"):
			system = "iOS"
		case strings.Contains(ua, "Android"):
			system = "Android"
		case strings.Contains(ua, "Windows"):
			system = "Windows"
		case strings.Contains(ua, "Macintosh"), strings.Contains(ua, "Mac OS"),
			strings.Contains(ua, "Mac OS X"), strings.Contains(ua, "macOS"):
			system = "macOS"
		case strings.Contains(ua, "CrOS"):
			system = "ChromeOS"
		case strings.Contains(ua, "HarmonyOS"), strings.Contains(ua, "harmony"):
			system = "HarmonyOS"
		case strings.Contains(ua, "Linux"):
			system = "Linux"
		default:
			system = "其他"
		}
	}

	// 客户端
	switch {
	case strings.Contains(ua, "Flutter"):
		client = "Flutter"
		if v := splitToken(ua, "Flutter/"); v != "" {
			client = "Flutter " + v
		}
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

// parseFnOSPlatform 从 "com.trim.music.<平台>; ..." 片段解析系统。
func parseFnOSPlatform(seg string) string {
	end := strings.IndexAny(seg, "; ")
	if end < 0 {
		end = len(seg)
	}
	plat := seg[len("com.trim.music."):end]
	switch {
	case strings.Contains(plat, "ios"), strings.Contains(plat, "ipad"):
		return "iOS"
	case strings.Contains(plat, "android"):
		return "Android"
	case strings.Contains(plat, "osx"), strings.Contains(plat, "mac"):
		return "macOS"
	case strings.Contains(plat, "windows"):
		return "Windows"
	case strings.Contains(plat, "linux"):
		return "Linux"
	case strings.Contains(plat, "harmony"), strings.Contains(plat, "hmos"):
		return "HarmonyOS"
	default:
		return ""
	}
}

// parseFnOSOSName 从 "..., <系统> <版本>)" 取系统名，如 "iOS 17.7.11" -> "iOS"。
func parseFnOSOSName(ua string) string {
	semi := strings.LastIndex(ua, ";")
	if semi < 0 {
		return ""
	}
	rest := ua[semi+1:]
	if close := strings.Index(rest, ")"); close >= 0 {
		rest = rest[:close]
	}
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return ""
	}
	os, _, _ := strings.Cut(rest, " ") // "iOS 17.7.11" -> "iOS"
	switch {
	case strings.EqualFold(os, "iOS"), strings.EqualFold(os, "iPadOS"):
		return "iOS"
	case strings.EqualFold(os, "Android"):
		return "Android"
	case strings.EqualFold(os, "macOS"), strings.EqualFold(os, "Mac OS X"):
		return "macOS"
	case strings.EqualFold(os, "Windows"):
		return "Windows"
	case strings.EqualFold(os, "Linux"):
		return "Linux"
	case strings.EqualFold(os, "HarmonyOS"):
		return "HarmonyOS"
	default:
		return ""
	}
}

// splitToken 从 ua 中取 key 之后的、到空白或括号为止的版本串。
func splitToken(ua, key string) string {
	_, after, ok := strings.Cut(ua, key)
	if !ok {
		return ""
	}
	v := after
	if sp := strings.IndexAny(v, " );"); sp >= 0 {
		v = v[:sp]
	}
	return strings.TrimSpace(v)
}
