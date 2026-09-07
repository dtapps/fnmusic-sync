package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// SocketKind 描述某个 unix socket 路径当前由谁持有。
type SocketKind string

const (
	// SocketNone 表示 socket 不存在，或文件还在但已经没有进程监听（死文件）。
	SocketNone SocketKind = "none"
	// SocketTrim 表示持有者是飞牛官方 trim-music。
	SocketTrim SocketKind = "trim"
	// SocketProxy 表示持有者是本代理。
	SocketProxy SocketKind = "proxy"
	// SocketStale 表示有人在监听，但无法识别身份。
	SocketStale SocketKind = "stale"
)

const (
	// ProbeURI 用来识别官方 trim-music：未登录时会返回 INVALID TOKEN / 99999。
	ProbeURI = "/music/api/v1/search/track?keyword=fnmusic_probe"
	// HealthzPath 代理自身的健康检查端点，与参考项目保持一致。
	HealthzPath = "/_ext/healthz"

	// DefaultSocketMode 官方 socket 权限无法读取时使用的兜底权限。
	//
	// 必须是 0666：fnOS 的 nginx worker 以 www-data 运行，
	// 只有 0660 会导致 nginx connect() 被 EACCES 拒绝，表现为音乐服务 502。
	DefaultSocketMode os.FileMode = 0o666
)

const (
	probeDialTimeout = 2 * time.Second
	probeTimeout     = 3 * time.Second
)

// ProbeSocket 探测 socket 路径的持有者。
func ProbeSocket(path string) SocketKind {
	info, err := os.Stat(path)
	if err != nil || info.Mode()&os.ModeSocket == 0 {
		return SocketNone
	}

	// 先做传输层探测：文件存在但没人监听时，不能当成官方后端处理。
	conn, err := net.DialTimeout("unix", path, probeDialTimeout)
	if err != nil {
		return SocketNone
	}

	conn.Close()

	client := unixHTTPClient(path, probeTimeout)

	// 1) 是不是代理自己
	if code, body, err := probeGet(client, HealthzPath); err == nil &&
		code < http.StatusInternalServerError &&
		strings.Contains(body, "upstream") {
		return SocketProxy
	}

	// 2) 是不是官方 trim-music
	if _, body, err := probeGet(client, ProbeURI); err == nil {
		if strings.Contains(body, "INVALID TOKEN") ||
			strings.Contains(body, "99999") {
			return SocketTrim
		}
	}

	return SocketStale
}

// SocketMode 返回 socket 文件的权限与属主。
func SocketMode(path string) (os.FileMode, int, int, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, -1, -1, false
	}

	uid, gid := -1, -1

	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		uid, gid = int(st.Uid), int(st.Gid)
	}

	return info.Mode(), uid, gid, true
}

// Takeover 负责 /var/run/trim_music.socket 的接管与恢复。
//
// 接管前：
//
//	nginx ──► /var/run/trim_music.socket ──► trim-music
//
// 接管后：
//
//	nginx ──► /var/run/trim_music.socket ──► Go Proxy ──► /var/run/trim_music_upstream.socket ──► trim-music
//
// 关键点：unix socket 的监听绑定在 inode 上，mv 只是改目录项，
// trim-music 持有的监听句柄不受影响，仍然可以正常 accept。
type Takeover struct {
	listen   string
	upstream string

	// 官方 socket 原本的权限/属主，接管后原样复刻，保证 nginx 依旧能连。
	mode os.FileMode
	uid  int
	gid  int

	// changed 表示当前处于"已接管"状态，退出时需要恢复官方直连。
	changed bool

	logger *slog.Logger
}

func NewTakeover(listen, upstream string, logger *slog.Logger) *Takeover {
	return &Takeover{
		listen:   listen,
		upstream: upstream,
		uid:      -1,
		gid:      -1,
		logger:   logger,
	}
}

// Prepare 完成 socket 接管，保证 listen 路径可以被本代理绑定，
// upstream 路径上是官方 trim-music。整个过程是幂等的。
func (t *Takeover) Prepare() error {
	listenKind := ProbeSocket(t.listen)
	upstreamKind := ProbeSocket(t.upstream)

	t.logSocket("接管前", t.listen, listenKind)
	t.logSocket("接管前", t.upstream, upstreamKind)

	switch {
	case upstreamKind == SocketTrim:
		// 官方后端已经在 upstream 上（重复启动 / systemd 重启场景），
		// 只需要把 listen 路径腾出来。
		switch listenKind {
		case SocketNone:
			// 已经是接管态且 listen 是死文件，直接复用。
			t.logger.Info(
				"上游已是官方后端，直接进入接管态",
				"监听", t.listen,
				"上游", t.upstream,
			)
		case SocketTrim:
			// 官方重启后又绑回了原路径：保留 upstream，移除 listen。
			t.logger.Warn(
				"原路径与上游路径都是官方后端，保留上游并移除原路径",
				"路径", t.listen,
			)

			if err := t.remove(t.listen); err != nil {
				return err
			}
		default:
			// 上一次代理残留 / 无法识别的监听者，清理掉。
			if err := t.remove(t.listen); err != nil {
				return err
			}
		}

	case listenKind == SocketTrim:
		// 正常接管：把官方 socket 改名为 upstream。
		t.captureMode(t.listen)

		if upstreamKind != SocketNone {
			// upstream 位置上是残留文件，清掉再 mv。
			if err := t.remove(t.upstream); err != nil {
				return err
			}
		}

		if err := os.Rename(t.listen, t.upstream); err != nil {
			return fmt.Errorf("接管 socket 失败 (%s -> %s): %w",
				t.listen, t.upstream, err)
		}

		t.logger.Info(
			"已接管官方socket",
			"原路径", t.listen,
			"上游路径", t.upstream,
		)

	default:
		return fmt.Errorf(
			"未找到可接管的 trim-music socket (listen=%s upstream=%s)，"+
				"请确认飞牛音乐应用已启动",
			listenKind, upstreamKind,
		)
	}

	t.changed = true

	return nil
}

// Listen 在原路径创建代理监听，并设置与官方 socket 一致的权限。
func (t *Takeover) Listen(explicit os.FileMode) (net.Listener, error) {
	// 安全兜底：绝不覆盖仍在服务的官方后端。
	if kind := ProbeSocket(t.listen); kind == SocketTrim {
		return nil, fmt.Errorf("拒绝绑定 %s：该 socket 仍是官方 trim-music", t.listen)
	}

	if err := t.remove(t.listen); err != nil {
		return nil, err
	}

	if dir := filepath.Dir(t.listen); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}

	listener, err := net.Listen("unix", t.listen)
	if err != nil {
		return nil, fmt.Errorf("监听 %s 失败: %w", t.listen, err)
	}

	mode := t.effectiveMode(explicit)

	if err := os.Chmod(t.listen, mode); err != nil {
		listener.Close()

		return nil, fmt.Errorf("设置 %s 权限失败: %w", t.listen, err)
	}

	if err := t.applyOwner(t.listen); err != nil {
		t.logger.Warn(
			"设置 socket 属主失败（不影响 nginx 连接）",
			"路径", t.listen,
			"错误", err,
		)
	}

	t.logger.Info(
		"代理已监听",
		"监听", t.listen,
		"上游", t.upstream,
		"权限", fmt.Sprintf("%#o", mode),
	)

	return listener, nil
}

// Restore 把 upstream 还原回原路径，让飞牛音乐回到官方直连。
//
// 任何退出路径（正常退出、启动失败、信号终止）都必须调用，
// 否则 trim-music 会一直呆在 upstream 路径上，nginx 找不到 socket → 502。
func (t *Takeover) Restore() {
	if !t.changed {
		return
	}

	t.changed = false

	// listen 上已经是官方后端（说明 trim-music 重启后自己抢回了路径），
	// 此时什么都不用做，天然就是官方直连。
	if kind := ProbeSocket(t.listen); kind == SocketTrim {
		t.logger.Info(
			"官方后端已回到原路径，无需恢复",
			"路径", t.listen,
		)

		return
	}

	if err := t.remove(t.listen); err != nil {
		t.logger.Warn(
			"清理代理 socket 失败",
			"路径", t.listen,
			"错误", err,
		)
	}

	if kind := ProbeSocket(t.upstream); kind == SocketNone {
		// 恢复失败意味着飞牛音乐将一直不可用，属于严重问题。
		t.logger.Error(
			"上游 socket 不可用，无法恢复官方直连",
			"上游", t.upstream,
			"请手动执行", fmt.Sprintf("mv %s %s", t.upstream, t.listen),
		)

		return
	}

	if err := os.Rename(t.upstream, t.listen); err != nil {
		t.logger.Error(
			"恢复官方 socket 失败",
			"上游", t.upstream,
			"原路径", t.listen,
			"错误", err,
		)

		return
	}

	if mode := t.effectiveMode(0); mode != 0 {
		if err := os.Chmod(t.listen, mode); err != nil {
			t.logger.Warn(
				"恢复 socket 权限失败",
				"路径", t.listen,
				"错误", err,
			)
		}
	}

	_ = t.applyOwner(t.listen)

	t.logger.Info(
		"已恢复官方直连",
		"路径", t.listen,
	)
}

// logSocket 输出 socket 的详细状态（归属、权限、属主），便于排查接管问题。
func (t *Takeover) logSocket(stage, path string, kind SocketKind) {
	attrs := []any{
		"阶段", stage,
		"路径", path,
		"归属", string(kind),
	}

	if mode, uid, gid, ok := SocketMode(path); ok {
		attrs = append(attrs,
			"权限", fmt.Sprintf("%#o", mode&os.ModePerm),
			"属主", fmt.Sprintf("%s:%s", formatID(uid), formatID(gid)),
		)
	}

	t.logger.Info("socket状态", attrs...)
}

func formatID(value int) string {
	if value < 0 {
		return "-"
	}

	return strconv.Itoa(value)
}

// effectiveMode 计算代理 socket 应该使用的权限。
//
// 默认沿用官方 socket 的权限与属主；当官方权限不允许 other 访问时
// （例如 0660 root:root），强制补上 rw，否则 www-data 连不上。
func (t *Takeover) effectiveMode(explicit os.FileMode) os.FileMode {
	mode := explicit

	if mode == 0 {
		mode = t.mode
	}

	if mode == 0 {
		mode = DefaultSocketMode
	}

	mode &= os.ModePerm

	if mode&0o007 == 0 {
		mode |= 0o006
	}

	return mode
}

func (t *Takeover) captureMode(path string) {
	mode, uid, gid, ok := SocketMode(path)
	if !ok {
		return
	}

	t.mode, t.uid, t.gid = mode, uid, gid
}

func (t *Takeover) applyOwner(path string) error {
	if t.uid < 0 || t.gid < 0 {
		return nil
	}

	if _, uid, gid, ok := SocketMode(path); ok && uid == t.uid && gid == t.gid {
		return nil
	}

	return os.Chown(path, t.uid, t.gid)
}

func (t *Takeover) remove(path string) error {
	err := os.Remove(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("删除 %s 失败: %w", path, err)
	}

	return nil
}

func unixHTTPClient(path string, timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: probeDialTimeout}

	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DisableKeepAlives: true,
			DialContext: func(
				ctx context.Context,
				_ string,
				_ string,
			) (net.Conn, error) {
				return dialer.DialContext(ctx, "unix", path)
			},
		},
	}
}

func probeGet(client *http.Client, uri string) (int, string, error) {
	req, err := http.NewRequest(
		http.MethodGet,
		"http://trim-music"+uri,
		nil,
	)
	if err != nil {
		return 0, "", err
	}

	resp, err := client.Do(req)
	if err != nil {
		return 0, "", err
	}

	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))

	return resp.StatusCode, string(body), nil
}
