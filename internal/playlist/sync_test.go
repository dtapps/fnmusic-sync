package playlist

import (
	"testing"

	"cnb.cool/dtapp/fnmusic-sync/internal/config"
	"cnb.cool/dtapp/fnmusic-sync/internal/feiniu"
)

func TestPeriodToChinese(t *testing.T) {
	t.Parallel()

	tests := []struct {
		period string
		want   string
	}{
		{period: "7day", want: "7天"},
		{period: "1month", want: "1个月"},
		{period: "3month", want: "3个月"},
		{period: "6month", want: "6个月"},
		{period: "12month", want: "12个月"},
		// overall / 空值返回空串，调用方会把 {period} 占位符整段移除，
		// 避免出现「全部时间榜」这种冗余命名
		{period: "overall", want: ""},
		{period: "", want: ""},
		// 未知周期原样返回
		{period: "2year", want: "2year"},
	}

	for _, tt := range tests {
		t.Run(tt.period, func(t *testing.T) {
			t.Parallel()

			if got := periodToChinese(tt.period); got != tt.want {
				t.Errorf("periodToChinese(%q) = %q, want %q", tt.period, got, tt.want)
			}
		})
	}
}

func TestResolvePlaylistName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		desc     string
		name     string
		fallback string
		vars     map[string]string
		want     string
	}{
		{desc: "空名用 fallback", name: "", fallback: "默认歌单", vars: nil, want: "默认歌单"},
		{desc: "无占位符原样返回", name: "我的歌单", fallback: "x", vars: nil, want: "我的歌单"},
		{desc: "替换 year", name: "LB {year} 年度发现", fallback: "x", vars: map[string]string{"year": "2025"}, want: "LB 2025 年度发现"},
		{desc: "占位符为空则移除并压缩空格", name: "LF {period} 最常听", fallback: "x", vars: map[string]string{"period": ""}, want: "LF 最常听"},
		{desc: "多占位符同时替换", name: "{year} {period} 榜", fallback: "x", vars: map[string]string{"year": "2025", "period": "7天"}, want: "2025 7天 榜"},
		{desc: "首尾空格清理", name: "  名称  ", fallback: "x", vars: nil, want: "名称"},
		{desc: "未知占位符保持原样", name: "榜 {other}", fallback: "x", vars: map[string]string{"year": "2025"}, want: "榜 {other}"},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			t.Parallel()

			if got := resolvePlaylistName(tt.name, tt.fallback, tt.vars); got != tt.want {
				t.Errorf("resolvePlaylistName(%q, %q, %v) = %q, want %q",
					tt.name, tt.fallback, tt.vars, got, tt.want)
			}
		})
	}
}

func TestPlaylistName(t *testing.T) {
	t.Parallel()

	// 未配置 name 时用 fallback，绝不产出空名歌单
	if got := playlistName(config.PlaylistSourceConfig{}, "LB 周榜"); got != "LB 周榜" {
		t.Errorf("未配置 name 时 = %q, want LB 周榜", got)
	}

	if got := playlistName(config.PlaylistSourceConfig{Name: "我的推荐"}, "x"); got != "我的推荐" {
		t.Errorf("配置 name 时 = %q, want 我的推荐", got)
	}
}

func TestLastFMPlaylistName(t *testing.T) {
	t.Parallel()

	if got := lastFMPlaylistName(config.LastFMPlaylistSourceConfig{}, "LF 周榜"); got != "LF 周榜" {
		t.Errorf("未配置 name 时 = %q, want LF 周榜", got)
	}

	if got := lastFMPlaylistName(config.LastFMPlaylistSourceConfig{Name: "我的爱听"}, "x"); got != "我的爱听" {
		t.Errorf("配置 name 时 = %q, want 我的爱听", got)
	}
}

// trackIndexBuilder 构造 feiniu.TrackIndexes 测试数据。
type trackIndexBuilder struct {
	idx          *feiniu.TrackIndexes
	titleCounter map[string][]string
}

func newTrackIndexBuilder() *trackIndexBuilder {
	return &trackIndexBuilder{
		idx: &feiniu.TrackIndexes{
			MBIDToGUID:        make(map[string]string),
			ArtistTrackToGUID: make(map[string]string),
			TitleToGUID:       make(map[string]string),
			GUIDToTrack:       make(map[string]feiniu.Track),
		},
		titleCounter: make(map[string][]string),
	}
}

func (b *trackIndexBuilder) add(guid, artist, title, mbid string) *trackIndexBuilder {
	tr := feiniu.Track{GUID: guid, Title: title}
	if artist != "" {
		tr.Artists = append(tr.Artists, struct {
			GUID string `json:"guid"`
			Name string `json:"name"`
		}{Name: artist})
	}

	b.idx.GUIDToTrack[guid] = tr

	if mbid != "" {
		b.idx.MBIDToGUID[mbid] = guid
	}

	if artist != "" && title != "" {
		b.idx.ArtistTrackToGUID[feiniu.BuildArtistTrackKey(artist, title)] = guid

		// 与 feiniu.BuildTrackIndexes 一致：同时按主标题建索引
		if main := feiniu.MainTitle(title); main != title {
			b.idx.ArtistTrackToGUID[feiniu.BuildArtistTrackKey(artist, main)] = guid
		}
	}

	if title != "" {
		key := feiniu.NormalizeForMatch(title)
		b.titleCounter[key] = append(b.titleCounter[key], guid)

		if mainKey := feiniu.NormalizeForMatch(feiniu.MainTitle(title)); mainKey != key {
			b.titleCounter[mainKey] = append(b.titleCounter[mainKey], guid)
		}
	}

	return b
}

// build 收尾：曲名唯一的才进 TitleToGUID（与生产口径一致）。
func (b *trackIndexBuilder) build() *feiniu.TrackIndexes {
	for key, guids := range b.titleCounter {
		if len(guids) == 1 {
			b.idx.TitleToGUID[key] = guids[0]
		}
	}

	return b.idx
}

func TestMatchTrack_ByMBID(t *testing.T) {
	t.Parallel()

	idx := newTrackIndexBuilder().
		add("g1", "Artist A", "Song A", "rec-mbid-1").
		add("g2", "Artist B", "Song B", "").
		build()

	// MBID 命中优先级最高：即使艺人/曲名对不上也应命中
	got, kind := matchTrack(idx, "完全不同的艺人", "完全不同的歌", "rec-mbid-1")
	if got != "g1" || kind != matchMBID {
		t.Errorf("matchTrack = (%q, %v), want (g1, matchMBID)", got, kind)
	}
}

func TestMatchTrack_ByArtistTitle(t *testing.T) {
	t.Parallel()

	idx := newTrackIndexBuilder().
		add("g1", "Jay Chou", "Rice Field", "").
		add("g2", "Jay Chou", "Nunchucks", "").
		build()

	got, kind := matchTrack(idx, "Jay Chou", "Rice Field", "")
	if got != "g1" || kind != matchArtistTitle {
		t.Errorf("matchTrack = (%q, %v), want (g1, matchArtistTitle)", got, kind)
	}
}

func TestMatchTrack_CaseAndSpaceInsensitive(t *testing.T) {
	t.Parallel()

	idx := newTrackIndexBuilder().add("g1", "Jay Chou", "Rice Field", "").build()

	for _, c := range []struct{ artist, title string }{
		{"jay chou", "RICE FIELD"},
		{" Jay Chou ", " Rice  Field "},
	} {
		got, kind := matchTrack(idx, c.artist, c.title, "")
		if got != "g1" || kind != matchArtistTitle {
			t.Errorf("matchTrack(%q, %q) = (%q, %v), want (g1, matchArtistTitle)", c.artist, c.title, got, kind)
		}
	}
}

func TestMatchTrack_MainTitleFallback(t *testing.T) {
	t.Parallel()

	// 曲库标题带附注，来源是主标题
	idx := newTrackIndexBuilder().add("g1", "李荣浩", "不将就 (电影片尾曲)", "").build()

	got, kind := matchTrack(idx, "李荣浩", "不将就", "")
	if got != "g1" || kind != matchArtistTitle {
		t.Errorf("主标题降级失败: (%q, %v), want (g1, matchArtistTitle)", got, kind)
	}
}

func TestMatchTrack_TitleOnlyWhenUnique(t *testing.T) {
	t.Parallel()

	idx := newTrackIndexBuilder().
		add("g1", "Artist A", "Unique Song", "").
		add("g2", "Artist B", "Another Song", "").
		build()

	// 艺人名对不上（译名/繁简差异），曲名唯一 → 降级到纯曲名
	got, kind := matchTrack(idx, "Unknown Artist", "Unique Song", "")
	if got != "g1" || kind != matchTitleOnly {
		t.Errorf("纯曲名降级失败: (%q, %v), want (g1, matchTitleOnly)", got, kind)
	}
}

func TestMatchTrack_AmbiguousTitleNotMatched(t *testing.T) {
	t.Parallel()

	// 同名两首：纯曲名索引被丢弃，不能瞎猜
	idx := newTrackIndexBuilder().
		add("g1", "Artist A", "Same Song", "").
		add("g2", "Artist B", "Same Song", "").
		build()

	if got, kind := matchTrack(idx, "Unknown", "Same Song", ""); got != "" || kind != matchNone {
		t.Errorf("歧义曲名不应匹配: (%q, %v)", got, kind)
	}
}

func TestMatchTrack_NoMatch(t *testing.T) {
	t.Parallel()

	idx := newTrackIndexBuilder().add("g1", "A", "T", "").build()

	if got, kind := matchTrack(idx, "X", "Y", ""); got != "" || kind != matchNone {
		t.Errorf("不应匹配: (%q, %v)", got, kind)
	}

	// 空索引不能崩
	if got, kind := matchTrack(&feiniu.TrackIndexes{}, "A", "T", ""); got != "" || kind != matchNone {
		t.Errorf("空索引不应匹配: (%q, %v)", got, kind)
	}
}

func TestMatchTrack_EmptyInput(t *testing.T) {
	t.Parallel()

	idx := newTrackIndexBuilder().add("g1", "A", "T", "").build()

	// 脏数据（缺艺人/曲名）不应误命中
	if got, _ := matchTrack(idx, "", "", ""); got != "" {
		t.Errorf("全空输入不应匹配，got %q", got)
	}

	if got, _ := matchTrack(idx, "", "T", ""); got != "g1" {
		t.Errorf("仅曲名唯一时应命中，got %q", got)
	}
}
