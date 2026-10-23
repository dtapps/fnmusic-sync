package feiniu

import "testing"

func TestNormalizeForMatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "空串", in: "", want: ""},
		{name: "小写化", in: "Hello", want: "hello"},
		{name: "去除空格", in: "G.E.M.", want: "g.e.m."},
		{name: "去除首尾空格", in: "  gem  ", want: "gem"},
		{name: "去除中间空格", in: "a b c", want: "abc"},
		{name: "去除制表符与换行", in: "a\tb\nc", want: "abc"},
		{name: "去除全角空格", in: "中\u3000文", want: "中文"},
		{name: "保留标点", in: "Can't Stop", want: "can'tstop"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := NormalizeForMatch(tt.in); got != tt.want {
				t.Errorf("NormalizeForMatch(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestBuildArtistTrackKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		artist string
		track  string
		want   string
	}{
		{name: "常规", artist: "Jay Chou", track: "Rice Field", want: "jaychou|ricefield"},
		{name: "大小写与空格差异归一到同一键", artist: "jay chou", track: "RICE FIELD", want: "jaychou|ricefield"},
		{name: "空艺人", artist: "", track: "Song", want: "|song"},
		{name: "空曲名", artist: "Artist", track: "", want: "artist|"},
		{name: "均为空", artist: "", track: "", want: "|"},
		{name: "中文", artist: "李荣浩", track: "不将就", want: "李荣浩|不将就"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := BuildArtistTrackKey(tt.artist, tt.track); got != tt.want {
				t.Errorf("BuildArtistTrackKey(%q, %q) = %q, want %q", tt.artist, tt.track, got, tt.want)
			}
		})
	}

	// 大小写/空格不同的同一首歌应产出相同键（匹配的前提）
	if a, b := BuildArtistTrackKey("G.E.M.", "Light Years"), BuildArtistTrackKey("g.e.m.", "lightyears"); a != b {
		t.Errorf("同一首歌不同写法键不等: %q vs %q", a, b)
	}
}

func TestMainTitle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "无附注", in: "不将就", want: "不将就"},
		{name: "英文圆括号附注", in: "不将就 (Can't Bear It)", want: "不将就"},
		{name: "多重附注", in: "不将就 (Can't Bear It) (电影《何以笙箫默》片尾曲)", want: "不将就"},
		{name: "方括号附注", in: "Song [Remastered 2011]", want: "Song"},
		{name: "全角圆括号", in: "曲名（Live）", want: "曲名"},
		{name: "全角方括号", in: "曲名【伴奏】", want: "曲名"},
		{name: "附注在开头则原样返回", in: "(Intro) Song", want: "(Intro) Song"},
		{name: "括号前只有空格则原样返回", in: "   (x)", want: "   (x)"},
		{name: "空串", in: "", want: ""},
		{name: "括号在末尾且主标题为空", in: "(only)", want: "(only)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := MainTitle(tt.in); got != tt.want {
				t.Errorf("MainTitle(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
