import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { get } from 'svelte/store';
import { locale } from 'svelte-i18n';
// 注册 i18n 消息并 init（reloadConfig 失败文案走 t() 翻译，未注册时只能拿到 key）
import '$lib/i18n';
import {
  currentUser,
  activeTab,
  setActiveTab,
  toasts,
  showToast,
  theme,
  themeMode,
  languageMode,
  platformConfig,
  applyTheme,
  resolveTheme,
  resolveLanguage,
  setThemeMode,
  setLanguageMode,
  onPlatformConfigUpdate,
  reloadConfig,
  config,
} from '$lib/stores';

// window 桩提供的最小事件派发能力（setup.ts 中挂载）
function dispatchHashChange() {
  (window as unknown as { dispatchEvent: (type: string) => void }).dispatchEvent('hashchange');
}

describe('currentUser 初始值', () => {
  it('默认本地管理员', () => {
    expect(get(currentUser)).toEqual({ uid: '0', username: 'local', isAdmin: true });
  });
});

describe('activeTab / setActiveTab', () => {
  it('初始为 users（hash 为空）', () => {
    expect(get(activeTab)).toBe('users');
  });

  it('切换 Tab 同步 store 与 URL hash', () => {
    setActiveTab('settings');
    expect(get(activeTab)).toBe('settings');
    expect(window.location.hash).toBe('settings');
  });

  it('hashchange 回调同步 store（合法值）', () => {
    window.location.hash = 'state';
    dispatchHashChange();
    expect(get(activeTab)).toBe('state');
  });

  it('hashchange 非法值回落 users', () => {
    window.location.hash = 'bogus';
    dispatchHashChange();
    expect(get(activeTab)).toBe('users');
  });
});

describe('showToast', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('追加 toast，3 秒后自动移除', () => {
    showToast('保存成功');
    expect(get(toasts)).toHaveLength(1);
    expect(get(toasts)[0]).toMatchObject({ message: '保存成功', type: 'success' });

    vi.advanceTimersByTime(3000);
    expect(get(toasts)).toHaveLength(0);
  });

  it('多条 toast 按 id 递增，独立移除', () => {
    showToast('a', 'error');
    showToast('b', 'success');
    const items = get(toasts);
    expect(items).toHaveLength(2);
    expect(items[0].type).toBe('error');
    expect(items[1].type).toBe('success');
    expect(items[1].id).toBeGreaterThan(items[0].id);

    vi.advanceTimersByTime(3000);
    expect(get(toasts)).toHaveLength(0);
  });
});

describe('主题', () => {
  beforeEach(() => {
    localStorage.clear();
    themeMode.set('auto');
    platformConfig.set({ theme: 'light', language: 'zh-CN', systemVersion: '' });
  });

  it('applyTheme 写入 theme store', () => {
    applyTheme('dark');
    expect(get(theme)).toBe('dark');
  });

  it('mode=light/dark 时固定主题', () => {
    platformConfig.set({ theme: 'dark', language: 'zh-CN', systemVersion: '' });
    themeMode.set('light');
    resolveTheme();
    expect(get(theme)).toBe('light');
  });

  it('mode=auto 跟随平台配置', () => {
    themeMode.set('auto');
    platformConfig.set({ theme: 'dark', language: 'zh-CN', systemVersion: '' });
    resolveTheme();
    expect(get(theme)).toBe('dark');
  });

  it('setThemeMode 持久化到 localStorage 并立即应用', () => {
    setThemeMode('dark');
    expect(get(themeMode)).toBe('dark');
    expect(localStorage.getItem('fnmusic-theme-mode')).toBe('dark');
    expect(get(theme)).toBe('dark');
  });
});

describe('语言', () => {
  beforeEach(() => {
    localStorage.clear();
    languageMode.set('auto');
    platformConfig.set({ theme: 'light', language: 'zh-CN', systemVersion: '' });
  });

  it('mode=auto 跟随平台配置（en-US → en）', () => {
    languageMode.set('auto');
    platformConfig.set({ theme: 'light', language: 'en-US', systemVersion: '' });
    resolveLanguage();
    expect(get(locale)).toBe('en');
  });

  it('mode=auto 平台中文映射 zh-CN', () => {
    languageMode.set('auto');
    platformConfig.set({ theme: 'light', language: 'zh-TW', systemVersion: '' });
    resolveLanguage();
    expect(get(locale)).toBe('zh-CN');
  });

  it('mode=en 固定英文', () => {
    languageMode.set('en');
    platformConfig.set({ theme: 'light', language: 'zh-CN', systemVersion: '' });
    resolveLanguage();
    expect(get(locale)).toBe('en');
  });

  it('setLanguageMode 持久化到 localStorage 并立即生效', () => {
    setLanguageMode('en');
    expect(get(languageMode)).toBe('en');
    expect(localStorage.getItem('fnmusic-language-mode')).toBe('en');
    expect(get(locale)).toBe('en');
  });
});

describe('onPlatformConfigUpdate', () => {
  it('同步平台配置并重算主题与语言', () => {
    onPlatformConfigUpdate({ theme: 'dark', language: 'en-US', systemVersion: '1.2.3' });
    expect(get(platformConfig).systemVersion).toBe('1.2.3');
    expect(get(theme)).toBe('dark');
    expect(get(locale)).toBe('en');
  });
});

describe('reloadConfig', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('成功时更新 config store', async () => {
    const cfg = { logging: { level: 'debug' } };
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, json: async () => cfg }));
    await reloadConfig();
    expect(get(config)).toEqual(cfg);
  });

  it('失败时弹错误 toast 且不动 config', async () => {
    config.set({ logging: { level: 'info' } });
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new Error('network down')));
    await reloadConfig();
    expect(get(config)).toEqual({ logging: { level: 'info' } });
    const items = get(toasts);
    expect(items).toHaveLength(1);
    expect(items[0].type).toBe('error');
    expect(items[0].message).toContain('network down');
  });
});
