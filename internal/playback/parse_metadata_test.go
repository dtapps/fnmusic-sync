package playback

import (
	"testing"
	"time"
)

func TestParseMetadata(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		body    string
		wantOK  bool
		check   func(t *testing.T, tr trackView)
		wantBad bool
	}{
		{
			name:   "完整元数据",
			body:   `{"code":0,"data":{"track":{"title":"不将就","artists":[{"name":"李荣浩"}],"album":{"name":"嗯"},"duration":215000,"trackNo":3}}}`,
			wantOK: true,
			check: func(t *testing.T, tr trackView) {
				t.Helper()

				if tr.title != "不将就" || tr.artist != "李荣浩" || tr.album != "嗯" {
					t.Errorf("字段解析错误: %+v", tr)
				}

				// duration 单位是毫秒
				if tr.duration != 215*time.Second {
					t.Errorf("duration = %v, want 215s", tr.duration)
				}

				if tr.trackNo != 3 {
					t.Errorf("trackNo = %d, want 3", tr.trackNo)
				}
			},
		},
		{
			name:   "多艺人用逗号拼接",
			body:   `{"data":{"track":{"title":"S","artists":[{"name":"A"},{"name":"B"},{"name":""}],"duration":1000}}}`,
			wantOK: true,
			check: func(t *testing.T, tr trackView) {
				t.Helper()

				if tr.artist != "A, B" {
					t.Errorf("artist = %q, want \"A, B\"（空名应被跳过）", tr.artist)
				}
			},
		},
		{
			name:    "缺艺人不合法",
			body:    `{"data":{"track":{"title":"S","artists":[],"duration":1000}}}`,
			wantOK:  false,
			wantBad: true,
		},
		{
			name:    "缺标题不合法",
			body:    `{"data":{"track":{"artists":[{"name":"A"}],"duration":1000}}}`,
			wantOK:  false,
			wantBad: true,
		},
		{name: "JSON 非法", body: `not json`, wantOK: false, wantBad: true},
		{name: "空对象", body: `{}`, wantOK: false, wantBad: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tr, ok := parseMetadata("guid-1", []byte(tt.body))
			if ok != tt.wantOK {
				t.Fatalf("parseMetadata() ok = %v, want %v", ok, tt.wantOK)
			}

			if !ok {
				return
			}

			if tr.GUID != "guid-1" {
				t.Errorf("GUID = %q, want guid-1（应取自入参）", tr.GUID)
			}

			if tt.check != nil {
				tt.check(t, trackView{
					title: tr.Title, artist: tr.Artist, album: tr.Album,
					duration: tr.Duration, trackNo: tr.TrackNumber,
				})
			}
		})
	}
}

// trackView 把 model.Track 里关心的字段抽出来，让上面的断言更短。
type trackView struct {
	title    string
	artist   string
	album    string
	duration time.Duration
	trackNo  int
}

func TestParsePlayEvent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want string
		ok   bool
	}{
		{
			name: "track_play 事件",
			body: `{"events":[{"eventType":"track_play","payload":{"trackGUID":"g1"}}]}`,
			want: "g1", ok: true,
		},
		{
			name: "跳过非 track_play",
			body: `{"events":[{"eventType":"page_view","payload":{"trackGUID":"g0"}},{"eventType":"track_play","payload":{"trackGUID":"g2"}}]}`,
			want: "g2", ok: true,
		},
		{
			name: "track_play 但 GUID 为空则继续找",
			body: `{"events":[{"eventType":"track_play","payload":{}},{"eventType":"track_play","payload":{"trackGUID":"g3"}}]}`,
			want: "g3", ok: true,
		},
		{name: "无 track_play", body: `{"events":[{"eventType":"pause","payload":{"trackGUID":"g"}}]}`, ok: false},
		{name: "空 events", body: `{"events":[]}`, ok: false},
		{name: "JSON 非法", body: `{`, ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := parsePlayEvent([]byte(tt.body))
			if ok != tt.ok {
				t.Fatalf("parsePlayEvent() ok = %v, want %v", ok, tt.ok)
			}

			if ok && got != tt.want {
				t.Errorf("parsePlayEvent() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseUsername(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "顶层 username", body: `{"username":"alice"}`, want: "alice"},
		{name: "data 内嵌 username", body: `{"code":0,"data":{"username":"bob"}}`, want: "bob"},
		{name: "name 字段", body: `{"data":{"name":"carol"}}`, want: "carol"},
		{name: "nickname 字段", body: `{"data":{"nickname":"dave"}}`, want: "dave"},
		{name: "优先顶层", body: `{"username":"top","data":{"username":"nested"}}`, want: "top"},
		{name: "空值跳过", body: `{"username":"","data":{"username":"fallback"}}`, want: "fallback"},
		{name: "无可用字段", body: `{"code":0}`, want: ""},
		{name: "JSON 非法", body: `nope`, want: ""},
		{name: "非字符串字段", body: `{"username":123}`, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := parseUsername([]byte(tt.body)); got != tt.want {
				t.Errorf("parseUsername(%q) = %q, want %q", tt.body, got, tt.want)
			}
		})
	}
}

func TestParseLoginToken(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		body      string
		wantToken string
		wantName  string
	}{
		{
			name:      "标准登录响应",
			body:      `{"code":0,"data":{"userToken":"tok-abc","user":{"name":"admin"}}}`,
			wantToken: "tok-abc",
			wantName:  "admin",
		},
		{
			name:      "只有 token",
			body:      `{"data":{"userToken":"tok-only"}}`,
			wantToken: "tok-only",
			wantName:  "",
		},
		{name: "无 data", body: `{"code":0}`, wantToken: "", wantName: ""},
		{name: "data 不是对象", body: `{"data":"x"}`, wantToken: "", wantName: ""},
		{name: "JSON 非法", body: `{`, wantToken: "", wantName: ""},
		{name: "空串", body: ``, wantToken: "", wantName: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			token, name := ParseLoginToken([]byte(tt.body))
			if token != tt.wantToken || name != tt.wantName {
				t.Errorf("ParseLoginToken(%q) = (%q, %q), want (%q, %q)",
					tt.body, token, name, tt.wantToken, tt.wantName)
			}
		})
	}
}
