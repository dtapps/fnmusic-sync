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
  top_tracks?: LastFmTopTracks;
  loved_tracks?: LastFmLovedTracks;
  recent_tracks?: LastFmRecentTracks;
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
  playlist?: LastFmPlaylist;
}

// ===== ListenBrainz 配置 =====
export interface ListenBrainzPlaylist {
  daily_jams?: LBPlaylistItem;
  weekly_jams?: LBPlaylistItem;
  weekly_exploration?: LBPlaylistItem;
  year_discoveries?: LBPlaylistItem;
  year_missed?: LBPlaylistItem;
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
  playlist?: ListenBrainzPlaylist;
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
