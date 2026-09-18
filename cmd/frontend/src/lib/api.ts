// 飞牛音乐 Scrobble 代理 - API 封装
import type { VersionInfo, AppConfig, StateData, UserAccount, LogData, ActiveUserList, SocketOverview } from './types';

// 从 Go 模板注入的 window.BaseURL 获取前缀路径
const BASE_URL = window.BaseURL || '/app/fnmusic-sync';
const API = `${BASE_URL}/api`;

/**
 * 通用 API 调用函数
 */
export async function apiCall<T = unknown>(path: string, method: string = 'GET', body?: unknown): Promise<T> {
  const opts: RequestInit = { method, headers: {} };
  if (body) {
    opts.headers = { 'Content-Type': 'application/json' };
    opts.body = JSON.stringify(body);
  }
  const resp = await fetch(`${API}${path}`, opts);
  const data = await resp.json();
  if (!resp.ok) {
    throw new Error(data.error || `HTTP ${resp.status}`);
  }
  return data as T;
}

// ===== API 方法 =====

export function getVersion() {
  return apiCall<VersionInfo>('/version');
}

export function getCurrentUser() {
  return apiCall<{ uid: string; username: string; isAdmin: boolean }>('/me');
}

export function getConfig() {
  return apiCall<AppConfig>('/config');
}

export function getState() {
  return apiCall<StateData>('/state');
}

export function getLogs() {
  return apiCall<LogData>('/logs');
}

// 返回两个固定 socket（监听 / 上游）的状态与是否正常判断
export function getSockets() {
  return apiCall<SocketOverview>('/sockets');
}

// 返回当前通过 token 识别的活跃用户列表（仅管理员可访问）
export function getActiveUsers() {
  return apiCall<ActiveUserList>('/users');
}

export function saveUser(name: string, data: UserAccount) {
  return apiCall(`/user/${encodeURIComponent(name)}`, 'POST', data);
}

export function deleteUser(name: string) {
  return apiCall(`/user/${encodeURIComponent(name)}`, 'DELETE');
}

export function saveSettings(settings: AppConfig) {
  return apiCall('/settings', 'PUT', settings);
}

export function startLastFmAuth(apiKey: string, apiSecret: string, username: string) {
  return apiCall<{ auth_url: string; token: string }>('/lastfm/auth', 'POST', {
    api_key: apiKey,
    api_secret: apiSecret,
    username,
  });
}

export function pollLastFmAuth(apiKey: string, apiSecret: string, username: string) {
  return apiCall<{
    authorized: boolean;
    expired?: boolean;
    error?: string;
    session_key?: string;
    lastfm_username?: string;
  }>('/lastfm/poll', 'POST', {
    api_key: apiKey,
    api_secret: apiSecret,
    username,
  });
}

// ===== 升级相关 API =====

export function checkUpgrade() {
  return apiCall<{
    current_version: string;
    latest_version: string;
    has_update: boolean;
    repo_source: string;
  }>('/upgrade/check');
}

export function doUpgrade() {
  return apiCall<{ ok: boolean; latest_version: string; message: string }>('/upgrade', 'POST');
}

export { BASE_URL };
