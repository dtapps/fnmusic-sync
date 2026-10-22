import { describe, it, expect } from 'vitest';
import {
  cn,
  formatDateTime,
  formatBuildTime,
  formatDuration,
  errMessage,
  lastfmProfileUrl,
  listenbrainzProfileUrl,
  hasSessionKey,
} from '$lib/utils';
import { createEmptyUser, type LastFmConfig } from '$lib/types';

// 按本地时区格式化 Date，用于构造与时区无关的期望值
function localFormat(d: Date): string {
  const p = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`;
}

describe('cn', () => {
  it('合并多个类名', () => {
    expect(cn('a', 'b')).toBe('a b');
  });

  it('过滤假值（false / undefined / null）', () => {
    expect(cn('a', false, undefined, 'b', null)).toBe('a b');
  });

  it('同名工具类后者覆盖前者（twMerge）', () => {
    expect(cn('px-2', 'px-4')).toBe('px-4');
  });
});

describe('formatDateTime', () => {
  it('空串 / undefined 返回空串', () => {
    expect(formatDateTime('')).toBe('');
    expect(formatDateTime(undefined)).toBe('');
  });

  it('合法 RFC3339 转本地时间 YYYY-MM-DD HH:mm:ss', () => {
    const iso = new Date(2026, 8, 17, 14, 13, 13).toISOString();
    expect(formatDateTime(iso)).toBe('2026-09-17 14:13:13');
  });

  it('带时区偏移的 RFC3339 按本地时区格式化', () => {
    expect(formatDateTime('2026-09-17T14:13:13+08:00')).toBe(localFormat(new Date('2026-09-17T14:13:13+08:00')));
  });

  it('非法时间串原样返回', () => {
    expect(formatDateTime('not-a-time')).toBe('not-a-time');
  });
});

describe('formatBuildTime', () => {
  it('空串返回空串', () => {
    expect(formatBuildTime('')).toBe('');
  });

  it('UTC 时间串转本地时区', () => {
    expect(formatBuildTime('2026-01-02 03:04:05')).toBe(localFormat(new Date('2026-01-02 03:04:05 UTC')));
  });
});

describe('formatDuration', () => {
  it('0 / 负数 / undefined 返回空串', () => {
    expect(formatDuration(0)).toBe('');
    expect(formatDuration(-1000)).toBe('');
    expect(formatDuration(undefined)).toBe('');
  });

  it('秒级补零：m:ss', () => {
    expect(formatDuration(59000)).toBe('0:59');
    expect(formatDuration(61000)).toBe('1:01');
    expect(formatDuration(605000)).toBe('10:05');
  });

  it('超过一小时显示 h:mm:ss', () => {
    expect(formatDuration(3661000)).toBe('1:01:01');
  });

  it('毫秒四舍五入到秒', () => {
    expect(formatDuration(1499)).toBe('0:01');
    expect(formatDuration(500)).toBe('0:01');
  });
});

describe('errMessage', () => {
  it('Error 取 message', () => {
    expect(errMessage(new Error('boom'))).toBe('boom');
  });

  it('非 Error 转字符串', () => {
    expect(errMessage('plain')).toBe('plain');
    expect(errMessage(42)).toBe('42');
  });
});

describe('lastfmProfileUrl', () => {
  it('未配置用户名返回空串', () => {
    expect(lastfmProfileUrl(undefined)).toBe('');
    expect(lastfmProfileUrl('')).toBe('');
    expect(lastfmProfileUrl('   ')).toBe('');
  });

  it('正常用户名拼接主页', () => {
    expect(lastfmProfileUrl('alice')).toBe('https://www.last.fm/user/alice');
  });

  it('特殊字符编码', () => {
    expect(lastfmProfileUrl('a b')).toBe('https://www.last.fm/user/a%20b');
  });
});

describe('listenbrainzProfileUrl', () => {
  it('未配置用户名返回空串', () => {
    expect(listenbrainzProfileUrl(undefined)).toBe('');
    expect(listenbrainzProfileUrl('  ')).toBe('');
  });

  it('正常用户名拼接主页', () => {
    expect(listenbrainzProfileUrl('bob')).toBe('https://listenbrainz.org/user/bob');
  });
});

describe('hasSessionKey', () => {
  function lfm(sessionKey?: string): LastFmConfig {
    const user = createEmptyUser();
    user.lastfm.session_key = sessionKey;
    return user.lastfm;
  }

  it('有 session_key 返回 true', () => {
    expect(hasSessionKey(lfm('abc123'))).toBe(true);
  });

  it('空串 / undefined 返回 false', () => {
    expect(hasSessionKey(lfm(''))).toBe(false);
    expect(hasSessionKey(lfm(undefined))).toBe(false);
  });
});
