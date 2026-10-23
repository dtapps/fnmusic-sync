package playback

import (
	"testing"
	"time"
)

func TestParseScrobbleThreshold_Off(t *testing.T) {
	t.Parallel()

	// off/disabled/false/none/no/0 一律视为「不判断阈值」，函数恒返回 0
	for _, spec := range []string{"off", "OFF", " disabled ", "false", "none", "no", "0"} {
		fn, err := parseScrobbleThreshold(spec)
		if err != nil {
			t.Fatalf("parseScrobbleThreshold(%q) 返回错误: %v", spec, err)

			continue
		}

		if got := fn(10 * time.Minute); got != 0 {
			t.Errorf("parseScrobbleThreshold(%q)(10m) = %v, want 0", spec, got)
		}
	}
}

func TestParseScrobbleThreshold_Auto(t *testing.T) {
	t.Parallel()

	for _, spec := range []string{"", "auto", "AUTO", "  Auto  "} {
		fn, err := parseScrobbleThreshold(spec)
		if err != nil {
			t.Fatalf("parseScrobbleThreshold(%q) 返回错误: %v", spec, err)

			continue
		}

		for _, d := range []time.Duration{30 * time.Second, 3 * time.Minute, 10 * time.Minute} {
			if got, want := fn(d), defaultScrobbleThreshold(d); got != want {
				t.Errorf("parseScrobbleThreshold(%q)(%v) = %v, want %v", spec, d, got, want)
			}
		}
	}
}

func TestParseScrobbleThreshold_Percent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		spec    string
		song    time.Duration
		want    time.Duration
		wantErr bool
	}{
		{spec: "50%", song: 4 * time.Minute, want: 2 * time.Minute},
		{spec: "100%", song: 3 * time.Minute, want: 3 * time.Minute},
		{spec: "25%", song: 4 * time.Minute, want: 1 * time.Minute},
		{spec: "0.5", song: 4 * time.Minute, want: 2 * time.Minute},
		{spec: "0.25", song: 2 * time.Minute, want: 30 * time.Second},
		{spec: "0%", wantErr: true},
		{spec: "abc%", wantErr: true},
		{spec: "-10%", wantErr: true},
		// 1 及以上不是「比例」语义，会落到时长解析 → 解析失败
		{spec: "1", wantErr: true},
		{spec: "1.5", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.spec, func(t *testing.T) {
			t.Parallel()

			fn, err := parseScrobbleThreshold(tt.spec)
			if tt.wantErr {
				if err == nil {
					t.Errorf("parseScrobbleThreshold(%q) 期望报错，实际通过", tt.spec)
				}

				return
			}

			if err != nil {
				t.Fatalf("parseScrobbleThreshold(%q) 返回错误: %v", tt.spec, err)
			}

			if got := fn(tt.song); got != tt.want {
				t.Errorf("parseScrobbleThreshold(%q)(%v) = %v, want %v", tt.spec, tt.song, got, tt.want)
			}
		})
	}
}

func TestParseScrobbleThreshold_FixedDuration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		spec string
		want time.Duration
	}{
		{spec: "30s", want: 30 * time.Second},
		{spec: "2m", want: 2 * time.Minute},
		{spec: "1m30s", want: 90 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.spec, func(t *testing.T) {
			t.Parallel()

			fn, err := parseScrobbleThreshold(tt.spec)
			if err != nil {
				t.Fatalf("parseScrobbleThreshold(%q) 返回错误: %v", tt.spec, err)

				return
			}

			// 固定阈值与歌曲时长无关
			for _, d := range []time.Duration{10 * time.Second, 10 * time.Minute} {
				if got := fn(d); got != tt.want {
					t.Errorf("parseScrobbleThreshold(%q)(%v) = %v, want %v", tt.spec, d, got, tt.want)
				}
			}
		})
	}
}

func TestParseScrobbleThreshold_Invalid(t *testing.T) {
	t.Parallel()

	for _, spec := range []string{"abc", "%", "5x", "-1m"} {
		if _, err := parseScrobbleThreshold(spec); err == nil {
			t.Errorf("parseScrobbleThreshold(%q) 期望报错，实际通过", spec)
		}
	}
}

func TestDefaultScrobbleThreshold(t *testing.T) {
	t.Parallel()

	// Last.fm 官方规则：min(时长/2, 4min)，先到为准
	tests := []struct {
		song time.Duration
		want time.Duration
	}{
		{song: 30 * time.Second, want: 15 * time.Second},
		{song: 3 * time.Minute, want: 90 * time.Second},
		{song: 8 * time.Minute, want: 4 * time.Minute},
		{song: 20 * time.Minute, want: 4 * time.Minute},
		{song: 0, want: 0},
	}

	for _, tt := range tests {
		if got := defaultScrobbleThreshold(tt.song); got != tt.want {
			t.Errorf("defaultScrobbleThreshold(%v) = %v, want %v", tt.song, got, tt.want)
		}
	}
}

func TestParseContentRange(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		in        string
		wantEnd   int64
		wantTotal int64
		wantOK    bool
	}{
		{name: "常规", in: "bytes 0-1048575/30274485", wantEnd: 1048575, wantTotal: 30274485, wantOK: true},
		{name: "单字节", in: "bytes 0-0/100", wantEnd: 0, wantTotal: 100, wantOK: true},
		{name: "带空格", in: "bytes 0-99 / 1000", wantEnd: 99, wantTotal: 1000, wantOK: true},
		{name: "总大小未知", in: "bytes 0-99/*", wantOK: false},
		{name: "缺前缀", in: "0-99/100", wantOK: false},
		{name: "缺斜杠", in: "bytes 0-99", wantOK: false},
		{name: "缺连字符", in: "bytes 0/100", wantOK: false},
		{name: "结束位置非数字", in: "bytes 0-abc/100", wantOK: false},
		{name: "总大小非数字", in: "bytes 0-99/abc", wantOK: false},
		{name: "空串", in: "", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			end, total, ok := parseContentRange(tt.in)
			if ok != tt.wantOK {
				t.Fatalf("parseContentRange(%q) ok = %v, want %v", tt.in, ok, tt.wantOK)
			}

			if !tt.wantOK {
				return
			}

			if end != tt.wantEnd || total != tt.wantTotal {
				t.Errorf("parseContentRange(%q) = (%d, %d), want (%d, %d)",
					tt.in, end, total, tt.wantEnd, tt.wantTotal)
			}
		})
	}
}

func TestParseHLSSegmentPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		in       string
		wantGUID string
		wantSeg  int
		wantOK   bool
	}{
		{
			name: "标准分片", in: "/music/api/v1/track/hls/abc123/00007.m4s",
			wantGUID: "abc123", wantSeg: 7, wantOK: true,
		},
		{
			name: "首片", in: "/track/hls/g1/0.m4s",
			wantGUID: "g1", wantSeg: 0, wantOK: true,
		},
		{name: "非 m4s 分片", in: "/track/hls/g1/index.m3u8", wantOK: false},
		{name: "序号非数字", in: "/track/hls/g1/abc.m4s", wantOK: false},
		{name: "缺 markdown 标记", in: "/other/path/00001.m4s", wantOK: false},
		{name: "空串", in: "", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			guid, seg, ok := parseHLSSegmentPath(tt.in)
			if ok != tt.wantOK {
				t.Fatalf("parseHLSSegmentPath(%q) ok = %v, want %v", tt.in, ok, tt.wantOK)
			}

			if !tt.wantOK {
				return
			}

			if guid != tt.wantGUID || seg != tt.wantSeg {
				t.Errorf("parseHLSSegmentPath(%q) = (%q, %d), want (%q, %d)",
					tt.in, guid, seg, tt.wantGUID, tt.wantSeg)
			}
		})
	}
}

func TestHLSGUIDFromPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want string
	}{
		{in: "/track/hls/guid-1/seg.m4s", want: "guid-1"},
		{in: "/x/track/hls/guid-2", want: "guid-2"},
		{in: "/no/marker/here", want: ""},
		{in: "", want: ""},
	}

	for _, tt := range tests {
		if got := hlsGUIDFromPath(tt.in); got != tt.want {
			t.Errorf("hlsGUIDFromPath(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestParseHLSSegmentDuration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want time.Duration
	}{
		{
			name: "取第一条 EXTINF",
			body: "#EXTM3U\n#EXT-X-VERSION:7\n#EXTINF:4.0,\n0.m4s\n#EXTINF:4.0,\n1.m4s\n",
			want: 4 * time.Second,
		},
		{
			name: "带空格与注释",
			body: "\n  #EXTINF: 2.5 ,\n",
			want: 2500 * time.Millisecond,
		},
		{name: "无 EXTINF", body: "#EXTM3U\n0.m4s\n", want: 0},
		{name: "非法时长跳过", body: "#EXTINF:abc,\n#EXTINF:3.0,\n", want: 3 * time.Second},
		{name: "负时长跳过", body: "#EXTINF:-1,\n", want: 0},
		{name: "空响应", body: "", want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := parseHLSSegmentDuration([]byte(tt.body)); got != tt.want {
				t.Errorf("parseHLSSegmentDuration(%q) = %v, want %v", tt.body, got, tt.want)
			}
		})
	}
}
