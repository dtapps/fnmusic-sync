// i18n 初始化配置
import { init, getLocaleFromNavigator, addMessages } from 'svelte-i18n';
import zhCN from './zh-CN.json';
import en from './en.json';

// 注册翻译消息
addMessages('zh-CN', zhCN);
addMessages('en', en);

// 映射飞牛语言到 i18n locale
const languageMap: Record<string, string> = {
  'zh-CN': 'zh-CN',
  'zh-TW': 'zh-CN',
  'zh-HK': 'zh-CN',
  en: 'en',
  'en-US': 'en',
  'en-GB': 'en',
};

/**
 * 将飞牛语言代码映射到支持的 locale
 */
export function mapLanguage(lang: string): string {
  return languageMap[lang] || (lang.startsWith('en') ? 'en' : 'zh-CN');
}

// 初始化 i18n
init({
  fallbackLocale: 'zh-CN',
  initialLocale: mapLanguage(getLocaleFromNavigator() || 'zh-CN'),
});
