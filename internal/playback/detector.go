package playback

import (
	"context"
	"encoding/json"
	"log/slog"
	"sort"
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
//  2. POST /music/api/v1/event/report          → track_play 事件（开始播放的“候选”信号）
//  3. GET  /music/api/v1/track/stream?guid=X   → 边播边下，用 Range 进度推算播放位置
//
// 关键修正（修复第三方客户端“缓存一大堆歌”导致记录错乱）：
//
//	track_play 只是“候选”——客户端（尤其网页端）会在打开歌单 / 预加载下一首时
//	抢发 track_play，若直接当作“开始播放”会产生幽灵会话、甚至把没听的歌 scrobble 出去。
//	因此只有“真实取流”（下载偏移 end>0，排除 bytes=0-0 探针）佐证后，或音频直连 CDN
//	无取流可参考时超时回退，才真正开会话。这样既根治错记，又对正常客户端（官方 App /
//	走代理取流）行为不变、对音频直连 CDN 的客户端保持旧行为（不漏记）。
type Detector struct {
	mu sync.Mutex

	// tracks 缓存近期曲目元数据（不再盲目清空，避免真实播放的元数据被“换歌复位”冲掉）。
	tracks map[string]model.Track
	// stream 记录每首歌的取流进度（总大小 / 已下载到的最大偏移）。
	stream map[string]*streamState

	// candidates：已收到 track_play、但尚未被“真实取流”佐证的候选播放。
	// 只有出现真实取流（end>0）或超时回退，才真正开会话。
	candidates map[string]*candidate
	// streamSeen：最近一次“真实取流”（end>0）的时间，用于“取流先于 track_play”的佐证。
	streamSeen map[string]time.Time

	// stop/done 控制后台超时回退 goroutine 的生命周期（Proxy.Close 时退出）。
	stop chan struct{}
	done chan struct{}

	manager *Manager
	logger  *slog.Logger
}

type streamState struct {
	total int64
	end   int64
}

// candidate 表示一个尚未被真实取流佐证的 track_play 候选播放。
type candidate struct {
	user      string
	at        time.Time
	metaReady bool
}

const (
	// candidateConfirmTimeout 候选在无真实取流佐证时，超时回退开会话的等待时长。
	// 需小于 scrobble 阈值，避免“已开始播放”的计时被严重延迟；同时给走代理的
	// 客户端足够时间发起真实取流（通常 track_play 后 1~3s 内）。
	candidateConfirmTimeout = 3 * time.Second
	// streamSeenWindow 取流记录的有效佐证窗口（“取流先于 track_play”时仍可匹配）。
	streamSeenWindow = 30 * time.Second
	// maxTrackCache 元数据缓存上限，超出时清理无候选 / 无取流的条目，避免无限增长。
	maxTrackCache = 400
)

func NewDetector(manager *Manager, logger *slog.Logger) *Detector {
	d := &Detector{
		tracks:     make(map[string]model.Track),
		stream:     make(map[string]*streamState),
		candidates: make(map[string]*candidate),
		streamSeen: make(map[string]time.Time),
		stop:       make(chan struct{}),
		done:       make(chan struct{}),
		manager:    manager,
		logger:     logger,
	}

	go d.expireLoop()

	return d
}

// Stop 停止后台超时回退 goroutine，应在 Proxy 关闭时调用。
func (d *Detector) Stop() {
	close(d.stop)
	<-d.done
}

// expireLoop 周期性扫描候选，对超时且无真实取流佐证的候选做回退处理。
func (d *Detector) expireLoop() {
	defer close(d.done)

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-d.stop:
			return
		case <-ticker.C:
			d.expireCandidates()
		}
	}
}

// expireCandidates 处理超时的候选：
//   - 若当前播放已被真实取流锁定，所有预加载候选直接丢弃（不覆盖真听）；
//   - 否则（音频直连 CDN 无取流可参考）在同时到期的候选中只开"最近到达(at 最大)"
//     的那首、其余丢弃。歌单预加载会抢发一堆 track_play，用户实际在听 / 最后点击的
//     就是最后到达的那一个，避免逐个预加载歌都生成幽灵会话。
func (d *Detector) expireCandidates() {
	d.mu.Lock()

	now := time.Now()

	type expired struct {
		guid string
		at   time.Time
	}
	var list []expired
	for guid, c := range d.candidates {
		if c.metaReady && now.Sub(c.at) >= candidateConfirmTimeout {
			list = append(list, expired{guid, c.at})
		}
	}

	d.mu.Unlock()

	if len(list) == 0 {
		return
	}

	// 已被真实取流锁定的当前播放：预加载 / 下一首预热的候选直接丢弃，不覆盖真听。
	if d.manager.CurrentConfirmed() {
		d.mu.Lock()
		for _, e := range list {
			delete(d.candidates, e.guid)
		}
		d.mu.Unlock()

		return
	}

	// 同一时刻到期的候选里，按到达时间降序，只开"最近"的那首。
	sort.Slice(list, func(i, j int) bool {
		return list[i].at.After(list[j].at)
	})

	d.mu.Lock()

	var chosenTrack model.Track
	var chosenUser string
	hasChosen := false

	for _, e := range list {
		track, ok := d.tracks[e.guid]
		user := ""
		if c, exists := d.candidates[e.guid]; exists {
			user = c.user
		}
		delete(d.candidates, e.guid)

		// 从最近往旧找第一个元数据就绪的作为当前播放，其余候选一并丢弃。
		if !hasChosen && ok {
			chosenTrack = track
			chosenUser = user
			hasChosen = true
		}
	}

	d.mu.Unlock()

	if hasChosen {
		d.manager.Play(context.Background(), chosenUser, chosenTrack, false)
	}
}

// HandleMetadata 解析 /track/metadata 的响应体并缓存曲目信息。
func (d *Detector) HandleMetadata(guid string, body []byte) {
	if guid == "" {
		return
	}

	track, ok := parseMetadata(guid, body)
	if !ok {
		d.logger.Error("曲目元数据解析失败", "歌曲", guid)

		return
	}

	d.mu.Lock()
	d.tracks[guid] = track
	if c, exists := d.candidates[guid]; exists {
		c.metaReady = true
	}
	d.trimTrackCache()
	d.mu.Unlock()

	d.logger.Debug(
		"已缓存曲目",
		"歌曲", guid,
		"标题", track.Title,
		"艺人", track.Artist,
		"时长", track.Duration.String(),
	)

	d.maybeConfirm(guid)
}

// HandleEventReport 解析 /event/report 的请求体，识别 track_play 开始播放的“候选”信号。
//
// 注意：track_play 不再立即开会话，仅登记候选；真正开会话要等真实取流佐证或超时回退，
// 以排除客户端预加载 / 下一首预热抢发的 track_play（详见包注释）。
func (d *Detector) HandleEventReport(username string, body []byte) {
	guid, ok := parsePlayEvent(body)
	if !ok {
		return
	}

	d.mu.Lock()

	// 不再清空 d.tracks / d.stream：盲目“换歌复位”会把真实播放所需的元数据冲掉，
	// 导致第三方客户端预加载时真实播放的会话建不起来或记成别的歌。
	c := &candidate{user: username, at: time.Now()}
	if _, has := d.tracks[guid]; has {
		c.metaReady = true
	}
	d.candidates[guid] = c

	d.mu.Unlock()

	d.logger.Debug(
		"收到播放事件(track_play)，登记候选",
		"用户", username,
		"歌曲", guid,
	)

	d.maybeConfirm(guid)
}

// maybeConfirm 尝试把候选 guid 推进为真实播放会话：
// 若该 guid 近期出现过真实取流（end>0），说明真在听 → 立即开会话（已佐证）。
// 否则保持候选，等待取流佐证或后台超时回退。
func (d *Detector) maybeConfirm(guid string) {
	d.mu.Lock()

	c, ok := d.candidates[guid]
	if !ok || !c.metaReady {
		d.mu.Unlock()

		return
	}

	track, hasTrack := d.tracks[guid]
	seen, recently := d.streamSeen[guid]
	confirmedByStream := hasTrack && recently && time.Since(seen) < streamSeenWindow

	d.mu.Unlock()

	if !confirmedByStream {
		// 尚无真实取流佐证，等取流或超时回退。
		return
	}

	d.manager.Play(context.Background(), c.user, track, true)

	d.mu.Lock()
	delete(d.candidates, guid)
	d.mu.Unlock()
}

// HandleStreamProgress 用取流的 Content-Range 推算已播放位置，达标则 scrobble。
//
// 飞牛是边播边下（每块 Range 约 1MB），已下载到的最大偏移≈播放进度。
// 当该 guid 存在 track_play 候选且出现“真实取流”（end>0），则视为取流佐证、开会话。
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

	// 真实取流：下载到偏移 end>0（排除 bytes=0-0 探针）。
	if end > 0 {
		d.streamSeen[guid] = time.Now()

		// 该 guid 有 track_play 候选且元数据已就绪 → 取流佐证，开会话。
		if c, cand := d.candidates[guid]; cand && c.metaReady {
			candUser := c.user
			candTrack := track
			delete(d.candidates, guid)
			d.mu.Unlock()

			d.logger.Debug(
				"真实取流佐证播放",
				"用户", username,
				"歌曲", guid,
			)
			d.manager.Play(context.Background(), candUser, candTrack, true)

			if !hasTrack || curTotal <= 0 || candTrack.Duration <= 0 {
				return
			}

			position := time.Duration(
				float64(curEnd) / float64(curTotal) * float64(candTrack.Duration),
			)

			d.logger.Debug(
				"取流进度推算",
				"用户", username,
				"歌曲", guid,
				"已下载", curEnd,
				"总大小", curTotal,
				"播放位置", position.String(),
				"时长", candTrack.Duration.String(),
			)
			d.manager.CheckScrobble(context.Background(), position)

			return
		}
	}

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

// trimTrackCache 在缓存超限时清理无候选、且无近期真实取流的条目，避免无限增长。
func (d *Detector) trimTrackCache() {
	if len(d.tracks) <= maxTrackCache {
		return
	}

	for g := range d.tracks {
		if _, cand := d.candidates[g]; cand {
			continue
		}

		if _, seen := d.streamSeen[g]; seen {
			continue
		}

		delete(d.tracks, g)
	}
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
