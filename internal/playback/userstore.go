package playback

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/spf13/viper"
	"log/slog"
)

// UserState 记录单个飞牛用户的持久化基本信息。
type UserState struct {
	LastFMEnabled       bool
	ListenBrainzEnabled bool
	Scrobbles           int
	LastScrobbledAt     string
}

// UserStore 用 viper 管理用户状态文件（state.yaml）：
// 启动时读取旧值（“每次启动都去看旧的”），运行期识别到用户 / 推送成功后
// 更新并写回，跨重启保留各用户的启用标志与推送次数统计。
type UserStore struct {
	mu     sync.Mutex
	v      *viper.Viper
	path   string
	logger *slog.Logger
}

func NewUserStore(path string, logger *slog.Logger) *UserStore {
	v := viper.New()
	v.SetConfigType("yaml")

	if path != "" {
		v.SetConfigFile(path)

		if err := v.ReadInConfig(); err != nil {
			var notFound viper.ConfigFileNotFoundError
			if !errors.As(err, &notFound) && !os.IsNotExist(err) {
				logger.Warn("读取用户状态文件失败，将从空状态开始",
					"路径", path, "错误", err)
			}
		}
	}

	// 确保 users 段存在，避免 GetStringMap 返回 nil。
	if v.Get("users") == nil {
		v.Set("users", map[string]any{})
	}

	return &UserStore{v: v, path: path, logger: logger}
}

func (s *UserStore) readState(name string) UserState {
	st := UserState{}
	raw := s.v.GetStringMap("users." + name)
	if raw == nil {
		return st
	}

	if v, ok := raw["lastfm_enabled"].(bool); ok {
		st.LastFMEnabled = v
	}
	if v, ok := raw["listenbrainz_enabled"].(bool); ok {
		st.ListenBrainzEnabled = v
	}
	if v, ok := raw["scrobbles"].(int); ok {
		st.Scrobbles = v
	}
	if v, ok := raw["last_scrobbled_at"].(string); ok {
		st.LastScrobbledAt = v
	}

	return st
}

func (s *UserStore) writeState(name string, st UserState) {
	s.v.Set("users."+name, map[string]any{
		"lastfm_enabled":       st.LastFMEnabled,
		"listenbrainz_enabled": st.ListenBrainzEnabled,
		"scrobbles":            st.Scrobbles,
		"last_scrobbled_at":    st.LastScrobbledAt,
	})
}

// Get 读取用户当前状态（无记录时返回零值）。
func (s *UserStore) Get(name string) UserState {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.readState(name)
}

// Ensure 确保用户状态存在，并刷新其启用标志（依据当前已配置的 provider）。
// 第一次识别到某用户时会写入状态文件，记下该用户基本信息。
func (s *UserStore) Ensure(name string, lastfmEnabled, lbEnabled bool) UserState {
	s.mu.Lock()
	defer s.mu.Unlock()

	st := s.readState(name)
	st.LastFMEnabled = lastfmEnabled
	st.ListenBrainzEnabled = lbEnabled
	s.writeState(name, st)

	if err := s.saveLocked(); err != nil {
		s.logger.Warn("写回用户状态失败", "用户", name, "错误", err)
	}

	return st
}

// RecordScrobble 累加某用户的推送次数，并记录最近一次推送时间。
func (s *UserStore) RecordScrobble(name, provider string) {
	if name == "" {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	st := s.readState(name)
	st.Scrobbles++
	st.LastScrobbledAt = time.Now().Format(time.RFC3339)

	switch provider {
	case "Last.fm", "lastfm":
		st.LastFMEnabled = true
	case "ListenBrainz", "listenbrainz":
		st.ListenBrainzEnabled = true
	}

	s.writeState(name, st)

	if err := s.saveLocked(); err != nil {
		s.logger.Warn("写回用户状态失败", "用户", name, "错误", err)
	}
}

func (s *UserStore) saveLocked() error {
	if s.path == "" {
		return nil
	}

	if dir := filepath.Dir(s.path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}

	return s.v.WriteConfig()
}

// Save 在程序退出时显式写回一次。
func (s *UserStore) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.saveLocked()
}
