package feiniu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"

	"cnb.cool/dtapp/fnmusic-sync/internal/reqlog"
)

// virtualHost 是 Unix Socket 连接的虚拟主机名。
// 通过 Unix Socket 连接时，HTTP 请求需要指定 Host 头，
// 这里使用 "trim-music" 作为虚拟主机名。
const virtualHost = "http://trim-music"

// maxLogBody 请求日志中记录的响应体最大字节数。
const maxLogBody = 65536

// Client 飞牛音乐 API 客户端。
// 通过 Unix Socket 连接到飞牛音乐上游服务。
type Client struct {
	logger     *slog.Logger
	reqLog     *reqlog.Logger
	upstream   string // Unix Socket 路径
	userToken  string // 用户认证 token（music-token）
	httpClient *http.Client
}

// NewClient 创建飞牛音乐 API 客户端。
// upstreamSocket 是 Unix Socket 路径，userToken 是用户的 music-token。
// reqLog 为请求日志记录器，nil 时跳过请求日志。
func NewClient(upstreamSocket, userToken string, logger *slog.Logger, reqLog *reqlog.Logger) *Client {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			dialer := &net.Dialer{Timeout: 5 * time.Second}
			return dialer.DialContext(ctx, "unix", upstreamSocket)
		},
		// 空闲出站长连接 45s 即回收：不设 IdleConnTimeout 时 keep-alive 连接
		// 会永久驻留连接池，每对 readLoop/writeLoop 各挂一个 goroutine 空闲数十分钟，
		// 触发 goroutine 监视器的"出站长连接空闲"告警。
		IdleConnTimeout:     45 * time.Second,
		MaxIdleConnsPerHost: 2,
	}

	return &Client{
		logger:    logger,
		reqLog:    reqLog,
		upstream:  upstreamSocket,
		userToken: userToken,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   60 * time.Second,
		},
	}
}

// SetUserToken 更新用户认证 token。
func (c *Client) SetUserToken(token string) {
	c.userToken = token
}

// GetUserToken 获取当前用户认证 token。
func (c *Client) GetUserToken() string {
	return c.userToken
}

// doGet 发送 GET 请求。
func (c *Client) doGet(ctx context.Context, path string, result any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, virtualHost+path, nil)
	if err != nil {
		return err
	}

	// 设置用户认证 token
	if c.userToken != "" {
		req.Header.Set("Cookie", "music-token="+c.userToken)
	}

	// 请求日志：暂存请求部分，响应返回时合并写出。
	entry := c.reqLog.Begin(req.Method, path, "", req.Header.Clone(), nil)
	defer func() {
		if entry != nil {
			entry.Finish(0, nil, nil, 0) // 兜底：请求发出但未收到响应
		}
	}()

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("请求失败 %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		if entry != nil {
			entry.Finish(resp.StatusCode, resp.Header.Clone(), body, resp.ContentLength)
			entry = nil
		}
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	// 200：用 TeeReader 同时捕获响应体副本（限 maxLogBody）供日志使用，不影响正常解码。
	var logBuf limitedBuffer
	bodyReader := io.TeeReader(resp.Body, &logBuf)

	err = json.NewDecoder(bodyReader).Decode(result)

	if entry != nil {
		entry.Finish(resp.StatusCode, resp.Header.Clone(), logBuf.Bytes(), resp.ContentLength)
		entry = nil
	}

	return err
}

// doPost 发送 POST 请求。
func (c *Client) doPost(ctx context.Context, path string, body, result any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, virtualHost+path, bytes.NewReader(data))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")

	// 设置用户认证 token
	if c.userToken != "" {
		req.Header.Set("Cookie", "music-token="+c.userToken)
	}

	// 请求日志：暂存请求部分（含请求体），响应返回时合并写出。
	entry := c.reqLog.Begin(req.Method, path, "", req.Header.Clone(), data)
	defer func() {
		if entry != nil {
			entry.Finish(0, nil, nil, 0)
		}
	}()

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("请求失败 %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		if entry != nil {
			entry.Finish(resp.StatusCode, resp.Header.Clone(), respBody, resp.ContentLength)
			entry = nil
		}
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	// 200：用 TeeReader 同时捕获响应体副本供日志使用。
	var logBuf limitedBuffer
	bodyReader := io.TeeReader(resp.Body, &logBuf)

	err = json.NewDecoder(bodyReader).Decode(result)

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
		return len(p), nil // 丢弃，但告知已全部写入
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
