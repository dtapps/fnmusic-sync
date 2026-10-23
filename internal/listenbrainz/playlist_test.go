package listenbrainz

import (
	"encoding/json"
	"testing"
	"time"
)

// makePlaylist 构造带 JSPF 扩展的歌单，ext 为可选的 extensions 字典。
func makePlaylist(id, identifier, date string, ext map[string]jspfPlaylistExtension) *Playlist {
	return &Playlist{
		ID:         id,
		Identifier: identifier,
		Date:       date,
		Extension:  ext,
	}
}

func TestPlaylistMBID(t *testing.T) {
	t.Parallel()

	const mbid = "11111111-2222-3333-4444-555555555555"

	tests := []struct {
		name string
		p    *Playlist
		want string
	}{
		{name: "优先用 id", p: makePlaylist(mbid, "https://listenbrainz.org/playlist/other", "", nil), want: mbid},
		{
			name: "从 identifier 解析",
			p:    makePlaylist("", "https://listenbrainz.org/playlist/"+mbid, "", nil),
			want: mbid,
		},
		{
			name: "identifier 尾部斜杠",
			p:    makePlaylist("", "https://listenbrainz.org/playlist/"+mbid+"/", "", nil),
			want: mbid,
		},
		{name: "identifier 无 MBID", p: makePlaylist("", "https://listenbrainz.org/playlist/", "", nil), want: ""},
		{name: "全空", p: makePlaylist("", "", "", nil), want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.p.MBID(); got != tt.want {
				t.Errorf("MBID() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMBIDFromURL(t *testing.T) {
	t.Parallel()

	const mbid = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

	tests := []struct {
		in   string
		want string
	}{
		{in: "https://listenbrainz.org/playlist/" + mbid, want: mbid},
		{in: "https://musicbrainz.org/recording/" + mbid, want: mbid},
		{in: "", want: ""},
		{in: "not-a-url", want: ""},
		{in: "https://x/y/too-short", want: ""},
		// 长度对但不是 UUID 形状（连字符数不对）
		{in: "https://x/y/" + "aaaaaaaaabbbbccccddddeeeeeeeeeeee", want: ""},
	}

	for _, tt := range tests {
		if got := mbidFromURL(tt.in); got != tt.want {
			t.Errorf("mbidFromURL(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestRecordingMBIDFromIdentifiers(t *testing.T) {
	t.Parallel()

	const mbid = "12345678-1234-1234-1234-123456789012"

	tests := []struct {
		name string
		ids  []string
		want string
	}{
		{
			name: "从 recording 链接取",
			ids:  []string{"https://musicbrainz.org/release/x", "https://musicbrainz.org/recording/" + mbid},
			want: mbid,
		},
		{name: "无 recording 链接", ids: []string{"https://musicbrainz.org/release/x"}, want: ""},
		{name: "空列表", ids: nil, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := recordingMBIDFromIdentifiers(tt.ids); got != tt.want {
				t.Errorf("recordingMBIDFromIdentifiers(%v) = %q, want %q", tt.ids, got, tt.want)
			}
		})
	}
}

func TestSourcePatchAndYear(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		source    string
		wantPatch string
		wantYear  string
	}{
		{name: "daily-jams 无年份", source: "daily-jams", wantPatch: "daily-jams", wantYear: ""},
		{name: "年度发现", source: "top-discoveries-of-2025", wantPatch: "top-discoveries-of-2025", wantYear: "2025"},
		{name: "年度错过", source: "top-missed-recordings-of-2024", wantPatch: "top-missed-recordings-of-2024", wantYear: "2024"},
		{name: "周榜", source: "weekly-jams", wantPatch: "weekly-jams", wantYear: ""},
		{name: "其他年度前缀不识别", source: "top-tracks-of-2025", wantPatch: "top-tracks-of-2025", wantYear: ""},
		{name: "无扩展", source: "", wantPatch: "", wantYear: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var p *Playlist
			if tt.source == "" {
				p = makePlaylist("", "", "", nil)
			} else {
				ext := jspfPlaylistExtension{}
				ext.AdditionalMetadata.AlgorithmMetadata.SourcePatch = tt.source
				p = makePlaylist("", "", "", map[string]jspfPlaylistExtension{jspfPlaylistKey: ext})
			}

			if got := p.SourcePatch(); got != tt.wantPatch {
				t.Errorf("SourcePatch() = %q, want %q", got, tt.wantPatch)
			}

			if got := p.YearFromSourcePatch(); got != tt.wantYear {
				t.Errorf("YearFromSourcePatch() = %q, want %q", got, tt.wantYear)
			}
		})
	}
}

func TestPlaylistExpiresAt(t *testing.T) {
	t.Parallel()

	ext := jspfPlaylistExtension{}
	ext.AdditionalMetadata.ExpiresAt = "2026-10-30T00:00:00Z"

	p := makePlaylist("", "", "", map[string]jspfPlaylistExtension{jspfPlaylistKey: ext})

	got, ok := p.ExpiresAt()
	if !ok {
		t.Fatal("ExpiresAt() ok = false, want true")
	}

	want := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("ExpiresAt() = %v, want %v", got, want)
	}

	// 缺失 / 非法时间 → false
	if _, ok := makePlaylist("", "", "", nil).ExpiresAt(); ok {
		t.Error("无扩展时 ExpiresAt 应返回 false")
	}

	bad := jspfPlaylistExtension{}
	bad.AdditionalMetadata.ExpiresAt = "not-a-time"
	if _, ok := makePlaylist("", "", "", map[string]jspfPlaylistExtension{jspfPlaylistKey: bad}).ExpiresAt(); ok {
		t.Error("非法时间应返回 false")
	}
}

func TestPlaylistTimestamp(t *testing.T) {
	t.Parallel()

	d := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	// 优先 last_modified_at
	ext := jspfPlaylistExtension{}
	ext.LastModifiedAt = "2026-06-01T00:00:00Z"

	p := makePlaylist("", "", "2026-05-01T12:00:00Z", map[string]jspfPlaylistExtension{jspfPlaylistKey: ext})
	if got, want := p.Timestamp(), time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("Timestamp() = %v, want %v（应优先 last_modified_at）", got, want)
	}

	// 回退到 date
	p2 := makePlaylist("", "", "2026-05-01T12:00:00Z", nil)
	if got := p2.Timestamp(); !got.Equal(d) {
		t.Errorf("Timestamp() = %v, want %v", got, d)
	}

	// 都没有 → 零值
	if got := makePlaylist("", "", "", nil).Timestamp(); !got.IsZero() {
		t.Errorf("Timestamp() = %v, want 零值", got)
	}
}

func TestPlaylistTrackUnmarshalJSPF(t *testing.T) {
	t.Parallel()

	const mbid = "99999999-8888-7777-6666-555555555555"

	// JSPF 原生字段：title / creator / album / identifier
	body := `{
		"title": "Track Name",
		"creator": "Artist Name",
		"album": "Album Name",
		"identifier": ["https://musicbrainz.org/recording/` + mbid + `"],
		"extension": {
			"https://musicbrainz.org/doc/jspf#track": {
				"additional_metadata": {
					"artists": [{"artist_credit_name": "A", "artist_mbid": "` + mbid + `", "join_phrase": ""}]
				}
			}
		}
	}`

	var tr PlaylistTrack
	if err := json.Unmarshal([]byte(body), &tr); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if tr.TrackName != "Track Name" {
		t.Errorf("TrackName = %q（来自 title）", tr.TrackName)
	}

	if tr.ArtistName != "Artist Name" {
		t.Errorf("ArtistName = %q（来自 creator）", tr.ArtistName)
	}

	if tr.ReleaseName != "Album Name" {
		t.Errorf("ReleaseName = %q（来自 album）", tr.ReleaseName)
	}

	if tr.RecordingMBID != mbid {
		t.Errorf("RecordingMBID = %q（来自 identifier）", tr.RecordingMBID)
	}

	if tr.ArtistMBID != mbid {
		t.Errorf("ArtistMBID = %q（来自 extension）", tr.ArtistMBID)
	}
}

func TestPlaylistTrackUnmarshalCompatFields(t *testing.T) {
	t.Parallel()

	// 兼容字段优先于 JSPF 字段
	body := `{
		"title": "jspf title",
		"creator": "jspf creator",
		"track_name": "compat track",
		"artist_name": "compat artist",
		"recording_mbid": "direct-mbid"
	}`

	var tr PlaylistTrack
	if err := json.Unmarshal([]byte(body), &tr); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if tr.TrackName != "compat track" || tr.ArtistName != "compat artist" || tr.RecordingMBID != "direct-mbid" {
		t.Errorf("兼容字段未优先生效: %+v", tr)
	}
}

func TestPlaylistTrackUnmarshalInvalidJSON(t *testing.T) {
	t.Parallel()

	var tr PlaylistTrack
	if err := json.Unmarshal([]byte(`{`), &tr); err == nil {
		t.Error("非法 JSON 应返回错误")
	}
}

func TestBuildPayload(t *testing.T) {
	t.Parallel()

	c := &Client{Token: "t"}

	payload := c.buildPayload("playing_now", newTestTrack(), 0)

	if payload.ListenType != "playing_now" {
		t.Errorf("ListenType = %q", payload.ListenType)
	}

	if len(payload.Payload) != 1 {
		t.Fatalf("Payload 长度 = %d, want 1", len(payload.Payload))
	}

	item := payload.Payload[0]
	if item.TrackMetadata.ArtistName != "Artist" || item.TrackMetadata.TrackName != "Title" {
		t.Errorf("艺人/曲名映射错误: %+v", item.TrackMetadata)
	}

	if item.TrackMetadata.ReleaseName != "Album" {
		t.Errorf("ReleaseName = %q", item.TrackMetadata.ReleaseName)
	}

	// duration 以毫秒上报，且必须存在
	if got := item.TrackMetadata.AdditionalInfo["duration_ms"]; got != int64(215000) {
		t.Errorf("duration_ms = %v, want 215000", got)
	}

	if got := item.TrackMetadata.AdditionalInfo["recording_mbid"]; got != "rec-mbid" {
		t.Errorf("recording_mbid = %v", got)
	}

	if got := item.TrackMetadata.AdditionalInfo["release_mbid"]; got != "rel-mbid" {
		t.Errorf("release_mbid = %v", got)
	}

	if got := item.TrackMetadata.AdditionalInfo["artist_mbid"]; got != "art-mbid" {
		t.Errorf("artist_mbid = %v", got)
	}
}

func TestBuildPayloadOmitsMissingMBIDs(t *testing.T) {
	t.Parallel()

	c := &Client{Token: "t"}

	// 无 MBID、无时长时 additional_info 里不应出现这些键
	payload := c.buildPayload("single", newBareTrack(), 1700000000)

	info := payload.Payload[0].TrackMetadata.AdditionalInfo
	for _, key := range []string{"duration_ms", "recording_mbid", "release_mbid", "artist_mbid"} {
		if _, ok := info[key]; ok {
			t.Errorf("additional_info 不应包含 %s: %v", key, info)
		}
	}

	if payload.Payload[0].ListenedAt != 1700000000 {
		t.Errorf("ListenedAt = %d", payload.Payload[0].ListenedAt)
	}
}

func TestClientName(t *testing.T) {
	t.Parallel()

	if got := (&Client{}).Name(); got == "" {
		t.Error("Name() 不应为空")
	}
}
