package main

import (
	"context"
	"log/slog"
	"time"

	"cnb.cool/dtapp/fnmusic-sync/internal/config"
	"cnb.cool/dtapp/fnmusic-sync/internal/reqlog"
	"cnb.cool/dtapp/fnmusic-sync/internal/scrobbler"
)

// lastFMAuthTimeout 授权轮询总时长：超时后重启程序可重新发起。
const lastFMAuthTimeout = 180 * time.Second

// startLastFMAuth 对单个"缺 session_key"的 Last.fm 用户执行授权辅助流程：
// 申请临时 token → 打印授权链接到日志 → 异步轮询，用户点同意后把
// session_key 和用户名自动写回配置文件（config.Watch 热更新会立即使其生效）。
func startLastFMAuth(
	username, apiKey, apiSecret, configPath string,
	logger *slog.Logger,
	lfmReqLog *reqlog.Logger,
) {
	client := scrobbler.NewLastFM(apiKey, apiSecret, "", lfmReqLog)

	ctx, cancel := context.WithTimeout(context.Background(), lastFMAuthTimeout)
	defer cancel()

	token, err := client.RequestToken(ctx)
	if err != nil {
		logger.Warn("Last.fm 申请授权 token 失败", "用户", username, "错误", err)

		return
	}

	logger.Info("Last.fm 需要授权，请在浏览器打开以下链接并点击同意",
		"用户", username, "授权链接", client.AuthURL(token))

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Warn("Last.fm 授权轮询超时（60秒），重启程序可重试",
				"用户", username, "授权链接", client.AuthURL(token))

			return
		case <-ticker.C:
			sk, name, err := client.Session(ctx, token)
			if err != nil {
				logger.Warn("Last.fm 换取 session_key 失败", "用户", username, "错误", err)

				continue
			}

			if sk == "" {
				// 用户尚未在浏览器点同意，继续等待。
				continue
			}

			if err := config.SetLastFMCredentials(configPath, username, sk, name); err != nil {
				logger.Warn("Last.fm 凭证写入配置失败", "用户", username, "错误", err)

				return
			}

			logger.Info("Last.fm 授权成功，session_key 和用户名已写入配置并自动生效",
				"用户", username, "Last.fm账号", name)

			return
		}
	}
}

// startListenBrainzUsernameLookup 对"缺 username"的 ListenBrainz 用户调用
// /1/validate-token 获取用户名并写回配置文件（config.Watch 热更新会立即使其生效）。
func startListenBrainzUsernameLookup(
	feiniuUsername, token, configPath string,
	logger *slog.Logger,
	lbReqLog *reqlog.Logger,
) {
	client := scrobbler.NewListenBrainz(token, "", lbReqLog)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	lbUsername, err := client.ValidateToken(ctx)
	if err != nil {
		logger.Warn("ListenBrainz 获取用户名失败，请手动填写 username 或检查 token",
			"用户", feiniuUsername, "错误", err)

		return
	}

	if lbUsername == "" {
		logger.Warn("ListenBrainz validate-token 返回空用户名，请手动填写 username",
			"用户", feiniuUsername)

		return
	}

	if err := config.SetListenBrainzUsername(configPath, feiniuUsername, lbUsername); err != nil {
		logger.Warn("ListenBrainz 用户名写入配置失败", "用户", feiniuUsername, "错误", err)

		return
	}

	logger.Info("ListenBrainz 用户名已自动获取并写入配置，歌单同步可正常使用",
		"用户", feiniuUsername, "ListenBrainz账号", lbUsername)
}
