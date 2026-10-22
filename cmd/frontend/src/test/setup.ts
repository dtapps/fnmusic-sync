// vitest 环境桩：node 环境没有浏览器全局对象，而模块常在导入期读取它们，不补桩会加载即崩。
// 运行时机：setupFiles 在任何测试文件导入之前执行。

// ===== localStorage 桩（内存实现）=====
const memory = new Map<string, string>();

const localStorageStub: Storage = {
  get length() {
    return memory.size;
  },
  clear() {
    memory.clear();
  },
  getItem(key: string) {
    return memory.get(key) ?? null;
  },
  key(index: number) {
    return Array.from(memory.keys())[index] ?? null;
  },
  removeItem(key: string) {
    memory.delete(key);
  },
  setItem(key: string, value: string) {
    memory.set(key, value);
  },
};

Object.defineProperty(globalThis, 'localStorage', {
  value: localStorageStub,
  configurable: true,
  writable: true,
});

// ===== window 桩（最小事件系统）=====
// api.ts 导入期读 window.BaseURL；stores.ts 读 window.location.hash 并监听 hashchange。
type Listener = () => void;
const windowListeners = new Map<string, Set<Listener>>();

const windowStub = {
  // 与 Go 模板注入的 window.BaseURL 对齐（api.ts 据此拼 API 前缀）
  BaseURL: '/app/fnmusic-sync',
  location: {
    hash: '',
    // svelte-i18n getLocaleFromNavigator 会读 location.search（query 参数覆盖语言）
    search: '',
  },
  // svelte-i18n 读 window.navigator（不是全局 navigator）
  navigator: { language: 'zh-CN', languages: ['zh-CN'] },
  addEventListener(type: string, cb: Listener) {
    if (!windowListeners.has(type)) windowListeners.set(type, new Set());
    windowListeners.get(type)!.add(cb);
  },
  removeEventListener(type: string, cb: Listener) {
    windowListeners.get(type)?.delete(cb);
  },
  dispatchEvent(type: string) {
    windowListeners.get(type)?.forEach((cb) => cb());
  },
};

Object.defineProperty(globalThis, 'window', {
  value: windowStub,
  configurable: true,
  writable: true,
});

// ===== navigator 桩 =====
// node 无 navigator；i18n 'auto' 模式解析系统语言时需要 navigator.language
Object.defineProperty(globalThis, 'navigator', {
  value: { language: 'zh-CN' },
  configurable: true,
  writable: true,
});

// ===== document 桩（最小 DOM）=====
// svelte-i18n 的 locale store 变更时写 document.documentElement 的 lang 属性；
// stores.applyTheme 写 dataset.theme。没有 document 会 ReferenceError。
const documentStub = {
  documentElement: {
    dataset: {} as Record<string, string>,
    setAttribute: (_key: string, _value: string) => {},
  },
};

Object.defineProperty(globalThis, 'document', {
  value: documentStub,
  configurable: true,
  writable: true,
});

// 屏蔽 debug 噪音；console.error 保留可见，避免掩盖真实问题
console.debug = () => {};
