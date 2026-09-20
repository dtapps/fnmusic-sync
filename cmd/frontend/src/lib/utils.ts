// 工具函数

import { clsx, type ClassValue } from 'clsx';
import { twMerge } from 'tailwind-merge';
import type { LastFmConfig } from './types';

// Shadcn 标准的 cn 函数
export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

// 统一的日期时间格式：YYYY-MM-DD HH:mm:ss（本地时区）。
function pad2(n: number): string {
  return String(n).padStart(2, '0');
}

function formatDate(d: Date): string {
  return `${d.getFullYear()}-${pad2(d.getMonth() + 1)}-${pad2(d.getDate())} ${pad2(d.getHours())}:${pad2(d.getMinutes())}:${pad2(d.getSeconds())}`;
}

// 格式化 RFC3339 时间（如 2026-09-17T14:13:13+08:00）→ "2026-09-17 14:13:13"（本地时区）。
export function formatDateTime(timeStr?: string): string {
  if (!timeStr) return '';
  const d = new Date(timeStr);
  if (isNaN(d.getTime())) return timeStr;
  return formatDate(d);
}

// 格式化构建时间（UTC → 本地时区）
export function formatBuildTime(timeStr: string): string {
  if (!timeStr) return '';
  const d = new Date(timeStr + ' UTC');
  if (isNaN(d.getTime())) return timeStr;
  return formatDate(d);
}

// 格式化时长（毫秒）→ "m:ss"（超过一小时显示 "h:mm:ss"）。0/负数返回空串。
export function formatDuration(ms?: number): string {
  if (!ms || ms <= 0) return '';
  const totalSec = Math.round(ms / 1000);
  const h = Math.floor(totalSec / 3600);
  const m = Math.floor((totalSec % 3600) / 60);
  const s = totalSec % 60;
  const ss = String(s).padStart(2, '0');
  if (h > 0) {
    const mm = String(m).padStart(2, '0');
    return `${h}:${mm}:${ss}`;
  }
  return `${m}:${ss}`;
}

// 从 unknown 异常中安全提取错误信息（strict 模式下 catch 变量类型为 unknown）
export function errMessage(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

// ===== 外部音乐平台用户主页 =====

// Last.fm 用户主页（未配置用户名时返回空串）
export function lastfmProfileUrl(username?: string): string {
  const u = username?.trim();
  return u ? `https://www.last.fm/user/${encodeURIComponent(u)}` : '';
}

// ListenBrainz 用户主页（未配置用户名时返回空串）
export function listenbrainzProfileUrl(username?: string): string {
  const u = username?.trim();
  return u ? `https://listenbrainz.org/user/${encodeURIComponent(u)}` : '';
}

// 判断 Last.fm 是否有 session_key
export function hasSessionKey(lfm: LastFmConfig): boolean {
  return Boolean(lfm?.session_key && lfm.session_key.length > 0);
}
