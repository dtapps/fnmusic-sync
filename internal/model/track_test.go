package model

import (
	"testing"
	"time"
)

func TestTrackValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		track Track
		want  bool
	}{
		{name: "标题与艺人齐备", track: Track{Title: "Song", Artist: "Artist"}, want: true},
		{name: "缺标题", track: Track{Artist: "Artist"}, want: false},
		{name: "缺艺人", track: Track{Title: "Song"}, want: false},
		{name: "均为空", track: Track{}, want: false},
		{name: "仅空白字符不算缺失以外的语义", track: Track{Title: " ", Artist: " "}, want: true},
		{name: "其余字段不影响有效性", track: Track{
			Title: "Song", Artist: "Artist", Album: "", Duration: 0,
		}, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.track.Valid(); got != tt.want {
				t.Errorf("Track%+v.Valid() = %v, want %v", tt.track, got, tt.want)
			}
		})
	}
}

func TestTrackDuration(t *testing.T) {
	t.Parallel()

	// Duration 用 time.Duration 承载，构造时按秒换算必须正确——
	// scrobble 时长上报直接依赖这个值。
	tr := Track{Title: "S", Artist: "A", Duration: 215 * time.Second}
	if got := tr.Duration.Seconds(); got != 215 {
		t.Errorf("Duration.Seconds() = %v, want 215", got)
	}
}
