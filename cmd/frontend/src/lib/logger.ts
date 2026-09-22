// 全局日志模块：按配置文件 logging.level 过滤输出。
//
// 用法一（带模块 tag 的便捷 logger）：
//   import { createLogger } from '$lib/logger';
//   const log = createLogger('trim');
//   log.debug('TrimApp 初始化成功', { a: 1 });
//   log.warn('convertPaths 失败', err);
//
// 用法二（直接用核心函数，自行指定 tag）：
//   import { log } from '$lib/logger';
//   log('info', 'sync', '开始同步');
//
// 级别从配置读取（AppConfig.logging.level），支持 off/error/warn/info/debug，
// 配置未加载时默认 info。仅当「配置级别 >= 本条级别」时才会真正输出。

import { get } from 'svelte/store';
import { config } from '$lib/stores';

export type LogLevel = 'debug' | 'info' | 'warn' | 'error' | 'off';

const LEVEL_RANK: Record<LogLevel, number> = {
  off: 0,
  error: 1,
  warn: 2,
  info: 3,
  debug: 4,
};

// 从配置读取当前日志级别（配置未加载时默认 info）
function currentLevel(): LogLevel {
  const l = (get(config) as { logging?: { level?: string } } | undefined)?.logging?.level;
  return (LEVEL_RANK as Record<string, number>)[l ?? ''] !== undefined ? (l as LogLevel) : 'info';
}

// 核心：仅当配置级别 >= 本条级别时输出
export function log(level: LogLevel, tag: string, ...args: unknown[]): void {
  const msgRank = LEVEL_RANK[level] ?? 3;
  if (msgRank > (LEVEL_RANK[currentLevel()] ?? 3)) return;
  const prefix = tag ? `[${tag}:${level}]` : `[${level}]`;
  if (level === 'error') console.error(prefix, ...args);
  else if (level === 'warn') console.warn(prefix, ...args);
  else if (level === 'debug') console.debug(prefix, ...args);
  else console.log(prefix, ...args);
}

// 便捷封装：为某模块创建带固定 tag 的 logger
export function createLogger(tag: string) {
  return {
    debug: (...args: unknown[]) => log('debug', tag, ...args),
    info: (...args: unknown[]) => log('info', tag, ...args),
    warn: (...args: unknown[]) => log('warn', tag, ...args),
    error: (...args: unknown[]) => log('error', tag, ...args),
  };
}
