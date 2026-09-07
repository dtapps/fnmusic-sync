// Package feiniu 提供飞牛音乐 API 客户端。
// 所有与飞牛音乐服务器的交互都通过此包进行。
package feiniu

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"unicode"
)

// Track 飞牛音乐曲目结构。
type Track struct {
	GUID    string `json:"guid"`
	Title   string `json:"title"`
	CoverId string `json:"coverId"`
	// ISRC 国际标准录音代码，可用于精确匹配
	ISRC     string `json:"isrc,omitempty"`
	Duration int64  `json:"duration"`
	Album    struct {
		GUID string `json:"guid"`
		Name string `json:"name"`
	} `json:"album"`
	Artists []struct {
		GUID string `json:"guid"`
		Name string `json:"name"`
	} `json:"artists"`
	AudioSpec struct {
		Duration int64  `json:"duration"`
		Path     string `json:"path"`
	} `json:"audioSpec"`
}

// Playlist 飞牛音乐歌单结构。
type Playlist struct {
	GUID       string `json:"guid"`
	Name       string `json:"name"`
	CoverId    string `json:"coverId"`
	CreatedAt  int64  `json:"createdAt"`
	UpdatedAt  int64  `json:"updatedAt"`
	TrackCount int    `json:"trackCount"`
}

// trackListResponse 曲目列表响应。
type trackListResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		List []Track `json:"list"`
	} `json:"data"`
}

// playlistListResponse 歌单列表响应。
type playlistListResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		List []Playlist `json:"list"`
	} `json:"data"`
}

// createPlaylistRequest 创建歌单请求。
type createPlaylistRequest struct {
	CoverId string `json:"coverId"`
	Name    string `json:"name"`
}

// createPlaylistResponse 创建歌单响应。
type createPlaylistResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		GUID      string `json:"guid"`
		Name      string `json:"name"`
		CoverId   string `json:"coverId"`
		CreatedAt int64  `json:"createdAt"`
		UpdatedAt int64  `json:"updatedAt"`
	} `json:"data"`
}

// addTrackRequest 添加曲目到歌单请求。
type addTrackRequest struct {
	GUID       string   `json:"guid"`
	TrackGUIDs []string `json:"trackGUIDs"`
}

// Response 通用响应。
type Response struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data any    `json:"data"`
}

// TrackIndexes 曲目索引，支持多种匹配方式。
type TrackIndexes struct {
	// MBID -> GUID（精确匹配，飞牛曲库没有 MBID 时为空）
	MBIDToGUID map[string]string
	// 艺人名+曲名 -> GUID（模糊匹配，降级方案）
	ArtistTrackToGUID map[string]string
	// 曲名 -> GUID（仅当曲名在曲库中唯一时才建立，最后降级方案）
	TitleToGUID map[string]string
	// GUID -> Track（用于获取封面等信息）
	GUIDToTrack map[string]Track
}

// BuildTrackIndexes 获取所有曲目并建立索引。
// 飞牛音乐没有 MBID 字段，主要使用艺人名+曲名进行匹配，
// 曲名唯一的曲目额外建"仅曲名"索引作为最后降级（应对中英文/繁简差异）。
func (c *Client) BuildTrackIndexes(ctx context.Context) (*TrackIndexes, error) {
	indexes := &TrackIndexes{
		MBIDToGUID:        make(map[string]string),
		ArtistTrackToGUID: make(map[string]string),
		TitleToGUID:       make(map[string]string),
		GUIDToTrack:       make(map[string]Track),
	}
	// 曲名 -> 命中的 GUID 列表，只有唯一时才写入 TitleToGUID，避免误匹配。
	titleCandidates := make(map[string][]string)

	page := 1
	size := 200
	totalTracks := 0

	for {
		tracks, err := c.FetchTrackList(ctx, page, size)
		if err != nil {
			return nil, err
		}

		for _, track := range tracks {
			// 保存曲目信息（用于获取封面）
			indexes.GUIDToTrack[track.GUID] = track

			// 建立艺人名+曲名索引
			artistName := ""
			if len(track.Artists) > 0 {
				artistName = track.Artists[0].Name
			}
			if artistName != "" && track.Title != "" {
				key := BuildArtistTrackKey(artistName, track.Title)
				indexes.ArtistTrackToGUID[key] = track.GUID

				// 同时按"主标题"建索引：推荐歌单的曲名常带附注后缀，
				// 本地曲库往往只有主标题，截断后再比对可显著提高命中率。
				if main := MainTitle(track.Title); main != track.Title {
					indexes.ArtistTrackToGUID[BuildArtistTrackKey(artistName, main)] = track.GUID
				}
			}

			if track.Title != "" {
				titleKey := normalizeForMatch(track.Title)
				titleCandidates[titleKey] = append(titleCandidates[titleKey], track.GUID)

				if mainKey := normalizeForMatch(MainTitle(track.Title)); mainKey != titleKey {
					titleCandidates[mainKey] = append(titleCandidates[mainKey], track.GUID)
				}
			}
		}

		totalTracks += len(tracks)

		if len(tracks) < size {
			break
		}
		page++
	}

	for title, guids := range titleCandidates {
		if len(guids) == 1 {
			indexes.TitleToGUID[title] = guids[0]
		}
	}

	c.logger.Info("曲目索引统计",
		"总曲目数", totalTracks,
		"艺人+曲名索引数", len(indexes.ArtistTrackToGUID),
		"唯一曲名索引数", len(indexes.TitleToGUID),
	)

	return indexes, nil
}

// FindOrCreatePlaylist 查找或创建歌单。
// coverId 用于创建新歌单时的封面，如果为空则使用默认封面。
func (c *Client) FindOrCreatePlaylist(ctx context.Context, name, coverId string) (string, error) {
	// 先搜索同名歌单
	playlist, err := c.findPlaylistByName(ctx, name)
	if err != nil {
		return "", err
	}

	if playlist != nil {
		return playlist.GUID, nil
	}

	// 不存在则创建
	return c.createPlaylist(ctx, name, coverId)
}

// findPlaylistByName 根据名称查找歌单。
//
// 先遍历歌单列表（结果最全、不依赖搜索分词），找不到再用搜索接口兜底。
// 顺序反过来的话，搜索一旦返回空（中文名/分词问题）就会重复创建同名歌单。
func (c *Client) findPlaylistByName(ctx context.Context, name string) (*Playlist, error) {
	if p, err := c.findPlaylistInAllLists(ctx, name); err != nil || p != nil {
		return p, err
	}

	url := fmt.Sprintf("/music/api/v1/search/playlist?q=%s&page=1&size=50", url.QueryEscape(name))

	var resp playlistListResponse
	if err := c.doGet(ctx, url, &resp); err != nil {
		// 搜索接口不可用时按"未找到"处理，由调用方创建。
		return nil, nil
	}

	for _, p := range resp.Data.List {
		if p.Name == name {
			return &p, nil
		}
	}

	return nil, nil
}

// findPlaylistInAllLists 从所有歌单中查找。
func (c *Client) findPlaylistInAllLists(ctx context.Context, name string) (*Playlist, error) {
	url := "/music/api/v1/playlist/list?page=1&size=200"

	var resp playlistListResponse
	if err := c.doGet(ctx, url, &resp); err != nil {
		return nil, err
	}

	for _, p := range resp.Data.List {
		if p.Name == name {
			return &p, nil
		}
	}

	return nil, nil
}

// createPlaylist 创建歌单。
// coverId 为歌单封面 ID，如果为空则使用默认封面。
func (c *Client) createPlaylist(ctx context.Context, name, coverId string) (string, error) {
	if coverId == "" {
		coverId = "playlist_1acb445b79d5476aa2c46a58c44d3aee" // 默认封面
	}
	req := createPlaylistRequest{
		CoverId: coverId,
		Name:    name,
	}

	var resp createPlaylistResponse
	if err := c.doPost(ctx, "/music/api/v1/playlist/create", req, &resp); err != nil {
		return "", err
	}

	return resp.Data.GUID, nil
}

// addTracksBatchSize 单次 add-track 请求的曲目数量上限，避免请求体过大。
const addTracksBatchSize = 50

// AddTracksToPlaylist 添加曲目到歌单（按批提交）。
func (c *Client) AddTracksToPlaylist(ctx context.Context, playlistGUID string, trackGUIDs []string) error {
	for start := 0; start < len(trackGUIDs); start += addTracksBatchSize {
		end := min(start+addTracksBatchSize, len(trackGUIDs))

		req := addTrackRequest{
			GUID:       playlistGUID,
			TrackGUIDs: trackGUIDs[start:end],
		}

		var resp Response
		if err := c.doPost(ctx, "/music/api/v1/playlist/add-track", req, &resp); err != nil {
			return err
		}
	}

	return nil
}

// playlistDetailResponse 歌单详情响应。
// 不同版本返回的曲目字段名不一致（tracks / list / trackList / trackGUIDs），
// 这里做宽松解析，任一字段命中即可。
type playlistDetailResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		GUID       string   `json:"guid"`
		TrackGUIDs []string `json:"trackGUIDs"`
		Tracks     []struct {
			GUID string `json:"guid"`
		} `json:"tracks"`
		List []struct {
			GUID string `json:"guid"`
		} `json:"list"`
		TrackList []struct {
			GUID string `json:"guid"`
		} `json:"trackList"`
	} `json:"data"`
}

// PlaylistTrackGUIDs 读取歌单现有曲目 GUID 集合，用于差量同步。
//
// add-track 是追加语义，若每轮都全量追加，歌单会不断堆积重复曲目。
// 该接口不可用时返回错误，由调用方降级处理。
func (c *Client) PlaylistTrackGUIDs(ctx context.Context, playlistGUID string) (map[string]struct{}, error) {
	path := "/music/api/v1/playlist/detail?guid=" + url.QueryEscape(playlistGUID)

	var resp playlistDetailResponse
	if err := c.doGet(ctx, path, &resp); err != nil {
		return nil, err
	}

	existing := make(map[string]struct{}, len(resp.Data.TrackGUIDs))
	for _, guid := range resp.Data.TrackGUIDs {
		if guid != "" {
			existing[guid] = struct{}{}
		}
	}
	for _, t := range resp.Data.Tracks {
		if t.GUID != "" {
			existing[t.GUID] = struct{}{}
		}
	}
	for _, t := range resp.Data.List {
		if t.GUID != "" {
			existing[t.GUID] = struct{}{}
		}
	}
	for _, t := range resp.Data.TrackList {
		if t.GUID != "" {
			existing[t.GUID] = struct{}{}
		}
	}

	return existing, nil
}

// FetchTrackList 获取曲目列表。
func (c *Client) FetchTrackList(ctx context.Context, page, size int) ([]Track, error) {
	url := fmt.Sprintf("/music/api/v1/track/list?page=%d&size=%d", page, size)

	var resp trackListResponse
	if err := c.doGet(ctx, url, &resp); err != nil {
		return nil, err
	}

	return resp.Data.List, nil
}

// BuildArtistTrackKey 构建艺人名+曲名的匹配键。
// 统一格式化为小写并去除空格，用于模糊匹配。
func BuildArtistTrackKey(artist, track string) string {
	return normalizeForMatch(artist) + "|" + normalizeForMatch(track)
}

// NormalizeForMatch 导出匹配键的归一化规则，供上层做"仅曲名"匹配时使用。
func NormalizeForMatch(s string) string {
	return normalizeForMatch(s)
}

// titleNoteOpeners 曲名附注的起始括号（含全角）。
const titleNoteOpeners = "([（［【"

// MainTitle 截取曲名主标题：去掉第一个附注括号及其之后的内容。
//
// ListenBrainz 的曲名常带附注，如
// "不将就 (Can't Bear It) (电影《何以笙箫默》片尾曲)"，
// 而本地曲库通常只有 "不将就"，截断后再比对可以显著提高命中率。
// 没有附注（或附注在开头）时原样返回。
func MainTitle(title string) string {
	if idx := strings.IndexAny(title, titleNoteOpeners); idx > 0 {
		if main := strings.TrimSpace(title[:idx]); main != "" {
			return main
		}
	}

	return title
}

// normalizeForMatch 标准化字符串用于匹配：小写化、去除所有空白字符。
// 曲名/艺人名常出现大小写与空格差异（如 "G.E.M." 与 "gem "），统一后再比对。
func normalizeForMatch(s string) string {
	s = strings.ToLower(s)

	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}

		return r
	}, s)
}
