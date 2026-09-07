package playback

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"cnb.cool/dtapp/fnmusic-sync/internal/model"
	"cnb.cool/dtapp/fnmusic-sync/internal/scrobbler"
)

type Session struct {
	Track model.Track

	Username  string
	StartedAt time.Time

	Scrobbled bool
}

type Manager struct {
	mu sync.Mutex

	current *Session

	// provMu 保护 providersByUser，支持配置热更新时安全替换（与 mu 独立，避免死锁）。
	provMu sync.RWMutex
	// providersByUser 按用户名映射各自的推送平台实例（多用户隔离）。
	providersByUser map[string][]scrobbler.Scrobbler

	// thresholdFunc 由配置 scrobble_threshold 决定（auto / 固定时长 / 百分比），
	// 在 CheckScrobble 内用歌曲时长求出本次触发阈值。
	thresholdFunc func(time.Duration) time.Duration

	// noThreshold 为 true 时关闭阈值判断，每次播放即推送（scrobble_threshold=off）。
	noThreshold bool

	onScrobbled func(username, provider string)

	logger *slog.Logger
}

func NewManager(
	providersByUser map[string][]scrobbler.Scrobbler,
	logger *slog.Logger,
) *Manager {
	if providersByUser == nil {
		providersByUser = map[string][]scrobbler.Scrobbler{}
	}

	return &Manager{
		providersByUser: providersByUser,
		thresholdFunc:   defaultScrobbleThreshold,
		logger:          logger,
	}
}

// providersFor 返回某用户配置的推送平台；用户未在配置中或没有启用任何平台时返回 nil。
func (m *Manager) providersFor(username string) []scrobbler.Scrobbler {
	if username == "" {
		return nil
	}

	m.provMu.RLock()
	defer m.provMu.RUnlock()

	return m.providersByUser[username]
}

// UpdateProviders 热更新各用户的推送平台实例（配置变更后调用，无需重启）。
func (m *Manager) UpdateProviders(providersByUser map[string][]scrobbler.Scrobbler) {
	if providersByUser == nil {
		providersByUser = map[string][]scrobbler.Scrobbler{}
	}

	m.provMu.Lock()
	defer m.provMu.Unlock()

	m.providersByUser = providersByUser
}

// SetScrobbledHook 注册一个回调：每当某首歌成功推送到某个平台后触发，
// 用于持久化用户统计（如推送次数）。username 为空时不会被调用。
func (m *Manager) SetScrobbledHook(f func(username, provider string)) {
	m.onScrobbled = f
}

func (m *Manager) Play(
	ctx context.Context,
	username string,
	track model.Track,
) {
	if !track.Valid() {
		return
	}

	m.mu.Lock()

	now := time.Now()

	// 同一首歌重复收到播放请求，不重新创建 session。
	if m.current != nil &&
		m.current.Track.GUID == track.GUID &&
		!m.current.Scrobbled {
		m.mu.Unlock()
		return
	}

	noThreshold := m.noThreshold

	m.current = &Session{
		Track:     track,
		Username:  username,
		StartedAt: now,
	}

	m.mu.Unlock()

	if noThreshold {
		// 关闭阈值判断：每次播放即推送（不判断进度）。
		m.CheckScrobble(context.Background(), track.Duration)
	}

	m.logger.Info(
		"播放会话开始",
		"标题", track.Title,
		"艺人", track.Artist,
	)

	for _, provider := range m.providersFor(username) {
		p := provider

		go func() {
			if err := p.UpdateNowPlaying(
				ctx,
				track,
			); err != nil {
				m.logger.Error(
					"正在播放推送失败",
					"平台", p.Name(),
					"错误", err,
				)
			}
		}()
	}
}

func (m *Manager) CheckScrobble(
	ctx context.Context,
	position time.Duration,
) {
	m.mu.Lock()

	if m.current == nil ||
		m.current.Scrobbled {
		m.mu.Unlock()
		return
	}

	session := m.current

	if !m.noThreshold {
		// 流推算的 position 在客户端预加载整首或暂停后仍缓冲时会虚高，
		// 用"真实流逝时间"作为更保守的进度下界，避免"未真正播放却被记"。
		// 边播边下场景下两者基本同步，不会延迟正常触发。
		if elapsed := time.Since(session.StartedAt); elapsed < position {
			position = elapsed
		}

		threshold := m.thresholdFunc(session.Track.Duration)

		if threshold <= 0 {
			m.mu.Unlock()
			return
		}

		if position < threshold {
			m.logger.Debug(
				"scrobble 未到阈值",
				"用户", session.Username,
				"播放位置", position.String(),
				"阈值", threshold.String(),
			)
			m.mu.Unlock()
			return
		}
	}

	session.Scrobbled = true

	m.mu.Unlock()

	m.logger.Info(
		"推送scrobble",
		"用户", session.Username,
		"标题", session.Track.Title,
		"艺人", session.Track.Artist,
	)

	startedAt := session.StartedAt.Unix()

	for _, provider := range m.providersFor(session.Username) {
		p := provider

		go func() {
			if err := p.Scrobble(
				ctx,
				session.Track,
				startedAt,
			); err != nil {
				m.logger.Error(
					"scrobble推送失败",
					"用户", session.Username,
					"平台", p.Name(),
					"错误", err,
				)

				return
			}

			if m.onScrobbled != nil {
				m.onScrobbled(session.Username, p.Name())
			}
		}()
	}
}

func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.current = nil
}

// defaultScrobbleThreshold 实现 Last.fm 官方 scrobble 规则：
// 听满歌曲时长的一半，或听满 4 分钟（先到为准）。
func defaultScrobbleThreshold(d time.Duration) time.Duration {
	return min(d/2, 4*time.Minute)
}

// SetScrobbleThreshold 设置 scrobble 触发阈值，支持配置热更新。
// 解析失败时保留原阈值并打 Warn。支持：
//   - "auto"（默认）：min(时长/2, 4min)
//   - 时长字符串（"30s" / "2m"）：固定阈值
//   - 百分比（"50%" 或 "0.5"）：按歌曲时长比例
func (m *Manager) SetScrobbleThreshold(spec string) {
	fn, err := parseScrobbleThreshold(spec)
	if err != nil {
		m.logger.Warn("scrobble 阈值解析失败，沿用原阈值",
			"配置值", spec, "错误", err)

		return
	}

	low := strings.ToLower(strings.TrimSpace(spec))
	off := low == "off" || low == "disabled" || low == "false" ||
		low == "none" || low == "no" || low == "0"

	m.mu.Lock()
	m.thresholdFunc = fn
	m.noThreshold = off
	m.mu.Unlock()
}

// parseScrobbleThreshold 把配置字符串解析为阈值函数。
func parseScrobbleThreshold(spec string) (func(time.Duration) time.Duration, error) {
	spec = strings.TrimSpace(spec)
	low := strings.ToLower(spec)
	// off / disabled / false / none / no / 0：关闭阈值判断，每次播放即推送。
	if low == "off" || low == "disabled" || low == "false" ||
		low == "none" || low == "no" || low == "0" {
		return func(time.Duration) time.Duration { return 0 }, nil
	}
	if spec == "" || strings.EqualFold(spec, "auto") {
		return defaultScrobbleThreshold, nil
	}

	// 百分比：末尾带 %，或 0~1 之间的小数（如 0.5）
	if before, ok := strings.CutSuffix(spec, "%"); ok {
		f, err := strconv.ParseFloat(before, 64)
		if err != nil || f <= 0 {
			return nil, fmt.Errorf("阈值百分比非法: %q", spec)
		}

		ratio := f / 100

		return func(d time.Duration) time.Duration {
			return time.Duration(float64(d) * ratio)
		}, nil
	}

	if f, err := strconv.ParseFloat(spec, 64); err == nil && f > 0 && f < 1 {
		ratio := f

		return func(d time.Duration) time.Duration {
			return time.Duration(float64(d) * ratio)
		}, nil
	}

	// 固定时长
	if d, err := time.ParseDuration(spec); err == nil && d > 0 {
		return func(time.Duration) time.Duration { return d }, nil
	}

	return nil, fmt.Errorf("阈值格式不支持: %q（支持 auto / 30s / 2m / 50%% / 0.5）", spec)
}
