package playlist

import (
	"context"
	"fmt"
	"time"

	"cnb.cool/dtapp/fnmusic-sync/internal/buildinfo"
	"cnb.cool/dtapp/fnmusic-sync/internal/config"
	"cnb.cool/dtapp/fnmusic-sync/internal/feiniu"
	"cnb.cool/dtapp/fnmusic-sync/internal/scrobbler"
	"cnb.cool/dtapp/fnmusic-sync/internal/strutil"
)

// fetchRecommendations 获取推荐歌单，短时间内复用上次结果。
func (s *SyncService) fetchRecommendations(
	ctx context.Context,
	lbUsername string,
) (daily, weekly, exploration, yearDiscoveries, yearMissed *scrobbler.ListenBrainzPlaylist, err error) {
	s.stateMu.Lock()
	cached, ok := s.recommendations[lbUsername]
	s.stateMu.Unlock()

	if ok && time.Since(cached.fetchedAt) < recommendationCacheTTL {
		s.logger.Debug("复用缓存的 ListenBrainz 推荐歌单",
			"listenbrainz用户", lbUsername,
			"缓存时间", cached.fetchedAt.Format(time.RFC3339),
		)

		return cached.daily, cached.weekly, cached.exploration, cached.yearDiscoveries, cached.yearMissed, nil
	}

	lbClient := scrobbler.NewListenBrainzPlaylistClient(lbUsername, s.lbReqLog)

	daily, weekly, exploration, yearDiscoveries, yearMissed, err = lbClient.FetchTroiBotPlaylists(ctx)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}

	s.stateMu.Lock()
	s.recommendations[lbUsername] = recommendationCache{
		fetchedAt:       time.Now(),
		daily:           daily,
		weekly:          weekly,
		exploration:     exploration,
		yearDiscoveries: yearDiscoveries,
		yearMissed:      yearMissed,
	}
	s.stateMu.Unlock()

	return daily, weekly, exploration, yearDiscoveries, yearMissed, nil
}

// describePlaylist 生成歌单的可读摘要，便于在日志里确认同步的是哪一期。
func describePlaylist(p *scrobbler.ListenBrainzPlaylist) string {
	if p == nil {
		return "无"
	}

	return fmt.Sprintf("%s（%d 首，%s）", p.Title, len(p.TrackList), p.Date)
}

// syncUserTokens 用该用户的 token 依次尝试同步，直到成功或全部失效。
//
// 多客户端场景下同一个用户有多个 token：某个客户端退出登录导致 token 失效时，
// 自动换下一个 token 重试，保证"单个 token 失效不影响该用户的歌单同步"。
// 非凭证类错误（网络、接口异常）换 token 也不会更好，直接返回。
func (s *SyncService) syncUserTokens(
	ctx context.Context,
	tokens []string,
	username string,
	lb config.UserListenBrainz,
) syncResult {
	var last syncResult

	for i, token := range tokens {
		res := s.syncUser(ctx, token, username, lb)
		// 成功（或无可同步歌单）即终止尝试。
		if res.Err == nil {
			return res
		}

		last = res

		// 非凭证类错误换 token 不会更好，直接返回。
		if !isInvalidTokenError(res.Err) {
			return res
		}

		// 凭证失效：剔除后换该用户下一个客户端的 token 重试。
		s.dropInvalidToken(username, token)

		if i < len(tokens)-1 {
			s.logger.Warn("token 失效，改用该用户的其他 token 重试",
				"用户", username,
				"用户标识", strutil.FirstN8(token),
			)
		}
	}

	return last
}

// syncUser 同步单个用户的 ListenBrainz 推荐歌单。
// token 参数必须是当前有效的用户认证 token。
// 返回 syncResult：Err 为 nil 表示成功（或无可同步歌单），Playlists/Tracks 为本次同步的歌单数/新增曲目数。
func (s *SyncService) syncUser(
	ctx context.Context,
	token string,
	username string,
	lb config.UserListenBrainz,
) syncResult {
	playlistCfg := lb.Playlist

	// 检查是否有任何歌单需要同步
	if !playlistCfg.DailyJams.Enabled &&
		!playlistCfg.WeeklyJams.Enabled &&
		!playlistCfg.WeeklyExploration.Enabled &&
		!playlistCfg.YearDiscoveries.Enabled &&
		!playlistCfg.YearMissed.Enabled {
		return syncResult{Noop: true}
	}

	s.logger.Info("开始 ListenBrainz 歌单同步",
		"用户", username,
		"用户标识", strutil.FirstN8(token),
		"listenbrainz用户", lb.Username,
	)

	var res syncResult

	// 创建飞牛音乐 API 客户端（直接使用传入的 token）
	apiClient := feiniu.NewClient(buildinfo.DefaultUpstreamSocket, token, s.logger, s.feiniuReqLog)

	// 第一步：获取飞牛音乐曲目列表，建立索引
	s.logger.Info("正在获取飞牛音乐曲目列表",
		"用户", username,
		"用户标识", strutil.FirstN8(token),
	)
	indexes, err := apiClient.BuildTrackIndexes(ctx)
	if err != nil {
		// token 已失效（用户退出登录/服务端踢下线）时，上游返回 401 INVALID TOKEN。
		// 这类 token 必须剔除，否则每轮定时同步都会用它白跑一次并刷 ERROR。
		if isInvalidTokenError(err) {
			s.dropInvalidToken(username, token)
		}

		return syncResult{Err: fmt.Errorf("获取飞牛音乐曲目列表失败（用户标识=%s）: %w", strutil.FirstN8(token), err)}
	}

	// 该 token 可用，后续定时同步优先复用它。
	s.rememberToken(username, token)

	s.logger.Info("飞牛音乐曲目索引建立完成",
		"用户", username,
		"总曲目数", len(indexes.GUIDToTrack),
		"艺人+曲名索引数", len(indexes.ArtistTrackToGUID),
		"唯一曲名索引数", len(indexes.TitleToGUID),
	)

	// 第二步：获取 ListenBrainz 推荐歌单
	s.logger.Info("正在获取 ListenBrainz 推荐歌单...",
		"用户", username,
		"listenbrainz用户", lb.Username,
	)
	dailyJams, weeklyJams, weeklyExploration, yearDiscoveries, yearMissed, err := s.fetchRecommendations(ctx, lb.Username)
	if err != nil {
		return syncResult{Err: fmt.Errorf("获取 ListenBrainz 推荐歌单失败: %w", err)}
	}

	s.logger.Info("ListenBrainz 推荐歌单获取完成",
		"用户", username,
		"daily_jams存在", dailyJams != nil,
		"weekly_jams存在", weeklyJams != nil,
		"weekly_exploration存在", weeklyExploration != nil,
		"year_discoveries存在", yearDiscoveries != nil,
		"year_missed存在", yearMissed != nil,
		"daily_jams", describePlaylist(dailyJams),
		"weekly_jams", describePlaylist(weeklyJams),
		"weekly_exploration", describePlaylist(weeklyExploration),
		"year_discoveries", describePlaylist(yearDiscoveries),
		"year_missed", describePlaylist(yearMissed),
		"daily_jams启用", playlistCfg.DailyJams.Enabled,
		"weekly_jams启用", playlistCfg.WeeklyJams.Enabled,
		"weekly_exploration启用", playlistCfg.WeeklyExploration.Enabled,
		"year_discoveries启用", playlistCfg.YearDiscoveries.Enabled,
		"year_missed启用", playlistCfg.YearMissed.Enabled,
	)

	// 同步 daily_jams
	if playlistCfg.DailyJams.Enabled && dailyJams != nil {
		name := playlistName(playlistCfg.DailyJams, "LB 每日推荐")
		if added, err := s.syncPlaylist(ctx, apiClient, username, dailyJams, name, indexes); err != nil {
			s.logger.Error("同步 daily_jams 失败",
				"用户", username,
				"错误", err,
			)
		} else {
			res.Playlists++
			res.Tracks += added
		}
	}

	// 同步 weekly_jams
	if playlistCfg.WeeklyJams.Enabled && weeklyJams != nil {
		name := playlistName(playlistCfg.WeeklyJams, "LB 每周推荐")
		if added, err := s.syncPlaylist(ctx, apiClient, username, weeklyJams, name, indexes); err != nil {
			s.logger.Error("同步 weekly_jams 失败",
				"用户", username,
				"错误", err,
			)
		} else {
			res.Playlists++
			res.Tracks += added
		}
	}

	// 同步 weekly_exploration
	if playlistCfg.WeeklyExploration.Enabled && weeklyExploration != nil {
		name := playlistName(playlistCfg.WeeklyExploration, "LB 每周探索")
		if added, err := s.syncPlaylist(ctx, apiClient, username, weeklyExploration, name, indexes); err != nil {
			s.logger.Error("同步 weekly_exploration 失败",
				"用户", username,
				"错误", err,
			)
		} else {
			res.Playlists++
			res.Tracks += added
		}
	}

	// 同步 year_discoveries
	if playlistCfg.YearDiscoveries.Enabled && yearDiscoveries != nil {
		year := yearDiscoveries.YearFromSourcePatch()
		name := resolvePlaylistName(playlistCfg.YearDiscoveries.Name, "LB "+year+"年度发现", map[string]string{"year": year})
		if added, err := s.syncPlaylist(ctx, apiClient, username, yearDiscoveries, name, indexes); err != nil {
			s.logger.Error("同步 year_discoveries 失败",
				"用户", username,
				"错误", err,
			)
		} else {
			res.Playlists++
			res.Tracks += added
		}
	}

	// 同步 year_missed
	if playlistCfg.YearMissed.Enabled && yearMissed != nil {
		year := yearMissed.YearFromSourcePatch()
		name := resolvePlaylistName(playlistCfg.YearMissed.Name, "LB "+year+"年度遗珠", map[string]string{"year": year})
		if added, err := s.syncPlaylist(ctx, apiClient, username, yearMissed, name, indexes); err != nil {
			s.logger.Error("同步 year_missed 失败",
				"用户", username,
				"错误", err,
			)
		} else {
			res.Playlists++
			res.Tracks += added
		}
	}

	return res
}

// syncPlaylist 同步单个歌单到飞牛音乐。
// 支持三种匹配方式：
//  1. MBID 精确匹配（优先，飞牛曲库通常没有 MBID）
//  2. 艺人名+曲名匹配（主要方案）
//  3. 仅曲名匹配（应对艺人名繁简/译名差异，仅在曲库内曲名唯一时命中）
//
// 返回本次新增到歌单的曲目数，便于落库 sync_log。
func (s *SyncService) syncPlaylist(
	ctx context.Context,
	apiClient *feiniu.Client,
	username string,
	playlist *scrobbler.ListenBrainzPlaylist,
	playlistName string,
	indexes *feiniu.TrackIndexes,
) (int, error) {
	s.logger.Info("开始同步歌单到飞牛音乐",
		"用户", username,
		"歌单名称", playlistName,
		"歌单曲目数", len(playlist.TrackList),
	)

	trackGUIDs := make([]string, 0, len(playlist.TrackList))
	seen := make(map[string]struct{}, len(playlist.TrackList))
	var coverId string
	matched := 0
	mbidMatched := 0
	nameMatched := 0
	titleMatched := 0
	missed := make([]string, 0, unmatchedSampleSize)

	for _, track := range playlist.TrackList {
		guid, kind := matchTrack(indexes, track.ArtistName, track.TrackName, track.RecordingMBID)

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

		// 同一曲目可能在推荐歌单里重复出现，只添加一次。
		if _, dup := seen[guid]; dup {
			continue
		}
		seen[guid] = struct{}{}

		trackGUIDs = append(trackGUIDs, guid)
		matched++

		// 使用第一首歌的封面作为歌单封面
		if coverId == "" {
			if t, ok := indexes.GUIDToTrack[guid]; ok && t.CoverId != "" {
				coverId = t.CoverId
			}
		}
	}

	s.logger.Info("歌单曲目匹配完成",
		"用户", username,
		"歌单名称", playlistName,
		"歌单曲目数", len(playlist.TrackList),
		"匹配数", matched,
		"MBID匹配", mbidMatched,
		"艺人曲名匹配", nameMatched,
		"仅曲名匹配", titleMatched,
		"未匹配示例", missed,
	)

	if len(trackGUIDs) == 0 {
		s.logger.Warn("没有匹配的曲目，跳过歌单创建",
			"歌单", playlistName,
		)
		return 0, nil
	}

	// 查找或创建歌单
	playlistGUID, err := apiClient.FindOrCreatePlaylist(ctx, playlistName, coverId)
	if err != nil {
		return 0, fmt.Errorf("查找/创建歌单失败: %w", err)
	}

	// 只添加歌单里还没有的曲目：add-track 是追加语义，
	// 每轮全量追加会让歌单不断堆积重复曲目。
	newGUIDs := s.diffTracks(ctx, apiClient, username, playlistName, playlistGUID, trackGUIDs)
	if len(newGUIDs) == 0 {
		s.logger.Info("歌单内容无变化，跳过添加",
			"用户", username,
			"歌单名称", playlistName,
			"歌单曲目数", len(trackGUIDs),
		)

		return 0, nil
	}

	if err := apiClient.AddTracksToPlaylist(ctx, playlistGUID, newGUIDs); err != nil {
		return 0, fmt.Errorf("添加曲目失败: %w", err)
	}

	s.logger.Info("歌单同步完成",
		"用户", username,
		"歌单名称", playlistName,
		"本次新增", len(newGUIDs),
		"歌单曲目数", len(trackGUIDs),
	)

	return len(newGUIDs), nil
}
