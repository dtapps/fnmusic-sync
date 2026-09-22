package lastfm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"cnb.cool/dtapp/fnmusic-sync/internal/buildinfo"
)

// PlaylistTrack Last.fm 歌单曲目信息（用于歌单同步匹配）。
type PlaylistTrack struct {
	// ArtistName 艺人名
	ArtistName string
	// TrackName 曲目标题
	TrackName string
	// AlbumName 专辑名
	AlbumName string
	// MBID 曲目 MBID（Last.fm 有时有，可用于精确匹配）
	MBID string
	// ArtistMBID 艺人 MBID
	ArtistMBID string
	// PlayCount 播放次数（top_tracks 才有）
	PlayCount int
	// Date 日期（recent_tracks 才有，格式 "dd MMM yyyy, HH:mm")
	Date string
}

// Playlist Last.fm 智能歌单。
type Playlist struct {
	// Title 歌单标题（用于日志）
	Title string
	// TrackList 曲目列表
	TrackList []PlaylistTrack
	// Date 歌单日期标识（如 "2026-09-08"）
	Date string
}

// GetTopTracks 获取用户最常听的曲目。
// period 可选值：7day / 1month / 3month / 6month / 12month / overall
// limit 最多返回的曲目数（Last.fm 上限 1000），0 表示不限制（取上限 1000）
func (c *Client) GetTopTracks(ctx context.Context, username, period string, limit int) (*Playlist, error) {
	if username == "" {
		return nil, fmt.Errorf("用户名不能为空")
	}

	if period == "" {
		period = "overall"
	}

	if limit <= 0 || limit > 1000 {
		limit = 1000
	}

	params := url.Values{}
	params.Set("method", "user.getTopTracks")
	params.Set("api_key", c.APIKey)
	params.Set("user", username)
	params.Set("period", period)
	params.Set("limit", strconv.Itoa(limit))
	params.Set("format", "json")

	endpoint := APIEndpoint + "?" + params.Encode()

	var result struct {
		TopTracks struct {
			Track []struct {
				Name      string `json:"name"`
				MBID      string `json:"mbid"`
				PlayCount int    `json:"playcount,string"`
				Artist    struct {
					Name string `json:"name"`
					MBID string `json:"mbid"`
				} `json:"artist"`
				Album struct {
					Name string `json:"name"`
				} `json:"album"`
			} `json:"track"`
		} `json:"toptracks"`
		Error   int    `json:"error"`
		Message string `json:"message"`
	}

	if err := c.getJSON(ctx, endpoint, &result); err != nil {
		return nil, err
	}

	if result.Error != 0 {
		return nil, fmt.Errorf("last.fm error %d: %s", result.Error, result.Message)
	}

	p := &Playlist{
		Title: fmt.Sprintf("Last.fm Top Tracks (%s)", period),
		Date:  time.Now().Format("2006-01-02"),
	}

	for _, t := range result.TopTracks.Track {
		p.TrackList = append(p.TrackList, PlaylistTrack{
			ArtistName: t.Artist.Name,
			TrackName:  t.Name,
			AlbumName:  t.Album.Name,
			MBID:       t.MBID,
			ArtistMBID: t.Artist.MBID,
			PlayCount:  t.PlayCount,
		})
	}

	return p, nil
}

// GetLovedTracks 获取用户红心收藏的曲目。
// limit 最多返回的曲目数（Last.fm 上限 1000），0 表示不限制（取上限 1000）
func (c *Client) GetLovedTracks(ctx context.Context, username string, limit int) (*Playlist, error) {
	if username == "" {
		return nil, fmt.Errorf("用户名不能为空")
	}

	if limit <= 0 || limit > 1000 {
		limit = 1000
	}

	params := url.Values{}
	params.Set("method", "user.getLovedTracks")
	params.Set("api_key", c.APIKey)
	params.Set("user", username)
	params.Set("limit", strconv.Itoa(limit))
	params.Set("format", "json")

	endpoint := APIEndpoint + "?" + params.Encode()

	var result struct {
		LovedTracks struct {
			Track []struct {
				Name   string `json:"name"`
				MBID   string `json:"mbid"`
				Artist struct {
					Name string `json:"name"`
					MBID string `json:"mbid"`
				} `json:"artist"`
				Album struct {
					Name string `json:"name"`
				} `json:"album"`
				Date struct {
					Text string `json:"#text"`
				} `json:"date"`
			} `json:"track"`
		} `json:"lovedtracks"`
		Error   int    `json:"error"`
		Message string `json:"message"`
	}

	if err := c.getJSON(ctx, endpoint, &result); err != nil {
		return nil, err
	}

	if result.Error != 0 {
		return nil, fmt.Errorf("last.fm error %d: %s", result.Error, result.Message)
	}

	p := &Playlist{
		Title: "Last.fm Loved Tracks",
		Date:  time.Now().Format("2006-01-02"),
	}

	for _, t := range result.LovedTracks.Track {
		p.TrackList = append(p.TrackList, PlaylistTrack{
			ArtistName: t.Artist.Name,
			TrackName:  t.Name,
			AlbumName:  t.Album.Name,
			MBID:       t.MBID,
			ArtistMBID: t.Artist.MBID,
			Date:       t.Date.Text,
		})
	}

	return p, nil
}

// GetRecentTracks 获取用户最近播放的曲目。
// limit 最多返回的曲目数（Last.fm 上限 200），0 表示不限制（取上限 200）
func (c *Client) GetRecentTracks(ctx context.Context, username string, limit int) (*Playlist, error) {
	if username == "" {
		return nil, fmt.Errorf("用户名不能为空")
	}

	if limit <= 0 || limit > 200 {
		limit = 200
	}

	params := url.Values{}
	params.Set("method", "user.getRecentTracks")
	params.Set("api_key", c.APIKey)
	params.Set("user", username)
	params.Set("limit", strconv.Itoa(limit))
	params.Set("format", "json")

	endpoint := APIEndpoint + "?" + params.Encode()

	var result struct {
		RecentTracks struct {
			Track []struct {
				Name   string `json:"name"`
				MBID   string `json:"mbid"`
				Artist struct {
					Name string `json:"#text"`
					MBID string `json:"mbid"`
				} `json:"artist"`
				Album struct {
					Name string `json:"#text"`
				} `json:"album"`
				Date struct {
					Text string `json:"#text"`
				} `json:"date"`
			} `json:"track"`
		} `json:"recenttracks"`
		Error   int    `json:"error"`
		Message string `json:"message"`
	}

	if err := c.getJSON(ctx, endpoint, &result); err != nil {
		return nil, err
	}

	if result.Error != 0 {
		return nil, fmt.Errorf("last.fm error %d: %s", result.Error, result.Message)
	}

	p := &Playlist{
		Title: "Last.fm Recent Tracks",
		Date:  time.Now().Format("2006-01-02"),
	}

	for _, t := range result.RecentTracks.Track {
		// Last.fm getRecentTracks 可能返回 "now playing" 的曲目（date 为空），跳过
		if t.Date.Text == "" && t.Name == "" {
			continue
		}

		p.TrackList = append(p.TrackList, PlaylistTrack{
			ArtistName: t.Artist.Name,
			TrackName:  t.Name,
			AlbumName:  t.Album.Name,
			MBID:       t.MBID,
			ArtistMBID: t.Artist.MBID,
			Date:       t.Date.Text,
		})
	}

	return p, nil
}

// GetWeeklyTrackChart 获取用户本周曲目榜单（Last.fm weeklytrackchart）。
// limit 最多返回的曲目数（Last.fm 上限约 1000），0 表示不限制（取上限 1000）。
// 返回按排名排序的歌单；接口无数据时返回空歌单（TrackList 为空）。
func (c *Client) GetWeeklyTrackChart(ctx context.Context, username string, limit int) (*Playlist, error) {
	if username == "" {
		return nil, fmt.Errorf("用户名不能为空")
	}
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}

	params := url.Values{}
	params.Set("method", "user.getWeeklyTrackChart")
	params.Set("api_key", c.APIKey)
	params.Set("user", username)
	params.Set("limit", strconv.Itoa(limit))
	params.Set("format", "json")

	endpoint := APIEndpoint + "?" + params.Encode()

	var result struct {
		WeeklyTrackChart struct {
			Track []struct {
				Name   string `json:"name"`
				MBID   string `json:"mbid"`
				Artist struct {
					Name string `json:"#text"`
					MBID string `json:"mbid"`
				} `json:"artist"`
				PlayCount int `json:"playcount,string"`
			} `json:"track"`
		} `json:"weeklytrackchart"`
		Error   int    `json:"error"`
		Message string `json:"message"`
	}

	if err := c.getJSON(ctx, endpoint, &result); err != nil {
		return nil, err
	}
	if result.Error != 0 {
		return nil, fmt.Errorf("last.fm error %d: %s", result.Error, result.Message)
	}

	p := &Playlist{
		Title: "Last.fm Weekly Track Chart",
		Date:  time.Now().Format("2006-01-02"),
	}
	for _, t := range result.WeeklyTrackChart.Track {
		p.TrackList = append(p.TrackList, PlaylistTrack{
			ArtistName: t.Artist.Name,
			TrackName:  t.Name,
			MBID:       t.MBID,
			ArtistMBID: t.Artist.MBID,
			PlayCount:  t.PlayCount,
		})
	}

	return p, nil
}

// getJSON 发送 GET 请求并解析 JSON 响应。
// Last.fm 的读取类 API（user.getTopTracks 等）只需 api_key，无需签名。
func (c *Client) getJSON(ctx context.Context, endpoint string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("创建请求失败: %w", err)
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
			entry.Finish(0, nil, nil, 0) // 兜底：请求发出但未收到响应
		}
	}()

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("请求 Last.fm 失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		if entry != nil {
			entry.Finish(resp.StatusCode, resp.Header.Clone(), respBody, resp.ContentLength)
			entry = nil
		}
		return fmt.Errorf("last.fm HTTP status: %s", resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		if entry != nil {
			entry.Finish(resp.StatusCode, resp.Header.Clone(), nil, resp.ContentLength)
			entry = nil
		}
		return fmt.Errorf("读取 Last.fm 响应失败: %w", err)
	}

	if entry != nil {
		entry.Finish(resp.StatusCode, resp.Header.Clone(), body, resp.ContentLength)
		entry = nil
	}

	// Last.fm 有时返回 JSON 有时返回 XML，这里只处理 JSON
	// 如果 JSON 解析失败，尝试从 body 中提取错误信息
	if jsonErr := json.Unmarshal(body, out); jsonErr != nil {
		// 检查是否是 XML 错误响应（如 403 等）
		if strings.Contains(string(body), "Forbidden") {
			return fmt.Errorf("last.fm 返回 403 Forbidden（可能被反爬拦截）")
		}
		return fmt.Errorf("解析 Last.fm 响应失败: %w (响应片段: %s)", jsonErr, snippet(body))
	}

	return nil
}

// lastFMUsername 从 session key 获取不到，需要单独传入
// 这里提供一个辅助方法，从 auth.getSession 的返回中拿到的 username 存在 config 里

// PeriodLastFM 统计周期的有效值列表。
var validPeriods = []string{"7day", "1month", "3month", "6month", "12month", "overall"}

// IsValidPeriod 判断统计周期是否有效。
func IsValidPeriod(period string) bool {
	return slices.Contains(validPeriods, period)
}

// snippet 截取响应体片段。
func snippet(body []byte) string {
	const max = 200
	s := strings.TrimSpace(string(body))
	if len(s) > max {
		s = s[:max]
	}
	return s
}
