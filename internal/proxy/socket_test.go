package proxy

import (
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// shortSocketPath 返回一个足够短的 unix socket 路径。
// macOS 的 sockaddr_un.sun_path 只有 104 字节，t.TempDir() 往往超长。
func shortSocketPath(t *testing.T) string {
	t.Helper()

	dir, err := os.MkdirTemp("", "fnm")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)

		return ""
	}

	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	return filepath.Join(dir, "s.sock")
}

// startUnixServer 在 path 上启动一个 unix socket HTTP 服务。
func startUnixServer(t *testing.T, path string, handler http.Handler) {
	t.Helper()

	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("监听 unix socket 失败: %v", err)

		return
	}

	// ReadHeaderTimeout 仅供 gosec G112 检查通过，测试服务不会承受真实流量。
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}

	go func() { _ = srv.Serve(ln) }()

	t.Cleanup(func() { _ = srv.Close() })
}

func TestProbeSocket_NotExist(t *testing.T) {
	t.Parallel()

	if got := ProbeSocket(filepath.Join(os.TempDir(), "fnm-not-exist-socket")); got != SocketNone {
		t.Errorf("ProbeSocket(不存在) = %q, want %q", got, SocketNone)
	}
}

func TestProbeSocket_RegularFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "plain.txt")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	// 普通文件不是 socket，必须返回 none，否则会误判为官方后端在线
	if got := ProbeSocket(path); got != SocketNone {
		t.Errorf("ProbeSocket(普通文件) = %q, want %q", got, SocketNone)
	}
}

func TestProbeSocket_DeadSocket(t *testing.T) {
	t.Parallel()

	path := shortSocketPath(t)

	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("监听失败: %v", err)

		return
	}

	_ = ln.Close()
	// 关闭监听后文件仍在（死文件）：必须识别为 none，
	// 否则接管逻辑会以为官方后端还活着而不去启动自己的监听。
	if _, err := os.Stat(path); err != nil {
		t.Skipf("平台未保留 socket 文件，跳过: %v", err)
	}

	if got := ProbeSocket(path); got != SocketNone {
		t.Errorf("ProbeSocket(死文件) = %q, want %q", got, SocketNone)
	}
}

func TestProbeSocket_DetectsProxy(t *testing.T) {
	t.Parallel()

	path := shortSocketPath(t)

	startUnixServer(t, path, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != HealthzPath {
			http.NotFound(w, r)

			return
		}

		_, _ = w.Write([]byte(`{"status":"ok","upstream":"/var/run/trim_music_upstream.socket"}`))
	}))

	if got := ProbeSocket(path); got != SocketProxy {
		t.Errorf("ProbeSocket(代理自身) = %q, want %q", got, SocketProxy)
	}
}

func TestProbeSocket_DetectsTrim(t *testing.T) {
	t.Parallel()

	path := shortSocketPath(t)

	startUnixServer(t, path, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 官方 trim-music 未登录时对 probe 请求返回 INVALID TOKEN
		if r.URL.Path == ProbeURI || r.URL.Path == "/music/api/v1/search/track" {
			_, _ = w.Write([]byte(`{"code":99999,"msg":"INVALID TOKEN"}`))

			return
		}

		http.NotFound(w, r)
	}))

	if got := ProbeSocket(path); got != SocketTrim {
		t.Errorf("ProbeSocket(官方 trim) = %q, want %q", got, SocketTrim)
	}
}

func TestProbeSocket_UnknownHolderIsStale(t *testing.T) {
	t.Parallel()

	path := shortSocketPath(t)

	// 有人在监听，但既不是代理也不是官方：识别为 stale，不能当成官方后端
	startUnixServer(t, path, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("something else"))
	}))

	if got := ProbeSocket(path); got != SocketStale {
		t.Errorf("ProbeSocket(未知持有者) = %q, want %q", got, SocketStale)
	}
}

func TestSocketMode(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "f.txt")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Chmod 显式设置，避免 umask 干扰断言
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}

	mode, uid, gid, ok := SocketMode(path)
	if !ok {
		t.Fatal("SocketMode(存在) ok = false")
	}

	if mode.Perm() != 0o644 {
		t.Errorf("权限 = %o, want 644", mode.Perm())
	}

	if uid < 0 || gid < 0 {
		t.Errorf("uid/gid 解析失败: uid=%d gid=%d", uid, gid)
	}
}

func TestSocketMode_NotExist(t *testing.T) {
	t.Parallel()

	mode, uid, gid, ok := SocketMode(filepath.Join(t.TempDir(), "nope.sock"))
	if ok {
		t.Error("不存在的路径 ok 应为 false")
	}

	if mode != 0 || uid != -1 || gid != -1 {
		t.Errorf("不存在的路径应返回 (0, -1, -1), 实际 (%v, %d, %d)", mode, uid, gid)
	}
}

func TestDefaultSocketMode(t *testing.T) {
	t.Parallel()

	// 必须是 0666：fnOS 的 nginx worker 以 www-data 运行，
	// 0660 会导致 connect() 被 EACCES 拒绝（音乐服务 502）。
	if DefaultSocketMode != 0o666 {
		t.Errorf("DefaultSocketMode = %o, want 666", DefaultSocketMode)
	}
}
