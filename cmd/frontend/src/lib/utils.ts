// 工具函数

import { clsx, type ClassValue } from 'clsx';
import { twMerge } from 'tailwind-merge';
import type { LastFmConfig } from './types';

// Shadcn 标准的 cn 函数
export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

// 格式化构建时间（UTC → 本地时区）
export function formatBuildTime(timeStr: string): string {
  if (!timeStr) return '';
  const d = new Date(timeStr + ' UTC');
  if (isNaN(d.getTime())) return timeStr;
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
}

// 从 unknown 异常中安全提取错误信息（strict 模式下 catch 变量类型为 unknown）
export function errMessage(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

// 判断 Last.fm 是否有 session_key
export function hasSessionKey(lfm: LastFmConfig): boolean {
  return Boolean(lfm?.session_key && lfm.session_key.length > 0);
}
