package scrobbler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"cnb.cool/dtapp/fnmusic-sync/internal/model"
)

const listenBrainzEndpoint = "https://api.listenbrainz.org/1/submit-listens"

type ListenBrainz struct {
	Token string
	// Username 保留给将来的歌单同步使用（/1/user/{username}/playlists）；
	// scrobble（submit-listens）不需要它，可以为空。
	Username string

	Client *http.Client
}

func NewListenBrainz(token, username string) *ListenBrainz {
	return &ListenBrainz{
		Token:    token,
		Username: username,
		Client: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func (l *ListenBrainz) Name() string {
	return "listenbrainz"
}

type listenPayload struct {
	ListenType string `json:"listen_type"`
	Payload    []struct {
		ListenedAt    int64 `json:"listened_at,omitempty"`
		TrackMetadata struct {
			ArtistName  string `json:"artist_name"`
			TrackName   string `json:"track_name"`
			ReleaseName string `json:"release_name,omitempty"`

			AdditionalInfo map[string]any `json:"additional_info,omitempty"`
		} `json:"track_metadata"`
	} `json:"payload"`
}

func (l *ListenBrainz) buildPayload(
	listenType string,
	track model.Track,
	startedAt int64,
) listenPayload {
	var item struct {
		ListenedAt    int64 `json:"listened_at,omitempty"`
		TrackMetadata struct {
			ArtistName  string `json:"artist_name"`
			TrackName   string `json:"track_name"`
			ReleaseName string `json:"release_name,omitempty"`

			AdditionalInfo map[string]any `json:"additional_info,omitempty"`
		} `json:"track_metadata"`
	}

	item.ListenedAt = startedAt

	item.TrackMetadata.ArtistName = track.Artist
	item.TrackMetadata.TrackName = track.Title
	item.TrackMetadata.ReleaseName = track.Album

	item.TrackMetadata.AdditionalInfo =
		make(map[string]any)

	if track.Duration > 0 {
		item.TrackMetadata.AdditionalInfo["duration_ms"] =
			track.Duration.Milliseconds()
	}

	if track.RecordingMBID != "" {
		item.TrackMetadata.AdditionalInfo["recording_mbid"] =
			track.RecordingMBID
	}

	if track.ReleaseMBID != "" {
		item.TrackMetadata.AdditionalInfo["release_mbid"] =
			track.ReleaseMBID
	}

	if track.ArtistMBID != "" {
		item.TrackMetadata.AdditionalInfo["artist_mbid"] =
			track.ArtistMBID
	}

	return listenPayload{
		ListenType: listenType,
		Payload: []struct {
			ListenedAt    int64 `json:"listened_at,omitempty"`
			TrackMetadata struct {
				ArtistName     string         `json:"artist_name"`
				TrackName      string         `json:"track_name"`
				ReleaseName    string         `json:"release_name,omitempty"`
				AdditionalInfo map[string]any `json:"additional_info,omitempty"`
			} `json:"track_metadata"`
		}{
			item,
		},
	}
}

func (l *ListenBrainz) UpdateNowPlaying(
	ctx context.Context,
	track model.Track,
) error {
	payload := l.buildPayload(
		"playing_now",
		track,
		0,
	)

	// playing_now 不应该携带 listened_at。
	payload.Payload[0].ListenedAt = 0

	return l.post(ctx, payload)
}

func (l *ListenBrainz) Scrobble(
	ctx context.Context,
	track model.Track,
	startedAt int64,
) error {
	payload := l.buildPayload(
		"single",
		track,
		startedAt,
	)

	return l.post(ctx, payload)
}

func (l *ListenBrainz) post(
	ctx context.Context,
	payload listenPayload,
) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		listenBrainzEndpoint,
		bytes.NewReader(data),
	)
	if err != nil {
		return err
	}

	req.Header.Set(
		"Authorization",
		"Token "+l.Token,
	)

	req.Header.Set(
		"Content-Type",
		"application/json",
	)

	req.Header.Set(
		"User-Agent",
		userAgent(),
	)

	resp, err := l.Client.Do(req)
	if err != nil {
		return err
	}

	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf(
			"listenbrainz HTTP status: %s",
			resp.Status,
		)
	}

	return nil
}
