package listenbrainz

import (
	"time"

	"cnb.cool/dtapp/fnmusic-sync/internal/model"
)

// newTestTrack 返回一条字段齐全的曲目，用于验证 buildPayload 的字段映射。
func newTestTrack() model.Track {
	return model.Track{
		Title:         "Title",
		Artist:        "Artist",
		Album:         "Album",
		Duration:      215 * time.Second,
		RecordingMBID: "rec-mbid",
		ReleaseMBID:   "rel-mbid",
		ArtistMBID:    "art-mbid",
	}
}

// newBareTrack 返回最小曲目（无时长、无 MBID），用于验证 omitempty 行为。
func newBareTrack() model.Track {
	return model.Track{Title: "T", Artist: "A"}
}
