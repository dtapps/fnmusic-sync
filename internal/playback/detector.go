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

	// HLS 播放支持：此前只用 /track/stream 的 Content-Range 推算进度，客户端改用
	// HLS（/track/hls/<guid>/NNNNN.m4s）后就完全没有进度信号，导致“还在播放却不
	// 推送”。这里用播单（preset.m3u8）的分片时长 + 已见到的最大分片序号推算播放位置，
	// 把 HLS 也纳入识别（并作为“真实播放”的佐证）。
	hlsSegDur map[string]time.Duration
	hlsMaxSeg map[string]int

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
		hlsSegDur:  make(map[string]time.Duration),
		hlsMaxSeg:  make(map[string]int),
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
		d.logger.Debug("当前存在取流佐证的进行中播放，丢弃 track_play 候选", "候选数", len(list))

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

// HandleHLSPlaylist 解析 HLS 播单（preset.m3u8）得到分片时长，供 HLS 进度推算使用。
// 取第一条 #EXTINF 的时长即可（HLS 分片通常等长）。
func (d *Detector) HandleHLSPlaylist(path string, body []byte) {
	guid := hlsGUIDFromPath(path)
	if guid == "" {
		return
	}

	dur := parseHLSSegmentDuration(body)
	if dur <= 0 {
		return
	}

	d.mu.Lock()
	d.hlsSegDur[guid] = dur
	d.mu.Unlock()

	d.logger.Debug("HLS 播单分片时长", "歌曲", guid, "分片时长", dur.String())
}

// HandleHLSSegment 处理一个 HLS 分片请求（/track/hls/<guid>/NNNNN.m4s）：
//   - 作为“真实播放”的佐证（streamSeen），佐证 track_play 候选、开会话；
//   - 按“最大分片序号 × 分片时长”推算播放位置，驱动 scrobble。
//
// 说明：客户端会预缓冲，分片序号可能领先真实播放进度；但 Manager.CheckScrobble
// 会用“真实流逝时间”作为进度下界做钳制，因此这里偏高不会导致误推。
func (d *Detector) HandleHLSSegment(username, path string) {
	guid, seg, ok := parseHLSSegmentPath(path)
	if !ok {
		return
	}

	d.mu.Lock()

	if seg > d.hlsMaxSeg[guid] {
		d.hlsMaxSeg[guid] = seg
	}

	// HLS 分片 = 真实播放佐证（与 /track/stream 的 end>0 等价）。
	d.streamSeen[guid] = time.Now()

	track, hasTrack := d.tracks[guid]
	segDur := d.hlsSegDur[guid]
	maxSeg := d.hlsMaxSeg[guid]

	// 有 track_play 候选且元数据就绪 → HLS 佐证，开会话。
	if c, cand := d.candidates[guid]; cand && c.metaReady {
		candUser := c.user
		delete(d.candidates, guid)
		d.mu.Unlock()

		d.logger.Debug("HLS 分片佐证播放", "用户", username, "歌曲", guid)
		d.manager.Play(context.Background(), candUser, track, true)
		d.hlsCheckScrobble(guid, maxSeg, segDur, track)

		return
	}

	d.mu.Unlock()

	if hasTrack {
		d.hlsCheckScrobble(guid, maxSeg, segDur, track)
	}
}

// hlsCheckScrobble 由 HLS 分片序号推算播放位置并驱动 scrobble。
// 分片时长未知（未抓到播单）时不做进度推算，交给 Manager 的时长兜底定时器。
func (d *Detector) hlsCheckScrobble(guid string, maxSeg int, segDur time.Duration, track model.Track) {
	if segDur <= 0 || !track.Valid() {
		return
	}

	position := time.Duration(maxSeg+1) * segDur
	if track.Duration > 0 && position > track.Duration {
		position = track.Duration
	}

	d.logger.Debug(
		"HLS 进度推算",
		"歌曲", guid,
		"分片序号", maxSeg,
		"分片时长", segDur.String(),
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
		delete(d.hlsSegDur, g)
		delete(d.hlsMaxSeg, g)
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

// hlsGUIDFromPath 从 HLS 路径中取出曲目 guid：/track/hls/<guid>/... → <guid>。
func hlsGUIDFromPath(p string) string {
	const marker = "/track/hls/"

	_, after, ok := strings.Cut(p, marker)
	if !ok {
		return ""
	}

	rest := after
	guid, _, _ := strings.Cut(rest, "/")

	return guid
}

// parseHLSSegmentPath 解析 HLS 分片路径 /track/hls/<guid>/<NNNNN>.m4s，
// 返回 guid 与分片序号。
func parseHLSSegmentPath(p string) (guid string, seg int, ok bool) {
	guid = hlsGUIDFromPath(p)
	if guid == "" {
		return "", 0, false
	}

	name := p[strings.LastIndex(p, "/")+1:]
	if !strings.HasSuffix(name, ".m4s") {
		return "", 0, false
	}

	v, err := strconv.Atoi(strings.TrimSuffix(name, ".m4s"))
	if err != nil || v < 0 {
		return "", 0, false
	}

	return guid, v, true
}

// parseHLSSegmentDuration 从 HLS 播单文本里取第一条 #EXTINF 的时长（秒）。
func parseHLSSegmentDuration(body []byte) time.Duration {
	for line := range strings.SplitSeq(string(body), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "#EXTINF:") {
			continue
		}

		v := strings.TrimPrefix(line, "#EXTINF:")
		if c := strings.IndexByte(v, ','); c >= 0 {
			v = v[:c]
		}

		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil || f <= 0 {
			continue
		}

		return time.Duration(f * float64(time.Second))
	}

	return 0
}
