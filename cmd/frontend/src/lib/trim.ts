// 飞牛 fnOS JS SDK 封装
// 文档: https://developer.fnnas.com/api/calling/

import { TrimApp } from '@trimjs/web-app';
import type { PlatformConfig } from '@trimjs/web-app';

let sdk: TrimApp | null = null;
let initFailed = false;

/**
 * 获取 TrimApp SDK 实例（单例）。
 * 非宿主环境下返回 null，调用方应做降级处理。
 */
export function getTrimApp(): TrimApp | null {
  if (initFailed) return null;
  if (sdk) return sdk;
  try {
    sdk = new TrimApp();
    return sdk;
  } catch {
    initFailed = true;
    return null;
  }
}

const defaultConfig: PlatformConfig = {
  theme: 'light',
  language: 'zh-CN',
  systemVersion: '',
  format: { date: 'YYYY-MM-DD', time: '24h' },
};

/**
 * 获取平台配置（主题、语言、系统版本等）。
 * 在飞牛宿主环境（Web 或移动端 App）中通过 SDK 获取，
 * 非宿主环境返回默认值。
 */
export async function getPlatformConfig(): Promise<PlatformConfig> {
  const app = getTrimApp();
  if (!app) return { ...defaultConfig };
  try {
    return await app.getPlatformConfig();
  } catch {
    return { ...defaultConfig };
  }
}

/**
 * 监听主题变化（仅 Web 宿主环境有效，移动端不支持）。
 * 返回取消监听函数。
 */
export async function onThemeChange(cb: (theme: 'dark' | 'light') => void): Promise<() => void> {
  const app = getTrimApp();
  // $on 仅在 Web 宿主环境有效（isWeb === true && isStandaloneWeb === false）
  if (!app || !app.isWeb || app.isStandaloneWeb) return () => {};
  try {
    await app.$on('os/theme', cb);
    return () => {
      app.$off('os/theme', cb);
    };
  } catch {
    return () => {};
  }
}

/**
 * 监听语言变化（仅 Web 宿主环境有效，移动端不支持）。
 * 返回取消监听函数。
 */
export async function onLanguageChange(cb: (language: string) => void): Promise<() => void> {
  const app = getTrimApp();
  if (!app || !app.isWeb || app.isStandaloneWeb) return () => {};
  try {
    await app.$on('os/language', cb);
    return () => {
      app.$off('os/language', cb);
    };
  } catch {
    return () => {};
  }
}

/**
 * 判断是否运行在飞牛宿主环境中（包括 Web 宿主和移动端 App）。
 *
 * - Web 宿主: isWeb === true && isStandaloneWeb === false
 * - 移动端 App: isWeb === false（内嵌 WebView）
 * - 独立浏览器: isWeb === true && isStandaloneWeb === true
 *
 * 宿主环境 = Web 宿主 或 移动端 App（排除独立浏览器）
 */
export function isHostEnvironment(): boolean {
  const app = getTrimApp();
  if (!app) return false;
  // 移动端 App 内嵌页面：isWeb === false
  if (!app.isWeb) return true;
  // Web 宿主环境：isWeb === true 且非独立浏览器
  return !app.isStandaloneWeb;
}

/**
 * 打开 fnOS 原生文件夹选择器，返回用户选中的目录内部路径（如 /vol1/1000/音乐）。
 * 基于 TrimApp.pickFile({ directory: true })，返回 string[]（可多选）。
 *
 * 非宿主环境（独立浏览器 / 本地调试）返回 null，调用方应降级为手动输入路径。
 */
export async function pickDirectory(): Promise<string[] | null> {
  const app = getTrimApp();
  if (!app) return null;
  try {
    const params = { directory: true } as unknown as Parameters<TrimApp['pickFile']>[0];
    const paths = await app.pickFile(params);
    if (paths && paths.length > 0) return paths;
    return null;
  } catch {
    return null;
  }
}

/**
 * 单个路径转换结果。
 */
export interface ConvertPathItem {
  path: string;
  semanticPath: string;
}

/**
 * 将内部存储路径（如 /vol1/1000/音乐）转换为语义化展示路径（如 存储空间1/admin 的文件/音乐）。
 * 基于飞牛 trim.file.convertPath API（scope: trim.file.path）。
 *
 * - 非宿主环境或调用失败时返回空对象，调用方应降级为展示原始路径。
 * - language 必填（如 zh-CN / en-US），按当前界面语言传入以保证展示一致。
 * - 返回 { 内部路径: 语义路径 } 映射，便于按原路径索引渲染。
 */
export async function convertPaths(paths: string[], language?: string): Promise<Record<string, string>> {
  const app = getTrimApp();
  const valid = (paths || []).filter((p) => !!p);
  if (!app || valid.length === 0) return {};
  const lang = language && language.trim() ? language.trim() : (await getPlatformConfig()).language || 'zh-CN';
  try {
    const res = await app.query<{ status: number; result: ConvertPathItem[] }>({
      req: 'trim.file.convertPath',
      data: { path: valid, language: lang },
    });
    const map: Record<string, string> = {};
    const result = res?.data?.result;
    if (Array.isArray(result)) {
      for (const item of result) {
        if (item?.path) map[item.path] = item.semanticPath || item.path;
      }
    }
    return map;
  } catch {
    return {};
  }
}

/**
 * 判断是否为移动端环境（移动端 App 内嵌页面）。
 * 移动端 isWeb === false，不支持 $on 事件监听。
 */
export function isMobileEnvironment(): boolean {
  const app = getTrimApp();
  return !!app && !app.isWeb;
}

/**
 * 判断是否为 Web 宿主环境（支持 $on 事件监听）。
 */
export function isWebHostEnvironment(): boolean {
  const app = getTrimApp();
  return !!app && app.isWeb && !app.isStandaloneWeb;
}

export type { PlatformConfig };
