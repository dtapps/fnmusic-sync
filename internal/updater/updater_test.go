package updater

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"cnb.cool/dtapp/fnmusic-sync/internal/buildinfo"
)

// withRepoSource 临时改写构建来源，用完还原。
func withRepoSource(t *testing.T, source string) {
	t.Helper()

	old := buildinfo.RepoSource
	t.Cleanup(func() { buildinfo.RepoSource = old })

	buildinfo.RepoSource = source
}

// withVersion 临时改写当前版本号（-ldflags 注入的变量）。
func withVersion(t *testing.T, version string) {
	t.Helper()

	old := buildinfo.Version
	t.Cleanup(func() { buildinfo.Version = old })

	buildinfo.Version = version
}

// roundTripFunc 让任意请求都被本地函数接管，从而在不联网的前提下
// 测试写死了线上 URL 的 FetchLatestTag。
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// jsonClient 返回一个对所有请求都回固定状态码/正文的 client。
func jsonClient(t *testing.T, status int, body string, captured *http.Request) *http.Client {
	t.Helper()

	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if captured != nil {
			*captured = *r
		}

		return &http.Response{
			StatusCode: status,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
			Request:    r,
		}, nil
	})}
}

// errClient 模拟网络层错误。
func errClient(t *testing.T) *http.Client {
	t.Helper()

	return &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("network unreachable")
	})}
}

func TestRepoBase(t *testing.T) {
	tests := []struct {
		source string
		want   string
	}{
		{source: "github", want: RepoBaseGitHub},
		{source: "cnb", want: RepoBaseCNB},
		{source: "", want: RepoBaseCNB}, // 未知来源默认 CNB
	}

	for _, tt := range tests {
		t.Run("source="+tt.source, func(t *testing.T) {
			withRepoSource(t, tt.source)

			if got := RepoBase(); got != tt.want {
				t.Errorf("RepoBase() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildDownloadURL(t *testing.T) {
	const tag, file = "v1.2.3", "fnmusic-sync_1.2.3_amd64.deb"

	withRepoSource(t, "github")

	if got, want := BuildDownloadURL(tag, file),
		RepoBaseGitHub+"/releases/download/"+tag+"/"+file; got != want {
		t.Errorf("GitHub 下载地址 = %q, want %q", got, want)
	}

	withRepoSource(t, "cnb")

	if got, want := BuildDownloadURL(tag, file),
		RepoBaseCNB+"/-/releases/download/"+tag+"/"+file; got != want {
		t.Errorf("CNB 下载地址 = %q, want %q", got, want)
	}
}

func TestRelayDownloadURL(t *testing.T) {
	const original = "https://example.com/a/b.zip"

	withRepoSource(t, "github")
	if got, want := relayDownloadURL(original), relayBaseGitHub+"/"+original; got != want {
		t.Errorf("GitHub 中转地址 = %q, want %q", got, want)
	}

	withRepoSource(t, "cnb")
	if got, want := relayDownloadURL(original), relayBaseCNB+"/"+original; got != want {
		t.Errorf("CNB 中转地址 = %q, want %q", got, want)
	}
}

func TestNoRelayFiles(t *testing.T) {
	t.Parallel()

	// 元数据文件必须走直连（否则中转会污染下载统计）
	for _, name := range []string{"SHA256SUMS", "GIT_COMMIT", "BUILD_TIME"} {
		if !noRelayFiles[name] {
			t.Errorf("%s 应在 noRelayFiles 中", name)
		}
	}

	// 安装包必须走中转
	if noRelayFiles["fnmusic-sync_1.0.0_amd64.deb"] {
		t.Error("安装包不应在 noRelayFiles 中")
	}
}

func TestHasUpdate(t *testing.T) {
	tests := []struct {
		name    string
		current string
		latest  string
		want    bool
	}{
		{name: "dev 强制更新", current: "dev", latest: "v1.0.0", want: true},
		{name: "版本相同不更新", current: "v1.0.0", latest: "v1.0.0", want: false},
		{name: "版本不同要更新", current: "v1.0.0", latest: "v1.1.0", want: true},
		{name: "降级也算不同", current: "v1.1.0", latest: "v1.0.0", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withVersion(t, tt.current)

			if got := HasUpdate(tt.latest); got != tt.want {
				t.Errorf("HasUpdate(%q) version=%q = %v, want %v", tt.latest, tt.current, got, tt.want)
			}
		})
	}
}

func TestFetchLatestTagGitHub_OK(t *testing.T) {
	withRepoSource(t, "github")

	var gotReq http.Request
	client := jsonClient(t, http.StatusOK, `{"tag_name":"v9.9.9"}`, &gotReq)

	tag, err := FetchLatestTag(client)
	if err != nil {
		t.Fatalf("FetchLatestTag 返回错误: %v", err)
	}

	if tag != "v9.9.9" {
		t.Errorf("tag = %q, want v9.9.9", tag)
	}

	if !strings.Contains(gotReq.URL.String(), "api.github.com") {
		t.Errorf("请求地址异常: %s", gotReq.URL)
	}

	if gotReq.Header.Get("Accept") != "application/vnd.github+json" {
		t.Errorf("Accept 头 = %q", gotReq.Header.Get("Accept"))
	}
}

func TestFetchLatestTagGitHub_SendsToken(t *testing.T) {
	withRepoSource(t, "github")

	old := buildinfo.GithubToken
	t.Cleanup(func() { buildinfo.GithubToken = old })
	buildinfo.GithubToken = "gh-token"

	var gotReq http.Request
	client := jsonClient(t, http.StatusOK, `{"tag_name":"v1.0.0"}`, &gotReq)

	if _, err := FetchLatestTag(client); err != nil {
		t.Fatal(err)
	}

	if got := gotReq.Header.Get("Authorization"); got != "Bearer gh-token" {
		t.Errorf("Authorization = %q, want Bearer gh-token", got)
	}
}

func TestFetchLatestTagGitHub_Errors(t *testing.T) {
	withRepoSource(t, "github")

	t.Run("非 200", func(t *testing.T) {
		_, err := FetchLatestTag(jsonClient(t, http.StatusForbidden, `{"message":"rate limit"}`, nil))
		if err == nil || !strings.Contains(err.Error(), "403") {
			t.Errorf("err = %v, want 含 403", err)
		}
	})

	t.Run("响应体非法", func(t *testing.T) {
		_, err := FetchLatestTag(jsonClient(t, http.StatusOK, `not json`, nil))
		if err == nil {
			t.Error("非法 JSON 应报错")
		}
	})

	t.Run("tag 为空", func(t *testing.T) {
		_, err := FetchLatestTag(jsonClient(t, http.StatusOK, `{"tag_name":""}`, nil))
		if err == nil {
			t.Error("空 tag 应报错")
		}
	})

	t.Run("网络错误", func(t *testing.T) {
		_, err := FetchLatestTag(errClient(t))
		if err == nil || !strings.Contains(err.Error(), "无法连接到 GitHub") {
			t.Errorf("err = %v, want 网络错误提示", err)
		}
	})
}

func TestFetchLatestTagCNB_NoToken(t *testing.T) {
	withRepoSource(t, "cnb")

	old := buildinfo.CnbToken
	t.Cleanup(func() { buildinfo.CnbToken = old })
	buildinfo.CnbToken = ""

	_, err := FetchLatestTag(jsonClient(t, http.StatusOK, `[]`, nil))
	if err == nil || !strings.Contains(err.Error(), "访问令牌未配置") {
		t.Errorf("err = %v, want 令牌未配置提示", err)
	}
}

func TestFetchLatestTagCNB_PickLatest(t *testing.T) {
	withRepoSource(t, "cnb")

	old := buildinfo.CnbToken
	t.Cleanup(func() { buildinfo.CnbToken = old })
	buildinfo.CnbToken = "cnb-token"

	const body = `[
		{"tag_name":"v3.0.0","is_latest":false,"draft":false},
		{"tag_name":"v2.0.0","is_latest":true,"draft":false},
		{"tag_name":"v1.0.0","is_latest":false,"draft":false}
	]`

	var gotReq http.Request
	tag, err := FetchLatestTag(jsonClient(t, http.StatusOK, body, &gotReq))
	if err != nil {
		t.Fatalf("FetchLatestTag: %v", err)
	}

	if tag != "v2.0.0" {
		t.Errorf("tag = %q, want v2.0.0（应优先 is_latest）", tag)
	}

	if got := gotReq.Header.Get("Authorization"); got != "Bearer cnb-token" {
		t.Errorf("Authorization = %q", got)
	}
}

func TestFetchLatestTagCNB_FallbackToFirst(t *testing.T) {
	withRepoSource(t, "cnb")

	old := buildinfo.CnbToken
	t.Cleanup(func() { buildinfo.CnbToken = old })
	buildinfo.CnbToken = "t"

	const body = `[
		{"tag_name":"v3.0.0","is_latest":true,"draft":true},
		{"tag_name":"v2.5.0","is_latest":false,"draft":false}
	]`

	// is_latest 但为 draft，应跳过并取第一个
	tag, err := FetchLatestTag(jsonClient(t, http.StatusOK, body, nil))
	if err != nil {
		t.Fatal(err)
	}

	if tag != "v3.0.0" {
		t.Errorf("tag = %q, want v3.0.0（无可用 is_latest 时取首项）", tag)
	}
}

func TestFetchLatestTagCNB_Errors(t *testing.T) {
	withRepoSource(t, "cnb")

	old := buildinfo.CnbToken
	t.Cleanup(func() { buildinfo.CnbToken = old })
	buildinfo.CnbToken = "t"

	t.Run("401", func(t *testing.T) {
		_, err := FetchLatestTag(jsonClient(t, http.StatusUnauthorized, `{}`, nil))
		if err == nil || !strings.Contains(err.Error(), "令牌无效或已过期") {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("空发布列表", func(t *testing.T) {
		_, err := FetchLatestTag(jsonClient(t, http.StatusOK, `[]`, nil))
		if err == nil || !strings.Contains(err.Error(), "暂无任何发布版本") {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("非 200", func(t *testing.T) {
		_, err := FetchLatestTag(jsonClient(t, http.StatusInternalServerError, `boom`, nil))
		if err == nil || !strings.Contains(err.Error(), "500") {
			t.Errorf("err = %v", err)
		}
	})
}

func TestDownloadFile_MetadataBypassesRelay(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 元数据文件必须直连，绝不能经过中转域名
		if r.Host != strings.TrimPrefix(srv.URL, "http://") {
			t.Errorf("元数据文件走了中转: %s", r.URL)
		}

		_, _ = w.Write([]byte("abc123  fnmusic-sync.deb\n"))
	}))
	defer srv.Close()

	const name = "SHA256SUMS_test_metadata"
	path, err := DownloadFile(srv.Client(), srv.URL+"/SHA256SUMS", name)
	if err != nil {
		t.Fatalf("DownloadFile: %v", err)
	}

	t.Cleanup(func() { _ = os.Remove(path) })

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(data), "abc123") {
		t.Errorf("内容异常: %q", data)
	}
}

func TestDownloadFile_RelaySuccess(t *testing.T) {
	// 中转地址形如 <relayBase>/<原始URL>，用自定义 Transport 把它重定向到本地 server
	const payload = "relay-ok"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(payload))
	}))
	defer srv.Close()

	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		// 断言确实先走了中转
		if !strings.HasPrefix(r.URL.String(), relayBaseCNB) && !strings.HasPrefix(r.URL.String(), relayBaseGitHub) {
			t.Errorf("首次请求未走中转: %s", r.URL)
		}

		return srv.Client().Get(srv.URL)
	})}

	const name = "fnmusic-sync_test_relay_pkg"
	path, err := DownloadFile(client, "https://origin.example/pkg", name)
	if err != nil {
		t.Fatalf("DownloadFile: %v", err)
	}

	t.Cleanup(func() { _ = os.Remove(path) })

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if string(data) != payload {
		t.Errorf("内容 = %q, want %q", data, payload)
	}
}

func TestDownloadFile_RelayFailsFallsBack(t *testing.T) {
	const payload = "fallback-ok"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(payload))
	}))
	defer srv.Close()

	var attempts []string

	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		u := r.URL.String()
		attempts = append(attempts, u)

		// 中转地址返回 502，触发回退
		if strings.HasPrefix(u, relayBaseCNB) || strings.HasPrefix(u, relayBaseGitHub) {
			return &http.Response{
				StatusCode: http.StatusBadGateway,
				Body:       io.NopCloser(strings.NewReader("relay down")),
				Header:     make(http.Header),
				Request:    r,
			}, nil
		}

		return srv.Client().Get(srv.URL)
	})}

	const name = "fnmusic-sync_test_fallback_pkg"
	path, err := DownloadFile(client, "https://origin.example/pkg", name)
	if err != nil {
		t.Fatalf("DownloadFile 应回退成功: %v", err)
	}

	t.Cleanup(func() { _ = os.Remove(path) })

	if len(attempts) != 2 {
		t.Errorf("请求次数 = %d, want 2（中转 + 回退）: %v", len(attempts), attempts)
	}

	data, _ := os.ReadFile(path)
	if string(data) != payload {
		t.Errorf("内容 = %q, want %q", data, payload)
	}
}

func TestDownloadFile_AllFail(t *testing.T) {
	const name = "fnmusic-sync_test_all_fail_pkg"

	_, err := DownloadFile(errClient(t), "https://origin.example/pkg", name)
	if err == nil {
		t.Fatal("两次都失败时应返回错误")
	}

	if !strings.Contains(err.Error(), "下载失败") {
		t.Errorf("err = %v, want 含「下载失败」", err)
	}
}

func TestDownloadFile_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.NotFound(w, nil)
	}))
	defer srv.Close()

	const name = "fnmusic-sync_test_404_pkg"
	if _, err := DownloadFile(srv.Client(), srv.URL+"/x", name); err == nil {
		t.Error("404 应返回错误")
	}
}
