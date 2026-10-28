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
		{source: "gitlab", want: RepoBaseGitLab},
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

	// GitLab 走 Release 的 direct asset 链接（Web 的 /-/packages/generic/... 对匿名用户是登录页）
	withRepoSource(t, "gitlab")

	if got, want := BuildDownloadURL(tag, file),
		RepoBaseGitLab+"/-/releases/"+tag+"/downloads/"+file; got != want {
		t.Errorf("GitLab 下载地址 = %q, want %q", got, want)
	}
}

func TestRelayDownloadURL(t *testing.T) {
	const original = "https://example.com/a/b.zip"

	// 不分来源：github / cnb / gitlab 一律 dl-stats.dtapp.net
	for _, source := range []string{"github", "cnb", "gitlab", ""} {
		withRepoSource(t, source)

		if got, want := relayDownloadURL(original), relayBase+"/"+original; got != want {
			t.Errorf("RepoSource=%q 中转地址 = %q, want %q", source, got, want)
		}
	}

	if relayBase != "https://dl-stats.dtapp.net" {
		t.Errorf("relayBase = %q，应固定为 dl-stats.dtapp.net", relayBase)
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
	client := jsonClient(t, http.StatusOK, `[{"tag_name":"v9.9.9"}]`, &gotReq)

	tag, err := FetchLatestTag(client)
	if err != nil {
		t.Fatalf("FetchLatestTag 返回错误: %v", err)
	}

	if tag != "v9.9.9" {
		t.Errorf("tag = %q, want v9.9.9", tag)
	}

	if uri := gotReq.URL.String(); !strings.Contains(uri, "api.github.com") || strings.Contains(uri, "/latest") {
		t.Errorf("请求地址异常（应为 releases 列表而非 /latest）: %s", uri)
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
	client := jsonClient(t, http.StatusOK, `[{"tag_name":"v1.0.0"}]`, &gotReq)

	if _, err := FetchLatestTag(client); err != nil {
		t.Fatal(err)
	}

	if got := gotReq.Header.Get("Authorization"); got != "Bearer gh-token" {
		t.Errorf("Authorization = %q, want Bearer gh-token", got)
	}
}

// 平台字段标了 prerelease → 稳定客户端跳过
func TestFetchLatestTagGitHub_SkipsPrereleaseFlag(t *testing.T) {
	withRepoSource(t, "github")
	withVersion(t, "v1.0.0")

	const body = `[
		{"tag_name":"v2.0.0","prerelease":true,"draft":false},
		{"tag_name":"v1.9.0","prerelease":false,"draft":false}
	]`

	tag, err := FetchLatestTag(jsonClient(t, http.StatusOK, body, nil))
	if err != nil {
		t.Fatal(err)
	}

	if tag != "v1.9.0" {
		t.Errorf("tag = %q, want v1.9.0（应跳过 prerelease 标记）", tag)
	}
}

// 平台字段没标、但 tag 带后缀 → 一样跳过（后缀约定的兜底）
func TestFetchLatestTagGitHub_SkipsPrereleaseByTagSuffix(t *testing.T) {
	withRepoSource(t, "github")
	withVersion(t, "v1.0.0")

	const body = `[
		{"tag_name":"v2.0.0-rc.1","prerelease":false,"draft":false},
		{"tag_name":"v1.9.0","prerelease":false,"draft":false}
	]`

	tag, err := FetchLatestTag(jsonClient(t, http.StatusOK, body, nil))
	if err != nil {
		t.Fatal(err)
	}

	if tag != "v1.9.0" {
		t.Errorf("tag = %q, want v1.9.0（字段没标时应靠 tag 后缀识别）", tag)
	}
}

// 预发布客户端可以拿到预发布
func TestFetchLatestTagGitHub_PrereleaseClientGetsPrerelease(t *testing.T) {
	withRepoSource(t, "github")
	withVersion(t, "v2.0.0-rc.1")

	const body = `[
		{"tag_name":"v2.0.0-rc.2","prerelease":true,"draft":false},
		{"tag_name":"v1.9.0","prerelease":false,"draft":false}
	]`

	tag, err := FetchLatestTag(jsonClient(t, http.StatusOK, body, nil))
	if err != nil {
		t.Fatal(err)
	}

	if tag != "v2.0.0-rc.2" {
		t.Errorf("tag = %q, want v2.0.0-rc.2", tag)
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

	t.Run("空 tag", func(t *testing.T) {
		_, err := FetchLatestTag(jsonClient(t, http.StatusOK, `[{"tag_name":""}]`, nil))
		if err == nil || !strings.Contains(err.Error(), "暂无任何发布版本") {
			t.Errorf("err = %v, want 暂无发布版本", err)
		}
	})

	t.Run("空列表", func(t *testing.T) {
		_, err := FetchLatestTag(jsonClient(t, http.StatusOK, `[]`, nil))
		if err == nil || !strings.Contains(err.Error(), "暂无任何发布版本") {
			t.Errorf("err = %v, want 暂无发布版本", err)
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

func TestFetchLatestTagCNB_SkipsPrerelease(t *testing.T) {
	withRepoSource(t, "cnb")
	withVersion(t, "v1.0.0")

	old := buildinfo.CnbToken
	t.Cleanup(func() { buildinfo.CnbToken = old })
	buildinfo.CnbToken = "t"

	// 首个带 is_latest 但是预发布，稳定版用户应拿正式的 v0.9.0
	const body = `[
		{"tag_name":"v1.1.0-rc.1","is_latest":true,"prerelease":true,"draft":false},
		{"tag_name":"v0.9.0","is_latest":false,"prerelease":false,"draft":false}
	]`

	tag, err := FetchLatestTag(jsonClient(t, http.StatusOK, body, nil))
	if err != nil {
		t.Fatal(err)
	}

	if tag != "v0.9.0" {
		t.Errorf("tag = %q, want v0.9.0（应跳过 prerelease）", tag)
	}
}

// CNB 的 prerelease 字段没标、但 tag 带后缀时同样视为预发布
func TestFetchLatestTagCNB_SkipsPrereleaseByTagSuffix(t *testing.T) {
	withRepoSource(t, "cnb")
	withVersion(t, "v1.0.0")

	old := buildinfo.CnbToken
	t.Cleanup(func() { buildinfo.CnbToken = old })
	buildinfo.CnbToken = "t"

	const body = `[
		{"tag_name":"v1.1.0-rc.1","is_latest":true,"prerelease":false,"draft":false},
		{"tag_name":"v0.9.0","is_latest":false,"prerelease":false,"draft":false}
	]`

	tag, err := FetchLatestTag(jsonClient(t, http.StatusOK, body, nil))
	if err != nil {
		t.Fatal(err)
	}

	if tag != "v0.9.0" {
		t.Errorf("tag = %q, want v0.9.0（字段没标时应靠 tag 后缀识别）", tag)
	}
}

func TestFetchLatestTagCNB_SkipsDraft(t *testing.T) {
	withRepoSource(t, "cnb")
	withVersion(t, "v1.0.0")

	old := buildinfo.CnbToken
	t.Cleanup(func() { buildinfo.CnbToken = old })
	buildinfo.CnbToken = "t"

	const body = `[
		{"tag_name":"v3.0.0","is_latest":true,"draft":true},
		{"tag_name":"v2.5.0","is_latest":false,"draft":false}
	]`

	// is_latest 但为 draft（还没真正发布）：必须跳过，取第一个可用的正式版。
	// 旧实现在这一步会退回列表首项，把 draft 当成可升级目标。
	tag, err := FetchLatestTag(jsonClient(t, http.StatusOK, body, nil))
	if err != nil {
		t.Fatal(err)
	}

	if tag != "v2.5.0" {
		t.Errorf("tag = %q, want v2.5.0（draft 不应被当成可升级目标）", tag)
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

func TestIsPrereleaseTag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		tag  string
		want bool
	}{
		{tag: "v1.2.3", want: false},
		{tag: "1.2.3", want: false},
		{tag: "v1.2.3-rc.1", want: true},
		{tag: "1.2.3-beta", want: true},
		{tag: "v1.2.4-pre", want: true},
		{tag: "v1.2.3-", want: false}, // 空的预发布段不算
		{tag: "v1.2.3.4-rc1", want: true},
		{tag: "dev", want: false},
		{tag: "nightly-2026", want: false}, // 前缀不是 x.y.z，不当预发布
		{tag: "", want: false},
	}

	for _, tt := range tests {
		if got := isPrereleaseTag(tt.tag); got != tt.want {
			t.Errorf("isPrereleaseTag(%q) = %v, want %v", tt.tag, got, tt.want)
		}
	}
}

// 稳定版用户：列表里同时有 rc 和正式版时，必须拿正式版。
func TestFetchLatestTagGitLab_PrefersStable(t *testing.T) {
	withRepoSource(t, "gitlab")
	withVersion(t, "v0.1.30")

	const body = `[
		{"tag_name":"v0.1.32-rc.1","upcoming_release":false},
		{"tag_name":"v0.1.31","upcoming_release":false}
	]`

	tag, err := FetchLatestTag(jsonClient(t, http.StatusOK, body, nil))
	if err != nil {
		t.Fatal(err)
	}

	if tag != "v0.1.31" {
		t.Errorf("tag = %q, want v0.1.31（稳定版用户不该被推到 rc）", tag)
	}
}

// 当前自己就是预发布：允许继续拿到预发布。
func TestFetchLatestTagGitLab_PrereleaseClientGetsPrerelease(t *testing.T) {
	withRepoSource(t, "gitlab")
	withVersion(t, "v0.1.32-rc.1")

	const body = `[
		{"tag_name":"v0.1.32-rc.2","upcoming_release":false},
		{"tag_name":"v0.1.31","upcoming_release":false}
	]`

	tag, err := FetchLatestTag(jsonClient(t, http.StatusOK, body, nil))
	if err != nil {
		t.Fatal(err)
	}

	if tag != "v0.1.32-rc.2" {
		t.Errorf("tag = %q, want v0.1.32-rc.2（预发布客户端应能继续升级）", tag)
	}
}

// 只有预发布时，稳定版用户退回最新一条，而不是卡死。
func TestFetchLatestTagGitLab_OnlyPrereleaseFallsBack(t *testing.T) {
	withRepoSource(t, "gitlab")
	withVersion(t, "v0.1.30")

	const body = `[
		{"tag_name":"v0.1.32-rc.1","upcoming_release":false},
		{"tag_name":"v0.1.31-beta","upcoming_release":false}
	]`

	tag, err := FetchLatestTag(jsonClient(t, http.StatusOK, body, nil))
	if err != nil {
		t.Fatal(err)
	}

	if tag != "v0.1.32-rc.1" {
		t.Errorf("tag = %q, want v0.1.32-rc.1（仅有预发布时退回最新一条）", tag)
	}
}

func TestFetchLatestTagGitLab_OK(t *testing.T) {
	withRepoSource(t, "gitlab")

	const body = `[
		{"tag_name":"v0.1.31","name":"v0.1.31","released_at":"2026-10-28T05:18:56.486Z","upcoming_release":false},
		{"tag_name":"v0.1.30","name":"v0.1.30","released_at":"2026-10-20T01:00:00.000Z","upcoming_release":false}
	]`

	var gotReq http.Request
	tag, err := FetchLatestTag(jsonClient(t, http.StatusOK, body, &gotReq))
	if err != nil {
		t.Fatalf("FetchLatestTag 返回错误: %v", err)
	}

	if tag != "v0.1.31" {
		t.Errorf("tag = %q, want v0.1.31", tag)
	}

	// 项目标识必须 URL 编码，否则嵌套命名空间会被解析错
	if uri := gotReq.URL.RequestURI(); !strings.Contains(uri, "projects/dtapps%2Ffnmusic-sync/releases") {
		t.Errorf("请求地址异常: %s", uri)
	}

	if got := gotReq.Header.Get("Accept"); got != "application/json" {
		t.Errorf("Accept 头 = %q", got)
	}

	// 未配置 token 时不应带鉴权头（公开项目匿名可读）
	if got := gotReq.Header.Get("PRIVATE-TOKEN"); got != "" {
		t.Errorf("PRIVATE-TOKEN = %q, want 空", got)
	}
}

func TestFetchLatestTagGitLab_SendsToken(t *testing.T) {
	withRepoSource(t, "gitlab")

	old := buildinfo.GitlabToken
	t.Cleanup(func() { buildinfo.GitlabToken = old })
	buildinfo.GitlabToken = "glpat-xxx"

	var gotReq http.Request
	if _, err := FetchLatestTag(jsonClient(t, http.StatusOK,
		`[{"tag_name":"v1.0.0","upcoming_release":false}]`, &gotReq)); err != nil {
		t.Fatal(err)
	}

	if got := gotReq.Header.Get("PRIVATE-TOKEN"); got != "glpat-xxx" {
		t.Errorf("PRIVATE-TOKEN = %q, want glpat-xxx", got)
	}
}

func TestFetchLatestTagGitLab_SkipsUpcoming(t *testing.T) {
	withRepoSource(t, "gitlab")

	// 首项是「计划发布」的占位，必须跳过
	const body = `[
		{"tag_name":"v2.0.0","upcoming_release":true},
		{"tag_name":"v1.9.0","upcoming_release":false}
	]`

	tag, err := FetchLatestTag(jsonClient(t, http.StatusOK, body, nil))
	if err != nil {
		t.Fatal(err)
	}

	if tag != "v1.9.0" {
		t.Errorf("tag = %q, want v1.9.0（应跳过 upcoming_release）", tag)
	}
}

func TestFetchLatestTagGitLab_Errors(t *testing.T) {
	withRepoSource(t, "gitlab")

	old := buildinfo.GitlabToken
	t.Cleanup(func() { buildinfo.GitlabToken = old })
	buildinfo.GitlabToken = ""

	t.Run("404 私有项目", func(t *testing.T) {
		// 未认证访问私有项目时 GitLab 返回 404 而不是 401
		_, err := FetchLatestTag(jsonClient(t, http.StatusNotFound, `{"message":"404 Project Not Found"}`, nil))
		if err == nil || !strings.Contains(err.Error(), "GitlabToken") {
			t.Errorf("err = %v, want 提示需要 Token", err)
		}
	})

	t.Run("401", func(t *testing.T) {
		_, err := FetchLatestTag(jsonClient(t, http.StatusUnauthorized, `{}`, nil))
		if err == nil || !strings.Contains(err.Error(), "不可读") {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("空发布列表", func(t *testing.T) {
		_, err := FetchLatestTag(jsonClient(t, http.StatusOK, `[]`, nil))
		if err == nil || !strings.Contains(err.Error(), "暂无任何发布版本") {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("只有 upcoming", func(t *testing.T) {
		_, err := FetchLatestTag(jsonClient(t, http.StatusOK, `[{"tag_name":"v1.0.0","upcoming_release":true}]`, nil))
		if err == nil || !strings.Contains(err.Error(), "暂无任何发布版本") {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("响应体非法", func(t *testing.T) {
		_, err := FetchLatestTag(jsonClient(t, http.StatusOK, `not json`, nil))
		if err == nil || !strings.Contains(err.Error(), "解析 GitLab API 响应失败") {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("非 200", func(t *testing.T) {
		_, err := FetchLatestTag(jsonClient(t, http.StatusInternalServerError, `boom`, nil))
		if err == nil || !strings.Contains(err.Error(), "500") {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("网络错误", func(t *testing.T) {
		_, err := FetchLatestTag(errClient(t))
		if err == nil || !strings.Contains(err.Error(), "无法连接到 GitLab") {
			t.Errorf("err = %v, want 网络错误提示", err)
		}
	})
}

// GitLab 源与 CNB 共用 dl-stats.dtapp.net，下载要先走中转。
func TestDownloadFile_GitLabUsesRelay(t *testing.T) {
	withRepoSource(t, "gitlab")

	const payload = "gitlab-direct-ok"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(payload))
	}))
	defer srv.Close()

	var attempts []string

	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		attempts = append(attempts, r.URL.String())
		return srv.Client().Get(srv.URL)
	})}

	const name = "fnmusic-sync_test_gitlab_pkg"
	path, err := DownloadFile(client, "https://gitlab.com/o/r/-/releases/v1/downloads/x.deb", name)
	if err != nil {
		t.Fatalf("DownloadFile: %v", err)
	}

	t.Cleanup(func() { _ = os.Remove(path) })

	if len(attempts) != 1 {
		t.Errorf("请求次数 = %d, want 1: %v", len(attempts), attempts)
	}

	if !strings.HasPrefix(attempts[0], relayBase) {
		t.Errorf("GitLab 源应走 %s 中转，实际: %s", relayBase, attempts[0])
	}

	if data, _ := os.ReadFile(path); string(data) != payload {
		t.Errorf("内容 = %q, want %q", data, payload)
	}
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
		if !strings.HasPrefix(r.URL.String(), relayBase) {
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
		if strings.HasPrefix(u, relayBase) {
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
