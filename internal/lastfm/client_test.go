package lastfm

import (
	"net/url"
	"strings"
	"testing"
)

func TestSignatureProperties(t *testing.T) {
	t.Parallel()

	c := &Client{APIKey: "key", APISecret: "secret"}

	base := url.Values{
		"method":  {"auth.getSession"},
		"api_key": {"key"},
		"token":   {"tok"},
	}

	sig := c.signature(base)
	if len(sig) != 32 {
		t.Fatalf("签名长度 = %d, want 32（MD5 十六进制）: %q", len(sig), sig)
	}

	if strings.ToLower(sig) != sig {
		t.Errorf("签名应为小写十六进制: %q", sig)
	}

	// 参数顺序不影响签名（内部会排序）
	shuffled := url.Values{
		"token":   {"tok"},
		"method":  {"auth.getSession"},
		"api_key": {"key"},
	}
	if got := c.signature(shuffled); got != sig {
		t.Errorf("参数顺序影响签名: %q vs %q", got, sig)
	}

	// format / callback 不参与签名（Last.fm 规范）
	withFormat := url.Values{
		"method":   {"auth.getSession"},
		"api_key":  {"key"},
		"token":    {"tok"},
		"format":   {"json"},
		"callback": {"x"},
	}
	if got := c.signature(withFormat); got != sig {
		t.Errorf("format/callback 参与了签名: %q vs %q", got, sig)
	}

	// 参数值变化必须导致签名变化
	changed := url.Values{
		"method":  {"auth.getSession"},
		"api_key": {"key"},
		"token":   {"other"},
	}
	if got := c.signature(changed); got == sig {
		t.Error("token 改变后签名未变")
	}

	// 密钥变化必须导致签名变化
	other := &Client{APIKey: "key", APISecret: "secret2"}
	if got := other.signature(base); got == sig {
		t.Error("api_secret 改变后签名未变")
	}
}

func TestSignatureDeterministic(t *testing.T) {
	t.Parallel()

	c := &Client{APIKey: "k", APISecret: "s"}
	params := url.Values{"method": {"track.scrobble"}, "artist": {"A"}, "track": {"T"}}

	// 固定输入必须产出固定签名，否则服务端会随机拒绝
	want := c.signature(params)
	for i := range 3 {
		if got := c.signature(url.Values{"track": {"T"}, "artist": {"A"}, "method": {"track.scrobble"}}); got != want {
			t.Fatalf("第 %d 次计算结果不同: %q vs %q", i, got, want)
		}
	}
}

func TestAuthURL(t *testing.T) {
	t.Parallel()

	c := &Client{APIKey: "my key&value"}

	got := c.AuthURL("tok en/+")
	if !strings.HasPrefix(got, "https://www.last.fm/api/auth/?api_key=") {
		t.Errorf("AuthURL 前缀错误: %q", got)
	}

	// 必须做 URL 转义，否则 & 会截断参数
	if strings.Contains(got, "my key&value") {
		t.Errorf("api_key 未转义: %q", got)
	}

	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("AuthURL 不是合法 URL: %v", err)
	}

	if u.Query().Get("api_key") != "my key&value" {
		t.Errorf("api_key 解析回来 = %q", u.Query().Get("api_key"))
	}

	if u.Query().Get("token") != "tok en/+" {
		t.Errorf("token 解析回来 = %q", u.Query().Get("token"))
	}
}

func TestIsValidPeriod(t *testing.T) {
	t.Parallel()

	valid := []string{"7day", "1month", "3month", "6month", "12month", "overall"}
	for _, p := range valid {
		if !IsValidPeriod(p) {
			t.Errorf("IsValidPeriod(%q) = false, want true", p)
		}
	}

	for _, p := range []string{"", "7d", "1year", "2month", "OVERALL", " overall"} {
		if IsValidPeriod(p) {
			t.Errorf("IsValidPeriod(%q) = true, want false", p)
		}
	}
}

func TestSnippet(t *testing.T) {
	t.Parallel()

	short := "  hello  "
	if got := snippet([]byte(short)); got != "hello" {
		t.Errorf("snippet(%q) = %q, want hello", short, got)
	}

	long := strings.Repeat("x", 500)

	got := snippet([]byte(long))
	if len(got) != 200 {
		t.Errorf("snippet 截断长度 = %d, want 200", len(got))
	}
}

func TestClientName(t *testing.T) {
	t.Parallel()

	if got := (&Client{}).Name(); got == "" {
		t.Error("Name() 不应为空")
	}
}
