// 飞牛音乐 Scrobble 代理 - TypeScript 类型定义

// ===== 用户身份 =====
export interface CurrentUser {
  uid: string;
  username: string;
  isAdmin: boolean;
}

// ===== 版本信息 =====
export interface VersionInfo {
  version: string;
  gitCommit: string;
  buildTime: string;
  binaryName: string;
}

// ===== Last.fm 配置 =====
export interface LastFmPlaylist {
  top_tracks: LastFmTopTracks;
  loved_tracks: LastFmLovedTracks;
  recent_tracks: LastFmRecentTracks;
}

export interface LastFmTopTracks {
  enabled?: boolean;
  name?: string;
  period?: string; // overall | 7day | 1month | 3month | 6month | 12month
  limit?: number;
}

export interface LastFmLovedTracks {
  enabled?: boolean;
  name?: string;
  limit?: number;
}

export interface LastFmRecentTracks {
  enabled?: boolean;
  name?: string;
  limit?: number;
}

export interface LastFmConfig {
  enabled?: boolean;
  api_key?: string;
  api_secret?: string;
  session_key?: string;
  username?: string;
  playlist: LastFmPlaylist;
}

// ===== ListenBrainz 配置 =====
export interface ListenBrainzPlaylist {
  daily_jams: LBPlaylistItem;
  weekly_jams: LBPlaylistItem;
  weekly_exploration: LBPlaylistItem;
  year_discoveries: LBPlaylistItem;
  year_missed: LBPlaylistItem;
}

export interface LBPlaylistItem {
  enabled?: boolean;
  name?: string;
  limit?: number;
}

export interface ListenBrainzConfig {
  enabled?: boolean;
  token?: string;
  username?: string;
  playlist: ListenBrainzPlaylist;
}

// ===== 用户配置 =====
export interface UserAccount {
  lastfm: LastFmConfig;
  listenbrainz: ListenBrainzConfig;
}

// ===== 全局设置 =====
export interface PlaybackConfig {
  scrobble_threshold?: string; // auto | 30s | 2m | 50% | off
}

export interface PlaylistConfig {
  enabled?: boolean;
  sync_interval?: string; // 如 30m
}

export interface LoggingConfig {
  enabled?: boolean; // 是否写入日志文件
  level?: string; // info | debug | warn | error
  max_size?: number;
  max_backups?: number;
  max_age?: number;
  compress?: boolean;
}

// ===== 完整配置 =====
export interface AppConfig {
  users?: Record<string, UserAccount>;
  playback?: PlaybackConfig;
  playlist?: PlaylistConfig;
  logging?: LoggingConfig;
}

// ===== 运行状态 =====
export interface UserState {
  scrobbles?: number;
  last_scrobbled_at?: string;
  lastfm_enabled?: boolean;
  listenbrainz_enabled?: boolean;
}

export interface StateData {
  users?: Record<string, UserState>;
}

// ===== 日志 =====
export interface LogData {
  lines?: string[];
  file_enabled?: boolean;
}

// ===== Socket 状态（/api/sockets）=====
export interface SocketStatus {
  path: string;
  exists: boolean;
  is_socket: boolean;
  perm: string; // 权限八进制，如 "0666"
  uid: number;
  gid: number;
  kind: string; // none | trim | proxy | stale
  connectable: boolean;
  healthy: boolean;
  peer_pid: number; // 持有该 socket 的进程 PID
  detail: string;
}

export interface SocketOverview {
  listen: SocketStatus;
  upstream: SocketStatus;
  all_healthy: boolean;
  message: string;
}

// ===== 已识别用户（来自 /api/users，由数据库持久化）=====
export interface ActiveUser {
  token: string; // 脱敏后的 token 前缀
  username: string; // 【音乐用户】用户名（来自 /user/me）
  platform_username?: string; // 【平台用户】绑定的 fnOS 用户名
  is_admin?: boolean; // 绑定的平台用户是否为管理员
  ua_system?: string; // 解析出的系统（iOS / Android / Windows / macOS / Linux ...）
  ua_client?: string; // 解析出的客户端（Flutter / okhttp / 浏览器 ...）
  ua_raw?: string; // 原始 User-Agent
  first_seen_at?: string; // 首次识别时间（RFC3339）
  last_seen_at?: string; // 最后使用时间（RFC3339）
}

export interface ActiveUserList {
  users: ActiveUser[];
}

// ===== Last.fm 授权响应 =====
export interface LastFmAuthResponse {
  auth_url: string;
  token: string;
}

export interface LastFmPollResponse {
  authorized: boolean;
  expired?: boolean;
  error?: string;
  session_key?: string;
  lastfm_username?: string;
}

// ===== 空用户配置工厂 =====
export function createEmptyUser(): UserAccount {
  return {
    lastfm: {
      enabled: false,
      api_key: '',
      api_secret: '',
      session_key: '',
      username: '',
      playlist: {
        top_tracks: {
          enabled: false,
          name: 'LF {period} 最常听',
          period: 'overall',
          limit: 50,
        },
        loved_tracks: { enabled: false, name: 'LF 红心收藏', limit: 50 },
        recent_tracks: { enabled: false, name: 'LF 最近播放', limit: 50 },
      },
    },
    listenbrainz: {
      enabled: false,
      token: '',
      username: '',
      playlist: {
        daily_jams: { enabled: false, name: 'LB 每日推荐', limit: 50 },
        weekly_jams: { enabled: false, name: 'LB 每周推荐', limit: 50 },
        weekly_exploration: { enabled: false, name: 'LB 每周探索', limit: 50 },
        year_discoveries: {
          enabled: false,
          name: 'LB {year} 年度发现',
          limit: 50,
        },
        year_missed: { enabled: false, name: 'LB {year} 年度遗珠', limit: 50 },
      },
    },
  };
}
