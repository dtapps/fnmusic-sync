package scrobbler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// RequestToken 向 Last.fm 申请一个临时授权 token（有效期到被使用为止）。
// 拿到 token 后配合 AuthURL 引导用户在浏览器点同意。
func (l *LastFM) RequestToken(ctx context.Context) (string, error) {
	params := url.Values{}
	params.Set("method", "auth.getToken")
	params.Set("api_key", l.APIKey)
	params.Set("api_sig", l.signature(params))
	params.Set("format", "json")

	var r struct {
		Token   string `json:"token"`
		Error   int    `json:"error"`
		Message string `json:"message"`
	}
	if err := l.postJSON(ctx, params, &r); err != nil {
		return "", err
	}
	if r.Error != 0 {
		return "", fmt.Errorf("last.fm error %d: %s", r.Error, r.Message)
	}
	if r.Token == "" {
		return "", fmt.Errorf("last.fm 未返回 token")
	}
	return r.Token, nil
}

// AuthURL 构造用户在浏览器打开的授权页面链接。
func (l *LastFM) AuthURL(token string) string {
	return "https://www.last.fm/api/auth/?api_key=" +
		url.QueryEscape(l.APIKey) + "&token=" + url.QueryEscape(token)
}

// Session 用临时 token 换取长期 session key。
// 用户尚未在浏览器点同意时返回 ("", "", nil)，调用方应重试；
// 其他错误（网络故障、签名错误等）返回非 nil error。
func (l *LastFM) Session(
	ctx context.Context,
	token string,
) (sessionKey, username string, err error) {
	params := url.Values{}
	params.Set("method", "auth.getSession")
	params.Set("api_key", l.APIKey)
	params.Set("token", token)
	params.Set("api_sig", l.signature(params))
	params.Set("format", "json")

	var r struct {
		Session struct {
			Name       string `json:"name"`
			Key        string `json:"key"`
			Subscriber int    `json:"subscriber"`
		} `json:"session"`
		Error   int    `json:"error"`
		Message string `json:"message"`
	}
	if err := l.postJSON(ctx, params, &r); err != nil {
		return "", "", err
	}
	// 14 = This token has not been authorized：用户还没点同意，等待重试。
	if r.Error == 14 {
		return "", "", nil
	}
	if r.Error != 0 {
		return "", "", fmt.Errorf("last.fm error %d: %s", r.Error, r.Message)
	}
	if r.Session.Key == "" {
		return "", "", fmt.Errorf("last.fm 未返回 session key")
	}
	return r.Session.Key, r.Session.Name, nil
}

// postJSON 以 application/x-www-form-urlencoded 提交并解码 JSON 响应。
func (l *LastFM) postJSON(
	ctx context.Context,
	params url.Values,
	out any,
) error {
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		lastFMEndpoint,
		strings.NewReader(params.Encode()),
	)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", userAgent())

	resp, err := l.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("last.fm HTTP status: %s", resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
