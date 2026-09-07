package scrobbler

import (
	"context"

	"cnb.cool/dtapp/fnmusic-sync/internal/model"
)

type Scrobbler interface {
	Name() string

	UpdateNowPlaying(ctx context.Context, track model.Track) error

	Scrobble(ctx context.Context, track model.Track, startedAt int64) error
}

// Version 由 Makefile 通过 -ldflags 注入（fnmusic-sync/internal/scrobbler.Version）；
// 用于上报给 Last.fm / ListenBrainz 的 User-Agent，默认值 dev 用于本地未注入时。
var Version = "dev"

// userAgent 返回上报给第三方服务的 User-Agent（含版本号）。
func userAgent() string {
	return "cnb.cool/dtapp/fnmusic-sync/" + Version
}
