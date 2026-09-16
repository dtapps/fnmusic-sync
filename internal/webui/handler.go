//go:build fpk

// Package webui 提供 fpk 模式下的 Web 配置界面。
//
// 飞牛 fnOS 统一网关把 /app/fnmusic-sync 路径的请求转发到 app.sock，
// 本包在 app.sock 上监听 HTTP，提供：
//   - 静态前端页面（嵌入 www 目录）
//   - REST API：读取/保存配置、读取状态、读取日志尾部
package webui

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"cnb.cool/dtapp/fnmusic-sync/internal/buildinfo"
	"cnb.cool/dtapp/fnmusic-sync/internal/config"
	"cnb.cool/dtapp/fnmusic-sync/internal/lastfm"
	"cnb.cool/dtapp/fnmusic-sync/internal/strutil"
	"cnb.cool/dtapp/fnmusic-sync/internal/updater"
	"github.com/spf13/viper"
)

//go:embed www
var wwwFS embed.FS

// ActiveUserProvider 提供当前已通过 token 识别的活跃用户列表。
// 由 internal/playback.UserCache 实现（ActiveUsers 返回 token→用户名 映射）。
// 通过接口解耦，避免 webui 直接依赖 playback 包。
type ActiveUserProvider interface {
	ActiveUsers() map[string]string
}

// Server 提供 Web 配置界面 HTTP 服务。
type Server struct {
	configPath   string
	statePath    string
	logDir       string
	logger       *slog.Logger
	httpServer   *http.Server
	listener     net.Listener
	mu           sync.RWMutex
	latestCfg    *config.Config
	userProvider ActiveUserProvider
}

// NewServer 创建 Web UI 服务。
// configPath/statePath/logDir 由 runtime 模式决定（fpk 模式下使用 TRIM_* 变量）。
func NewServer(configPath, statePath, logDir string, logger *slog.Logger, userProvider ActiveUserProvider) *Server {
	return &Server{
		configPath:   configPath,
		statePath:    statePath,
		logDir:       logDir,
		logger:       logger,
		userProvider: userProvider,
	}
}

// Start 在指定 Unix Socket 上启动 HTTP 服务。
// socketPath 通常是 ${TRIM_APPDEST}/app.sock
func (s *Server) Start(socketPath string) error {
	// 清理残留 socket 文件
	if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("清理残留 socket 失败 %s: %w", socketPath, err)
	}

	dir := filepath.Dir(socketPath)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("创建 socket 目录失败 %s: %w", dir, err)
		}
	}

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("监听 %s 失败: %w", socketPath, err)
	}

	// 飞牛 fnOS 网关以 www-data 用户连接，socket 需要 0666 权限
	if err := os.Chmod(socketPath, 0o666); err != nil {
		listener.Close()
		return fmt.Errorf("设置 socket 权限失败 %s: %w", socketPath, err)
	}

	s.listener = listener

	mux := http.NewServeMux()
	s.registerRoutes(mux)

	s.httpServer = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// 预加载配置
	if cfg, err := config.Load(s.configPath); err == nil {
		s.latestCfg = cfg
	}

	s.logger.Info("Web UI 已启动", "监听", socketPath)

	go func() {
		if err := s.httpServer.Serve(listener); err != nil && err != http.ErrServerClosed {
			s.logger.Error("Web UI 服务异常", "错误", err)
		}
	}()

	return nil
}

// Stop 停止 Web UI 服务。
func (s *Server) Stop() {
	if s.httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.httpServer.Shutdown(ctx)
	}
	if s.listener != nil {
		_ = s.listener.Close()
	}
	s.logger.Info("Web UI 已停止")
}

// gatewayPrefix 是飞牛统一网关的路径前缀。
const gatewayPrefix = "/app/fnmusic-sync"

// registerRoutes 注册所有 HTTP 路由。
//
// 飞牛统一网关会将原始请求路径（如 /app/fnmusic-sync/api/config）
// 原样转发到 app.sock，因此所有路由都需带上 gatewayPrefix。
//
// 同时注册带斜杠和不带斜杠的根路径，避免 Go http.ServeMux 自动 301
// 重定向（ServeMux 对已注册 "/prefix/" 访问 "/prefix" 时会 301）。
// 任何 3xx 重定向在网关 iframe 中都可能触发重定向循环。
func (s *Server) registerRoutes(mux *http.ServeMux) {
	// 公开 API（无需鉴权）
	mux.HandleFunc(gatewayPrefix+"/api/version", s.handleVersion)
	mux.HandleFunc(gatewayPrefix+"/api/health", s.handleHealth)
	mux.HandleFunc(gatewayPrefix+"/api/me", s.handleMe)

	// 用户级 API（普通用户可访问自己的，管理员可访问全部）
	mux.HandleFunc(gatewayPrefix+"/api/config", s.handleConfig)
	mux.HandleFunc(gatewayPrefix+"/api/state", s.handleState)
	mux.HandleFunc(gatewayPrefix+"/api/user/{name}", s.handleUser)
	mux.HandleFunc(gatewayPrefix+"/api/lastfm/auth", s.handleLastFMAuth)
	mux.HandleFunc(gatewayPrefix+"/api/lastfm/poll", s.handleLastFMPoll)

	// 管理员级 API
	mux.HandleFunc(gatewayPrefix+"/api/logs", s.requireAdmin(s.handleLogs))
	mux.HandleFunc(gatewayPrefix+"/api/settings", s.requireAdmin(s.handleSettings))
	mux.HandleFunc(gatewayPrefix+"/api/users", s.requireAdmin(s.handleActiveUsers))
	mux.HandleFunc(gatewayPrefix+"/api/upgrade/check", s.requireAdmin(s.handleUpgradeCheck))
	mux.HandleFunc(gatewayPrefix+"/api/upgrade", s.requireAdmin(s.handleUpgrade))

	// 静态文件：从 www 子目录提取
	wwwSub, err := fs.Sub(wwwFS, "www")
	if err != nil {
		s.logger.Error("嵌入静态资源失败", "错误", err)
		return
	}

	handler := s.serveStatic(wwwSub)

	// 注册带斜杠和不带斜杠两个路径，避免 Go mux 自动 301 重定向
	mux.HandleFunc(gatewayPrefix+"/", handler)
	mux.HandleFunc(gatewayPrefix, handler)
}

// serveStatic 处理静态文件请求，不使用 http.FileServer 以避免其内置的
// 301 重定向行为（如 /index.html → /，目录路径补斜杠等）。
// 所有响应直接读取文件并写入，绝不发出 HTTP 重定向。
// 对于 index.html，使用 html/template 渲染，将 BaseURL（gatewayPrefix）
// 传入模板，前端通过 {{.BaseURL}} 引用资源路径，无需写死前缀。
func (s *Server) serveStatic(efs fs.FS) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 去掉网关前缀，得到相对于 www 根的路径
		rel := strings.TrimPrefix(r.URL.Path, gatewayPrefix)
		// 清理路径，防止目录穿越
		rel = "/" + strings.TrimLeft(rel, "/")
		if rel == "/" {
			rel = "/index.html"
		}

		// index.html 需要注入 <base> 标签
		if rel == "/index.html" {
			s.serveIndexWithBase(efs, w)
			return
		}

		// 尝试从嵌入文件系统打开
		f, err := efs.Open(strings.TrimLeft(rel, "/"))
		if err != nil {
			// 文件不存在 → SPA 回退到 index.html（不重定向）
			s.serveIndexWithBase(efs, w)
			return
		}
		defer f.Close()

		stat, err := f.Stat()
		if err != nil || stat.IsDir() {
			// 目录 → 返回 index.html
			s.serveIndexWithBase(efs, w)
			return
		}

		// 正常返回文件内容
		s.serveEmbeddedFile(efs, strings.TrimLeft(rel, "/"), w)
	}
}

// serveIndexWithBase 用 html/template 渲染 index.html，
// 将 BaseURL（gatewayPrefix）传入模板，前端通过 {{.BaseURL}} 引用资源路径。
func (s *Server) serveIndexWithBase(efs fs.FS, w http.ResponseWriter) {
	data, err := fs.ReadFile(efs, "index.html")
	if err != nil {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintln(w, "index.html 未找到")
		return
	}

	tmpl, err := template.New("index").Parse(string(data))
	if err != nil {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintln(w, "模板解析失败:", err)
		return
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, struct{ BaseURL string }{BaseURL: gatewayPrefix}); err != nil {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintln(w, "模板渲染失败:", err)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	w.Write(buf.Bytes())
}

// serveEmbeddedFile 从嵌入文件系统读取指定文件并写入 ResponseWriter。
// 使用 http.ServeContent 以获得正确的 Content-Type 和 Range 支持。
func (s *Server) serveEmbeddedFile(efs fs.FS, name string, w http.ResponseWriter) {
	f, err := efs.Open(name)
	if err != nil {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintln(w, "文件未找到:", name)
		return
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// 使用 http.ServeContent 以获得正确的 Content-Type
	http.ServeContent(w, &http.Request{Method: "GET"}, filepath.Base(name), stat.ModTime(), f.(io.ReadSeeker))
}

// gatewayUser 从飞牛统一网关 Header 中提取当前用户信息。
// 网关校验用户会话后，通过以下 Header 转发用户身份：
//
//	X-Trim-Userid:   用户 UID（如 1000）
//	X-Trim-Isadmin:  是否管理员（"true" 或 "false"）
//	X-Trim-Username: 用户名（如 admin）
//
// 非网关环境（如直接访问 app.sock）下，这些 Header 不存在，
// 此时默认视为管理员（本地调试场景），以便开发测试。
type gatewayUser struct {
	UID      string
	Username string
	IsAdmin  bool
}

func readGatewayUser(r *http.Request) gatewayUser {
	uid := r.Header.Get("X-Trim-Userid")
	username := r.Header.Get("X-Trim-Username")
	isAdmin := r.Header.Get("X-Trim-Isadmin") == "true"

	// 非网关环境（无 Header），默认管理员权限（本地调试）
	if uid == "" && username == "" {
		return gatewayUser{UID: "0", Username: "local", IsAdmin: true}
	}

	return gatewayUser{UID: uid, Username: username, IsAdmin: isAdmin}
}

// requireAdmin 是一个中间件，仅允许管理员访问。
func (s *Server) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := readGatewayUser(r)
		if !user.IsAdmin {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			writeJSONError(w, http.StatusForbidden, "仅管理员可执行此操作")
			return
		}
		next(w, r)
	}
}

// handleMe GET 返回当前用户的身份信息（从网关 Header 读取）。
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		writeJSONError(w, http.StatusMethodNotAllowed, "不支持的请求方法")
		return
	}

	user := readGatewayUser(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"uid":      user.UID,
		"username": user.Username,
		"isAdmin":  user.IsAdmin,
	})
}

// handleConfig GET 返回当前配置。
// 管理员返回全部配置；普通用户只返回自己的用户配置和全局设置。
// 配置的修改通过 /api/user/{name} 和 /api/settings 完成。
func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		writeJSONError(w, http.StatusMethodNotAllowed, "不支持的请求方法")
		return
	}

	s.mu.RLock()
	cfg := s.latestCfg
	s.mu.RUnlock()

	if cfg == nil {
		cfgLoaded, err := config.Load(s.configPath)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "读取配置失败: "+err.Error())
			return
		}
		cfg = cfgLoaded
		s.mu.Lock()
		s.latestCfg = cfg
		s.mu.Unlock()
	}

	user := readGatewayUser(r)
	if user.IsAdmin {
		// 管理员返回全部配置
		writeJSON(w, http.StatusOK, cfg)
		return
	}

	// 普通用户只返回自己的用户配置 + 全局设置
	userCfg, exists := cfg.Users[user.Username]
	if !exists {
		// 用户不存在配置时返回空配置
		writeJSON(w, http.StatusOK, map[string]any{
			"users":    map[string]any{},
			"playback": cfg.Playback,
			"playlist": cfg.Playlist,
			"logging":  cfg.Logging,
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"users":    map[string]any{user.Username: userCfg},
		"playback": cfg.Playback,
		"playlist": cfg.Playlist,
		"logging":  cfg.Logging,
	})
}

// handleState GET 返回状态文件内容。
// 管理员返回全部用户状态；普通用户只返回自己的状态。
func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		writeJSONError(w, http.StatusMethodNotAllowed, "不支持的请求方法")
		return
	}

	// 状态文件是 YAML 格式（由 viper 写入），不能用 json.Unmarshal 解析。
	v := viper.New()
	v.SetConfigType("yaml")
	v.SetConfigFile(s.statePath)
	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok || os.IsNotExist(err) {
			writeJSON(w, http.StatusOK, map[string]any{"users": map[string]any{}})
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "读取状态失败: "+err.Error())
		return
	}

	raw := v.AllSettings()

	user := readGatewayUser(r)
	if user.IsAdmin {
		// 管理员返回全部
		writeJSON(w, http.StatusOK, raw)
		return
	}

	// 普通用户只返回自己的状态
	users, ok := raw["users"].(map[string]any)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"users": map[string]any{}})
		return
	}

	userState, exists := users[user.Username]
	if !exists {
		writeJSON(w, http.StatusOK, map[string]any{"users": map[string]any{}})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"users": map[string]any{user.Username: userState},
	})
}

// handleLogs GET 返回日志文件尾部内容。
func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		writeJSONError(w, http.StatusMethodNotAllowed, "不支持的请求方法")
		return
	}

	// 检查配置中是否关闭了文件日志
	s.mu.RLock()
	cfg := s.latestCfg
	s.mu.RUnlock()
	fileEnabled := true
	if cfg != nil {
		// Enabled 默认为 true，只有显式设为 false 才关闭
		if !cfg.Logging.Enabled {
			fileEnabled = false
		}
	}

	if !fileEnabled {
		writeJSON(w, http.StatusOK, map[string]any{"lines": []string{}, "file_enabled": false})
		return
	}

	logFile := filepath.Join(s.logDir, "fnmusic-sync.log")
	data, err := os.ReadFile(logFile)
	if err != nil {
		if os.IsNotExist(err) {
			// 日志文件不存在（可能被配置为关闭）
			writeJSON(w, http.StatusOK, map[string]any{"lines": []string{}, "file_enabled": false})
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "读取日志失败: "+err.Error())
		return
	}

	// 返回最后 200 行
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) > 200 {
		lines = lines[len(lines)-200:]
	}

	writeJSON(w, http.StatusOK, map[string]any{"lines": lines, "file_enabled": true})
}

// handleVersion GET 返回版本信息。
func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		writeJSONError(w, http.StatusMethodNotAllowed, "不支持的请求方法")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"version":    buildinfo.Version,
		"gitCommit":  buildinfo.GitCommit,
		"buildTime":  buildinfo.BuildTime,
		"binaryName": buildinfo.BinaryName,
	})
}

// handleHealth GET 返回健康状态。
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		writeJSONError(w, http.StatusMethodNotAllowed, "不支持的请求方法")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"config": s.configPath,
		"state":  s.statePath,
		"logDir": s.logDir,
	})
}

// handleSettings 处理全局设置（playback、playlist、logging）的更新 API。
// PUT /api/settings → body: { playback: {...}, playlist: {...}, logging: {...} }
// 利用 viper 逐字段更新，不影响 users 等其他配置项。
func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		w.Header().Set("Allow", "PUT, POST")
		writeJSONError(w, http.StatusMethodNotAllowed, "不支持的请求方法")
		return
	}

	var req struct {
		Playback config.PlaybackConfig `json:"playback"`
		Playlist config.PlaylistConfig `json:"playlist"`
		Logging  config.LoggingConfig  `json:"logging"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "解析请求体失败: "+err.Error())
		return
	}

	if err := config.SaveSettings(s.configPath, req.Playback, req.Playlist, req.Logging); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "保存设置失败: "+err.Error())
		return
	}

	// 刷新内存缓存
	if cfg, err := config.Load(s.configPath); err == nil {
		s.mu.Lock()
		s.latestCfg = cfg
		s.mu.Unlock()
	}

	s.logger.Info("全局设置已通过 Web UI 保存")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleUser 处理单个用户的增删改 API。
//
//	POST   /api/user/{name}  → 新增或更新用户（body: UserAccount JSON）
//	DELETE /api/user/{name}  → 删除用户（仅管理员）
//
// 权限规则：
//   - 管理员可以 POST/DELETE 任意用户
//   - 普通用户只能 POST 自己的用户名（保存自己的配置），不能 DELETE
//   - 普通用户不能操作其他人的用户名
func (s *Server) handleUser(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	name := r.PathValue("name")
	if name == "" {
		writeJSONError(w, http.StatusBadRequest, "用户名不能为空")
		return
	}

	user := readGatewayUser(r)

	switch r.Method {
	case http.MethodPost:
		// 普通用户只能保存自己的配置
		if !user.IsAdmin && name != user.Username {
			writeJSONError(w, http.StatusForbidden, "只能编辑自己的用户配置")
			return
		}

		var account config.UserAccount
		if err := json.NewDecoder(r.Body).Decode(&account); err != nil {
			writeJSONError(w, http.StatusBadRequest, "解析请求体失败: "+err.Error())
			return
		}

		if err := config.SaveUser(s.configPath, name, account); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "保存用户失败: "+err.Error())
			return
		}

		// 刷新内存缓存
		if cfg, err := config.Load(s.configPath); err == nil {
			s.mu.Lock()
			s.latestCfg = cfg
			s.mu.Unlock()
		}

		s.logger.Info("用户配置已通过 Web UI 保存", "用户", name, "操作者", user.Username)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})

	case http.MethodDelete:
		// 删除用户仅管理员可操作
		if !user.IsAdmin {
			writeJSONError(w, http.StatusForbidden, "仅管理员可删除用户")
			return
		}

		if err := config.DeleteUser(s.configPath, name); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "删除用户失败: "+err.Error())
			return
		}

		// 刷新内存缓存
		if cfg, err := config.Load(s.configPath); err == nil {
			s.mu.Lock()
			s.latestCfg = cfg
			s.mu.Unlock()
		}

		s.logger.Info("用户配置已通过 Web UI 删除", "用户", name, "操作者", user.Username)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})

	default:
		w.Header().Set("Allow", "POST, DELETE")
		writeJSONError(w, http.StatusMethodNotAllowed, "不支持的请求方法")
	}
}

// lastfmAuthSession 保存正在进行中的 Last.fm 授权会话（token + 用户名）。
// 授权完成后或超时后自动清理。同一用户同时只允许一个授权会话。
type lastfmAuthSession struct {
	token     string
	username  string
	startedAt time.Time
}

// 授权会话内存存储（进程级），无需持久化。
var lastfmAuthSessions sync.Map // map[username] *lastfmAuthSession

// handleLastFMAuth POST /api/lastfm/auth
//
// 从请求体读取 api_key + api_secret + 飞牛用户名，
// 向 Last.fm 申请临时 token，返回授权 URL。
// 前端拿到 URL 后在新标签页打开，然后轮询 /api/lastfm/poll。
//
// 权限：普通用户只能为自己授权；管理员可以为任意用户授权。
func (s *Server) handleLastFMAuth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeJSONError(w, http.StatusMethodNotAllowed, "不支持的请求方法")
		return
	}

	var req struct {
		APIKey    string `json:"api_key"`
		APISecret string `json:"api_secret"`
		Username  string `json:"username"` // 飞牛用户名（配置文件中的 key）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "解析请求体失败: "+err.Error())
		return
	}

	if req.APIKey == "" || req.APISecret == "" {
		writeJSONError(w, http.StatusBadRequest, "api_key 和 api_secret 不能为空")
		return
	}

	gwUser := readGatewayUser(r)
	// 普通用户只能为自己授权
	if !gwUser.IsAdmin && req.Username != gwUser.Username {
		writeJSONError(w, http.StatusForbidden, "只能为自己的账号授权")
		return
	}

	client := lastfm.NewClient(req.APIKey, req.APISecret, "", nil)
	token, err := client.RequestToken(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "申请授权 token 失败: "+err.Error())
		return
	}

	// 记录授权会话，供轮询接口使用
	lastfmAuthSessions.Store(req.Username, &lastfmAuthSession{
		token:     token,
		username:  req.Username,
		startedAt: time.Now(),
	})

	s.logger.Info("Last.fm 授权已发起", "用户", req.Username, "操作者", gwUser.Username)
	writeJSON(w, http.StatusOK, map[string]string{
		"auth_url": client.AuthURL(token),
		"token":    token,
	})
}

// handleLastFMPoll POST /api/lastfm/poll
//
// 读取正在进行的授权会话的 token，向 Last.fm 轮询 auth.getSession。
// - 用户尚未点同意 → 返回 {"authorized": false}，前端继续轮询
// - 授权成功 → 调用 config.SetLastFMCredentials 写回配置，返回 {"authorized": true, ...}
// - 超时/无会话 → 返回 {"authorized": false, "expired": true}，前端停止轮询
func (s *Server) handleLastFMPoll(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeJSONError(w, http.StatusMethodNotAllowed, "不支持的请求方法")
		return
	}

	var req struct {
		APIKey    string `json:"api_key"`
		APISecret string `json:"api_secret"`
		Username  string `json:"username"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "解析请求体失败: "+err.Error())
		return
	}

	gwUser := readGatewayUser(r)
	if !gwUser.IsAdmin && req.Username != gwUser.Username {
		writeJSONError(w, http.StatusForbidden, "只能查询自己的授权状态")
		return
	}

	// 取出授权会话
	val, ok := lastfmAuthSessions.Load(req.Username)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{
			"authorized": false,
			"expired":    true,
		})
		return
	}

	session := val.(*lastfmAuthSession)

	// 超时检查（3 分钟）
	if time.Since(session.startedAt) > 3*time.Minute {
		lastfmAuthSessions.Delete(req.Username)
		writeJSON(w, http.StatusOK, map[string]any{
			"authorized": false,
			"expired":    true,
		})
		return
	}

	client := lastfm.NewClient(req.APIKey, req.APISecret, "", nil)
	sk, lfmUsername, err := client.Session(r.Context(), session.token)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"authorized": false,
			"error":      err.Error(),
		})
		return
	}

	if sk == "" {
		// 用户尚未在浏览器点同意，继续等待
		writeJSON(w, http.StatusOK, map[string]any{
			"authorized": false,
		})
		return
	}

	// 授权成功，写回配置
	if err := config.SetLastFMCredentials(s.configPath, req.Username, sk, lfmUsername); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "写入配置失败: "+err.Error())
		return
	}

	// 刷新内存缓存
	if cfg, err := config.Load(s.configPath); err == nil {
		s.mu.Lock()
		s.latestCfg = cfg
		s.mu.Unlock()
	}

	// 清理授权会话
	lastfmAuthSessions.Delete(req.Username)

	s.logger.Info("Last.fm 授权成功，session_key 已写回配置",
		"用户", req.Username, "Last.fm账号", lfmUsername)
	writeJSON(w, http.StatusOK, map[string]any{
		"authorized":      true,
		"session_key":     sk,
		"lastfm_username": lfmUsername,
	})
}

// handleActiveUsers GET 返回当前通过 token 识别的活跃用户列表。
// 仅管理员可访问。token 为敏感凭证，仅返回前 8 位用于展示，避免完整泄露。
func (s *Server) handleActiveUsers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		writeJSONError(w, http.StatusMethodNotAllowed, "不支持的请求方法")
		return
	}

	users := make([]map[string]string, 0)
	if s.userProvider != nil {
		for k, v := range s.userProvider.ActiveUsers() {
			users = append(users, map[string]string{
				"token":    strutil.FirstN8(k),
				"username": v,
			})
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"users": users,
	})
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// ===== 升级相关 Handler =====
// 公共逻辑（获取版本号、拼接下载地址、下载文件）抽到 internal/updater 包，
// 这里只保留 HTTP handler 和 fpk 专属的 appcenter-cli 安装逻辑。

// handleUpgradeCheck GET /api/upgrade/check
// 检查最新版本，返回当前版本、最新版本、是否有更新。
func (s *Server) handleUpgradeCheck(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		writeJSONError(w, http.StatusMethodNotAllowed, "不支持的请求方法")
		return
	}

	client := updater.DefaultHTTPClient()

	latestTag, err := updater.FetchLatestTag(client)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"current_version": buildinfo.Version,
		"latest_version":  latestTag,
		"has_update":      updater.HasUpdate(latestTag),
		"repo_source":     buildinfo.RepoSource,
	})
}

// handleUpgrade POST /api/upgrade
// 下载对应架构的 .fpk 安装包，调用飞牛官方升级命令 appcenter-cli install-local，
// 由飞牛完成「停止 → 卸载 → 重装 → 启动」的标准升级流程，并刷新 App Center 版本元数据。
// 说明：飞牛开放 API 未提供应用自升级能力，:8000 的升级接口又只认 Web 会话 Cookie；
// 而 appcenter-cli 运行在系统上下文、无需浏览器 Cookie，是对已安装应用执行官方升级的正确入口。
func (s *Server) handleUpgrade(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeJSONError(w, http.StatusMethodNotAllowed, "不支持的请求方法")
		return
	}

	// 升级过程需要 stop/start 应用，依赖 appcenter-cli
	appcenterCLI, err := exec.LookPath("appcenter-cli")
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "未检测到 appcenter-cli 工具，请通过飞牛应用中心手动升级")
		return
	}

	client := updater.DefaultHTTPClient()

	// 1. 获取最新版本号
	latestTag, err := updater.FetchLatestTag(client)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// 2. 拼接 fpk 下载地址并下载
	arch := runtime.GOARCH
	fileName := fmt.Sprintf("%s-%s.fpk", buildinfo.BinaryName, arch)
	downloadURL := updater.BuildDownloadURL(latestTag, fileName)

	// 3. 下载 fpk 到临时目录
	tmpFile, err := updater.DownloadFile(client, downloadURL, fileName)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 进程被飞牛系统杀掉后 defer 不会执行，但留着无害。
	defer func() { _ = os.Remove(tmpFile) }()

	// 4. 记录升级前的信息，便于诊断
	exePath, _ := os.Executable()
	s.logger.Info("升级前状态",
		"当前版本", buildinfo.Version,
		"目标版本", latestTag,
		"二进制路径", exePath,
		"fpk文件", tmpFile,
		"文件大小", func() int64 {
			if fi, e := os.Stat(tmpFile); e == nil {
				return fi.Size()
			}
			return -1
		}(),
	)

	// 5. 解包工作区（staging 需保留到重启脚本执行完，故不在此 defer 删除）
	staging, err := os.MkdirTemp("", "fnmusic-upgrade-")
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "创建临时目录失败: "+err.Error())
		return
	}
	outerDir := filepath.Join(staging, "outer")
	if err := os.MkdirAll(outerDir, 0o755); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "创建临时目录失败: "+err.Error())
		return
	}

	// 6. 解包 fpk 外层 tar.gz（得到 app.tgz / cmd / config / manifest / wizard / 图标 等）。
	//    app.tgz 由后续 appcenter-cli install-local 自行读取，无需在此二次解包。
	if err := extractTarGz(tmpFile, outerDir); err != nil {
		s.logger.Error("解包 fpk 外层失败", "错误", err)
		writeJSONError(w, http.StatusInternalServerError, "解包 fpk 失败: "+err.Error())
		return
	}

	// 8. 确定目标运行目录（TRIM_APPDEST），并据此推导应用所在卷序号。
	//    install-local 在升级场景下会忽略 -v 参数，这里仅作兼容保留。
	appDest := os.Getenv("TRIM_APPDEST")
	if appDest == "" {
		appDest = filepath.Join("/var/apps", buildinfo.BinaryName, "target")
	}
	volume := detectVolumeFromPath(appDest)

	s.logger.Info("升级前状态",
		"当前版本", buildinfo.Version,
		"目标版本", latestTag,
		"运行目录", appDest,
		"fpk文件", tmpFile,
	)

	// 9. 调用飞牛官方升级命令 appcenter-cli install-local。
	//    它会对已安装应用执行「停止 → 卸载 → 重装 → 启动」的标准升级流程，
	//    并刷新 App Center 的版本元数据（这正是此前「自行覆盖文件」方案缺失、导致版本号不更新的部分）。
	//    该命令执行时会先停止当前正在运行的本进程，因此必须在返回 HTTP 响应之后、
	//    以脱离会话（setsid）的方式在后台执行，确保进程被停止后升级仍能继续完成。
	upgradeCmdStr := "sleep 1; " + appcenterCLI + " install-local -d " + outerDir +
		" -v " + strconv.Itoa(volume) + "; rm -rf " + staging
	s.logger.Info("执行官方升级命令", "命令", upgradeCmdStr)

	upgradeCmd := exec.Command("bash", "-c", upgradeCmdStr)
	upgradeCmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := upgradeCmd.Start(); err != nil {
		s.logger.Error("启动官方升级命令失败", "错误", err)
		writeJSONError(w, http.StatusInternalServerError, "启动升级失败: "+err.Error())
		return
	}
	// 不 Wait：让命令在后台独立运行，应用被停止时本进程随之退出亦无妨。

	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	s.logger.Info("已触发官方后台升级，应用即将重启", "版本", latestTag)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":             true,
		"latest_version": latestTag,
		"message":        "已通过飞牛应用中心执行升级，应用正在重启以完成升级",
	})
}

// detectVolumeFromPath 从 TRIM_APPDEST 路径（如 /vol1/@appcenter/fnmusic-sync）
// 推导应用所在卷序号，供 appcenter-cli install-local -v 使用。
// 说明：升级场景下 install-local 会忽略 -v 参数，此处仅作兼容保留，默认返回卷 1。
func detectVolumeFromPath(appDest string) int {
	re := regexp.MustCompile(`/vol(\d+)/`)
	if m := re.FindStringSubmatch(appDest); m != nil {
		if v, err := strconv.Atoi(m[1]); err == nil {
			return v
		}
	}
	return 1
}

// extractTarGz 解压 gzip 压缩的 tar 到 dest 目录。
func extractTarGz(src, dest string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("gzip 解压失败: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("读取 tar 失败: %w", err)
		}
		target, err := safeJoin(dest, hdr.Name)
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.ModeDir|os.FileMode(hdr.Mode&0o777)); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode&0o777))
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			out.Close()
		default:
			// 跳过符号链接/硬链接等；fpk 内不应包含，忽略即可。
			continue
		}
	}
	return nil
}

// safeJoin 将 tar 内的条目名安全拼接进 base，防止路径穿越。
func safeJoin(base, name string) (string, error) {
	clean := filepath.Clean("/" + name)
	rel := strings.TrimPrefix(clean, "/")
	if rel == "" || rel == ".." || strings.HasPrefix(rel, "../") {
		return "", fmt.Errorf("非法的 tar 路径: %s", name)
	}
	target := filepath.Join(base, rel)
	baseClean := filepath.Clean(base)
	if target != baseClean && !strings.HasPrefix(target, baseClean+string(os.PathSeparator)) {
		return "", fmt.Errorf("非法的 tar 路径: %s", name)
	}
	return target, nil
}
