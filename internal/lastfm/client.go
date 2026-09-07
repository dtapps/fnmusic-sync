// Package lastfm 提供 Last.fm API 客户端。
// 所有与 Last.fm 的交互（scrobble、认证）都通过此包进行。
package lastfm

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"cnb.cool/dtapp/fnmusic-sync/internal/buildinfo"
	"cnb.cool/dtapp/fnmusic-sync/internal/model"
	"cnb.cool/dtapp/fnmusic-sync/internal/reqlog"
)

// APIEndpoint 是 Last.fm API 的固定地址。
const APIEndpoint = "https://ws.audioscrobbler.com/2.0/"

// maxLogBody 请求日志中记录的响应体最大字节数。
const maxLogBody = 65536

// Client Last.fm API 客户端。
type Client struct {
	APIKey     string
	APISecret  string
	SessionKey string
	reqLog     *reqlog.Logger
	httpClient *http.Client
}

// NewClient 创建 Last.fm API 客户端。
// reqLog 为请求日志记录器，nil 时跳过请求日志。
func NewClient(apiKey, apiSecret, sessionKey string, reqLog *reqlog.Logger) *Client {
	return &Client{
		APIKey:     apiKey,
		APISecret:  apiSecret,
		SessionKey: sessionKey,
		reqLog:     reqLog,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// Name 返回平台标识。
func (c *Client) Name() string {
	return "lastfm"
}

// UpdateNowPlaying 更新当前播放状态。
func (c *Client) UpdateNowPlaying(ctx context.Context, track model.Track) error {
	params := url.Values{}

	params.Set("method", "track.updateNowPlaying")
	params.Set("api_key", c.APIKey)
	params.Set("sk", c.SessionKey)

	params.Set("artist", track.Artist)
	params.Set("track", track.Title)

	if track.Album != "" {
		params.Set("album", track.Album)
	}

	if track.AlbumArtist != "" {
		params.Set("albumArtist", track.AlbumArtist)
	}

	if track.Duration > 0 {
		params.Set("duration", strconv.FormatInt(int64(track.Duration.Seconds()), 10))
	}

	if track.MBID != "" {
		params.Set("mbid", track.MBID)
	}

	params.Set("api_sig", c.signature(params))
	params.Set("format", "json")

	return c.post(ctx, params)
}

// Scrobble 提交播放记录。
func (c *Client) Scrobble(ctx context.Context, track model.Track, startedAt int64) error {
	params := url.Values{}

	params.Set("method", "track.scrobble")
	params.Set("api_key", c.APIKey)
	params.Set("sk", c.SessionKey)

	params.Set("artist[0]", track.Artist)
	params.Set("track[0]", track.Title)
	params.Set("timestamp[0]", strconv.FormatInt(startedAt, 10))

	if track.Album != "" {
		params.Set("album[0]", track.Album)
	}

	if track.AlbumArtist != "" {
		params.Set("albumArtist[0]", track.AlbumArtist)
	}

	if track.MBID != "" {
		params.Set("mbid[0]", track.MBID)
	}

	if track.Duration > 0 {
		params.Set("duration[0]", strconv.FormatInt(int64(track.Duration.Seconds()), 10))
	}

	params.Set("api_sig", c.signature(params))
	params.Set("format", "json")

	return c.post(ctx, params)
}

// signature 计算 Last.fm API 请求签名。
func (c *Client) signature(params url.Values) string {
	keys := make([]string, 0, len(params))

	for key := range params {
		if key == "format" || key == "callback" {
			continue
		}
		keys = append(keys, key)
	}

	sort.Strings(keys)

	var b strings.Builder
	for _, key := range keys {
		b.WriteString(key)
		b.WriteString(params.Get(key))
	}
	b.WriteString(c.APISecret)

	sum := md5.Sum([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// post 发送 POST 请求到 Last.fm API。
func (c *Client) post(ctx context.Context, params url.Values) error {
	body := []byte(params.Encode())

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		APIEndpoint,
		strings.NewReader(params.Encode()),
	)
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", buildinfo.UserAgent())

	// 请求日志：暂存请求部分，响应返回时合并写出。
	entry := c.reqLog.Begin(req.Method, APIEndpoint, params.Encode(), req.Header.Clone(), body)
	defer func() {
		if entry != nil {
			entry.Finish(0, nil, nil, 0) // 兜底：请求发出但未收到响应
		}
	}()

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
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

	// 200：用 TeeReader 同时捕获响应体副本供日志使用，不影响正常解码。
	var logBuf limitedBuffer
	bodyReader := io.TeeReader(resp.Body, &logBuf)

	var result struct {
		Error   int    `json:"error"`
		Message string `json:"message"`
	}

	err = json.NewDecoder(bodyReader).Decode(&result)

	if entry != nil {
		entry.Finish(resp.StatusCode, resp.Header.Clone(), logBuf.Bytes(), resp.ContentLength)
		entry = nil
	}

	if err != nil {
		return err
	}

	if result.Error != 0 {
		return fmt.Errorf("last.fm error %d: %s", result.Error, result.Message)
	}

	return nil
}

// postJSON 以 application/x-www-form-urlencoded 提交并解码 JSON 响应。
func (c *Client) postJSON(ctx context.Context, params url.Values, out any) error {
	body := []byte(params.Encode())

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		APIEndpoint,
		strings.NewReader(params.Encode()),
	)
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", buildinfo.UserAgent())

	// 请求日志。
	entry := c.reqLog.Begin(req.Method, APIEndpoint, params.Encode(), req.Header.Clone(), body)
	defer func() {
		if entry != nil {
			entry.Finish(0, nil, nil, 0)
		}
	}()

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
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

	var logBuf limitedBuffer
	bodyReader := io.TeeReader(resp.Body, &logBuf)

	err = json.NewDecoder(bodyReader).Decode(out)

	if entry != nil {
		entry.Finish(resp.StatusCode, resp.Header.Clone(), logBuf.Bytes(), resp.ContentLength)
		entry = nil
	}

	return err
}

// limitedBuffer 是一个最多写入 maxLogBody 字节的 bytes.Buffer。
// 超出部分静默丢弃，用于请求日志中捕获响应体副本而不占用过多内存。
type limitedBuffer struct {
	buf  bytes.Buffer
	full bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.full {
		return len(p), nil
	}
	remaining := maxLogBody - b.buf.Len()
	if remaining <= 0 {
		b.full = true
		return len(p), nil
	}
	if len(p) <= remaining {
		return b.buf.Write(p)
	}
	b.buf.Write(p[:remaining])
	b.full = true
	return len(p), nil
}

func (b *limitedBuffer) Bytes() []byte {
	return b.buf.Bytes()
}
