package playback

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"cnb.cool/dtapp/fnmusic-sync/internal/model"
)

// Detector 从代理流量中识别"在播什么、听了多久"，驱动 Manager 做 scrobble。
//
// 数据来源（ai.md 第 19/20 节，已实机抓包确认）：
//  1. GET  /music/api/v1/track/metadata?guid=X → 曲目元数据（标题/艺人/专辑/时长）
//  2. POST /music/api/v1/event/report          → track_play 事件（开始播放）
//  3. GET  /music/api/v1/track/stream?guid=X   → 边播边下，用 Range 进度推算播放位置
type Detector struct {
	mu sync.Mutex

	// tracks 缓存当前播放曲目的元数据（换歌时只保留最新一首，避免无限增长）。
	tracks map[string]model.Track
	// stream 记录每首歌的取流进度（总大小 / 已下载到的最大偏移）。
	stream map[string]*streamState
	// pendingPlay 缓存“track_play 已到、但 metadata 尚未到达”的播放请求，
	// key=guid，value=发起播放的用户名；等 HandleMetadata 补发 Play。
	pendingPlay map[string]string

	manager *Manager
	logger  *slog.Logger
}

type streamState struct {
	total int64
	end   int64
}

func NewDetector(manager *Manager, logger *slog.Logger) *Detector {
	return &Detector{
		tracks:      make(map[string]model.Track),
		stream:      make(map[string]*streamState),
		pendingPlay: make(map[string]string),
		manager:     manager,
		logger:      logger,
	}
}

// HandleMetadata 解析 /track/metadata 的响应体并缓存曲目信息。
func (d *Detector) HandleMetadata(guid string, body []byte) {
	if guid == "" {
		return
	}

	track, ok := parseMetadata(guid, body)
	if !ok {
		d.logger.Debug("曲目元数据解析失败", "歌曲", guid)

		return
	}

	d.mu.Lock()
	d.tracks[guid] = track
	// 补发之前因 metadata 未到而暂存的 track_play 播放会话。
	pendingUser, pending := d.pendingPlay[guid]
	if pending {
		delete(d.pendingPlay, guid)
	}
	d.mu.Unlock()

	d.logger.Debug(
		"已缓存曲目",
		"歌曲", guid,
		"标题", track.Title,
		"艺人", track.Artist,
		"时长", track.Duration.String(),
	)

	if pending {
		d.logger.Debug(
			"补发暂存的播放会话",
			"用户", pendingUser,
			"歌曲", guid,
		)
		// 锁外调用，避免与 manager 内部锁形成嵌套/死锁。
		d.manager.Play(context.Background(), pendingUser, track)
	}
}

// HandleEventReport 解析 /event/report 的请求体，识别 track_play 开始播放。
func (d *Detector) HandleEventReport(username string, body []byte) {
	guid, ok := parsePlayEvent(body)
	if !ok {
		return
	}

	d.mu.Lock()
	track, has := d.tracks[guid]
	if has {
		// 换歌：只保留当前曲目，避免缓存无限增长。
		d.tracks = map[string]model.Track{guid: track}
		d.stream = map[string]*streamState{}
		delete(d.pendingPlay, guid)
	} else {
		// 元数据尚未到达（客户端常先发 track_play 再发 metadata，
		// 网页则相反），暂存用户名，等 metadata 到达后补发 Play。
		d.pendingPlay[guid] = username
	}
	d.mu.Unlock()

	if !has {
		d.logger.Debug(
			"播放事件缺少曲目元数据，暂存等待",
			"用户", username,
			"歌曲", guid,
		)

		return
	}

	if username == "" {
		d.logger.Debug(
			"播放事件尚未识别出用户，跳过",
			"歌曲", guid,
		)

		return
	}

	// 用 Background：scrobble 不应随单个请求结束而取消，
	// 超时由各 scrobbler 自带的 http.Client.Timeout 兜底。
	d.manager.Play(context.Background(), username, track)
}

// HandleStreamProgress 用取流的 Content-Range 推算已播放位置，达标则 scrobble。
//
// 飞牛是边播边下（每块 Range 约 1MB），已下载到的最大偏移≈播放进度。
func (d *Detector) HandleStreamProgress(
	username string,
	guid string,
	contentRange string,
) {
	if guid == "" || username == "" {
		return
	}

	end, total, ok := parseContentRange(contentRange)
	if !ok {
		return
	}

	d.mu.Lock()

	st, exists := d.stream[guid]
	if !exists {
		st = &streamState{}
		d.stream[guid] = st
	}

	if total > 0 {
		st.total = total
	}

	if end > st.end {
		st.end = end
	}

	track, hasTrack := d.tracks[guid]
	curTotal, curEnd := st.total, st.end

	d.mu.Unlock()

	if !hasTrack || curTotal <= 0 || track.Duration <= 0 {
		return
	}

	// 播放位置 = 已下载到的最大偏移 / 总大小 × 时长
	position := time.Duration(
		float64(curEnd) / float64(curTotal) * float64(track.Duration),
	)

	d.logger.Debug(
		"取流进度推算",
		"用户", username,
		"歌曲", guid,
		"已下载", curEnd,
		"总大小", curTotal,
		"播放位置", position.String(),
		"时长", track.Duration.String(),
	)

	d.manager.CheckScrobble(context.Background(), position)
}

// parseMetadata 解析 /track/metadata 响应，duration 单位为毫秒。
func parseMetadata(guid string, body []byte) (model.Track, bool) {
	var resp struct {
		Data struct {
			Track struct {
				Title   string `json:"title"`
				Artists []struct {
					Name string `json:"name"`
				} `json:"artists"`
				Album struct {
					Name string `json:"name"`
				} `json:"album"`
				Duration int64 `json:"duration"`
				TrackNo  int   `json:"trackNo"`
			} `json:"track"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &resp); err != nil {
		return model.Track{}, false
	}

	t := resp.Data.Track

	track := model.Track{
		GUID:        guid,
		Title:       t.Title,
		Album:       t.Album.Name,
		Duration:    time.Duration(t.Duration) * time.Millisecond,
		TrackNumber: t.TrackNo,
	}

	names := make([]string, 0, len(t.Artists))
	for _, a := range t.Artists {
		if a.Name != "" {
			names = append(names, a.Name)
		}
	}

	track.Artist = strings.Join(names, ", ")

	if !track.Valid() {
		return model.Track{}, false
	}

	return track, true
}

// parsePlayEvent 从 /event/report 请求体中取出 track_play 的 trackGUID。
func parsePlayEvent(body []byte) (string, bool) {
	var report struct {
		Events []struct {
			EventType string `json:"eventType"`
			Payload   struct {
				TrackGUID string `json:"trackGUID"`
			} `json:"payload"`
		} `json:"events"`
	}

	if err := json.Unmarshal(body, &report); err != nil {
		return "", false
	}

	for _, e := range report.Events {
		if e.EventType == "track_play" && e.Payload.TrackGUID != "" {
			return e.Payload.TrackGUID, true
		}
	}

	return "", false
}

// parseContentRange 解析 "bytes 0-1048575/30274485" 形式的 Content-Range，
// 返回结束偏移与资源总大小。
func parseContentRange(v string) (end, total int64, ok bool) {
	const prefix = "bytes "

	if !strings.HasPrefix(v, prefix) {
		return 0, 0, false
	}

	rest := strings.TrimPrefix(v, prefix)

	before, after, ok := strings.Cut(rest, "/")
	if !ok {
		return 0, 0, false
	}

	rangePart, totalPart := before, after

	_, after0, ok0 := strings.Cut(rangePart, "-")
	if !ok0 {
		return 0, 0, false
	}

	end, err := strconv.ParseInt(strings.TrimSpace(after0), 10, 64)
	if err != nil {
		return 0, 0, false
	}

	// 总大小未知（bytes 0-99/*）时无法推算播放位置。
	if strings.TrimSpace(totalPart) == "*" {
		return 0, 0, false
	}

	total, err = strconv.ParseInt(strings.TrimSpace(totalPart), 10, 64)
	if err != nil {
		return 0, 0, false
	}

	return end, total, true
}
