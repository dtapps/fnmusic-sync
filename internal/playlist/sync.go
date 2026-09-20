// package playlist 实现歌单同步服务。
//
// 同步流程：
//  1. 定时（默认 30 分钟）从 ListenBrainz / Last.fm 拉取推荐歌单
//  2. 解析歌单中的曲目信息
//  3. 在飞牛音乐中匹配曲目（按 MBID 或艺人名+曲名建立索引）
//  4. 创建或更新同名歌单
//  5. 将匹配的曲目添加到歌单
//
// 文件组织：
//   - sync.go              共享：结构体、构造函数、主循环、调度、通用工具
//   - listenbrainz_sync.go  ListenBrainz 推荐歌单同步
//   - lastfm_sync.go        Last.fm 智能歌单同步
package playlist

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"cnb.cool/dtapp/fnmusic-sync/internal/config"
	"cnb.cool/dtapp/fnmusic-sync/internal/db"
	"cnb.cool/dtapp/fnmusic-sync/internal/feiniu"
	"cnb.cool/dtapp/fnmusic-sync/internal/playback"
	"cnb.cool/dtapp/fnmusic-sync/internal/reqlog"
	"cnb.cool/dtapp/fnmusic-sync/internal/safego"
	"cnb.cool/dtapp/fnmusic-sync/internal/scrobbler"
	"cnb.cool/dtapp/fnmusic-sync/internal/strutil"
)

// unmatchedSampleSize 未匹配曲目在日志里展示的样本数量。
const unmatchedSampleSize = 5

// playlistName 返回歌单在飞牛音乐中的名称，未配置时用默认名，
// 避免配置漏填 name 时创建出空名歌单。
func playlistName(cfg config.PlaylistSourceConfig, fallback string) string {
	return resolvePlaylistName(cfg.Name, fallback, nil)
}

// lastFMPlaylistName 返回 Last.fm 歌单在飞牛音乐中的名称，未配置时用默认名。
func lastFMPlaylistName(cfg config.LastFMPlaylistSourceConfig, fallback string) string {
	return resolvePlaylistName(cfg.Name, fallback, nil)
}

// periodToChinese 将 Last.fm 统计周期转成中文显示。
// overall 为"全部时间"，此时返回空字符串——歌单名里 {period} 会被移除，
// 避免出现 "LF 全部时间最常听" 这种冗余表述。
// 例如 name="LF {period} 最常听":
//
//	period="7day"    -> "LF 7天 最常听"
//	period="overall" -> "LF 最常听"（{period} 被移除）
func periodToChinese(period string) string {
	switch period {
	case "7day":
		return "7天"
	case "1month":
		return "1个月"
	case "3month":
		return "3个月"
	case "6month":
		return "6个月"
	case "12month":
		return "12个月"
	case "overall", "":
		return ""
	default:
		return period
	}
}

// resolvePlaylistName 解析歌单名称，支持 {year} 和 {period} 占位符。
// name 为空时返回 fallback；占位符对应的值为空时移除占位符本身（并清理多余空格）。
// 例如 name="LF {period} 最常听", period="7天" -> "LF 7天 最常听"
//
//	name="LB {year} 年度发现", year="" -> "LB 年度发现"
func resolvePlaylistName(name, fallback string, vars map[string]string) string {
	if name == "" {
		return fallback
	}

	result := name
	for key, val := range vars {
		result = strings.ReplaceAll(result, "{"+key+"}", val)
	}

	// 移除可能因占位符为空而产生的连续多余空格
	for strings.Contains(result, "  ") {
		result = strings.ReplaceAll(result, "  ", " ")
	}
	result = strings.TrimSpace(result)

	return result
}

// matchKind 表示曲目命中曲库的方式，用于统计与日志。
type matchKind int

const (
	matchNone matchKind = iota
	// matchMBID 录音 MBID 精确匹配
	matchMBID
	// matchArtistTitle 艺人名+曲名匹配（含主标题降级）
	matchArtistTitle
	// matchTitleOnly 仅曲名匹配（曲库内曲名唯一时）
	matchTitleOnly
)

// matchTrack 在本地曲库索引里查找推荐曲目，按精度从高到低依次尝试：
//  1. 录音 MBID 精确匹配（飞牛曲库没有 MBID 时直接落空）
//  2. 艺人名 + 曲名
//  3. 艺人名 + 曲名主标题（去掉括号附注，如 "(电影xxx主题曲)"）
//  4. 仅曲名 / 仅主标题（应对艺人名繁简、译名差异，要求曲库内曲名唯一）
func matchTrack(indexes *feiniu.TrackIndexes, artist, title, mbid string) (string, matchKind) {
	if mbid != "" {
		if guid, ok := indexes.MBIDToGUID[mbid]; ok {
			return guid, matchMBID
		}
	}

	if title == "" {
		return "", matchNone
	}

	// 去掉附注后的主标题（"不将就 (Can't Bear It)" → "不将就"）。
	main := feiniu.MainTitle(title)

	if artist != "" {
		if guid, ok := indexes.ArtistTrackToGUID[feiniu.BuildArtistTrackKey(artist, title)]; ok {
			return guid, matchArtistTitle
		}
		if main != title {
			if guid, ok := indexes.ArtistTrackToGUID[feiniu.BuildArtistTrackKey(artist, main)]; ok {
				return guid, matchArtistTitle
			}
		}
	}

	if guid, ok := indexes.TitleToGUID[feiniu.NormalizeForMatch(title)]; ok {
		return guid, matchTitleOnly
	}
	if main != title {
		if guid, ok := indexes.TitleToGUID[feiniu.NormalizeForMatch(main)]; ok {
			return guid, matchTitleOnly
		}
	}

	return "", matchNone
}

// SyncService 歌单同步服务。
type SyncService struct {
	cfg       *config.Config
	logger    *slog.Logger
	client    *http.Client
	userCache *playback.UserCache
	dbStore   *db.Store

	// 请求日志：各客户端独立写入各自的日志文件。
	feiniuReqLog *reqlog.Logger
	lfReqLog     *reqlog.Logger
	lbReqLog     *reqlog.Logger

	mu      sync.Mutex
	running bool
	cancel  context.CancelFunc

	// syncingUsers 记录正在同步中的用户，避免并发重复同步。
	syncingUsers map[string]bool

	stateMu sync.Mutex
	// preferredToken 记录每个用户上次同步成功的 token。
	// 同一用户名可能对应多个 token（多客户端登录），定时同步只需挑一个。
	preferredToken map[string]string
	// syncedTracks 记录每个歌单上次同步后的曲目集合（username|playlistGUID -> GUID 列表）。
	// 歌单详情接口不可用时的兜底，避免 add-track 重复追加。
	syncedTracks map[string][]string
	// recommendations 按 ListenBrainz 用户名缓存推荐歌单结果。
	// 用户识别回调与定时同步可能接连触发，缓存可减少对 ListenBrainz 的请求
	// （对方有反爬校验，短时间反复请求会拿到 HTML 校验页）。
	recommendations map[string]recommendationCache
}

// recommendationCache 推荐歌单缓存项。
type recommendationCache struct {
	fetchedAt       time.Time
	daily           *scrobbler.ListenBrainzPlaylist
	weekly          *scrobbler.ListenBrainzPlaylist
	exploration     *scrobbler.ListenBrainzPlaylist
	yearDiscoveries *scrobbler.ListenBrainzPlaylist
	yearMissed      *scrobbler.ListenBrainzPlaylist
}

// recommendationCacheTTL 推荐歌单缓存有效期。
const recommendationCacheTTL = 2 * time.Minute

// syncResult 汇总一次 provider（lastfm / listenbrainz）同步的结果，用于落库 sync_log。
type syncResult struct {
	Playlists int   // 实际同步的歌单数
	Tracks    int   // 实际新增到歌单的曲目数
	Noop      bool  // 没有可同步的歌单（子项全关），不落库
	Err       error // 同步错误（nil 表示成功）
}

// syncInterval 解析配置中的同步间隔，非法或 <=0 时回退到默认 30 分钟。
func (s *SyncService) syncInterval() time.Duration {
	interval, err := time.ParseDuration(s.cfg.Playlist.SyncInterval)
	if err != nil || interval <= 0 {
		return 30 * time.Minute
	}
	return interval
}

// syncProvider 执行一次 provider 同步，并：
//  1. 同步前查该用户+provider 上一次成功时间，未到间隔则跳过——这是"持久化的同步间隔"，
//     跨重启、跨用户识别回调的多次触发都能生效（interval 未到不再重复跑）；
//  2. 同步后无论成功/失败都落库 sync_log（供 Web UI「同步列表」展示与排查）。
//
// fn 执行真正的同步逻辑（返回 syncResult）；Noop=true 时不记录（无歌单可同步）。
func (s *SyncService) syncProvider(ctx context.Context, username, provider, trigger string, interval time.Duration, fn func() syncResult) {
	// 1. 间隔检查（仅当配置生效且已有上次成功记录时）。
	if s.dbStore != nil && interval > 0 {
		if last, err := s.dbStore.GetLastSuccessTime(ctx, username, provider); err == nil && !last.IsZero() {
			if elapsed := time.Since(last); elapsed < interval {
				s.logger.Info("同步间隔未到，跳过",
					"用户", username,
					"provider", provider,
					"上次成功", last.Format(time.RFC3339),
					"已过去", elapsed.Round(time.Second).String(),
					"间隔", interval.String(),
				)
				return
			}
		}
	}

	// 2. 执行同步。
	started := time.Now()
	res := fn()
	if res.Noop {
		return
	}

	// 3. 落库（无论成功或失败）。
	if s.dbStore != nil {
		status := db.SyncLogStatusSuccess
		msg := ""
		if res.Err != nil {
			status = db.SyncLogStatusFailed
			msg = res.Err.Error()
			s.logger.Error("歌单同步失败",
				"用户", username, "provider", provider, "错误", res.Err)
		}
		if _, rerr := s.dbStore.RecordSyncLog(db.SyncLogRecord{
			Username:  username,
			Provider:  provider,
			Status:    status,
			Trigger:   trigger,
			Playlists: res.Playlists,
			Tracks:    res.Tracks,
			Message:   msg,
			StartedAt: started,
		}); rerr != nil {
			s.logger.Warn("写入同步记录失败", "用户", username, "provider", provider, "错误", rerr)
		}
	}
}

// NewSyncService 创建歌单同步服务。
func NewSyncService(
	cfg *config.Config,
	logger *slog.Logger,
	userCache *playback.UserCache,
	dbStore *db.Store,
	feiniuReqLog, lfReqLog, lbReqLog *reqlog.Logger,
) *SyncService {
	s := &SyncService{
		cfg:    cfg,
		logger: logger,
		client: &http.Client{
			Timeout: 60 * time.Second,
		},
		userCache:       userCache,
		dbStore:         dbStore,
		feiniuReqLog:    feiniuReqLog,
		lfReqLog:        lfReqLog,
		lbReqLog:        lbReqLog,
		syncingUsers:    make(map[string]bool),
		preferredToken:  make(map[string]string),
		syncedTracks:    make(map[string][]string),
		recommendations: make(map[string]recommendationCache),
	}

	// 注册用户识别回调：当首次识别到新用户时，立即触发同步。
	userCache.SetOnUserIdentifiedCallback(s.onUserIdentified)

	return s
}

// UpdateConfig 更新配置（热更新时调用）。
func (s *SyncService) UpdateConfig(cfg *config.Config) {
	s.mu.Lock()
	defer s.mu.Unlock()

	wasRunning := s.running
	if wasRunning {
		s.stopLocked()
	}

	s.cfg = cfg

	if wasRunning && cfg.Playlist.Enabled {
		s.startLocked()
	}
}

// Start 启动歌单同步服务。
func (s *SyncService) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return
	}

	s.startLocked()
}

// Stop 停止歌单同步服务。
func (s *SyncService) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return
	}

	s.stopLocked()
}

func (s *SyncService) startLocked() {
	if !s.cfg.Playlist.Enabled {
		s.logger.Info("歌单同步已禁用，跳过启动")
		return
	}

	interval, err := time.ParseDuration(s.cfg.Playlist.SyncInterval)
	if err != nil || interval <= 0 {
		interval = 30 * time.Minute
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.running = true

	s.logger.Info("歌单同步服务启动",
		"间隔", interval.String(),
	)

	safego.Go(s.logger, "playlist.SyncService.run", func() { s.run(ctx, interval) })
}

func (s *SyncService) stopLocked() {
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	s.running = false
}

// run 是同步服务的主循环。
// 注意：启动时不执行同步，因为此时还没有用户被识别。
// 同步会在用户首次被识别时通过回调触发。
func (s *SyncService) run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("歌单同步服务停止")
			return
		case <-ticker.C:
			s.syncAll(ctx)
		}
	}
}

// onUserIdentified 当识别到新用户时由 UserCache 回调。
// 立即触发该用户的歌单同步。
// 同一用户名可能有多个 token（不同客户端），每个 token 都会触发同步，
// 如果某个 token 无效（401），不影响其他 token 的同步。
func (s *SyncService) onUserIdentified(token, username string) {
	s.mu.Lock()
	// 避免同一用户被并发同步多次。
	if s.syncingUsers[username] {
		s.mu.Unlock()
		s.logger.Debug("歌单同步跳过：用户正在同步中",
			"用户", username,
			"用户标识", strutil.FirstN8(token),
		)
		return
	}
	s.syncingUsers[username] = true
	s.mu.Unlock()

	// 确保同步完成后清除标记。
	defer func() {
		s.mu.Lock()
		delete(s.syncingUsers, username)
		s.mu.Unlock()
	}()

	// 检查用户是否配置了 ListenBrainz。
	userCfg, ok := s.cfg.Users[username]
	if !ok {
		s.logger.Debug("歌单同步跳过：用户未在配置中",
			"用户", username,
			"用户标识", strutil.FirstN8(token),
		)
		return
	}

	lb := userCfg.ListenBrainz
	lfm := userCfg.LastFM

	lbOK := lb.Enabled && lb.Username != ""
	lfmOK := lfm.Enabled && lfm.Username != ""
	if !lbOK && !lfmOK {
		s.logger.Debug("歌单同步跳过：用户未启用 ListenBrainz 或 Last.fm",
			"用户", username,
			"用户标识", strutil.FirstN8(token),
		)
		return
	}

	s.logger.Info("用户识别回调触发歌单同步",
		"用户", username,
		"用户标识", strutil.FirstN8(token),
		"listenbrainz用户", lb.Username,
		"lastfm用户", lfm.Username,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	interval := s.syncInterval()

	if lbOK {
		s.syncProvider(ctx, username, "listenbrainz", db.SyncLogTriggerUserIdentified, interval, func() syncResult {
			return s.syncUser(ctx, token, username, lb)
		})
	}

	if lfmOK {
		s.syncProvider(ctx, username, "lastfm", db.SyncLogTriggerUserIdentified, interval, func() syncResult {
			return s.syncLastFMUser(ctx, token, username, lfm)
		})
	}
}

// syncAll 对所有已登录且启用歌单同步的用户执行同步。
// 只同步当前活跃的用户（已识别到用户名的）。
func (s *SyncService) syncAll(ctx context.Context) {
	s.mu.Lock()
	cfg := s.cfg
	s.mu.Unlock()

	// 获取当前已识别的用户（token → 用户名）
	activeUsers := s.userCache.ActiveUsers()
	if len(activeUsers) == 0 {
		s.logger.Debug("当前没有活跃用户，跳过歌单同步")
		return
	}

	// 同一用户名会对应多个 token（多客户端登录）。歌单属于用户维度的资源，
	// 用任意一个有效 token 同步的结果都一样，所以每个用户只同步一次
	// （旧日志里"活跃用户数=4/6"其实只有 1 个用户，同一份歌单被重复同步了 6 遍）。
	tokensByUser := s.groupTokensByUser(activeUsers)

	s.logger.Info("开始歌单同步",
		"活跃用户标识数", len(activeUsers),
		"待同步用户数", len(tokensByUser),
	)

	for username, tokens := range tokensByUser {
		userCfg, ok := cfg.Users[username]
		if !ok {
			continue
		}

		lb := userCfg.ListenBrainz
		lfm := userCfg.LastFM

		interval := s.syncInterval()

		// ListenBrainz 歌单同步
		if lb.Enabled && lb.Username != "" {
			s.syncProvider(ctx, username, "listenbrainz", db.SyncLogTriggerInterval, interval, func() syncResult {
				return s.syncUserTokens(ctx, tokens, username, lb)
			})
		}

		// Last.fm 智能歌单同步
		if lfm.Enabled && lfm.Username != "" {
			s.syncProvider(ctx, username, "lastfm", db.SyncLogTriggerInterval, interval, func() syncResult {
				return s.syncLastFMUserTokens(ctx, tokens, username, lfm)
			})
		}
	}
}

// groupTokensByUser 按用户名归集该用户的全部 token（多客户端各一个）。
// 上次同步成功的 token 排在最前：它已被验证可用，
// 避免 map 随机遍历挑到某个客户端已失效的旧 token 白跑一趟。
func (s *SyncService) groupTokensByUser(activeUsers map[string]string) map[string][]string {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()

	grouped := make(map[string][]string, len(activeUsers))
	for token, username := range activeUsers {
		grouped[username] = append(grouped[username], token)
	}

	for username, tokens := range grouped {
		preferred := s.preferredToken[username]
		if preferred == "" {
			continue
		}

		for i, token := range tokens {
			if token == preferred && i > 0 {
				tokens[0], tokens[i] = tokens[i], tokens[0]

				break
			}
		}
	}

	return grouped
}

// rememberToken 记录该用户最近一次同步成功的 token。
func (s *SyncService) rememberToken(username, token string) {
	s.stateMu.Lock()
	s.preferredToken[username] = token
	s.stateMu.Unlock()
}

// diffTracks 计算需要新增的曲目 GUID。
// 先读歌单现有曲目；详情接口不可用时，退回上次同步记录去重。
func (s *SyncService) diffTracks(
	ctx context.Context,
	apiClient *feiniu.Client,
	username, playlistName, playlistGUID string,
	trackGUIDs []string,
) []string {
	stateKey := username + "|" + playlistGUID

	existing, err := apiClient.PlaylistTrackGUIDs(ctx, playlistGUID)
	if err != nil {
		s.logger.Warn("读取歌单现有曲目失败，改用上次同步记录去重",
			"用户", username,
			"歌单名称", playlistName,
			"错误", err,
		)
		existing = nil
	}

	// 详情接口返回空（或不可用）时，用上次同步记录兜底，避免重复追加。
	if len(existing) == 0 {
		existing = s.loadSynced(stateKey)
	}

	newGUIDs := make([]string, 0, len(trackGUIDs))
	for _, guid := range trackGUIDs {
		if _, ok := existing[guid]; ok {
			continue
		}
		// 同一次调用内也可能出现重复 GUID。
		existing[guid] = struct{}{}
		newGUIDs = append(newGUIDs, guid)
	}

	// 记录当前歌单曲目集合，供下次无详情接口时兜底。
	merged := make([]string, 0, len(existing))
	for guid := range existing {
		merged = append(merged, guid)
	}
	s.saveSynced(stateKey, merged)

	return newGUIDs
}

// loadSynced 读取上次同步后的歌单曲目集合。
func (s *SyncService) loadSynced(key string) map[string]struct{} {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()

	result := make(map[string]struct{}, len(s.syncedTracks[key]))
	for _, guid := range s.syncedTracks[key] {
		result[guid] = struct{}{}
	}

	return result
}

// saveSynced 保存本次同步后的歌单曲目集合。
func (s *SyncService) saveSynced(key string, guids []string) {
	s.stateMu.Lock()
	s.syncedTracks[key] = guids
	s.stateMu.Unlock()
}

// isInvalidTokenError 判断错误是否是 token 失效（401 INVALID TOKEN）。
func isInvalidTokenError(err error) bool {
	if err == nil {
		return false
	}

	msg := err.Error()

	return strings.Contains(msg, "HTTP 401") || strings.Contains(msg, "INVALID TOKEN")
}

// dropInvalidToken 剔除失效 token，避免每轮定时同步重复报错。
// 同时清掉"上次成功 token"记录，下一轮改用该用户的其他客户端 token。
func (s *SyncService) dropInvalidToken(username, token string) {
	// 已被剔除过的 token 不再重复打日志（同一轮可能多次遇到同一个失效 token）。
	if s.userCache.InvalidateToken(token) {
		s.logger.Warn("用户 token 已失效，已移出同步列表",
			"用户", username,
			"用户标识", strutil.FirstN8(token),
		)
	}

	s.stateMu.Lock()
	if s.preferredToken[username] == token {
		delete(s.preferredToken, username)
	}
	s.stateMu.Unlock()
}
