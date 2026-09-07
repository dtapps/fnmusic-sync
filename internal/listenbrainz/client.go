// Package listenbrainz 提供 ListenBrainz API 客户端。
// 所有与 ListenBrainz 的交互（scrobble、歌单获取）都通过此包进行。
package listenbrainz

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"cnb.cool/dtapp/fnmusic-sync/internal/buildinfo"
	"cnb.cool/dtapp/fnmusic-sync/internal/model"
	"cnb.cool/dtapp/fnmusic-sync/internal/reqlog"
)

// APIEndpoint 是 ListenBrainz submit-listens 的固定地址。
const APIEndpoint = "https://api.listenbrainz.org/1/submit-listens"

// validateTokenEndpoint 验证 token 并返回用户名的 API 地址。
// 响应格式：{"valid": true, "user_name": "alice"}
const validateTokenEndpoint = "https://api.listenbrainz.org/1/validate-token"

// maxLogBody 请求日志中记录的响应体最大字节数。
const maxLogBody = 65536

// Client ListenBrainz API 客户端。
type Client struct {
	Token string
	// Username 保留给将来的歌单同步使用（/1/user/{username}/playlists）；
	// scrobble（submit-listens）不需要它，可以为空。
	Username string

	reqLog     *reqlog.Logger
	httpClient *http.Client
}

// NewClient 创建 ListenBrainz API 客户端。
// reqLog 为请求日志记录器，nil 时跳过请求日志。
func NewClient(token, username string, reqLog *reqlog.Logger) *Client {
	return &Client{
		Token:    token,
		Username: username,
		reqLog:   reqLog,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// Name 返回平台标识。
func (c *Client) Name() string {
	return "listenbrainz"
}

// ValidateToken 调用 /1/validate-token 验证 token 有效性并返回 ListenBrainz 用户名。
// token 有效时返回 (username, nil)；无效或网络错误时返回 ("", err)。
func (c *Client) ValidateToken(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, validateTokenEndpoint, nil)
	if err != nil {
		return "", fmt.Errorf("创建请求失败: %w", err)
	}

	req.Header.Set("Authorization", "Token "+c.Token)
	req.Header.Set("User-Agent", buildinfo.UserAgent())

	// 请求日志
	entry := c.reqLog.Begin(req.Method, validateTokenEndpoint, "", req.Header.Clone(), nil)
	defer func() {
		if entry != nil {
			entry.Finish(0, nil, nil, 0)
		}
	}()

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("请求 ListenBrainz 失败: %w", err)
	}
	defer resp.Body.Close()

	var logBuf limitedBuffer
	bodyReader := io.TeeReader(resp.Body, &logBuf)

	var result struct {
		Valid    bool   `json:"valid"`
		Username string `json:"user_name"`
	}
	if err := json.NewDecoder(bodyReader).Decode(&result); err != nil {
		if entry != nil {
			entry.Finish(resp.StatusCode, resp.Header.Clone(), logBuf.Bytes(), resp.ContentLength)
			entry = nil
		}
		return "", fmt.Errorf("解析 ListenBrainz 响应失败: %w", err)
	}

	if entry != nil {
		entry.Finish(resp.StatusCode, resp.Header.Clone(), logBuf.Bytes(), resp.ContentLength)
		entry = nil
	}

	if !result.Valid {
		return "", fmt.Errorf("ListenBrainz token 无效")
	}

	return result.Username, nil
}

// listenPayload 提交给 ListenBrainz 的数据结构。
type listenPayload struct {
	ListenType string `json:"listen_type"`
	Payload    []struct {
		ListenedAt    int64 `json:"listened_at,omitempty"`
		TrackMetadata struct {
			ArtistName  string `json:"artist_name"`
			TrackName   string `json:"track_name"`
			ReleaseName string `json:"release_name,omitempty"`

			AdditionalInfo map[string]any `json:"additional_info,omitempty"`
		} `json:"track_metadata"`
	} `json:"payload"`
}

// buildPayload 构建 ListenBrainz 提交数据。
func (c *Client) buildPayload(listenType string, track model.Track, startedAt int64) listenPayload {
	var item struct {
		ListenedAt    int64 `json:"listened_at,omitempty"`
		TrackMetadata struct {
			ArtistName  string `json:"artist_name"`
			TrackName   string `json:"track_name"`
			ReleaseName string `json:"release_name,omitempty"`

			AdditionalInfo map[string]any `json:"additional_info,omitempty"`
		} `json:"track_metadata"`
	}

	item.ListenedAt = startedAt
	item.TrackMetadata.ArtistName = track.Artist
	item.TrackMetadata.TrackName = track.Title
	item.TrackMetadata.ReleaseName = track.Album

	item.TrackMetadata.AdditionalInfo = make(map[string]any)

	if track.Duration > 0 {
		item.TrackMetadata.AdditionalInfo["duration_ms"] = track.Duration.Milliseconds()
	}

	if track.RecordingMBID != "" {
		item.TrackMetadata.AdditionalInfo["recording_mbid"] = track.RecordingMBID
	}

	if track.ReleaseMBID != "" {
		item.TrackMetadata.AdditionalInfo["release_mbid"] = track.ReleaseMBID
	}

	if track.ArtistMBID != "" {
		item.TrackMetadata.AdditionalInfo["artist_mbid"] = track.ArtistMBID
	}

	return listenPayload{
		ListenType: listenType,
		Payload: []struct {
			ListenedAt    int64 `json:"listened_at,omitempty"`
			TrackMetadata struct {
				ArtistName     string         `json:"artist_name"`
				TrackName      string         `json:"track_name"`
				ReleaseName    string         `json:"release_name,omitempty"`
				AdditionalInfo map[string]any `json:"additional_info,omitempty"`
			} `json:"track_metadata"`
		}{item},
	}
}

// UpdateNowPlaying 更新当前播放状态。
func (c *Client) UpdateNowPlaying(ctx context.Context, track model.Track) error {
	payload := c.buildPayload("playing_now", track, 0)

	// playing_now 不应该携带 listened_at。
	payload.Payload[0].ListenedAt = 0

	return c.post(ctx, payload)
}

// Scrobble 提交播放记录。
func (c *Client) Scrobble(ctx context.Context, track model.Track, startedAt int64) error {
	payload := c.buildPayload("single", track, startedAt)

	return c.post(ctx, payload)
}

// post 发送 POST 请求到 ListenBrainz API。
func (c *Client) post(ctx context.Context, payload listenPayload) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		APIEndpoint,
		bytes.NewReader(data),
	)
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Token "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", buildinfo.UserAgent())

	// 请求日志：暂存请求部分（含请求体），响应返回时合并写出。
	entry := c.reqLog.Begin(req.Method, APIEndpoint, "", req.Header.Clone(), data)
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

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		if entry != nil {
			entry.Finish(resp.StatusCode, resp.Header.Clone(), respBody, resp.ContentLength)
			entry = nil
		}
		return fmt.Errorf("listenbrainz HTTP status: %s", resp.Status)
	}

	// 2xx：用 TeeReader 同时捕获响应体副本供日志使用。
	var logBuf limitedBuffer
	bodyReader := io.TeeReader(resp.Body, &logBuf)

	// 消费响应体以完成 TeeReader 读取。
	_, _ = io.Copy(io.Discard, bodyReader)

	if entry != nil {
		entry.Finish(resp.StatusCode, resp.Header.Clone(), logBuf.Bytes(), resp.ContentLength)
		entry = nil
	}

	return nil
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
