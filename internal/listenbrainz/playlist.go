package listenbrainz

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"cnb.cool/dtapp/fnmusic-sync/internal/buildinfo"
	"cnb.cool/dtapp/fnmusic-sync/internal/reqlog"
)

// playlistsCreatedForEndpoint 获取"分享给该用户"的歌单。
//
// 注意：/1/user/{user}/playlists 只返回用户**自己创建**的歌单，而
// daily_jams / weekly_jams / weekly_exploration 这类推荐歌单是由
// troi-bot / listenbrainz 生成后分享给用户的，只能从 createdfor 取到。
const playlistsCreatedForEndpoint = "https://api.listenbrainz.org/1/user/%s/playlists/createdfor"

// playlistDetailEndpoint 单个歌单详情。
// 列表接口（createdfor）返回的歌单 track 恒为空数组，必须再按 MBID 拉详情才有曲目。
const playlistDetailEndpoint = "https://api.listenbrainz.org/1/playlist/%s"

// 用户统计类歌单接口（基于收听数据，非 troi-bot 推荐）。
const (
	// userTopRecordingsEndpoint 用户最常听录音（按收听次数排序）。
	// 注意：旧端点 /1/user/{user}/recordings 已被 ListenBrainz 废弃（返回 308→404），
	// 现统一走 stats 接口 /1/stats/user/{user}/recordings，返回 payload.recordings，字段扁平。
	// range 维度：week/month/quarter/half_year/year/all_time/this_year。
	userTopRecordingsEndpoint = "https://api.listenbrainz.org/1/stats/user/%s/recordings"
	// userListensEndpoint 用户最近收听记录（listens），用于"最近在听"歌单。
	userListensEndpoint = "https://api.listenbrainz.org/1/user/%s/listens"
)

// lbStatsTrackMetadata 是 LB 统计接口（如 top recordings）曲目元数据的统一结构，
// 与 JSPF 不同，元信息包在 track_metadata 下；stats 接口的扁平字段由 lbStatsRecording 直接承载。
type lbStatsTrackMetadata struct {
	ArtistName         string `json:"artist_name"`
	TrackName          string `json:"track_name"`
	ReleaseName        string `json:"release_name"`
	AdditionalMetadata struct {
		RecordingMBID string   `json:"recording_mbid"`
		ArtistMBIDs   []string `json:"artist_mbids"`
		ReleaseMBID   string   `json:"release_mbid"`
	} `json:"additional_metadata"`
}

// lbStatsRecording 是 LB 统计接口返回的单个录音条目。
// 兼容两种返回结构：
//   - 旧 /1/user/{user}/recordings 与 listens 衍生：元信息包在 track_metadata 下；
//   - 新 /1/stats/user/{user}/recordings：artist_name/track_name/release_name/
//     recording_mbid/release_mbid/artist_mbids 等字段扁平化（无 track_metadata 包裹）。
//
// top recordings 使用该结构（loved recordings 接口已在 ListenBrainz 上游废弃，故移除）。
type lbStatsRecording struct {
	RecordingMBID string               `json:"recording_mbid"`
	TrackMetadata lbStatsTrackMetadata `json:"track_metadata"`
	ListenCount   int                  `json:"listen_count"`
	CreatedAt     string               `json:"created_at"`

	// 兼容 ListenBrainz stats 接口的扁平字段（/1/stats/user/{user}/recordings）。
	ArtistName  string   `json:"artist_name"`
	TrackName   string   `json:"track_name"`
	ReleaseName string   `json:"release_name"`
	ArtistMBIDs []string `json:"artist_mbids"`
	ReleaseMBID string   `json:"release_mbid"`
}

// toPlaylistTrack 把统计条目映射为统一的 PlaylistTrack，便于复用现有匹配逻辑。
// 优先使用扁平字段（stats 接口），缺失时回退到 track_metadata（旧接口 / listens 衍生）。
func (r lbStatsRecording) toPlaylistTrack() PlaylistTrack {
	artist := firstNonEmpty(r.ArtistName, r.TrackMetadata.ArtistName)
	track := firstNonEmpty(r.TrackName, r.TrackMetadata.TrackName)
	release := firstNonEmpty(r.ReleaseName, r.TrackMetadata.ReleaseName)
	mbid := firstNonEmpty(r.RecordingMBID, r.TrackMetadata.AdditionalMetadata.RecordingMBID)

	artistMBID := ""
	if len(r.ArtistMBIDs) > 0 {
		artistMBID = r.ArtistMBIDs[0]
	} else if len(r.TrackMetadata.AdditionalMetadata.ArtistMBIDs) > 0 {
		artistMBID = r.TrackMetadata.AdditionalMetadata.ArtistMBIDs[0]
	}
	releaseMBID := firstNonEmpty(r.ReleaseMBID, r.TrackMetadata.AdditionalMetadata.ReleaseMBID)

	return PlaylistTrack{
		ArtistName:    artist,
		TrackName:     track,
		ReleaseName:   release,
		RecordingMBID: mbid,
		ArtistMBID:    artistMBID,
		ReleaseMBID:   releaseMBID,
	}
}

// JSPF 扩展字段的命名空间 key。
const (
	jspfPlaylistKey = "https://musicbrainz.org/doc/jspf#playlist"
	jspfTrackKey    = "https://musicbrainz.org/doc/jspf#track"
)

// 推荐歌单的 algorithm_metadata.source_patch 取值。
const (
	SourcePatchDailyJams         = "daily-jams"
	SourcePatchWeeklyJams        = "weekly-jams"
	SourcePatchWeeklyExploration = "weekly-exploration"
	// 年度歌单的 source_patch 带年份后缀（如 top-discoveries-of-2025），
	// 用前缀匹配即可覆盖任意年份。
	SourcePatchYearDiscoveriesPrefix = "top-discoveries-of-"
	SourcePatchYearMissedPrefix      = "top-missed-recordings-of-"
)

// TroiBotName 是 ListenBrainz 官方推荐机器人用户名。
// 注意：历史推荐歌单的 creator 为 "troi-bot"，新生成的为 "listenbrainz"，
// 因此不能只按 creator 过滤，应优先按 source_patch 识别。
const (
	TroiBotName         = "troi-bot"
	ListenBrainzBotName = "listenbrainz"
)

// PlaylistClient ListenBrainz 歌单 API 客户端。
// 用于获取分享给用户的推荐歌单（daily_jams、weekly_jams、weekly_exploration、年度歌单）。
type PlaylistClient struct {
	Username   string
	reqLog     *reqlog.Logger
	httpClient *http.Client
}

// NewPlaylistClient 创建 ListenBrainz 歌单客户端。
// username 是 ListenBrainz 用户名，用于查询分享给该用户的推荐歌单。
// reqLog 为请求日志记录器，nil 时跳过请求日志。
func NewPlaylistClient(username string, reqLog *reqlog.Logger) *PlaylistClient {
	return &PlaylistClient{
		Username: username,
		reqLog:   reqLog,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// recommendationKind 推荐歌单类型。
type recommendationKind int

const (
	kindUnknown recommendationKind = iota
	kindDailyJams
	kindWeeklyJams
	kindWeeklyExploration
	kindYearDiscoveries
	kindYearMissed
)

// jspfPlaylistExtension 歌单的 JSPF 扩展信息。
type jspfPlaylistExtension struct {
	AdditionalMetadata struct {
		AlgorithmMetadata struct {
			SourcePatch string `json:"source_patch"`
		} `json:"algorithm_metadata"`
		ExpiresAt string `json:"expires_at"`
	} `json:"additional_metadata"`
	CreatedFor     string `json:"created_for"`
	Creator        string `json:"creator"`
	LastModifiedAt string `json:"last_modified_at"`
	Public         bool   `json:"public"`
}

// jspfTrackExtension 曲目的 JSPF 扩展信息。
type jspfTrackExtension struct {
	AdditionalMetadata struct {
		Artists []struct {
			ArtistCreditName string `json:"artist_credit_name"`
			ArtistMBID       string `json:"artist_mbid"`
			JoinPhrase       string `json:"join_phrase"`
		} `json:"artists"`
	} `json:"additional_metadata"`
	AddedAt string `json:"added_at"`
	AddedBy string `json:"added_by"`
}

// Playlist ListenBrainz 歌单结构（JSPF 格式）。
type Playlist struct {
	// ID 歌单 MBID（部分接口直接给出，列表接口只有 identifier）
	ID string `json:"id"`
	// Identifier 歌单地址，形如 https://listenbrainz.org/playlist/<mbid>
	Identifier string `json:"identifier"`
	// Title 歌单标题，如 "Daily Jams for alice, 2026-09-08 Tue"
	Title string `json:"title"`
	// Creator 创建者用户名（troi-bot / listenbrainz）
	Creator string `json:"creator"`
	// Annotation 歌单描述/注释
	Annotation string `json:"annotation"`
	// Date 创建时间（RFC3339）
	Date string `json:"date"`
	// TrackList 歌单中的曲目列表（列表接口恒为空，详情接口才有）
	TrackList []PlaylistTrack `json:"track"`
	// Extension JSPF 扩展信息（source_patch、expires_at 等）
	Extension map[string]jspfPlaylistExtension `json:"extension"`
}

// MBID 返回歌单的 MBID，优先用 id，否则从 identifier 中解析。
func (p *Playlist) MBID() string {
	if p.ID != "" {
		return p.ID
	}

	return mbidFromURL(p.Identifier)
}

// SourcePatch 返回推荐算法标识（daily-jams / weekly-jams / weekly-exploration / top-discoveries-of-2025 等）。
func (p *Playlist) SourcePatch() string {
	if ext, ok := p.Extension[jspfPlaylistKey]; ok {
		return ext.AdditionalMetadata.AlgorithmMetadata.SourcePatch
	}

	return ""
}

// YearFromSourcePatch 从年度歌单的 source_patch 中提取年份。
// source_patch 形如 "top-discoveries-of-2025"，提取出 "2025"。
// 非年度歌单或无法识别时返回空字符串。
func (p *Playlist) YearFromSourcePatch() string {
	sp := p.SourcePatch()
	for _, prefix := range []string{SourcePatchYearDiscoveriesPrefix, SourcePatchYearMissedPrefix} {
		if after, ok := strings.CutPrefix(strings.ToLower(sp), prefix); ok {
			return after
		}
	}

	return ""
}

// ExpiresAt 返回歌单过期时间（LB 到期后不再更新该歌单）。
func (p *Playlist) ExpiresAt() (time.Time, bool) {
	ext, ok := p.Extension[jspfPlaylistKey]
	if !ok || ext.AdditionalMetadata.ExpiresAt == "" {
		return time.Time{}, false
	}

	t, err := time.Parse(time.RFC3339, ext.AdditionalMetadata.ExpiresAt)
	if err != nil {
		return time.Time{}, false
	}

	return t, true
}

// Timestamp 返回用于排序的时间（优先 last_modified_at，其次 date）。
func (p *Playlist) Timestamp() time.Time {
	if ext, ok := p.Extension[jspfPlaylistKey]; ok && ext.LastModifiedAt != "" {
		if t, err := time.Parse(time.RFC3339, ext.LastModifiedAt); err == nil {
			return t
		}
	}

	if t, err := time.Parse(time.RFC3339, p.Date); err == nil {
		return t
	}

	return time.Time{}
}

// kind 判定歌单属于哪一类推荐歌单，无法识别时返回 kindUnknown。
func (p *Playlist) kind() recommendationKind {
	sp := strings.ToLower(p.SourcePatch())
	switch sp {
	case SourcePatchDailyJams:
		return kindDailyJams
	case SourcePatchWeeklyJams:
		return kindWeeklyJams
	case SourcePatchWeeklyExploration:
		return kindWeeklyExploration
	}

	// 年度歌单按前缀匹配（top-discoveries-of-2025 / top-missed-recordings-of-2025）
	switch {
	case strings.HasPrefix(sp, SourcePatchYearDiscoveriesPrefix):
		return kindYearDiscoveries
	case strings.HasPrefix(sp, SourcePatchYearMissedPrefix):
		return kindYearMissed
	}

	// 兜底：早期歌单没有 source_patch，按标题识别。
	title := strings.ToLower(p.Title)
	switch {
	case strings.Contains(title, "daily jams"):
		return kindDailyJams
	case strings.Contains(title, "weekly exploration"):
		return kindWeeklyExploration
	case strings.Contains(title, "weekly jams"):
		return kindWeeklyJams
	case strings.Contains(title, "top discoveries of"):
		return kindYearDiscoveries
	case strings.Contains(title, "top missed recordings of"):
		return kindYearMissed
	}

	return kindUnknown
}

// PlaylistTrack ListenBrainz 歌单中的单曲信息。
//
// ListenBrainz 返回的是 JSPF，曲目字段为 title / creator / album / identifier，
// 这里统一映射到 ArtistName / TrackName / ReleaseName / RecordingMBID，
// 便于上层匹配逻辑使用。
type PlaylistTrack struct {
	// ArtistName 艺人名
	ArtistName string `json:"artist_name"`
	// TrackName 曲目标题
	TrackName string `json:"track_name"`
	// ReleaseName 专辑名
	ReleaseName string `json:"release_name"`
	// RecordingMBID 录音 MBID（MusicBrainz Identifier）
	RecordingMBID string `json:"recording_mbid"`
	// ReleaseMBID 专辑 MBID
	ReleaseMBID string `json:"release_mbid"`
	// ArtistMBID 艺人 MBID
	ArtistMBID string `json:"artist_mbid"`
	// AdditionalInfo 附加信息
	AdditionalInfo map[string]any `json:"additional_info,omitempty"`
}

// rawPlaylistTrack 是 JSPF 与兼容字段的并集，用于反序列化。
type rawPlaylistTrack struct {
	// JSPF 实际字段
	Title      string                        `json:"title"`
	Creator    string                        `json:"creator"`
	Album      string                        `json:"album"`
	Identifier []string                      `json:"identifier"`
	Extension  map[string]jspfTrackExtension `json:"extension"`

	// 兼容字段（部分实现/旧版本使用）
	TrackName      string         `json:"track_name"`
	ArtistName     string         `json:"artist_name"`
	ReleaseName    string         `json:"release_name"`
	RecordingMBID  string         `json:"recording_mbid"`
	ReleaseMBID    string         `json:"release_mbid"`
	ArtistMBID     string         `json:"artist_mbid"`
	AdditionalInfo map[string]any `json:"additional_info,omitempty"`
}

// UnmarshalJSON 把 JSPF 曲目映射为统一的 PlaylistTrack。
func (t *PlaylistTrack) UnmarshalJSON(data []byte) error {
	var raw rawPlaylistTrack
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	t.TrackName = firstNonEmpty(raw.TrackName, raw.Title)
	t.ArtistName = firstNonEmpty(raw.ArtistName, raw.Creator)
	t.ReleaseName = firstNonEmpty(raw.ReleaseName, raw.Album)
	t.RecordingMBID = firstNonEmpty(raw.RecordingMBID, recordingMBIDFromIdentifiers(raw.Identifier))
	t.ArtistMBID = firstNonEmpty(raw.ArtistMBID, artistMBIDFromExtension(raw.Extension))
	t.ReleaseMBID = raw.ReleaseMBID
	t.AdditionalInfo = raw.AdditionalInfo

	return nil
}

// playlistResponse /1/user/{username}/playlists/createdfor 的响应结构。
type playlistResponse struct {
	Count         int             `json:"count"`
	Offset        int             `json:"offset"`
	PlaylistCount int             `json:"playlist_count"`
	Playlists     []playlistEntry `json:"playlists"`
}

type playlistEntry struct {
	Playlist Playlist `json:"playlist"`
}

// playlistDetailResponse /1/playlist/{mbid} 的响应结构。
type playlistDetailResponse struct {
	Playlist Playlist `json:"playlist"`
}

// FetchPlaylists 获取分享给该用户的歌单（推荐歌单的来源）。
//
// 兼容旧命名：行为等价于 FetchCreatedForPlaylists。
func (c *PlaylistClient) FetchPlaylists(ctx context.Context) ([]Playlist, error) {
	return c.FetchCreatedForPlaylists(ctx)
}

// FetchCreatedForPlaylists 获取"为该用户创建"的歌单，
// 即 troi-bot / listenbrainz 生成并分享给该用户的推荐歌单。
func (c *PlaylistClient) FetchCreatedForPlaylists(ctx context.Context) ([]Playlist, error) {
	endpoint := fmt.Sprintf(playlistsCreatedForEndpoint, url.PathEscape(c.Username))

	var result playlistResponse
	if err := c.getJSON(ctx, endpoint, &result); err != nil {
		return nil, err
	}

	playlists := make([]Playlist, 0, len(result.Playlists))
	for _, entry := range result.Playlists {
		playlists = append(playlists, entry.Playlist)
	}

	return playlists, nil
}

// FetchPlaylist 按 MBID 获取单个歌单详情（含完整曲目）。
// 列表接口返回的 track 为空，必须用它补齐曲目。
func (c *PlaylistClient) FetchPlaylist(ctx context.Context, mbid string) (*Playlist, error) {
	if mbid == "" {
		return nil, fmt.Errorf("缺少歌单 MBID")
	}

	endpoint := fmt.Sprintf(playlistDetailEndpoint, url.PathEscape(mbid))

	var result playlistDetailResponse
	if err := c.getJSON(ctx, endpoint, &result); err != nil {
		return nil, err
	}

	return &result.Playlist, nil
}

// FetchTroiBotPlaylists 获取 troi-bot / listenbrainz 生成的推荐歌单。
//
// 同类歌单会有多期（weekly_jams 每周一期），这里取最新一期；
// 未过期的一期优先于已过期的一期。
// 若某类歌单不存在（ListenBrainz 尚未生成），对应位置返回 nil。
func (c *PlaylistClient) FetchTroiBotPlaylists(
	ctx context.Context,
) (dailyJams, weeklyJams, weeklyExploration, yearDiscoveries, yearMissed *Playlist, err error) {
	playlists, err := c.FetchCreatedForPlaylists(ctx)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}

	latest := map[recommendationKind]*Playlist{}
	// 记录当前选中歌单是否已过期：未过期的优先级高于已过期。
	latestExpired := map[recommendationKind]bool{}

	for i := range playlists {
		p := &playlists[i]

		kind := p.kind()
		if kind == kindUnknown {
			continue
		}

		expired := false
		if expiresAt, ok := p.ExpiresAt(); ok && time.Now().After(expiresAt) {
			expired = true
		}

		cur, exists := latest[kind]
		switch {
		case !exists:
			latest[kind] = p
			latestExpired[kind] = expired
		case latestExpired[kind] && !expired:
			latest[kind] = p
			latestExpired[kind] = false
		case latestExpired[kind] == expired && p.Timestamp().After(cur.Timestamp()):
			latest[kind] = p
		}
	}

	dailyJams = c.withTracks(ctx, latest[kindDailyJams])
	weeklyJams = c.withTracks(ctx, latest[kindWeeklyJams])
	weeklyExploration = c.withTracks(ctx, latest[kindWeeklyExploration])
	yearDiscoveries = c.withTracks(ctx, latest[kindYearDiscoveries])
	yearMissed = c.withTracks(ctx, latest[kindYearMissed])

	return dailyJams, weeklyJams, weeklyExploration, yearDiscoveries, yearMissed, nil
}

// FetchTopRecordings 获取用户最常听的录音（基于 ListenBrainz 收听统计）。
// range 维度可选：week/month/quarter/half_year/year/all_time/this_year，默认 all_time。
// count 为返回数量上限（<=0 表示不限制，由 LB 决定）。
// 返回 *Playlist（TrackList 已按收听次数降序），无数据或接口失败时返回 (nil, err)。
func (c *PlaylistClient) FetchTopRecordings(ctx context.Context, count int, rng string) (*Playlist, error) {
	if rng == "" {
		rng = "all_time"
	}

	endpoint := fmt.Sprintf(userTopRecordingsEndpoint, url.PathEscape(c.Username))
	q := url.Values{}
	q.Set("range", rng)
	if count > 0 {
		q.Set("count", strconv.Itoa(count))
	}
	endpoint += "?" + q.Encode()

	var result struct {
		Payload struct {
			Recordings []lbStatsRecording `json:"recordings"`
		} `json:"payload"`
	}
	if err := c.getJSON(ctx, endpoint, &result); err != nil {
		return nil, err
	}

	return statsToPlaylist(result.Payload.Recordings, "Top Recordings ("+rng+")"), nil
}

// lbListen 是 ListenBrainz listens 端点返回的单个收听记录。
type lbListen struct {
	ListenedAt    int64 `json:"listened_at"`
	TrackMetadata struct {
		ArtistName     string `json:"artist_name"`
		TrackName      string `json:"track_name"`
		ReleaseName    string `json:"release_name"`
		AdditionalInfo struct {
			RecordingMBID string   `json:"recording_mbid"`
			ArtistMBIDs   []string `json:"artist_mbids"`
			ReleaseMBID   string   `json:"release_mbid"`
		} `json:"additional_info"`
	} `json:"track_metadata"`
}

// FetchRecentlyPlayed 获取用户最近收听的录音（基于 ListenBrainz listens 记录）.
// count 为返回数量上限（<=0 表示不限制，由 LB 决定，默认近若干条）。
// 返回 *Playlist（TrackList 按收听时间倒序），无数据或接口失败时返回 (nil, err)。
func (c *PlaylistClient) FetchRecentlyPlayed(ctx context.Context, count int) (*Playlist, error) {
	endpoint := fmt.Sprintf(userListensEndpoint, url.PathEscape(c.Username))
	q := url.Values{}
	if count > 0 {
		q.Set("count", strconv.Itoa(count))
	}
	endpoint += "?" + q.Encode()

	var result struct {
		Payload struct {
			Listens []lbListen `json:"listens"`
		} `json:"payload"`
	}
	if err := c.getJSON(ctx, endpoint, &result); err != nil {
		return nil, err
	}

	recs := make([]lbStatsRecording, 0, len(result.Payload.Listens))
	for i := range result.Payload.Listens {
		l := &result.Payload.Listens[i]
		tm := lbStatsTrackMetadata{
			ArtistName:  l.TrackMetadata.ArtistName,
			TrackName:   l.TrackMetadata.TrackName,
			ReleaseName: l.TrackMetadata.ReleaseName,
		}
		tm.AdditionalMetadata.RecordingMBID = l.TrackMetadata.AdditionalInfo.RecordingMBID
		tm.AdditionalMetadata.ArtistMBIDs = l.TrackMetadata.AdditionalInfo.ArtistMBIDs
		tm.AdditionalMetadata.ReleaseMBID = l.TrackMetadata.AdditionalInfo.ReleaseMBID
		recs = append(recs, lbStatsRecording{
			RecordingMBID: l.TrackMetadata.AdditionalInfo.RecordingMBID,
			TrackMetadata: tm,
		})
	}

	return statsToPlaylist(recs, "Recently Played"), nil
}

// statsToPlaylist 把统计接口返回的录音列表组装成 *Playlist。
// 空列表返回 nil（调用方按"歌单不存在"处理）。
func statsToPlaylist(recs []lbStatsRecording, title string) *Playlist {
	if len(recs) == 0 {
		return nil
	}

	pl := &Playlist{Title: title}
	pl.TrackList = make([]PlaylistTrack, 0, len(recs))
	for i := range recs {
		pl.TrackList = append(pl.TrackList, recs[i].toPlaylistTrack())
	}

	return pl
}

// withTracks 为歌单补齐曲目：列表里的歌单 track 为空，需要拉详情。
// 详情拉取失败时不打断整体同步，返回 nil（调用方按"歌单不存在"处理）。
func (c *PlaylistClient) withTracks(ctx context.Context, p *Playlist) *Playlist {
	if p == nil {
		return nil
	}

	if len(p.TrackList) > 0 {
		return p
	}

	mbid := p.MBID()
	if mbid == "" {
		return nil
	}

	detail, err := c.FetchPlaylist(ctx, mbid)
	if err != nil {
		return nil
	}

	if detail.MBID() == "" {
		detail.ID = mbid
	}

	return detail
}

// getJSON 发起 GET 请求并解析 JSON 响应，对网络类错误做有限重试。
func (c *PlaylistClient) getJSON(ctx context.Context, endpoint string, out any) error {
	const maxAttempts = 3

	var lastErr error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * time.Second):
			}
		}

		body, statusCode, err := c.do(ctx, endpoint)
		if err == nil {
			// ListenBrainz 触发反爬校验时会返回 200 + HTML 页面，
			// 按临时错误处理并重试，避免一次拦截就让整轮同步失败。
			if jsonErr := json.Unmarshal(body, out); jsonErr == nil {
				return nil
			} else {
				lastErr = fmt.Errorf("解析 ListenBrainz 响应失败: %w（响应片段: %s）", jsonErr, snippet(body))

				continue
			}
		}

		lastErr = err

		// 4xx（用户名不存在、歌单不存在等）重试无意义。
		if statusCode >= 400 && statusCode < 500 {
			return err
		}
	}

	return lastErr
}

// snippet 截取响应体片段，便于在日志里判断失败原因（如反爬校验页）。
func snippet(body []byte) string {
	const max = 200

	s := strings.TrimSpace(string(body))
	if len(s) > max {
		s = s[:max]
	}

	return s
}

// maxLogBodyLB 请求日志中记录的响应体最大字节数。
const maxLogBodyLB = 65536

// do 执行一次 GET 请求，返回响应体与状态码。
func (c *PlaylistClient) do(ctx context.Context, endpoint string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("创建请求失败: %w", err)
	}

	req.Header.Set("User-Agent", buildinfo.UserAgent())
	req.Header.Set("Accept", "application/json")

	// 请求日志：暂存请求部分，响应返回时合并写出。
	u, _ := url.Parse(endpoint)
	query := ""
	if u != nil {
		query = u.RawQuery
	}
	entry := c.reqLog.Begin(req.Method, endpoint, query, req.Header.Clone(), nil)
	defer func() {
		if entry != nil {
			entry.Finish(0, nil, nil, 0)
		}
	}()

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("请求 ListenBrainz 失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		if entry != nil {
			entry.Finish(resp.StatusCode, resp.Header.Clone(), respBody, resp.ContentLength)
			entry = nil
		}
		return nil, resp.StatusCode, fmt.Errorf("ListenBrainz 歌单接口 HTTP 状态码: %s", resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		if entry != nil {
			entry.Finish(resp.StatusCode, resp.Header.Clone(), nil, resp.ContentLength)
			entry = nil
		}
		return nil, resp.StatusCode, fmt.Errorf("读取 ListenBrainz 响应失败: %w", err)
	}

	if entry != nil {
		logBody := body
		if len(logBody) > maxLogBodyLB {
			logBody = logBody[:maxLogBodyLB]
		}
		entry.Finish(resp.StatusCode, resp.Header.Clone(), logBody, resp.ContentLength)
		entry = nil
	}

	return body, resp.StatusCode, nil
}

// mbidFromURL 从形如 https://.../<uuid> 的地址中取出 MBID。
func mbidFromURL(raw string) string {
	if raw == "" {
		return ""
	}

	base := path.Base(strings.TrimRight(raw, "/"))
	if len(base) != 36 || strings.Count(base, "-") != 4 {
		return ""
	}

	return base
}

// recordingMBIDFromIdentifiers 从曲目的 identifier 中解析 recording MBID。
func recordingMBIDFromIdentifiers(identifiers []string) string {
	for _, id := range identifiers {
		if !strings.Contains(id, "/recording/") {
			continue
		}
		if mbid := mbidFromURL(id); mbid != "" {
			return mbid
		}
	}

	return ""
}

// artistMBIDFromExtension 从曲目 JSPF 扩展里取首个艺人 MBID。
func artistMBIDFromExtension(ext map[string]jspfTrackExtension) string {
	track, ok := ext[jspfTrackKey]
	if !ok {
		return ""
	}

	for _, artist := range track.AdditionalMetadata.Artists {
		if artist.ArtistMBID != "" {
			return artist.ArtistMBID
		}
	}

	return ""
}

// firstNonEmpty 返回第一个非空字符串。
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}

	return ""
}
