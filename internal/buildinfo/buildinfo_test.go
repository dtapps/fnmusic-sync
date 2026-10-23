package buildinfo

import "testing"

// withRepoSource 临时改写包级变量并在用例结束时还原，
// 避免污染同包其它用例（这些变量由 -ldflags 注入，测试里可安全模拟）。
func withRepoSource(t *testing.T, source string) {
	t.Helper()

	old := RepoSource
	t.Cleanup(func() { RepoSource = old })

	RepoSource = source
}

func TestUserAgent(t *testing.T) {
	old := Version
	t.Cleanup(func() { Version = old })

	Version = "v1.2.3"

	const want = "cnb.cool/dtapp/fnmusic-sync/v1.2.3"
	if got := UserAgent(); got != want {
		t.Errorf("UserAgent() = %q, want %q", got, want)
	}
}

func TestIsCNBAndIsGitHub(t *testing.T) {
	tests := []struct {
		source   string
		wantCNB  bool
		wantGitH bool
	}{
		{source: "cnb", wantCNB: true, wantGitH: false},
		{source: "github", wantCNB: false, wantGitH: true},
		{source: "", wantCNB: false, wantGitH: false},
		{source: "unknown", wantCNB: false, wantGitH: false},
	}

	for _, tt := range tests {
		t.Run("source="+tt.source, func(t *testing.T) {
			withRepoSource(t, tt.source)

			if got := IsCNB(); got != tt.wantCNB {
				t.Errorf("RepoSource=%q IsCNB() = %v, want %v", tt.source, got, tt.wantCNB)
			}

			if got := IsGitHub(); got != tt.wantGitH {
				t.Errorf("RepoSource=%q IsGitHub() = %v, want %v", tt.source, got, tt.wantGitH)
			}
		})
	}
}

func TestDefaults(t *testing.T) {
	t.Parallel()

	// 未注入 -ldflags 时应回退到 dev（本地直接 go run / go test 的场景）。
	if Version != "dev" {
		t.Errorf("Version 默认值 = %q, want dev（测试不应带 -ldflags 注入）", Version)
	}

	if BinaryName != "fnmusic-sync" {
		t.Errorf("BinaryName 默认值 = %q, want fnmusic-sync", BinaryName)
	}

	// Socket 路径是飞牛环境的固定约定，改动会导致 nginx 转发失败。
	if DefaultListenSocket != "/var/run/trim_music.socket" {
		t.Errorf("DefaultListenSocket = %q", DefaultListenSocket)
	}

	if DefaultUpstreamSocket != "/var/run/trim_music_upstream.socket" {
		t.Errorf("DefaultUpstreamSocket = %q", DefaultUpstreamSocket)
	}
}
