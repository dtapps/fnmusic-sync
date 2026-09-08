package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"
)

// Config 对应 config.yaml 的完整结构（参见 ai.md 第 20 节，按多用户场景扩展）。
type Config struct {
	Server   ServerConfig           `mapstructure:"server" json:"server"`
	Users    map[string]UserAccount `mapstructure:"users" json:"users"`
	Playback PlaybackConfig         `mapstructure:"playback" json:"playback"`
	Playlist PlaylistConfig         `mapstructure:"playlist" json:"playlist"`
	Logging  LoggingConfig          `mapstructure:"logging" json:"logging"`
}

type ServerConfig struct {
	SocketMode   int    `mapstructure:"socket_mode" json:"socket_mode"`
	UpstreamWait string `mapstructure:"upstream_wait" json:"upstream_wait"`
}

// UserAccount 单个飞牛用户的推送平台凭证。
// 多用户隔离：每个用户各自配置自己的 Last.fm / ListenBrainz 账号，
// 不会把 A 的听歌记录推到 B 的账号。
type UserAccount struct {
	LastFM       UserLastFM       `mapstructure:"lastfm" json:"lastfm"`
	ListenBrainz UserListenBrainz `mapstructure:"listenbrainz" json:"listenbrainz"`
}

type UserLastFM struct {
	Enabled    bool   `mapstructure:"enabled" json:"enabled"`
	APIKey     string `mapstructure:"api_key" json:"api_key"`
	APISecret  string `mapstructure:"api_secret" json:"api_secret"`
	SessionKey string `mapstructure:"session_key" json:"session_key"`
	// Username Last.fm 用户名（用于歌单同步的 user.getTopTracks 等读取接口）。
	// 授权成功后由程序自动写回配置；也可手动填写。
	Username string `mapstructure:"username" json:"username"`
	// Playlist Last.fm 歌单同步配置。
	// Last.fm 虽然没有 playlist CRUD API，但有 user.getTopTracks / getLovedTracks /
	// getRecentTracks 等读取接口，可基于用户的 scrobble 数据自动生成智能歌单同步到飞牛。
	Playlist UserLastFMPlaylistConfig `mapstructure:"playlist" json:"playlist"`
}

type UserListenBrainz struct {
	Enabled bool   `mapstructure:"enabled" json:"enabled"`
	Token   string `mapstructure:"token" json:"token"`
	// Username ListenBrainz 用户名，用于歌单同步（daily-jams、weekly-jams、weekly-exploration）。
	// scrobble 不需要（submit-listens 只认 token），但开启歌单同步时必须填写。
	Username string `mapstructure:"username" json:"username"`
	// Playlist 该用户的 ListenBrainz 推荐歌单同步配置。
	Playlist UserPlaylistConfig `mapstructure:"playlist" json:"playlist"`
}

type PlaybackConfig struct {
	ScrobbleThreshold string `mapstructure:"scrobble_threshold" json:"scrobble_threshold"`
}

type PlaylistConfig struct {
	Enabled      bool   `mapstructure:"enabled" json:"enabled"`
	SyncInterval string `mapstructure:"sync_interval" json:"sync_interval"`
}

// UserPlaylistConfig 单个用户的 ListenBrainz 推荐歌单同步配置。
type UserPlaylistConfig struct {
	DailyJams         PlaylistSourceConfig `mapstructure:"daily_jams" json:"daily_jams"`
	WeeklyJams        PlaylistSourceConfig `mapstructure:"weekly_jams" json:"weekly_jams"`
	WeeklyExploration PlaylistSourceConfig `mapstructure:"weekly_exploration" json:"weekly_exploration"`
	// YearDiscoveries 年度发现歌单（top-discoveries-of-{year}），ListenBrainz 每年自动生成。
	YearDiscoveries PlaylistSourceConfig `mapstructure:"year_discoveries" json:"year_discoveries"`
	// YearMissed 年度遗珠歌单（top-missed-recordings-of-{year}），ListenBrainz 每年自动生成。
	YearMissed PlaylistSourceConfig `mapstructure:"year_missed" json:"year_missed"`
}

// UserLastFMPlaylistConfig Last.fm 智能歌单同步配置。
// 基于 user.getTopTracks / getLovedTracks / getRecentTracks 等读取接口，
// 利用用户已有的 scrobble 数据自动生成歌单同步到飞牛音乐。
type UserLastFMPlaylistConfig struct {
	// TopTracks 最常听曲目歌单，可按时间维度（7day/1month/3month/6month/12month/overall）统计
	TopTracks LastFMPlaylistSourceConfig `mapstructure:"top_tracks" json:"top_tracks"`
	// LovedTracks 红心收藏曲目歌单
	LovedTracks LastFMPlaylistSourceConfig `mapstructure:"loved_tracks" json:"loved_tracks"`
	// RecentTracks 最近播放曲目歌单
	RecentTracks LastFMPlaylistSourceConfig `mapstructure:"recent_tracks" json:"recent_tracks"`
}

// LastFMPlaylistSourceConfig Last.fm 单个智能歌单源的同步配置。
type LastFMPlaylistSourceConfig struct {
	Enabled bool   `mapstructure:"enabled" json:"enabled"`
	Name    string `mapstructure:"name" json:"name"`
	// Period 统计周期，仅 top_tracks 适用。
	// 可选值：7day / 1month / 3month / 6month / 12month / overall，默认 overall
	Period string `mapstructure:"period" json:"period"`
	// Limit 最多同步的曲目数量，0 表示不限制（使用 Last.fm API 默认上限）。
	Limit int `mapstructure:"limit" json:"limit"`
}

// PlaylistSourceConfig ListenBrainz 单个推荐歌单源的同步配置。
type PlaylistSourceConfig struct {
	Enabled bool   `mapstructure:"enabled" json:"enabled"`
	Name    string `mapstructure:"name" json:"name"`
	// Limit 最多同步的曲目数量，0 表示不限制（使用歌单全量）。
	Limit int `mapstructure:"limit" json:"limit"`
}

// LoggingConfig 日志配置。
// 注意：日志目录与文件名是代码常量（/var/log/fnmusic-sync/fnmusic-sync.log），
// 不在这里暴露，配置里只放级别与轮转策略。
type LoggingConfig struct {
	Level string `mapstructure:"level" json:"level"`
	// MaxSize 单个日志文件超过该大小(MB)后自动切割，0 = 不按大小切割。
	MaxSize int `mapstructure:"max_size" json:"max_size"`
	// MaxBackups 保留的历史日志文件份数，0 = 不限份数（仍受 MaxAge 约束）。
	MaxBackups int `mapstructure:"max_backups" json:"max_backups"`
	// MaxAge 历史日志保留天数，0 = 不按时间过期。
	MaxAge int `mapstructure:"max_age" json:"max_age"`
	// Compress 切割后的历史日志是否 gzip 压缩。
	Compress bool `mapstructure:"compress" json:"compress"`
}

// prepare 初始化 viper 实例：默认值、读取配置文件。
// path 为空或文件不存在时返回内置默认值（不报错）。
func prepare(v *viper.Viper, path string) error {
	setDefaults(v)

	if path != "" {
		v.SetConfigFile(path)
		v.SetConfigType("yaml")

		if err := v.ReadInConfig(); err != nil {
			var notFound viper.ConfigFileNotFoundError
			switch {
			case errors.As(err, &notFound):
				// viper 未找到文件（无 configFile）：用默认值。
			case os.IsNotExist(err):
				// 显式指定了文件路径但文件尚不存在（如首次启动）：
				// 同样用默认值，不报错，等 Watch 在文件创建后热加载。
			default:
				return fmt.Errorf("读取配置文件 %s 失败: %w", path, err)
			}
		}
	}

	return nil
}

// reload 把当前 viper 内容解析成 Config。
func reload(v *viper.Viper) (*Config, error) {
	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("解析配置失败: %w", err)
	}

	return &cfg, nil
}

// Load 读取一次 config.yaml（全部由配置文件决定，不使用环境变量）。
func Load(path string) (*Config, error) {
	v := viper.New()
	if err := prepare(v, path); err != nil {
		return nil, err
	}

	return reload(v)
}

// SaveDefault 首次启动时把内置默认配置保存成一份配置文件。
// 直接用 viper 自己写出，配置结构变化会自动同步，无需另维护模板。
// 父目录会被自动创建；仅应在确认文件不存在时调用。
func SaveDefault(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	v := viper.New()
	v.SetConfigType("yaml")
	setDefaults(v)

	return v.WriteConfigAs(path)
}

// Watch 监听配置文件变化，变化时重新解析并调用 onConfigChange（无需重启）。
// 即使启动时配置文件尚不存在，也会监听其所在目录，待文件被创建后自动热加载。
func Watch(
	path string,
	onConfigChange func(*Config),
	onError func(error),
) error {
	reloadAndApply := func(v *viper.Viper) {
		cfg, err := reload(v)
		if err != nil {
			if onError != nil {
				onError(fmt.Errorf("配置热更新解析失败: %w", err))
			}

			return
		}

		onConfigChange(cfg)
	}

	v := viper.New()
	if err := prepare(v, path); err != nil {
		return err
	}

	// 文件已存在：直接用 viper 自带的 WatchConfig 监听文件本身。
	if _, err := os.Stat(path); err == nil {
		v.OnConfigChange(func(e fsnotify.Event) {
			reloadAndApply(v)
		})
		v.WatchConfig()

		return nil
	}

	// 文件尚不存在：监听所在目录，等目标文件被创建后再接管为文件 watch。
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}

	if err := w.Add(filepath.Dir(path)); err != nil {
		_ = w.Close()

		return err
	}

	go func() {
		defer w.Close()

		for {
			select {
			case ev, ok := <-w.Events:
				if !ok {
					return
				}

				if ev.Op&fsnotify.Create != 0 &&
					filepath.Clean(ev.Name) == filepath.Clean(path) {
					nv := viper.New()
					if perr := prepare(nv, path); perr != nil {
						if onError != nil {
							onError(perr)
						}

						continue
					}

					reloadAndApply(nv)

					// 文件已存在，切到 viper 自带的文件级 watch，后续修改仍可热更新。
					nv.OnConfigChange(func(e fsnotify.Event) {
						reloadAndApply(nv)
					})
					nv.WatchConfig()

					return
				}
			case _, ok := <-w.Errors:
				if !ok {
					return
				}
			}
		}
	}()

	return nil
}

// SetLastFMSessionKey 把指定用户的 Last.fm session_key 写回配置文件并持久化。
// 实现：读取现有配置 → 修改 users.<name>.lastfm.session_key → 写回。
// 注意：viper 写回会按自身格式重新序列化整个文件，若原文件含注释/特殊排版可能被
// 规范化；此函数仅在授权补全场景下调用（通常文件为默认生成、注释很少），影响很小。
func SetLastFMSessionKey(path, username, sessionKey string) error {
	return SetLastFMCredentials(path, username, sessionKey, "")
}

// SetLastFMCredentials 把指定用户的 Last.fm session_key 和用户名写回配置文件并持久化。
// lastfmUsername 是 Last.fm 返回的用户名（auth.getSession → session.name），
// 为空时只写 session_key（向后兼容 SetLastFMSessionKey 的调用方）。
func SetLastFMCredentials(path, username, sessionKey, lastfmUsername string) error {
	if path == "" {
		return fmt.Errorf("配置文件路径为空，无法写入")
	}

	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType("yaml")

	if err := v.ReadInConfig(); err != nil {
		return fmt.Errorf("读取配置文件 %s 失败: %w", path, err)
	}

	v.Set(fmt.Sprintf("users.%s.lastfm.session_key", username), sessionKey)

	if lastfmUsername != "" {
		v.Set(fmt.Sprintf("users.%s.lastfm.username", username), lastfmUsername)
	}

	return v.WriteConfig()
}

// DeleteUser 删除指定用户的所有配置并持久化。
// viper 不支持 v.Set("users.<name>", nil) 来删除单个 key（nil 会被忽略），
// 因此先读取整个 users map，从中 delete 指定 key，再整体写回。
func DeleteUser(path, username string) error {
	if path == "" {
		return fmt.Errorf("配置文件路径为空，无法写入")
	}

	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType("yaml")

	if err := v.ReadInConfig(); err != nil {
		return fmt.Errorf("读取配置文件 %s 失败: %w", path, err)
	}

	// 确认用户存在
	if !v.IsSet(fmt.Sprintf("users.%s", username)) {
		return fmt.Errorf("用户 %s 不存在", username)
	}

	// 获取整个 users map，删除指定用户，再整体写回
	usersMap := v.GetStringMap("users")
	delete(usersMap, username)
	v.Set("users", usersMap)

	return v.WriteConfig()
}

// SaveUser 新增或更新指定用户的完整配置并持久化。
// 利用 viper 的 v.Set 将 users.<name> 整体替换，再 WriteConfig 写回。
func SaveUser(path, username string, account UserAccount) error {
	if path == "" {
		return fmt.Errorf("配置文件路径为空，无法写入")
	}

	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType("yaml")

	if err := v.ReadInConfig(); err != nil {
		return fmt.Errorf("读取配置文件 %s 失败: %w", path, err)
	}

	// 将 UserAccount 转为 map[string]any 并设置到 users.<name>
	userMap := userAccountToMap(account)
	v.Set(fmt.Sprintf("users.%s", username), userMap)

	return v.WriteConfig()
}

// userAccountToMap 将 UserAccount 结构体转为 viper 可直接设置的 map[string]any。
// 不用 json.Marshal + Unmarshal 的方式（虽然可行），这里直接手写以保持顺序清晰。
func userAccountToMap(u UserAccount) map[string]any {
	// Last.fm
	lfmMap := map[string]any{
		"enabled":     u.LastFM.Enabled,
		"api_key":     u.LastFM.APIKey,
		"api_secret":  u.LastFM.APISecret,
		"session_key": u.LastFM.SessionKey,
		"username":    u.LastFM.Username,
		"playlist": map[string]any{
			"top_tracks": map[string]any{
				"enabled": u.LastFM.Playlist.TopTracks.Enabled,
				"name":     u.LastFM.Playlist.TopTracks.Name,
				"period":   u.LastFM.Playlist.TopTracks.Period,
				"limit":    u.LastFM.Playlist.TopTracks.Limit,
			},
			"loved_tracks": map[string]any{
				"enabled": u.LastFM.Playlist.LovedTracks.Enabled,
				"name":     u.LastFM.Playlist.LovedTracks.Name,
				"limit":    u.LastFM.Playlist.LovedTracks.Limit,
			},
			"recent_tracks": map[string]any{
				"enabled": u.LastFM.Playlist.RecentTracks.Enabled,
				"name":     u.LastFM.Playlist.RecentTracks.Name,
				"limit":    u.LastFM.Playlist.RecentTracks.Limit,
			},
		},
	}

	// ListenBrainz
	lbMap := map[string]any{
		"enabled":  u.ListenBrainz.Enabled,
		"token":    u.ListenBrainz.Token,
		"username": u.ListenBrainz.Username,
		"playlist": map[string]any{
			"daily_jams": map[string]any{
				"enabled": u.ListenBrainz.Playlist.DailyJams.Enabled,
				"name":     u.ListenBrainz.Playlist.DailyJams.Name,
				"limit":    u.ListenBrainz.Playlist.DailyJams.Limit,
			},
			"weekly_jams": map[string]any{
				"enabled": u.ListenBrainz.Playlist.WeeklyJams.Enabled,
				"name":     u.ListenBrainz.Playlist.WeeklyJams.Name,
				"limit":    u.ListenBrainz.Playlist.WeeklyJams.Limit,
			},
			"weekly_exploration": map[string]any{
				"enabled": u.ListenBrainz.Playlist.WeeklyExploration.Enabled,
				"name":     u.ListenBrainz.Playlist.WeeklyExploration.Name,
				"limit":    u.ListenBrainz.Playlist.WeeklyExploration.Limit,
			},
			"year_discoveries": map[string]any{
				"enabled": u.ListenBrainz.Playlist.YearDiscoveries.Enabled,
				"name":     u.ListenBrainz.Playlist.YearDiscoveries.Name,
				"limit":    u.ListenBrainz.Playlist.YearDiscoveries.Limit,
			},
			"year_missed": map[string]any{
				"enabled": u.ListenBrainz.Playlist.YearMissed.Enabled,
				"name":     u.ListenBrainz.Playlist.YearMissed.Name,
				"limit":    u.ListenBrainz.Playlist.YearMissed.Limit,
			},
		},
	}

	return map[string]any{
		"lastfm":       lfmMap,
		"listenbrainz": lbMap,
	}
}

// SaveSettings 保存全局设置（playback、playlist、logging）到配置文件并持久化。
// 利用 viper 的 v.Set 逐字段更新，不影响 users 等其他配置项。
func SaveSettings(path string, playback PlaybackConfig, playlist PlaylistConfig, logging LoggingConfig) error {
	if path == "" {
		return fmt.Errorf("配置文件路径为空，无法写入")
	}

	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType("yaml")

	if err := v.ReadInConfig(); err != nil {
		return fmt.Errorf("读取配置文件 %s 失败: %w", path, err)
	}

	// playback
	v.Set("playback.scrobble_threshold", playback.ScrobbleThreshold)

	// playlist
	v.Set("playlist.enabled", playlist.Enabled)
	v.Set("playlist.sync_interval", playlist.SyncInterval)

	// logging
	v.Set("logging.level", logging.Level)
	v.Set("logging.max_size", logging.MaxSize)
	v.Set("logging.max_backups", logging.MaxBackups)
	v.Set("logging.max_age", logging.MaxAge)
	v.Set("logging.compress", logging.Compress)

	return v.WriteConfig()
}

// SetListenBrainzUsername 把指定用户的 ListenBrainz 用户名写回配置文件并持久化。
// 在启动时对"已启用但缺 username"的用户自动调用 /1/validate-token 获取用户名后写入。
func SetListenBrainzUsername(path, feiniuUsername, lbUsername string) error {
	if path == "" {
		return fmt.Errorf("配置文件路径为空，无法写入")
	}

	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType("yaml")

	if err := v.ReadInConfig(); err != nil {
		return fmt.Errorf("读取配置文件 %s 失败: %w", path, err)
	}

	v.Set(fmt.Sprintf("users.%s.listenbrainz.username", feiniuUsername), lbUsername)

	return v.WriteConfig()
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("server.upstream_wait", "30s")
	v.SetDefault("playback.scrobble_threshold", "auto")
	v.SetDefault("playlist.enabled", true)
	v.SetDefault("playlist.sync_interval", "30m")
	v.SetDefault("logging.level", "info")
	v.SetDefault("logging.max_size", 3)
	v.SetDefault("logging.max_backups", 5)
	v.SetDefault("logging.max_age", 7)
	v.SetDefault("logging.compress", true)
}

// DefaultLogging 返回日志默认值（配置文件缺失或读取失败时的兜底），
// 与 setDefaults 中的 logging.* 保持一致，避免两处各写一份。
func DefaultLogging() LoggingConfig {
	v := viper.New()
	setDefaults(v)

	var cfg Config
	_ = v.Unmarshal(&cfg)

	return cfg.Logging
}
