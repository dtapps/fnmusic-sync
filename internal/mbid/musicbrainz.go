// Package mbid 的 MusicBrainz 在线查询客户端。
//
// 当本地音频文件标签缺 MBID 时（matched_by=tag 无从获取），可在线查询
// MusicBrainz WS/2 按「艺人名+曲名」补全录音 MBID，避免手动用 Picard 打标。
// MusicBrainz 服务条款要求：每秒最多 1 个请求，且必须携带有意义的 User-Agent，
// 否则返回 503。本客户端内置限速（~1.1s/请求）与 503 退避。
package mbid

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"cnb.cool/dtapp/fnmusic-sync/internal/buildinfo"
)

const (
	musicbrainzBase = "https://musicbrainz.org/ws/2"
	// minRequestInterval 略大于 1s，满足 MB「每秒最多 1 请求」的硬性限速。
	minRequestInterval = 1100 * time.Millisecond
)

// MusicBrainzClient 对 MusicBrainz WS/2 的受限查询客户端（单实例可安全复用）。
type MusicBrainzClient struct {
	http    *http.Client
	minWait time.Duration
	mu      sync.Mutex
	last    time.Time
}

// NewMusicBrainzClient 构造客户端（轻量，可随时创建）。
func NewMusicBrainzClient() *MusicBrainzClient {
	return &MusicBrainzClient{
		http:    &http.Client{Timeout: 10 * time.Second},
		minWait: minRequestInterval,
	}
}

type mbArtistCredit struct {
	Artist struct {
		Name string `json:"name"`
		ID   string `json:"id"`
	} `json:"artist"`
}

type mbRecording struct {
	ID           string           `json:"id"`
	Score        string           `json:"score"`
	Title        string           `json:"title"`
	ArtistCredit []mbArtistCredit `json:"artist-credit"`
}

type mbResponse struct {
	Recordings []mbRecording `json:"recordings"`
}

// RecordingMBIDByArtistTitle 按「艺人名+曲名」查询录音 MBID。
// 返回 ("", false) 表示未查到 / 查询被限流 / 网络错误（调用方应跳过该曲，不中断整体扫描）。
// 优先选取 artist-credit 与主艺人精确匹配的结果；无精确匹配时退化为评分最高者。
func (c *MusicBrainzClient) RecordingMBIDByArtistTitle(ctx context.Context, artist, title string) (string, bool) {
	if artist == "" || title == "" {
		return "", false
	}

	q := url.Values{}
	q.Set("query", fmt.Sprintf(`artist:"%s" AND title:"%s"`, escapeQuotes(artist), escapeQuotes(title)))
	q.Set("fmt", "json")
	reqURL := musicbrainzBase + "/recording/?" + q.Encode()

	c.throttle()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return "", false
	}
	req.Header.Set("User-Agent", buildinfo.UserAgent())
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()

	// 触发限流/服务不可用：退避后返回 false，由扫描循环决定是否重试。
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable {
		time.Sleep(2 * time.Second)
		return "", false
	}
	if resp.StatusCode != http.StatusOK {
		return "", false
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", false
	}
	var mb mbResponse
	if err := json.Unmarshal(body, &mb); err != nil {
		return "", false
	}
	if len(mb.Recordings) == 0 {
		return "", false
	}

	// 精确匹配艺人优先；否则取评分最高（MB 默认按 score 降序）的第一个。
	for _, rec := range mb.Recordings {
		for _, ac := range rec.ArtistCredit {
			if strings.EqualFold(ac.Artist.Name, artist) {
				return rec.ID, true
			}
		}
	}
	return mb.Recordings[0].ID, true
}

// throttle 保证两次请求间隔不小于 minWait（满足 MB 限速）。
func (c *MusicBrainzClient) throttle() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.last.IsZero() {
		if elapsed := time.Since(c.last); elapsed < c.minWait {
			time.Sleep(c.minWait - elapsed)
		}
	}
	c.last = time.Now()
}

// escapeQuotes 把曲名/艺人里的双引号换成空格，避免破坏 MB 查询的引号语法。
func escapeQuotes(s string) string {
	return strings.ReplaceAll(s, `"`, " ")
}
