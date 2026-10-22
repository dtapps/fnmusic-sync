import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { log, createLogger } from '$lib/logger';
import { config } from '$lib/stores';

describe('log 级别过滤', () => {
  beforeEach(() => {
    // 配置未加载时默认 info
    config.set({});
    vi.spyOn(console, 'log').mockImplementation(() => {});
    vi.spyOn(console, 'warn').mockImplementation(() => {});
    vi.spyOn(console, 'error').mockImplementation(() => {});
    vi.spyOn(console, 'debug').mockImplementation(() => {});
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('默认 info：info/warn/error 输出，debug 静默', () => {
    log('info', 'tag', 'x');
    log('warn', 'tag', 'x');
    log('error', 'tag', 'x');
    log('debug', 'tag', 'x');
    expect(console.log).toHaveBeenCalledWith('[tag:info]', 'x');
    expect(console.warn).toHaveBeenCalledWith('[tag:warn]', 'x');
    expect(console.error).toHaveBeenCalledWith('[tag:error]', 'x');
    expect(console.debug).not.toHaveBeenCalled();
  });

  it('off：所有级别静默', () => {
    config.set({ logging: { level: 'off' } });
    log('debug', 'tag', 'x');
    log('info', 'tag', 'x');
    log('warn', 'tag', 'x');
    log('error', 'tag', 'x');
    expect(console.log).not.toHaveBeenCalled();
    expect(console.warn).not.toHaveBeenCalled();
    expect(console.error).not.toHaveBeenCalled();
    expect(console.debug).not.toHaveBeenCalled();
  });

  it('error：仅 error 输出', () => {
    config.set({ logging: { level: 'error' } });
    log('info', 'tag', 'x');
    log('warn', 'tag', 'x');
    log('error', 'tag', 'x');
    expect(console.log).not.toHaveBeenCalled();
    expect(console.warn).not.toHaveBeenCalled();
    expect(console.error).toHaveBeenCalledWith('[tag:error]', 'x');
  });

  it('debug：所有级别输出', () => {
    config.set({ logging: { level: 'debug' } });
    log('debug', 'tag', 'x');
    log('info', 'tag', 'x');
    log('warn', 'tag', 'x');
    log('error', 'tag', 'x');
    expect(console.debug).toHaveBeenCalledWith('[tag:debug]', 'x');
    expect(console.log).toHaveBeenCalledWith('[tag:info]', 'x');
    expect(console.warn).toHaveBeenCalledWith('[tag:warn]', 'x');
    expect(console.error).toHaveBeenCalledWith('[tag:error]', 'x');
  });

  it('非法级别回落 info', () => {
    config.set({ logging: { level: 'bogus' } });
    log('debug', 'tag', 'x');
    log('info', 'tag', 'x');
    expect(console.debug).not.toHaveBeenCalled();
    expect(console.log).toHaveBeenCalledWith('[tag:info]', 'x');
  });

  it('空 tag 时前缀省略 tag', () => {
    log('info', '', 'x');
    expect(console.log).toHaveBeenCalledWith('[info]', 'x');
  });
});

describe('createLogger', () => {
  beforeEach(() => {
    config.set({});
    vi.spyOn(console, 'log').mockImplementation(() => {});
    vi.spyOn(console, 'warn').mockImplementation(() => {});
    vi.spyOn(console, 'error').mockImplementation(() => {});
    vi.spyOn(console, 'debug').mockImplementation(() => {});
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('固定 tag 前缀', () => {
    const trimLog = createLogger('trim');
    trimLog.info('hello', { a: 1 });
    expect(console.log).toHaveBeenCalledWith('[trim:info]', 'hello', { a: 1 });
  });

  it('warn/error 走对应 console 方法', () => {
    const trimLog = createLogger('trim');
    trimLog.warn('w');
    trimLog.error('e');
    expect(console.warn).toHaveBeenCalledWith('[trim:warn]', 'w');
    expect(console.error).toHaveBeenCalledWith('[trim:error]', 'e');
  });
});
