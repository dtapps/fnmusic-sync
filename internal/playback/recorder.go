package playback

import (
	"sync"
	"time"

	"cnb.cool/dtapp/fnmusic-sync/internal/db"
	"cnb.cool/dtapp/fnmusic-sync/internal/model"
)

// Recorder 把"用户在播什么"持久化到 playback_log 表，关联音乐用户名。
//
// 飞牛只上报 track_play（开始播放），没有 pause / stop / end 事件，
// 因此一首歌的"结束"无法实时获知。这里采用"下一首开始即上一首结束"的策略：
// 同一用户开始播放新歌时，把上一首的 ended_at 回填为本次开始时刻。
// 仍在进行（或进程退出前最后在听）的那首，ended_at 为 NULL，表示"未知/结束未明"。
type Recorder struct {
	db *db.Store
	mu sync.Mutex
	// last 记录每个用户最近一次写入的播放记录 id，用于回填 ended_at。
	last map[string]int64
}

// NewRecorder 创建播放记录器。db 为 nil 时所有方法静默失效。
func NewRecorder(store *db.Store) *Recorder {
	return &Recorder{db: store, last: make(map[string]int64)}
}

// OnPlay 在用户开始播放一首歌时调用：先回填上一首的结束时间，再写入本次播放。
// username 为空或曲目不合法时直接忽略。
func (r *Recorder) OnPlay(username string, track model.Track, startedAt time.Time) {
	if r == nil || r.db == nil || username == "" || !track.Valid() {
		return
	}

	r.mu.Lock()
	prevID := r.last[username]
	r.mu.Unlock()

	// 上一首结束于本次开始时刻（"下一首开始 = 上一首结束"）。
	if prevID > 0 {
		_ = r.db.ClosePlayback(prevID, startedAt.Format(time.RFC3339))
	}

	id, err := r.db.RecordPlayback(db.PlaybackRecord{
		Username:  username,
		GUID:      track.GUID,
		Title:     track.Title,
		Artist:    track.Artist,
		Album:     track.Album,
		Duration:  track.Duration,
		StartedAt: startedAt,
	})
	if err != nil {
		return
	}

	r.mu.Lock()
	r.last[username] = id
	r.mu.Unlock()
}

// CloseOpen 在进程退出时调用，把仍在进行的播放统一标记为在 endedAt 结束。
func (r *Recorder) CloseOpen(endedAt time.Time) {
	if r == nil || r.db == nil {
		return
	}
	_ = r.db.CloseOpenPlaybacks(endedAt.Format(time.RFC3339))
}
