package proxy

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"time"
)

const upstreamDialTimeout = 3 * time.Second

// healthResponse 兼容参考项目的 healthz 返回：
//
//	{"ok":true,"upstream":"ok","socket":"/var/run/trim_music_upstream.socket"}
type healthResponse struct {
	OK       bool   `json:"ok"`
	Upstream string `json:"upstream"`
	Socket   string `json:"socket"`
	Error    string `json:"error,omitempty"`
}

// CheckUpstream 检查 upstream socket 上的官方后端是否可用。
//
// 只要能连上并且拿到 HTTP 响应就算健康（未登录返回 INVALID TOKEN 也是健康的），
// 只有传输层失败才判定为不可用。
func (p *Proxy) CheckUpstream(ctx context.Context) error {
	return p.CheckSocket(ctx, p.cfg.UpstreamSocket)
}

// CheckSocket 检查指定 unix socket 上的 HTTP 服务是否可用。
func (p *Proxy) CheckSocket(ctx context.Context, path string) error {
	start := time.Now()

	err := p.checkSocket(ctx, path)

	p.logger.Debug("探测上游",
		"上游", path,
		"耗时", time.Since(start).String(),
		"结果", errText(err),
	)

	return err
}

func (p *Proxy) checkSocket(ctx context.Context, path string) error {
	if err := p.dialSocket(ctx, path); err != nil {
		return err
	}

	client := unixHTTPClient(path, upstreamDialTimeout)

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		"http://trim-music"+ProbeURI,
		nil,
	)
	if err != nil {
		return err
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}

	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
	}()

	return nil
}

// WaitUpstream 在超时时间内反复探测 upstream，用于启动时等待官方后端就绪。
func (p *Proxy) WaitUpstream(ctx context.Context, timeout time.Duration) error {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var lastErr error

	for attempt := 1; ; attempt++ {
		lastErr = p.CheckUpstream(waitCtx)
		if lastErr == nil {
			return nil
		}

		select {
		case <-waitCtx.Done():
			return lastErr
		case <-time.After(time.Second):
			if attempt == 1 || attempt%5 == 0 {
				p.logger.Warn(
					"等待上游就绪",
					"上游", p.cfg.UpstreamSocket,
					"第几次", attempt,
					"错误", lastErr,
				)
			}
		}
	}
}

func (p *Proxy) serveHealth(w http.ResponseWriter, r *http.Request) {
	resp := healthResponse{
		OK:       true,
		Upstream: "ok",
		Socket:   p.cfg.UpstreamSocket,
	}

	status := http.StatusOK

	if err := p.CheckUpstream(r.Context()); err != nil {
		resp.OK = false
		resp.Upstream = "error"
		resp.Error = err.Error()
		status = http.StatusServiceUnavailable

		p.logger.Error("健康检查失败",
			"上游", p.cfg.UpstreamSocket,
			"错误", err,
		)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(resp)
}

func (p *Proxy) dialSocket(ctx context.Context, path string) error {
	dialer := &net.Dialer{Timeout: upstreamDialTimeout}

	conn, err := dialer.DialContext(ctx, "unix", path)
	if err != nil {
		return err
	}

	return conn.Close()
}

func errText(err error) string {
	if err != nil {
		return err.Error()
	}

	return "正常"
}
