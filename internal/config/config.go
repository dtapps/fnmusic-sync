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
	Server   ServerConfig           `mapstructure:"server"`
	Users    map[string]UserAccount `mapstructure:"users"`
	Playback PlaybackConfig         `mapstructure:"playback"`
	Playlist PlaylistConfig         `mapstructure:"playlist"`
	Logging  LoggingConfig          `mapstructure:"logging"`
}

type ServerConfig struct {
	ListenSocket   string `mapstructure:"listen_socket"`
	UpstreamSocket string `mapstructure:"upstream_socket"`
	SocketMode     int    `mapstructure:"socket_mode"`
	UpstreamWait   string `mapstructure:"upstream_wait"`
}

// UserAccount 单个飞牛用户的推送平台凭证。
// 多用户隔离：每个用户各自配置自己的 Last.fm / ListenBrainz 账号，
// 不会把 A 的听歌记录推到 B 的账号。
type UserAccount struct {
	LastFM       UserLastFM       `mapstructure:"lastfm"`
	ListenBrainz UserListenBrainz `mapstructure:"listenbrainz"`
}

type UserLastFM struct {
	Enabled    bool   `mapstructure:"enabled"`
	APIKey     string `mapstructure:"api_key"`
	APISecret  string `mapstructure:"api_secret"`
	SessionKey string `mapstructure:"session_key"`
}

type UserListenBrainz struct {
	Enabled bool   `mapstructure:"enabled"`
	Token   string `mapstructure:"token"`
	// Username 供将来的 ListenBrainz 歌单同步使用
	// （端点 /1/user/{username}/playlists 需要用户名）。
	// 仅做 scrobble 时用不到（submit-listens 只认 token），可留空。
	Username string `mapstructure:"username"`
}

type PlaybackConfig struct {
	ScrobbleThreshold string `mapstructure:"scrobble_threshold"`
}

type PlaylistConfig struct {
	Enabled      bool   `mapstructure:"enabled"`
	SyncInterval string `mapstructure:"sync_interval"`
}

type LoggingConfig struct {
	Level string `mapstructure:"level"`
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
	if path == "" {
		return fmt.Errorf("配置文件路径为空，无法写入 session_key")
	}

	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType("yaml")

	if err := v.ReadInConfig(); err != nil {
		return fmt.Errorf("读取配置文件 %s 失败: %w", path, err)
	}

	v.Set(fmt.Sprintf("users.%s.lastfm.session_key", username), sessionKey)

	return v.WriteConfig()
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("server.listen_socket", "/var/run/trim_music.socket")
	v.SetDefault("server.upstream_socket", "/var/run/trim_music_upstream.socket")
	v.SetDefault("server.upstream_wait", "30s")
	v.SetDefault("playback.scrobble_threshold", "auto")
	v.SetDefault("playlist.enabled", true)
	v.SetDefault("playlist.sync_interval", "30m")
	v.SetDefault("logging.level", "info")
}
