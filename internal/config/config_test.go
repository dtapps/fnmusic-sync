package config

import (
	"os"
	"path/filepath"
	"testing"
)

// newTempConfigPath 返回临时目录下一个尚不存在的 config.yaml 路径。
func newTempConfigPath(t *testing.T) string {
	t.Helper()

	return filepath.Join(t.TempDir(), "config.yaml")
}

func TestLoadMissingFileUsesDefaults(t *testing.T) {
	t.Parallel()

	// 首次启动场景：文件不存在不应报错，直接返回默认值
	cfg, err := Load(newTempConfigPath(t))
	if err != nil {
		t.Fatalf("Load(不存在的文件) 返回错误: %v", err)
	}

	if cfg.Playback.ScrobbleThreshold != "auto" {
		t.Errorf("ScrobbleThreshold 默认值 = %q, want auto", cfg.Playback.ScrobbleThreshold)
	}

	if !cfg.Playlist.Enabled {
		t.Error("Playlist.Enabled 默认应为 true")
	}

	if cfg.Playlist.SyncInterval != "30m" {
		t.Errorf("Playlist.SyncInterval 默认值 = %q, want 30m", cfg.Playlist.SyncInterval)
	}

	if cfg.Library.ScanInterval != "6h" {
		t.Errorf("Library.ScanInterval 默认值 = %q, want 6h", cfg.Library.ScanInterval)
	}

	if cfg.Library.MBIDOnlineLookup {
		t.Error("Library.MBIDOnlineLookup 默认应为 false（离线优先）")
	}

	if cfg.Logging.Level != "info" || cfg.Logging.MaxSize != 3 || cfg.Logging.MaxBackups != 5 {
		t.Errorf("Logging 默认值异常: %+v", cfg.Logging)
	}
}

func TestLoadEmptyPathUsesDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load(\"\") 返回错误: %v", err)
	}

	if cfg.Logging.Level != "info" {
		t.Errorf("空路径时 Logging.Level = %q, want info", cfg.Logging.Level)
	}
}

func TestLoadInvalidYAMLFails(t *testing.T) {
	t.Parallel()

	path := newTempConfigPath(t)
	if err := os.WriteFile(path, []byte("playlist: [unclosed"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(path); err == nil {
		t.Error("Load(非法 YAML) 应返回错误")
	}
}

func TestLoadOverridesDefaults(t *testing.T) {
	t.Parallel()

	path := newTempConfigPath(t)

	const body = `
playback:
  scrobble_threshold: "50%"
playlist:
  enabled: false
  sync_interval: 5m
library:
  scan_interval: 1h
  mbid_online_lookup: true
logging:
  level: debug
  max_size: 10
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load 返回错误: %v", err)
	}

	if cfg.Playback.ScrobbleThreshold != "50%" {
		t.Errorf("ScrobbleThreshold = %q, want 50%%", cfg.Playback.ScrobbleThreshold)
	}

	if cfg.Playlist.Enabled {
		t.Error("Playlist.Enabled 应为 false")
	}

	if cfg.Playlist.SyncInterval != "5m" {
		t.Errorf("SyncInterval = %q, want 5m", cfg.Playlist.SyncInterval)
	}

	if cfg.Library.ScanInterval != "1h" {
		t.Errorf("ScanInterval = %q, want 1h", cfg.Library.ScanInterval)
	}

	if !cfg.Library.MBIDOnlineLookup {
		t.Error("MBIDOnlineLookup 应为 true")
	}

	if cfg.Logging.Level != "debug" || cfg.Logging.MaxSize != 10 {
		t.Errorf("Logging = %+v", cfg.Logging)
	}

	// 未覆盖的键仍保留默认值
	if cfg.Logging.MaxBackups != 5 {
		t.Errorf("未覆盖的 MaxBackups = %d, want 5", cfg.Logging.MaxBackups)
	}
}

func TestSaveDefaultThenLoadRoundTrip(t *testing.T) {
	t.Parallel()

	path := newTempConfigPath(t)

	if err := SaveDefault(path); err != nil {
		t.Fatalf("SaveDefault 返回错误: %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("SaveDefault 未生成文件: %v", err)
	}

	saved, err := Load(path)
	if err != nil {
		t.Fatalf("Load 返回错误: %v", err)
	}

	// 写出去再读回来，必须与内置默认值完全一致
	builtin, err := Load("")
	if err != nil {
		t.Fatal(err)
	}

	if saved.Playback.ScrobbleThreshold != builtin.Playback.ScrobbleThreshold ||
		saved.Playlist.SyncInterval != builtin.Playlist.SyncInterval ||
		saved.Library.ScanInterval != builtin.Library.ScanInterval ||
		saved.Logging.Level != builtin.Logging.Level ||
		saved.Logging.MaxSize != builtin.Logging.MaxSize ||
		saved.Logging.MaxBackups != builtin.Logging.MaxBackups ||
		saved.Logging.MaxAge != builtin.Logging.MaxAge ||
		saved.Logging.Compress != builtin.Logging.Compress {
		t.Errorf("SaveDefault 往返后配置与默认值不一致:\nsaved=%+v\nbuiltin=%+v", saved, builtin)
	}
}

func TestSaveDefaultCreatesParentDir(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "a", "b", "c", "config.yaml")
	if err := SaveDefault(path); err != nil {
		t.Fatalf("SaveDefault 应自动创建父目录，实际报错: %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Errorf("文件未创建: %v", err)
	}
}

func TestDefaultLoggingMatchesSetDefaults(t *testing.T) {
	t.Parallel()

	got := DefaultLogging()

	if !got.Enabled || got.Level != "info" || got.MaxSize != 3 ||
		got.MaxBackups != 5 || got.MaxAge != 7 || !got.Compress {
		t.Errorf("DefaultLogging() = %+v, 与 setDefaults 不一致", got)
	}
}
