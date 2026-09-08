package main

import (
	"log/slog"
	"os"

	"cnb.cool/dtapp/fnmusic-sync/internal/config"
	"cnb.cool/dtapp/fnmusic-sync/internal/reqlog"
	"cnb.cool/dtapp/fnmusic-sync/internal/scrobbler"
)

// buildUserProviders 按配置文件中的 users 段，为每个用户创建各自的推送平台实例。
// 多用户隔离：不同用户用各自的 Last.fm / ListenBrainz 凭证，互不干扰。
func buildUserProviders(
	cfg *config.Config,
	logger *slog.Logger,
	lfmReqLog *reqlog.Logger,
	lbReqLog *reqlog.Logger,
) map[string][]scrobbler.Scrobbler {
	result := map[string][]scrobbler.Scrobbler{}

	for name, u := range cfg.Users {
		var providers []scrobbler.Scrobbler

		lfm := u.LastFM
		if lfm.Enabled && lfm.APIKey != "" && lfm.APISecret != "" && lfm.SessionKey != "" {
			providers = append(
				providers,
				scrobbler.NewLastFM(lfm.APIKey, lfm.APISecret, lfm.SessionKey, lfmReqLog),
			)
		} else if lfm.Enabled {
			logger.Warn("用户 Last.fm 已启用但配置不完整，已跳过",
				"用户", name,
				"已设api_key", lfm.APIKey != "",
				"已设api_secret", lfm.APISecret != "",
				"已设session_key", lfm.SessionKey != "")
		}

		lb := u.ListenBrainz
		if lb.Enabled && lb.Token != "" {
			providers = append(
				providers,
				scrobbler.NewListenBrainz(lb.Token, lb.Username, lbReqLog),
			)
		}

		if len(providers) > 0 {
			result[name] = providers
		}
	}

	return result
}

// providerStatusOf 返回"某用户是否开通了 Last.fm / ListenBrainz"的查询函数，
// 供代理在识别到用户后判断是否需要推送。
func providerStatusOf(cfg *config.Config) func(username string) (bool, bool) {
	return func(username string) (bool, bool) {
		u, ok := cfg.Users[username]
		if !ok {
			return false, false
		}

		return u.LastFM.Enabled &&
				u.LastFM.APIKey != "" &&
				u.LastFM.APISecret != "" &&
				u.LastFM.SessionKey != "",
			u.ListenBrainz.Enabled && u.ListenBrainz.Token != ""
	}
}

// socketModeFromConfig 从配置文件的 server.socket_mode 取 socket 权限。
// 为 0 时由 takeover 的 effectiveMode 自动推断（继承官方 socket 权限）。
func socketModeFromConfig(cfg *config.Config) os.FileMode {
	return os.FileMode(cfg.Server.SocketMode) & os.ModePerm
}
