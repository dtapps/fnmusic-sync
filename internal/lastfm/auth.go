package lastfm

import (
	"context"
	"fmt"
	"net/url"
)

// RequestToken 向 Last.fm 申请一个临时授权 token（有效期到被使用为止）。
// 拿到 token 后配合 AuthURL 引导用户在浏览器点同意。
func (c *Client) RequestToken(ctx context.Context) (string, error) {
	params := url.Values{}
	params.Set("method", "auth.getToken")
	params.Set("api_key", c.APIKey)
	params.Set("api_sig", c.signature(params))
	params.Set("format", "json")

	var r struct {
		Token   string `json:"token"`
		Error   int    `json:"error"`
		Message string `json:"message"`
	}
	if err := c.postJSON(ctx, params, &r); err != nil {
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
func (c *Client) AuthURL(token string) string {
	return "https://www.last.fm/api/auth/?api_key=" +
		url.QueryEscape(c.APIKey) + "&token=" + url.QueryEscape(token)
}

// Session 用临时 token 换取长期 session key。
// 用户尚未在浏览器点同意时返回 ("", "", nil)，调用方应重试；
// 其他错误（网络故障、签名错误等）返回非 nil error。
func (c *Client) Session(ctx context.Context, token string) (sessionKey, username string, err error) {
	params := url.Values{}
	params.Set("method", "auth.getSession")
	params.Set("api_key", c.APIKey)
	params.Set("token", token)
	params.Set("api_sig", c.signature(params))
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
	if err := c.postJSON(ctx, params, &r); err != nil {
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
