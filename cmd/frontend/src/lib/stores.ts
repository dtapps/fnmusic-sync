// 飞牛音乐 Scrobble 代理 - Svelte Stores 状态管理

import { writable } from 'svelte/store';
import { _, locale } from 'svelte-i18n';
import { get } from 'svelte/store';
import { errMessage } from './utils';
import type { CurrentUser, AppConfig, VersionInfo, StateData } from './types';
import { getConfig } from './api';

// 当前用户身份
export const currentUser = writable<CurrentUser>({
  uid: '0',
  username: 'local',
  isAdmin: true,
});

// 版本信息
export const versionInfo = writable<VersionInfo | null>(null);

// 完整配置
export const config = writable<AppConfig>({});

// 运行状态
export const state = writable<StateData>({});

// Toast 通知
export interface ToastItem {
  id: number;
  message: string;
  type: 'success' | 'error';
}

export const toasts = writable<ToastItem[]>([]);

let toastId = 0;
export function showToast(message: string, type: 'success' | 'error' = 'success') {
  const id = ++toastId;
  toasts.update((items) => [...items, { id, message, type }]);
  setTimeout(() => {
    toasts.update((items) => items.filter((t) => t.id !== id));
  }, 3000);
}

// 当前激活的 Tab（从 URL hash 恢复，刷新后保持当前页）
const validTabs = ['users', 'settings', 'state', 'logs', 'users_list'];
function getTabFromHash(): string {
  if (typeof window === 'undefined') return 'users';
  const hash = window.location.hash.replace('#', '');
  return validTabs.includes(hash) ? hash : 'users';
}
export const activeTab = writable<string>(getTabFromHash());

// 切换 Tab 时同步到 URL hash
export function setActiveTab(tab: string) {
  activeTab.set(tab);
  if (typeof window !== 'undefined') {
    window.location.hash = tab;
  }
}

// 监听浏览器前进/后退按钮
if (typeof window !== 'undefined') {
  window.addEventListener('hashchange', () => {
    const tab = getTabFromHash();
    const current = get(activeTab);
    if (tab !== current) {
      activeTab.set(tab);
    }
  });
}

// ===== 主题与语言 =====

export type Theme = 'dark' | 'light';
export type ThemeMode = 'auto' | 'light' | 'dark';
export type LanguageMode = 'auto' | 'zh-CN' | 'en';

const LS_THEME_KEY = 'fnmusic-theme-mode';
const LS_LANG_KEY = 'fnmusic-language-mode';

// 从 localStorage 读取用户偏好
function readStored(key: string, fallback: string): string {
  if (typeof localStorage === 'undefined') return fallback;
  return localStorage.getItem(key) || fallback;
}

// 主题模式 store（用户选择：auto / light / dark）
export const themeMode = writable<ThemeMode>(readStored(LS_THEME_KEY, 'auto') as ThemeMode);

// 语言模式 store（用户选择：auto / zh-CN / en）
export const languageMode = writable<LanguageMode>(readStored(LS_LANG_KEY, 'auto') as LanguageMode);

// 实际生效的主题
export const theme = writable<Theme>('light');

// 飞牛平台配置 store（SDK 读取的宿主配置）
export interface PlatformConfig {
  theme: Theme;
  language: string;
  systemVersion: string;
}

export const platformConfig = writable<PlatformConfig>({
  theme: 'light',
  language: 'zh-CN',
  systemVersion: '',
});

// 应用主题到 DOM
export function applyTheme(t: Theme) {
  if (typeof document !== 'undefined') {
    document.documentElement.dataset.theme = t;
  }
  theme.set(t);
}

/**
 * 根据用户选择的 themeMode 和宿主配置，计算并应用实际主题。
 * - auto: 跟随飞牛宿主 SDK 的配置（非宿主环境用浅色）
 * - light/dark: 固定使用对应主题
 */
export function resolveTheme() {
  const mode = get(themeMode);
  const cfg = get(platformConfig);
  const effective: Theme = mode === 'auto' ? cfg.theme : mode;
  applyTheme(effective);
}

/**
 * 根据用户选择的 languageMode 和宿主配置，设置 svelte-i18n locale。
 * - auto: 跟随飞牛宿主 SDK 的配置（非宿主环境用浏览器语言）
 * - zh-CN/en: 固定使用对应语言
 */
export function resolveLanguage() {
  const mode = get(languageMode);
  if (mode === 'auto') {
    const cfg = get(platformConfig);
    locale.set(mapLanguageForStore(cfg.language));
  } else {
    locale.set(mode);
  }
}

// 语言映射辅助函数
function mapLanguageForStore(lang: string): string {
  if (!lang) return 'zh-CN';
  if (lang.startsWith('en')) return 'en';
  if (lang.startsWith('zh')) return 'zh-CN';
  return 'zh-CN';
}

/**
 * 用户手动切换主题模式。
 * 持久化到 localStorage 并立即应用。
 */
export function setThemeMode(mode: ThemeMode) {
  themeMode.set(mode);
  if (typeof localStorage !== 'undefined') {
    localStorage.setItem(LS_THEME_KEY, mode);
  }
  resolveTheme();
}

/**
 * 用户手动切换语言模式。
 * 持久化到 localStorage 并立即应用。
 */
export function setLanguageMode(mode: LanguageMode) {
  languageMode.set(mode);
  if (typeof localStorage !== 'undefined') {
    localStorage.setItem(LS_LANG_KEY, mode);
  }
  resolveLanguage();
}

/**
 * 宿主环境配置更新后，如果用户选择 auto，则重新计算实际值。
 */
export function onPlatformConfigUpdate(cfg: PlatformConfig) {
  platformConfig.set(cfg);
  resolveTheme();
  resolveLanguage();
}

// 重新从后端拉取配置并更新 config store
export async function reloadConfig() {
  try {
    const cfg = await getConfig();
    config.set(cfg);
  } catch (e) {
    const msg = get(_)('common.load_failed', { values: { message: errMessage(e) } });
    showToast(msg, 'error');
  }
}
