package playback

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"cnb.cool/dtapp/fnmusic-sync/internal/safego"
	"cnb.cool/dtapp/fnmusic-sync/internal/strutil"
)

// User 表示一个飞牛音乐用户。
// Key 是客户端 Cookie 里的 music-token / fnos-token（per-user 唯一且稳定），
// Name 是从 /music/api/v1/user/me 解析出的可读用户名（用于日志与配置映射）。
type User struct {
	Key  string
	Name string
}

// UserCache 维护 token → 用户名 的映射。
// 来源：① 被动——客户端自己调用 /user/me 时拦截其响应；
//
//	② 主动——首次见到未知 token 时，代理代为请求 /user/me（复用原认证头）。
//
// probeTimeout 主动探测 /user/me 的超时时间。
const (
	probeTimeout = 5 * time.Second
	// failedTTL 探测失败的 token 在 TTL 内不再重复探测，避免无效 token 刷屏。
	failedTTL = 5 * time.Minute
)

type UserCache struct {
	mu             sync.RWMutex
	byKey          map[string]*User
	logger         *slog.Logger
	rt             http.RoundTripper
	mePath         string
	store          *UserStore
	providerStatus func(username string) (lastfm, listenBrainz bool)

	// probing 记录正在探测中的 token，避免同一 token 被并发请求重复探测。
	probing map[string]bool
	// failed 记录近期探测失败的 token（如 401 失效 token），TTL 内不再重复探测，
	// 避免无效 token 持续发请求时反复打上游、刷 WARN。
	failed map[string]time.Time

	// onUserIdentified 当首次识别到新用户时回调，参数为 (token, username)。
	// 用于触发如歌单同步等需要用户信息的后台任务。
	onUserIdentified func(token, username string)
}

func NewUserCache(
	logger *slog.Logger,
	rt http.RoundTripper,
	store *UserStore,
	providerStatus func(username string) (lastfm, listenBrainz bool),
) *UserCache {
	return &UserCache{
		byKey:          make(map[string]*User),
		logger:         logger,
		rt:             rt,
		mePath:         "/music/api/v1/user/me",
		store:          store,
		providerStatus: providerStatus,
		probing:        make(map[string]bool),
		failed:         make(map[string]time.Time),
	}
}

// SetOnUserIdentifiedCallback 设置用户首次识别回调。
// 当某个 token 第一次被解析出用户名时调用，参数为 (token, username)。
// 注意：必须在 Start 之前设置，且只能设置一次。
func (c *UserCache) SetOnUserIdentifiedCallback(fn func(token, username string)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onUserIdentified = fn
}

// usernameFields 常见用户名所在字段（含嵌套 data.xxx），宽松匹配。
var usernameFields = []string{
	"username", "user_name", "userName", "name",
	"account", "nickname", "login", "displayName", "display_name",
}

func parseUsername(body []byte) string {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil {
		return ""
	}

	try := func(m map[string]json.RawMessage) string {
		for _, key := range usernameFields {
			raw, ok := m[key]
			if !ok {
				continue
			}

			var s string
			if json.Unmarshal(raw, &s) == nil && s != "" {
				return s
			}
		}

		return ""
	}

	if name := try(top); name != "" {
		return name
	}

	// 常见结构：{"code":0,"data":{"username":"..."}}
	if raw, ok := top["data"]; ok {
		var data map[string]json.RawMessage
		if json.Unmarshal(raw, &data) == nil {
			if name := try(data); name != "" {
				return name
			}
		}
	}

	return ""
}

// ParseLoginToken 从 /user/auth-login、/user/password-login 等登录响应里解析新签发的 userToken 与用户名。
// 响应形如 {"code":0,"data":{"userToken":"...","user":{"name":"admin",...}}}。
func ParseLoginToken(body []byte) (token, name string) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil {
		return "", ""
	}

	rawData, ok := top["data"]
	if !ok {
		return "", ""
	}

	var data struct {
		UserToken string `json:"userToken"`
		User      struct {
			Name string `json:"name"`
		} `json:"user"`
	}
	if err := json.Unmarshal(rawData, &data); err != nil {
		return "", ""
	}

	return data.UserToken, data.User.Name
}

// Update 用 /user/me 响应体更新缓存，返回解析到的用户名（空表示未识别）。
func (c *UserCache) Update(key string, body []byte) string {
	name := parseUsername(body)
	if name == "" {
		return ""
	}

	c.Register(key, name)

	return name
}

// Register 直接登记一个已知 token → 用户名 的映射。
//
// 典型场景：客户端 token 过期后重新调用 /user/auth-login（或 /user/password-login）拿到新的 userToken，
// 代理从登录响应里同时解析出 userToken 与用户名，主动登记。
// 这样客户端一旦切换到新 token，scrobble / 歌单同步立即生效，
// 既不依赖异步探测（探测复用触发请求的认证头，而音乐服务常要求随请求变化的签名，
// 直接复用会 401，导致新 token 始终探测失败、用户名解析不出来），
// 也不会因旧 token 失效而丢掉这一期间的播放记录。
// 对比只处理"过期 token"的 InvalidateToken：这里补上"检查新获取 token"这一环。
func (c *UserCache) Register(key, name string) {
	if key == "" || name == "" {
		return
	}

	c.mu.Lock()
	u, ok := c.byKey[key]
	isNew := !ok
	if !ok {
		u = &User{Key: key}
		c.byKey[key] = u
	}
	nameChanged := u.Name != name
	u.Name = name
	// 新 token 已确认有效，清掉可能的失败标记，避免被 failedTTL 挡住。
	delete(c.failed, key)
	onIdentified := c.onUserIdentified
	c.mu.Unlock()

	// 已识别过且用户名未变：仅静默更新，不重复打日志 / 触发回调。
	if !isNew && !nameChanged {
		return
	}

	c.logger.Info(
		"识别到用户",
		"用户标识", strutil.FirstN8(key),
		"用户名", name,
	)

	// 把用户基本信息（是否启用各平台）写入状态文件，供下次启动读取。
	if c.store != nil {
		lastfm, lb := false, false
		if c.providerStatus != nil {
			lastfm, lb = c.providerStatus(name)
		}
		c.store.Ensure(name, lastfm, lb)
	}

	// 首次识别到新用户（或用户名发生变化），触发回调（如歌单同步）。
	// 在锁外调用，避免回调内反向操作 UserCache 导致死锁。
	if onIdentified != nil {
		safego.Go(c.logger, "playback.onIdentified", func() { onIdentified(key, name) })
	}
}

// Resolve 返回已缓存的用户名（可能为空）。
// 若未缓存则异步发起 /user/me 探测，不阻塞当前请求。
//
// 注意：探测刻意不复用请求的 context——它在后台运行，
// 若跟随请求生命周期，请求一结束/客户端断开就会被取消（context canceled）。
func (c *UserCache) Resolve(key, source string, auth http.Header) string {
	if key == "" {
		return ""
	}

	// 热路径：已识别过，直接返回。
	c.mu.RLock()
	if u, ok := c.byKey[key]; ok {
		c.mu.RUnlock()

		return u.Name
	}
	// 失败缓存：近期探测失败的 token 不再反复打上游、刷 WARN。
	if t, ok := c.failed[key]; ok && time.Since(t) < failedTTL {
		c.mu.RUnlock()

		return ""
	}
	c.mu.RUnlock()

	// 仅对 music-token 主动探测：fnos-token 是系统级会话 token，
	// 音乐服务的 /user/me 不认它（必返回 401 INVALID TOKEN），探测纯属噪音
	// —— 客户端启动时发的 /initialization/state、/sys/config 就是这类请求。
	// 真实播放/识别请求都带 music-token，不受此跳过影响；
	// fnos-token 的归属靠它自身带 music-token 的后续请求补齐，无需在此硬探。
	if source != "music-token" {
		return ""
	}

	c.mu.Lock()
	// 同一个 token 只探测一次，避免并发请求重复打上游、重复打日志。
	if c.probing[key] {
		c.mu.Unlock()

		return ""
	}
	c.probing[key] = true
	c.mu.Unlock()

	safego.Go(c.logger, "playback.UserCache.probe", func() {
		defer c.finishProbe(key)

		c.probe(key, auth)
	})

	return ""
}

// finishProbe 清除探测中标记，允许后续（如换 token 后）重新探测。
func (c *UserCache) finishProbe(key string) {
	c.mu.Lock()
	delete(c.probing, key)
	c.mu.Unlock()
}

// ActiveUsers 返回当前已识别的用户列表（token → 用户名）。
// 注意：同一用户名可能有多个 token（不同客户端登录），都保留。
// 用于后台任务（如歌单同步）获取需要同步的用户。
func (c *UserCache) ActiveUsers() map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	users := make(map[string]string, len(c.byKey))
	for key, u := range c.byKey {
		if u.Name != "" {
			users[key] = u.Name
		}
	}

	return users
}

// InvalidateToken 移除已失效的 token（如上游返回 401 INVALID TOKEN）。
//
// 失效 token 会一直停留在活跃用户列表里，导致定时任务反复用它请求上游、
// 每轮都刷一遍 401。这里直接剔除；用户重新登录产生的新 token 会被重新识别。
// 返回值表示这个 token 之前还在活跃列表中（已被剔除过则为 false）。
func (c *UserCache) InvalidateToken(key string) bool {
	c.mu.Lock()
	_, existed := c.byKey[key]
	delete(c.byKey, key)
	c.failed[key] = time.Now()
	c.mu.Unlock()

	return existed
}

// UserByName 根据用户名查找对应的 token。
// 返回 token 和是否找到。
func (c *UserCache) UserByName(name string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	for key, u := range c.byKey {
		if u.Name == name {
			return key, true
		}
	}

	return "", false
}

// MEPath 返回探测所用的 /user/me 路径（被动识别时用于路径比对）。
func (c *UserCache) MEPath() string { return c.mePath }

// cookieNames 从 Cookie 头提取所有 cookie 名（不打值，避免泄露凭证）。
// 用于诊断"触发探测的请求到底带了哪些 token"。
func cookieNames(h http.Header) []string {
	raw := h.Get("Cookie")
	if raw == "" {
		return nil
	}

	var names []string
	for p := range strings.SplitSeq(raw, ";") {
		p = strings.TrimSpace(p)
		if i := strings.IndexByte(p, '='); i > 0 {
			names = append(names, p[:i])
		} else if p != "" {
			names = append(names, p)
		}
	}

	return names
}

// authHeadersPresent 返回触发请求中"非空"的认证头名（用于诊断）。
func authHeadersPresent(h http.Header) string {
	var present []string
	for _, name := range []string{"Cookie", "Authorization", "X-Auth-Token", "Authx"} {
		if h.Get(name) != "" {
			present = append(present, name)
		}
	}
	if len(present) == 0 {
		return "无"
	}

	return strings.Join(present, ",")
}

// readAndDecompress 读取响应体，必要时先解 gzip（上游按客户端 Accept-Encoding 压缩）。
// 代理 transport 透传客户端 Accept-Encoding，JSON 接口可能被压缩，
// 不解压直接 JSON 解析会静默失败、用户名永远解析不出。
func readAndDecompress(resp *http.Response, max int64) ([]byte, error) {
	r := io.LimitReader(resp.Body, max)
	if strings.TrimSpace(strings.ToLower(resp.Header.Get("Content-Encoding"))) == "gzip" {
		gz, err := gzip.NewReader(r)
		if err != nil {
			return nil, err
		}
		defer gz.Close()
		r = gz
	}
	return io.ReadAll(r)
}

// probe 代客户端请求 /user/me，复用原始认证头。
// 超时内聚在这里（不依赖调用方），避免后台探测无限期挂住。
func (c *UserCache) probe(key string, auth http.Header) {
	// 刻意用 Background：探测与单个请求生命周期无关，
	// 跟随请求 ctx 会在请求结束/客户端断开时被取消。
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()

	start := time.Now()

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		"http://trim-music"+c.mePath,
		nil,
	)
	if err != nil {
		return
	}

	for _, h := range []string{
		"Cookie", "Authorization", "X-Auth-Token", "Authx",
	} {
		if v := auth.Get(h); v != "" {
			req.Header.Set(h, v)
		}
	}

	client := &http.Client{Transport: c.rt}

	resp, err := client.Do(req)
	if err != nil {
		c.logger.Warn(
			"探测用户信息失败",
			"用户标识", strutil.FirstN8(key),
			"耗时", time.Since(start).String(),
			"错误", err,
		)
		c.markFailed(key)

		return
	}
	defer resp.Body.Close()

	body, _ := readAndDecompress(resp, 1<<16)

	if resp.StatusCode != http.StatusOK {
		c.logger.Warn(
			"探测用户信息失败",
			"用户标识", strutil.FirstN8(key),
			"探测路径", c.mePath,
			"触发请求携带认证头", authHeadersPresent(auth),
			"触发请求Cookie中的token", cookieNames(auth),
			"状态码", resp.StatusCode,
			"响应体", strutil.FirstN(string(body), 512),
		)
		c.markFailed(key)

		return
	}

	// 先解析用户名，成功日志才能带上它（方便对照"这个标识解析成了谁"）。
	name := c.Update(key, body)

	c.logger.Debug(
		"探测用户信息成功",
		"用户标识", strutil.FirstN8(key),
		"状态码", resp.StatusCode,
		"字节数", len(body),
		"耗时", time.Since(start).String(),
	)

	if name == "" {
		c.logger.Warn(
			"未能从 /user/me 解析出用户名",
			"用户标识", strutil.FirstN8(key),
			"状态码", resp.StatusCode,
			"响应体", strutil.FirstN(string(body), 512),
		)
		c.markFailed(key)
	} else {
		c.clearFailed(key)
	}
}

// markFailed 记录某 token 探测失败，TTL 内不再重复探测（避免无效 token 刷屏）。
func (c *UserCache) markFailed(key string) {
	c.mu.Lock()
	c.failed[key] = time.Now()
	c.mu.Unlock()
}

// clearFailed 清除失败标记（探测成功时调用）。
func (c *UserCache) clearFailed(key string) {
	c.mu.Lock()
	delete(c.failed, key)
	c.mu.Unlock()
}
