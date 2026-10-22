import { describe, it, expect } from 'vitest';
import { mapLanguage } from '$lib/i18n';

describe('mapLanguage（飞牛语言 → i18n locale）', () => {
  it('zh-CN 原样映射', () => {
    expect(mapLanguage('zh-CN')).toBe('zh-CN');
  });

  it('繁体中文降级到 zh-CN', () => {
    expect(mapLanguage('zh-TW')).toBe('zh-CN');
    expect(mapLanguage('zh-HK')).toBe('zh-CN');
  });

  it('英文各区域映射到 en', () => {
    expect(mapLanguage('en')).toBe('en');
    expect(mapLanguage('en-US')).toBe('en');
    expect(mapLanguage('en-GB')).toBe('en');
  });

  it('en 开头的未知变体映射到 en', () => {
    expect(mapLanguage('en-AU')).toBe('en');
  });

  it('未知语言回退 zh-CN', () => {
    expect(mapLanguage('fr')).toBe('zh-CN');
    expect(mapLanguage('ja-JP')).toBe('zh-CN');
    expect(mapLanguage('')).toBe('zh-CN');
  });
});
