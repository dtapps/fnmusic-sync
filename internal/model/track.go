package model

import "time"

type Track struct {
	GUID string `json:"guid"`

	Title       string `json:"title"`
	Artist      string `json:"artist"`
	Album       string `json:"album"`
	AlbumArtist string `json:"album_artist"`

	Duration time.Duration `json:"duration"`

	TrackNumber int `json:"track_number"`

	MBID          string `json:"mbid"`
	RecordingMBID string `json:"recording_mbid"`
	ReleaseMBID   string `json:"release_mbid"`
	ArtistMBID    string `json:"artist_mbid"`

	Path string `json:"path"`
}

func (t Track) Valid() bool {
	return t.Title != "" && t.Artist != ""
}
