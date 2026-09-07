package scrobbler

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"cnb.cool/dtapp/fnmusic-sync/internal/model"
)

const lastFMEndpoint = "https://ws.audioscrobbler.com/2.0/"

type LastFM struct {
	APIKey     string
	APISecret  string
	SessionKey string

	Client *http.Client
}

func NewLastFM(
	apiKey string,
	apiSecret string,
	sessionKey string,
) *LastFM {
	return &LastFM{
		APIKey:     apiKey,
		APISecret:  apiSecret,
		SessionKey: sessionKey,
		Client: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func (l *LastFM) Name() string {
	return "lastfm"
}

func (l *LastFM) UpdateNowPlaying(
	ctx context.Context,
	track model.Track,
) error {
	params := url.Values{}

	params.Set("method", "track.updateNowPlaying")
	params.Set("api_key", l.APIKey)
	params.Set("sk", l.SessionKey)

	params.Set("artist", track.Artist)
	params.Set("track", track.Title)

	if track.Album != "" {
		params.Set("album", track.Album)
	}

	if track.AlbumArtist != "" {
		params.Set("albumArtist", track.AlbumArtist)
	}

	if track.Duration > 0 {
		params.Set(
			"duration",
			strconv.FormatInt(int64(track.Duration.Seconds()), 10),
		)
	}

	if track.MBID != "" {
		params.Set("mbid", track.MBID)
	}

	params.Set("api_sig", l.signature(params))
	params.Set("format", "json")

	return l.post(ctx, params)
}

func (l *LastFM) Scrobble(
	ctx context.Context,
	track model.Track,
	startedAt int64,
) error {
	params := url.Values{}

	params.Set("method", "track.scrobble")
	params.Set("api_key", l.APIKey)
	params.Set("sk", l.SessionKey)

	params.Set("artist[0]", track.Artist)
	params.Set("track[0]", track.Title)

	params.Set(
		"timestamp[0]",
		strconv.FormatInt(startedAt, 10),
	)

	if track.Album != "" {
		params.Set("album[0]", track.Album)
	}

	if track.AlbumArtist != "" {
		params.Set("albumArtist[0]", track.AlbumArtist)
	}

	if track.MBID != "" {
		params.Set("mbid[0]", track.MBID)
	}

	if track.Duration > 0 {
		params.Set(
			"duration[0]",
			strconv.FormatInt(int64(track.Duration.Seconds()), 10),
		)
	}

	params.Set("api_sig", l.signature(params))
	params.Set("format", "json")

	return l.post(ctx, params)
}

func (l *LastFM) signature(params url.Values) string {
	keys := make([]string, 0, len(params))

	for key := range params {
		if key == "format" || key == "callback" {
			continue
		}

		keys = append(keys, key)
	}

	sort.Strings(keys)

	var b strings.Builder

	for _, key := range keys {
		b.WriteString(key)
		b.WriteString(params.Get(key))
	}

	b.WriteString(l.APISecret)

	sum := md5.Sum([]byte(b.String()))

	return hex.EncodeToString(sum[:])
}

func (l *LastFM) post(
	ctx context.Context,
	params url.Values,
) error {
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		lastFMEndpoint,
		strings.NewReader(params.Encode()),
	)
	if err != nil {
		return err
	}

	req.Header.Set(
		"Content-Type",
		"application/x-www-form-urlencoded",
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

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf(
			"last.fm HTTP status: %s",
			resp.Status,
		)
	}

	var result struct {
		Error   int    `json:"error"`
		Message string `json:"message"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}

	if result.Error != 0 {
		return fmt.Errorf(
			"last.fm error %d: %s",
			result.Error,
			result.Message,
		)
	}

	return nil
}
