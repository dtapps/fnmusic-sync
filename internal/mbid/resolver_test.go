package mbid

import (
	"testing"

	"cnb.cool/dtapp/fnmusic-sync/internal/feiniu"
)

func TestClassifyMBID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		want string
	}{
		// ID3v2 TXXX 帧的描述名（小写）
		{name: "musicbrainz album id", want: "release"},
		{name: "musicbrainz album artist id", want: "artist"},
		{name: "musicbrainz artist id", want: "artist"},
		{name: "musicbrainz release group id", want: "releasegroup"},
		{name: "musicbrainz release track id", want: "release"},
		{name: "musicbrainz recording id", want: "recording"},
		{name: "musicbrainz track id", want: "recording"},
		{name: "musicbrainz work id", want: "work"},
		// Vorbis / FLAC / MP4 键名
		{name: "musicbrainz_recordingid", want: "recording"},
		{name: "musicbrainz_albumid", want: "release"},
		{name: "musicbrainz_albumartistid", want: "artist"},
		{name: "musicbrainz_releasegroupid", want: "releasegroup"},
		{name: "musicbrainz_workid", want: "work"},
		// 顺序敏感：releasegroup 必须先于 release 判定
		{name: "musicbrainz releasegroup id", want: "releasegroup"},
		// 非 MBID 字段
		{name: "album", want: ""},
		{name: "artist", want: ""},
		{name: "date", want: ""},
		{name: "", want: ""},
		// 含 mbid 但非五类
		{name: "some mbid field", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := classifyMBID(tt.name); got != tt.want {
				t.Errorf("classifyMBID(%q) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

func TestNormalizeRawEntry_String(t *testing.T) {
	t.Parallel()

	// Vorbis/FLAC/MP4：键名即字段名，值需去空白
	key, val := normalizeRawEntry("  MUSICBRAINZ_RECORDINGID  ", "  abc-123  ")
	if key != "musicbrainz_recordingid" {
		t.Errorf("key = %q, want musicbrainz_recordingid（应小写去空白）", key)
	}

	if val != "abc-123" {
		t.Errorf("val = %q, want abc-123", val)
	}
}

func TestNormalizeRawEntry_Unsupported(t *testing.T) {
	t.Parallel()

	// 不支持的类型返回空串，调用方据此跳过
	for _, v := range []any{42, 3.14, nil, []string{"x"}} {
		if key, val := normalizeRawEntry("k", v); key != "" || val != "" {
			t.Errorf("normalizeRawEntry(%T) = (%q, %q), want 空", v, key, val)
		}
	}
}

func TestIsZeroMBID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		m    fileMeta
		want bool
	}{
		{name: "全空", m: fileMeta{}, want: true},
		{name: "只有标题艺人", m: fileMeta{Title: "T", Artist: "A", Album: "B"}, want: true},
		{name: "有 recording", m: fileMeta{RecordingMBID: "x"}, want: false},
		{name: "有 release", m: fileMeta{ReleaseMBID: "x"}, want: false},
		{name: "有 releasegroup", m: fileMeta{ReleaseGroupMBID: "x"}, want: false},
		{name: "有 artist", m: fileMeta{ArtistMBID: "x"}, want: false},
		{name: "有 work", m: fileMeta{WorkMBID: "x"}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := isZeroMBID(tt.m); got != tt.want {
				t.Errorf("isZeroMBID(%+v) = %v, want %v", tt.m, got, tt.want)
			}
		})
	}
}

// mkFeiniuTrack 构造一条飞牛曲目（含音频路径，供文件名兜底用）。
func mkFeiniuTrack(guid, artist, title, path string) feiniu.Track {
	tr := feiniu.Track{GUID: guid, Title: title}
	tr.AudioSpec.Path = path

	if artist != "" {
		tr.Artists = append(tr.Artists, struct {
			GUID string `json:"guid"`
			Name string `json:"name"`
		}{Name: artist})
	}

	return tr
}

func TestBuildTrackIndex(t *testing.T) {
	t.Parallel()

	tracks := []feiniu.Track{
		mkFeiniuTrack("g1", "Jay Chou", "Rice Field", "/music/jay/rice.flac"),
		mkFeiniuTrack("g2", "Jay Chou", "Nunchucks", "/music/jay/nunchucks.flac"),
		mkFeiniuTrack("g3", "李荣浩", "不将就 (电影片尾曲)", "/music/lrh/bujiangjiu.flac"),
	}

	byArtistTitle, byTitle, byBase := buildTrackIndex(tracks)

	// 艺人+曲名
	if got := byArtistTitle[feiniu.BuildArtistTrackKey("Jay Chou", "Rice Field")]; got != "g1" {
		t.Errorf("byArtistTitle 缺 g1: %q", got)
	}

	// 主标题降级键
	if got := byArtistTitle[feiniu.BuildArtistTrackKey("李荣浩", "不将就")]; got != "g3" {
		t.Errorf("byArtistTitle 未建立主标题降级键: %q", got)
	}

	// 曲名唯一 → 建立纯曲名索引
	if got := byTitle[feiniu.NormalizeForMatch("Rice Field")]; got != "g1" {
		t.Errorf("byTitle 缺 g1: %q", got)
	}

	// 文件名兜底索引（小写）
	if guids := byBase["rice.flac"]; len(guids) != 1 || guids[0] != "g1" {
		t.Errorf("byBase[rice.flac] = %v, want [g1]", guids)
	}
}

func TestBuildTrackIndex_AmbiguousTitleExcluded(t *testing.T) {
	t.Parallel()

	tracks := []feiniu.Track{
		mkFeiniuTrack("g1", "Artist A", "Same Song", "/a.flac"),
		mkFeiniuTrack("g2", "Artist B", "Same Song", "/b.flac"),
	}

	_, byTitle, _ := buildTrackIndex(tracks)

	if _, ok := byTitle[feiniu.NormalizeForMatch("Same Song")]; ok {
		t.Error("歧义曲名不应写入 byTitle")
	}
}

func TestBuildTrackIndex_EmptyFields(t *testing.T) {
	t.Parallel()

	// 缺艺人/曲名/路径的脏数据不应产生空键索引
	tracks := []feiniu.Track{
		{GUID: "g1"},
		mkFeiniuTrack("g2", "", "Only Title", ""),
	}

	byArtistTitle, byTitle, byBase := buildTrackIndex(tracks)

	if len(byArtistTitle) != 0 {
		t.Errorf("缺艺人不应建 artist+title 索引: %v", byArtistTitle)
	}

	if got := byTitle[feiniu.NormalizeForMatch("Only Title")]; got != "g2" {
		t.Errorf("有曲名时应建纯曲名索引, got %q", got)
	}

	if len(byBase) != 0 {
		t.Errorf("缺路径不应建文件名索引: %v", byBase)
	}
}

func TestMatchTrack_ByArtistTitle(t *testing.T) {
	t.Parallel()

	byArtistTitle, byTitle, byBase := buildTrackIndex([]feiniu.Track{
		mkFeiniuTrack("g1", "Jay Chou", "Rice Field", "/m/rice.flac"),
	})

	m := fileMeta{Title: "Rice Field", Artist: "Jay Chou", Album: "Common Jasmin Orange"}
	if got := matchTrack(byArtistTitle, byTitle, byBase, m, "/mnt/x/whatever.flac"); got != "g1" {
		t.Errorf("matchTrack = %q, want g1", got)
	}
}

func TestMatchTrack_MainTitleFallback(t *testing.T) {
	t.Parallel()

	byArtistTitle, byTitle, byBase := buildTrackIndex([]feiniu.Track{
		mkFeiniuTrack("g1", "Jay Chou", "Rice Field", "/m/rice.flac"),
	})

	// 文件标签带附注后缀，索引里是主标题
	m := fileMeta{Title: "Rice Field (Live 2004)", Artist: "Jay Chou"}
	if got := matchTrack(byArtistTitle, byTitle, byBase, m, "/m/x.flac"); got != "g1" {
		t.Errorf("主标题降级失败: got %q, want g1", got)
	}
}

func TestMatchTrack_ByTitleOnly(t *testing.T) {
	t.Parallel()

	byArtistTitle, byTitle, byBase := buildTrackIndex([]feiniu.Track{
		mkFeiniuTrack("g1", "Jay Chou", "Rice Field", "/m/rice.flac"),
		mkFeiniuTrack("g2", "Jay Chou", "Nunchucks", "/m/nunchucks.flac"),
	})

	// 艺人名对不上（繁简/译名），曲名唯一 → 纯曲名匹配
	m := fileMeta{Title: "Rice Field", Artist: "周杰倫"}
	if got := matchTrack(byArtistTitle, byTitle, byBase, m, "/m/x.flac"); got != "g1" {
		t.Errorf("纯曲名降级失败: got %q, want g1", got)
	}
}

func TestMatchTrack_ByFileName(t *testing.T) {
	t.Parallel()

	// 标签完全缺失，靠文件名兜底（跨挂载路径不同也能命中）
	byArtistTitle, byTitle, byBase := buildTrackIndex([]feiniu.Track{
		mkFeiniuTrack("g1", "Jay Chou", "Rice Field", "/music/jay/Rice Field.flac"),
	})

	if got := matchTrack(byArtistTitle, byTitle, byBase, fileMeta{}, "/other/mount/Rice Field.flac"); got != "g1" {
		t.Errorf("文件名兜底失败: got %q, want g1", got)
	}
}

func TestMatchTrack_FileNameAmbiguousNotMatched(t *testing.T) {
	t.Parallel()

	// 同名文件两份（不同目录）→ 不能猜
	byArtistTitle, byTitle, byBase := buildTrackIndex([]feiniu.Track{
		mkFeiniuTrack("g1", "A", "T1", "/d1/track01.flac"),
		mkFeiniuTrack("g2", "B", "T2", "/d2/track01.flac"),
	})

	if got := matchTrack(byArtistTitle, byTitle, byBase, fileMeta{}, "/d3/track01.flac"); got != "" {
		t.Errorf("歧义文件名不应匹配: got %q", got)
	}
}

func TestMatchTrack_NoMatch(t *testing.T) {
	t.Parallel()

	byArtistTitle, byTitle, byBase := buildTrackIndex([]feiniu.Track{
		mkFeiniuTrack("g1", "A", "T", "/m/a.flac"),
	})

	m := fileMeta{Title: "Completely Different", Artist: "Nobody"}
	if got := matchTrack(byArtistTitle, byTitle, byBase, m, "/x/y.flac"); got != "" {
		t.Errorf("不应匹配: got %q", got)
	}
}
