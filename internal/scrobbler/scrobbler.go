// Package scrobbler 提供统一的 Scrobbler 接口定义。
//
// 实际的 API 客户端实现位于子包：
//   - internal/lastfm       Last.fm API 客户端
//   - internal/listenbrainz ListenBrainz API 客户端
//
// 本包为了向后兼容，保留了新客户端的包装类型。
// 推荐直接使用子包中的客户端类型。
package scrobbler

import (
	"context"

	"cnb.cool/dtapp/fnmusic-sync/internal/lastfm"
	"cnb.cool/dtapp/fnmusic-sync/internal/listenbrainz"
	"cnb.cool/dtapp/fnmusic-sync/internal/model"
	"cnb.cool/dtapp/fnmusic-sync/internal/reqlog"
)

// Scrobbler 定义推送平台的统一接口。
type Scrobbler interface {
	Name() string

	UpdateNowPlaying(ctx context.Context, track model.Track) error

	Scrobble(ctx context.Context, track model.Track, startedAt int64) error
}

// LastFM 是 lastfm.Client 的包装类型（向后兼容）。
type LastFM = lastfm.Client

// LastFMPlaylist 是 lastfm.Playlist 的包装类型（向后兼容）。
type LastFMPlaylist = lastfm.Playlist

// LastFMPlaylistTrack 是 lastfm.PlaylistTrack 的包装类型（向后兼容）。
type LastFMPlaylistTrack = lastfm.PlaylistTrack

// ListenBrainz 是 listenbrainz.Client 的包装类型（向后兼容）。
type ListenBrainz = listenbrainz.Client

// ListenBrainzPlaylistClient 是 listenbrainz.PlaylistClient 的包装类型（向后兼容）。
type ListenBrainzPlaylistClient = listenbrainz.PlaylistClient

// ListenBrainzPlaylist 是 listenbrainz.Playlist 的包装类型（向后兼容）。
type ListenBrainzPlaylist = listenbrainz.Playlist

// NewLastFM 创建 Last.fm 客户端（向后兼容包装）。
// reqLog 为请求日志记录器，nil 时跳过请求日志。
func NewLastFM(apiKey, apiSecret, sessionKey string, reqLog *reqlog.Logger) *lastfm.Client {
	return lastfm.NewClient(apiKey, apiSecret, sessionKey, reqLog)
}

// NewListenBrainz 创建 ListenBrainz 客户端（向后兼容包装）。
// reqLog 为请求日志记录器，nil 时跳过请求日志。
func NewListenBrainz(token, username string, reqLog *reqlog.Logger) *listenbrainz.Client {
	return listenbrainz.NewClient(token, username, reqLog)
}

// NewListenBrainzPlaylistClient 创建 ListenBrainz 歌单客户端（向后兼容包装）。
// reqLog 为请求日志记录器，nil 时跳过请求日志。
func NewListenBrainzPlaylistClient(username string, reqLog *reqlog.Logger) *listenbrainz.PlaylistClient {
	return listenbrainz.NewPlaylistClient(username, reqLog)
}
