package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTempConfig 写一份最小可用配置，返回路径。
func writeTempConfig(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := SaveDefault(path); err != nil {
		t.Fatalf("SaveDefault: %v", err)
	}

	return path
}

func TestSaveUserThenLoadUser(t *testing.T) {
	t.Parallel()

	path := writeTempConfig(t)

	account := UserAccount{
		LastFM: UserLastFM{
			Enabled:    true,
			APIKey:     "key-1",
			APISecret:  "secret-1",
			SessionKey: "sess-1",
			Username:   "alice",
		},
	}

	if err := SaveUser(path, "alice", account); err != nil {
		t.Fatalf("SaveUser: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	got, ok := cfg.Users["alice"]
	if !ok {
		t.Fatalf("配置里找不到用户 alice，实际 users=%v", cfg.Users)
	}

	if !got.LastFM.Enabled || got.LastFM.APIKey != "key-1" ||
		got.LastFM.APISecret != "secret-1" || got.LastFM.SessionKey != "sess-1" ||
		got.LastFM.Username != "alice" {
		t.Errorf("Last.fm 配置往返不一致: %+v", got.LastFM)
	}
}

func TestSaveUserOverwritesExisting(t *testing.T) {
	t.Parallel()

	path := writeTempConfig(t)

	if err := SaveUser(path, "bob", UserAccount{
		LastFM: UserLastFM{Enabled: true, SessionKey: "old"},
	}); err != nil {
		t.Fatal(err)
	}

	if err := SaveUser(path, "bob", UserAccount{
		LastFM: UserLastFM{Enabled: false, SessionKey: "new"},
	}); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	got := cfg.Users["bob"].LastFM
	if got.SessionKey != "new" || got.Enabled {
		t.Errorf("覆盖写入失败: %+v", got)
	}
}

func TestSaveUserKeepsOtherUsers(t *testing.T) {
	t.Parallel()

	path := writeTempConfig(t)

	for _, name := range []string{"u1", "u2", "u3"} {
		if err := SaveUser(path, name, UserAccount{LastFM: UserLastFM{SessionKey: "k-" + name}}); err != nil {
			t.Fatal(err)
		}
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	if len(cfg.Users) != 3 {
		t.Fatalf("用户数 = %d, want 3 (%v)", len(cfg.Users), cfg.Users)
	}

	for _, name := range []string{"u1", "u2", "u3"} {
		if cfg.Users[name].LastFM.SessionKey != "k-"+name {
			t.Errorf("用户 %s 的数据被其它写入破坏: %+v", name, cfg.Users[name].LastFM)
		}
	}
}

func TestSaveUserEmptyPathFails(t *testing.T) {
	t.Parallel()

	if err := SaveUser("", "x", UserAccount{}); err == nil {
		t.Error("空路径应报错")
	}
}

func TestDeleteUser(t *testing.T) {
	t.Parallel()

	path := writeTempConfig(t)

	if err := SaveUser(path, "alice", UserAccount{LastFM: UserLastFM{SessionKey: "a"}}); err != nil {
		t.Fatal(err)
	}

	if err := SaveUser(path, "bob", UserAccount{LastFM: UserLastFM{SessionKey: "b"}}); err != nil {
		t.Fatal(err)
	}

	if err := DeleteUser(path, "alice"); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := cfg.Users["alice"]; ok {
		t.Error("alice 应已被删除")
	}

	if _, ok := cfg.Users["bob"]; !ok {
		t.Error("bob 不应被误删")
	}
}

func TestDeleteUserNotFound(t *testing.T) {
	t.Parallel()

	path := writeTempConfig(t)

	if err := DeleteUser(path, "ghost"); err == nil {
		t.Error("删除不存在的用户应报错")
	} else if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("错误信息应包含用户名，实际: %v", err)
	}
}

func TestDeleteUserEmptyPathFails(t *testing.T) {
	t.Parallel()

	if err := DeleteUser("", "x"); err == nil {
		t.Error("空路径应报错")
	}
}

func TestSetLastFMCredentials(t *testing.T) {
	t.Parallel()

	path := writeTempConfig(t)

	if err := SaveUser(path, "alice", UserAccount{}); err != nil {
		t.Fatal(err)
	}

	if err := SetLastFMCredentials(path, "alice", "sess-x", "lastfm-alice"); err != nil {
		t.Fatalf("SetLastFMCredentials: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	got := cfg.Users["alice"].LastFM
	if got.SessionKey != "sess-x" {
		t.Errorf("SessionKey = %q, want sess-x", got.SessionKey)
	}

	if got.Username != "lastfm-alice" {
		t.Errorf("Username = %q, want lastfm-alice", got.Username)
	}
}

func TestSetLastFMSessionKeyLeavesUsernameUntouched(t *testing.T) {
	t.Parallel()

	path := writeTempConfig(t)

	if err := SetLastFMCredentials(path, "alice", "old-sess", "alice-lfm"); err != nil {
		t.Fatal(err)
	}

	// 只更新 session_key 的旧接口：lastfmUsername 传空，不应清掉已有 username
	if err := SetLastFMSessionKey(path, "alice", "new-sess"); err != nil {
		t.Fatalf("SetLastFMSessionKey: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	got := cfg.Users["alice"].LastFM
	if got.SessionKey != "new-sess" {
		t.Errorf("SessionKey = %q, want new-sess", got.SessionKey)
	}

	if got.Username != "alice-lfm" {
		t.Errorf("Username = %q, want alice-lfm（不应被清空）", got.Username)
	}
}

func TestSetLastFMCredentialsMissingFileFails(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "nope.yaml")
	if err := SetLastFMCredentials(path, "u", "s", ""); err == nil {
		t.Error("配置文件不存在时应报错")
	}
}

func TestSetLastFMCredentialsEmptyPathFails(t *testing.T) {
	t.Parallel()

	if err := SetLastFMCredentials("", "u", "s", ""); err == nil {
		t.Error("空路径应报错")
	}
}

func TestSaveSettingsAndLibrary(t *testing.T) {
	t.Parallel()

	path := writeTempConfig(t)

	// 只做「写入后文件仍可正常解析」的健全性检查：
	// SaveSettings/SaveLibrary 的字段集合会随功能演进，用全字段断言会很脆。
	playback := PlaybackConfig{ScrobbleThreshold: "50%"}
	playlist := PlaylistConfig{Enabled: true, SyncInterval: "15m"}
	logging := LoggingConfig{Enabled: true, Level: "debug", MaxSize: 5, MaxBackups: 2, MaxAge: 3, Compress: false}

	if err := SaveSettings(path, playback, playlist, logging); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	if err := SaveLibrary(path, []string{"/music", "/music2"}, "2h", true); err != nil {
		t.Fatalf("SaveLibrary: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("写入后配置不可解析: %v", err)
	}

	if cfg.Playback.ScrobbleThreshold != "50%" || cfg.Playlist.SyncInterval != "15m" {
		t.Errorf("SaveSettings 未生效: playback=%+v playlist=%+v", cfg.Playback, cfg.Playlist)
	}

	if cfg.Logging.Level != "debug" || cfg.Logging.MaxSize != 5 || cfg.Logging.Compress {
		t.Errorf("SaveSettings logging 未生效: %+v", cfg.Logging)
	}

	if cfg.Library.ScanInterval != "2h" || !cfg.Library.MBIDOnlineLookup {
		t.Errorf("SaveLibrary 未生效: %+v", cfg.Library)
	}

	// 文件必须真实落盘且非空
	info, err := os.Stat(path)
	if err != nil || info.Size() == 0 {
		t.Fatalf("配置文件异常: info=%v err=%v", info, err)
	}
}
