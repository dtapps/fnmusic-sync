package playlist

import (
	"context"
	"fmt"

	"cnb.cool/dtapp/fnmusic-sync/internal/buildinfo"
	"cnb.cool/dtapp/fnmusic-sync/internal/config"
	"cnb.cool/dtapp/fnmusic-sync/internal/feiniu"
	"cnb.cool/dtapp/fnmusic-sync/internal/lastfm"
	"cnb.cool/dtapp/fnmusic-sync/internal/strutil"
)

// syncLastFMUserTokens
func (s *SyncService) syncLastFMUserTokens(ctx context.Context, tokens []string, username string, lfm config.UserLastFM) error {
	var lastErr error
	for i, token := range tokens {
		err := s.syncLastFMUser(ctx, token, username, lfm)
		if err == nil {
			return nil
		}
		lastErr = err
		if !isInvalidTokenError(err) {
			return err
		}
		s.dropInvalidToken(username, token)
		if i < len(tokens)-1 {
			s.logger.Warn("token 失效，改用该用户的其他 token 重试", "用户", username, "用户标识", strutil.FirstN8(token))
		}
	}
	return lastErr
}

// syncLastFMUser 同步单个用户的 Last.fm 智能歌单（Top Tracks / Loved Tracks / Recent Tracks）。
// token 参数必须是当前有效的用户认证 token（用于飞牛音乐 API）。
// Last.fm 读取类 API 只需 api_key，不需要 session_key。
func (s *SyncService) syncLastFMUser(ctx context.Context, token string, username string, lfm config.UserLastFM) error {
	playlistCfg := lfm.Playlist
	if !playlistCfg.TopTracks.Enabled && !playlistCfg.LovedTracks.Enabled && !playlistCfg.RecentTracks.Enabled {
		return nil
	}
	s.logger.Info("开始 Last.fm 智能歌单同步", "用户", username, "用户标识", strutil.FirstN8(token), "lastfm用户", lfm.Username)
	apiClient := feiniu.NewClient(buildinfo.DefaultUpstreamSocket, token, s.logger, s.feiniuReqLog)
	s.logger.Info("正在获取飞牛音乐曲目列表", "用户", username, "用户标识", strutil.FirstN8(token))
	indexes, err := apiClient.BuildTrackIndexes(ctx)
	if err != nil {
		if isInvalidTokenError(err) {
			s.dropInvalidToken(username, token)
		}
		return fmt.Errorf("获取飞牛音乐曲目列表失败（用户标识=%s）: %w", strutil.FirstN8(token), err)
	}
	s.rememberToken(username, token)
	s.logger.Info("飞牛音乐曲目索引建立完成", "用户", username, "总曲目数", len(indexes.GUIDToTrack), "艺人+曲名索引数", len(indexes.ArtistTrackToGUID), "唯一曲名索引数", len(indexes.TitleToGUID))
	lfmClient := lastfm.NewClient(lfm.APIKey, lfm.APISecret, lfm.SessionKey, s.lfReqLog)
	// 同步 Top Tracks
	if playlistCfg.TopTracks.Enabled {
		period := playlistCfg.TopTracks.Period
		if period == "" {
			period = "overall"
		}
		displayPeriod := periodToChinese(period)
		name := resolvePlaylistName(playlistCfg.TopTracks.Name, "LF 最常听", map[string]string{"period": displayPeriod})
		s.logger.Info("正在获取 Last.fm Top Tracks", "用户", username, "lastfm用户", lfm.Username, "统计周期", period, "数量限制", playlistCfg.TopTracks.Limit)
		playlist, err := lfmClient.GetTopTracks(ctx, lfm.Username, period, playlistCfg.TopTracks.Limit)
		if err != nil {
			s.logger.Error("获取 Last.fm Top Tracks 失败", "用户", username, "错误", err)
		} else if playlist != nil {
			s.logger.Info("Last.fm Top Tracks 获取完成", "用户", username, "曲目数", len(playlist.TrackList), "歌单标题", playlist.Title)
			if err := s.syncLastFMPlaylist(ctx, apiClient, username, playlist, name, indexes); err != nil {
				s.logger.Error("同步 Last.fm Top Tracks 歌单失败", "用户", username, "错误", err)
			}
		}
	}
	// 同步 Loved Tracks
	if playlistCfg.LovedTracks.Enabled {
		name := lastFMPlaylistName(playlistCfg.LovedTracks, "LF 红心收藏")
		s.logger.Info("正在获取 Last.fm Loved Tracks", "用户", username, "lastfm用户", lfm.Username, "数量限制", playlistCfg.LovedTracks.Limit)
		playlist, err := lfmClient.GetLovedTracks(ctx, lfm.Username, playlistCfg.LovedTracks.Limit)
		if err != nil {
			s.logger.Error("获取 Last.fm Loved Tracks 失败", "用户", username, "错误", err)
		} else if playlist != nil {
			s.logger.Info("Last.fm Loved Tracks 获取完成", "用户", username, "曲目数", len(playlist.TrackList), "歌单标题", playlist.Title)
			if err := s.syncLastFMPlaylist(ctx, apiClient, username, playlist, name, indexes); err != nil {
				s.logger.Error("同步 Last.fm Loved Tracks 歌单失败", "用户", username, "错误", err)
			}
		}
	}
	// 同步 Recent Tracks
	if playlistCfg.RecentTracks.Enabled {
		name := lastFMPlaylistName(playlistCfg.RecentTracks, "LF 最近播放")
		s.logger.Info("正在获取 Last.fm Recent Tracks", "用户", username, "lastfm用户", lfm.Username, "数量限制", playlistCfg.RecentTracks.Limit)
		playlist, err := lfmClient.GetRecentTracks(ctx, lfm.Username, playlistCfg.RecentTracks.Limit)
		if err != nil {
			s.logger.Error("获取 Last.fm Recent Tracks 失败", "用户", username, "错误", err)
		} else if playlist != nil {
			s.logger.Info("Last.fm Recent Tracks 获取完成", "用户", username, "曲目数", len(playlist.TrackList), "歌单标题", playlist.Title)
			if err := s.syncLastFMPlaylist(ctx, apiClient, username, playlist, name, indexes); err != nil {
				s.logger.Error("同步 Last.fm Recent Tracks 歌单失败", "用户", username, "错误", err)
			}
		}
	}
	s.logger.Info("Last.fm 智能歌单同步完成", "用户", username, "lastfm用户", lfm.Username, "top_tracks启用", playlistCfg.TopTracks.Enabled, "loved_tracks启用", playlistCfg.LovedTracks.Enabled, "recent_tracks启用", playlistCfg.RecentTracks.Enabled)
	return nil
}

// syncLastFMPlaylist 将 Last.fm 智能歌单同步到飞牛音乐。
// 复用与 ListenBrainz 歌单相同的匹配逻辑（matchTrack）。
func (s *SyncService) syncLastFMPlaylist(ctx context.Context, apiClient *feiniu.Client, username string, playlist *lastfm.Playlist, playlistName string, indexes *feiniu.TrackIndexes) error {
	s.logger.Info("开始同步 Last.fm 歌单到飞牛音乐", "用户", username, "歌单名称", playlistName, "歌单曲目数", len(playlist.TrackList))
	trackGUIDs := make([]string, 0, len(playlist.TrackList))
	seen := make(map[string]struct{}, len(playlist.TrackList))
	var coverId string
	matched := 0
	mbidMatched := 0
	nameMatched := 0
	titleMatched := 0
	missed := make([]string, 0, unmatchedSampleSize)
	for _, track := range playlist.TrackList {
		guid, kind := matchTrack(indexes, track.ArtistName, track.TrackName, track.MBID)
		switch kind {
		case matchMBID:
			mbidMatched++
		case matchArtistTitle:
			nameMatched++
		case matchTitleOnly:
			titleMatched++
		default:
			if len(missed) < unmatchedSampleSize {
				missed = append(missed, track.ArtistName+" - "+track.TrackName)
			}
			continue
		}
		if _, dup := seen[guid]; dup {
			continue
		}
		seen[guid] = struct{}{}
		trackGUIDs = append(trackGUIDs, guid)
		matched++
		if coverId == "" {
			if t, ok := indexes.GUIDToTrack[guid]; ok && t.CoverId != "" {
				coverId = t.CoverId
			}
		}
	}
	s.logger.Info("歌单曲目匹配完成", "用户", username, "歌单名称", playlistName, "歌单曲目数", len(playlist.TrackList), "匹配数", matched, "MBID匹配", mbidMatched, "艺人曲名匹配", nameMatched, "仅曲名匹配", titleMatched, "未匹配示例", missed)
	if len(trackGUIDs) == 0 {
		s.logger.Warn("没有匹配的曲目，跳过歌单创建", "歌单", playlistName)
		return nil
	}
	playlistGUID, err := apiClient.FindOrCreatePlaylist(ctx, playlistName, coverId)
	if err != nil {
		return fmt.Errorf("查找/创建歌单失败: %w", err)
	}
	newGUIDs := s.diffTracks(ctx, apiClient, username, playlistName, playlistGUID, trackGUIDs)
	if len(newGUIDs) == 0 {
		s.logger.Info("歌单内容无变化，跳过添加", "用户", username, "歌单名称", playlistName, "歌单曲目数", len(trackGUIDs))
		return nil
	}
	if err := apiClient.AddTracksToPlaylist(ctx, playlistGUID, newGUIDs); err != nil {
		return fmt.Errorf("添加曲目失败: %w", err)
	}
	s.logger.Info("歌单同步完成", "用户", username, "歌单名称", playlistName, "本次新增", len(newGUIDs), "歌单曲目数", len(trackGUIDs))
	return nil
}
