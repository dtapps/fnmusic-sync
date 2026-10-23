import { describe, it, expect, vi } from 'vitest';

// trim.ts 里有模块级状态（SDK 单例、initFailed、未授权禁用标志、并发合并句柄），
// 所以每个用例都要 resetModules + 重新动态导入，才能拿到干净的状态。

// 假 SDK 实例的字段形态完全由各用例决定，这里只需要一个可自由覆盖的字典类型。
type AnyApp = Record<string, unknown>;

/** 构造一个 errno 错误（trim.ts 用 errno 判断是否缺少权限）。 */
function errnoError(message: string, errno: number): Error {
  const err = new Error(message) as Error & { errno: number };
  err.errno = errno;

  return err;
}

/** 构造一个可用的 TrimApp 假实例，可局部覆盖字段。 */
function makeApp(over: AnyApp = {}): AnyApp {
  return {
    isWeb: true,
    isStandaloneWeb: false,
    getPlatformConfig: vi.fn(async () => ({
      theme: 'dark',
      language: 'en-US',
      systemVersion: '1.0.0',
      format: { date: 'YYYY/MM/DD', time: '12h' },
    })),
    $on: vi.fn(async () => {}),
    $off: vi.fn(),
    query: vi.fn(async () => ({
      data: {
        result: [
          {
            path: '/vol1/1000/音乐',
            semanticPath: '存储空间1/admin 的文件/音乐',
          },
        ],
      },
    })),
    pickFile: vi.fn(async () => ['/vol1/1000/音乐']),
    ...over,
  };
}

/** 重新加载 trim.ts（可选注入 SDK 行为）。 */
async function loadTrim(opts: { ctorThrows?: boolean; app?: AnyApp } = {}) {
  vi.resetModules();

  const app = opts.app ?? makeApp();

  // 必须用 class：trim.ts 里是 `new TrimApp()`，箭头函数不能被 new
  vi.doMock('@trimjs/web-app', () => ({
    TrimApp: class {
      constructor() {
        if (opts.ctorThrows) throw new Error('not in fnOS host');

        return app as never;
      }
    },
  }));

  const mod = await import('$lib/trim');

  return { mod, app };
}

describe('getTrimApp', () => {
  it('首次调用创建实例，之后复用同一个（单例）', async () => {
    const { mod, app } = await loadTrim();

    const a = mod.getTrimApp();
    const b = mod.getTrimApp();

    expect(a).toBe(app);
    expect(a).toBe(b);
  });

  it('构造失败返回 null，且不再重复尝试（initFailed 记忆化）', async () => {
    const { mod } = await loadTrim({ ctorThrows: true });

    expect(mod.getTrimApp()).toBeNull();
    // 第二次仍为 null，且不会靠异常扩散影响调用方
    expect(mod.getTrimApp()).toBeNull();
  });
});

describe('getPlatformConfig', () => {
  it('非宿主环境返回默认配置', async () => {
    const { mod } = await loadTrim({ ctorThrows: true });

    const cfg = await mod.getPlatformConfig();

    expect(cfg.theme).toBe('light');
    expect(cfg.language).toBe('zh-CN');
    expect(cfg.format).toEqual({ date: 'YYYY-MM-DD', time: '24h' });
  });

  it('宿主环境透传 SDK 返回的配置', async () => {
    const { mod } = await loadTrim();

    const cfg = await mod.getPlatformConfig();

    expect(cfg.theme).toBe('dark');
    expect(cfg.language).toBe('en-US');
    expect(cfg.systemVersion).toBe('1.0.0');
  });

  it('SDK 抛错时降级为默认配置', async () => {
    const app = makeApp({
      getPlatformConfig: vi.fn(async () => {
        throw new Error('boom');
      }),
    });
    const { mod } = await loadTrim({ app });

    const cfg = await mod.getPlatformConfig();

    expect(cfg.theme).toBe('light');
    expect(cfg.language).toBe('zh-CN');
  });
});

describe('onThemeChange / onLanguageChange', () => {
  it('Web 宿主环境订阅 os/theme，返回的取消函数会 $off', async () => {
    const { mod, app } = await loadTrim();

    const cb = vi.fn();
    const off = await mod.onThemeChange(cb);

    expect(app.$on).toHaveBeenCalledWith('os/theme', cb);

    off();
    expect(app.$off).toHaveBeenCalledWith('os/theme', cb);
  });

  it('Web 宿主环境订阅 os/language', async () => {
    const { mod, app } = await loadTrim();

    const cb = vi.fn();
    const off = await mod.onLanguageChange(cb);

    expect(app.$on).toHaveBeenCalledWith('os/language', cb);

    off();
    expect(app.$off).toHaveBeenCalledWith('os/language', cb);
  });

  it('非宿主环境不订阅，返回空操作', async () => {
    const { mod } = await loadTrim({ ctorThrows: true });

    const off = await mod.onThemeChange(vi.fn());
    expect(typeof off).toBe('function');
    expect(() => off()).not.toThrow();
  });

  it('独立浏览器（isStandaloneWeb）不订阅', async () => {
    const app = makeApp({ isStandaloneWeb: true });
    const { mod } = await loadTrim({ app });

    await mod.onThemeChange(vi.fn());
    await mod.onLanguageChange(vi.fn());

    expect(app.$on).not.toHaveBeenCalled();
  });

  it('移动端（isWeb=false）不订阅', async () => {
    const app = makeApp({ isWeb: false });
    const { mod } = await loadTrim({ app });

    await mod.onThemeChange(vi.fn());

    expect(app.$on).not.toHaveBeenCalled();
    expect(mod.isMobileEnvironment()).toBe(true);
    expect(mod.isWebHostEnvironment()).toBe(false);
  });

  it('$on 抛错时返回空操作，不把异常抛给调用方', async () => {
    const app = makeApp({
      $on: vi.fn(async () => {
        throw new Error('subscribe failed');
      }),
    });
    const { mod } = await loadTrim({ app });

    const off = await mod.onThemeChange(vi.fn());

    expect(typeof off).toBe('function');
    expect(() => off()).not.toThrow();
  });
});

describe('环境判断', () => {
  it('无 SDK 时三者均为 false', async () => {
    const { mod } = await loadTrim({ ctorThrows: true });

    expect(mod.isHostEnvironment()).toBe(false);
    expect(mod.isMobileEnvironment()).toBe(false);
    expect(mod.isWebHostEnvironment()).toBe(false);
  });

  it('Web 宿主：是宿主、非独立浏览器', async () => {
    const { mod } = await loadTrim();

    expect(mod.isHostEnvironment()).toBe(true);
    expect(mod.isWebHostEnvironment()).toBe(true);
    expect(mod.isMobileEnvironment()).toBe(false);
  });

  it('独立浏览器：不是宿主环境', async () => {
    const app = makeApp({ isStandaloneWeb: true });
    const { mod } = await loadTrim({ app });

    expect(mod.isHostEnvironment()).toBe(false);
    expect(mod.isWebHostEnvironment()).toBe(false);
    expect(mod.isMobileEnvironment()).toBe(false);
  });

  it('移动端 App：是宿主、但不是 Web 宿主', async () => {
    const app = makeApp({ isWeb: false });
    const { mod } = await loadTrim({ app });

    expect(mod.isHostEnvironment()).toBe(true);
    expect(mod.isWebHostEnvironment()).toBe(false);
  });
});

describe('pickDirectory', () => {
  it('无 SDK 返回 null', async () => {
    const { mod } = await loadTrim({ ctorThrows: true });

    expect(await mod.pickDirectory()).toBeNull();
  });

  it('返回选中的目录列表', async () => {
    const { mod, app } = await loadTrim();

    const paths = await mod.pickDirectory();

    expect(paths).toEqual(['/vol1/1000/音乐']);
    expect(app.pickFile).toHaveBeenCalledWith({ directory: true });
  });

  it('用户取消（空数组）返回 null', async () => {
    const app = makeApp({ pickFile: vi.fn(async () => []) });
    const { mod } = await loadTrim({ app });

    expect(await mod.pickDirectory()).toBeNull();
  });

  it('未授权（errno 10000002）后永久停用，不再重复调用 SDK', async () => {
    const pickFile = vi.fn(async () => {
      throw errnoError('no permission', 10000002);
    });
    const app = makeApp({ pickFile });
    const { mod } = await loadTrim({ app });

    expect(await mod.pickDirectory()).toBeNull();
    expect(pickFile).toHaveBeenCalledTimes(1);

    // 第二次直接返回 null，避免每次点击都刷日志 / 弹失败
    expect(await mod.pickDirectory()).toBeNull();
    expect(pickFile).toHaveBeenCalledTimes(1);
  });

  it('其它错误仅返回 null，不禁用（下次仍可重试）', async () => {
    const pickFile = vi.fn(async () => {
      throw new Error('transient');
    });
    const app = makeApp({ pickFile });
    const { mod } = await loadTrim({ app });

    expect(await mod.pickDirectory()).toBeNull();
    expect(await mod.pickDirectory()).toBeNull();

    expect(pickFile).toHaveBeenCalledTimes(2);
  });
});

describe('convertPaths', () => {
  it('无 SDK 返回空对象', async () => {
    const { mod } = await loadTrim({ ctorThrows: true });

    expect(await mod.convertPaths(['/vol1/a'])).toEqual({});
  });

  it('空列表 / 全为空串时直接返回，不打 SDK', async () => {
    const { mod, app } = await loadTrim();

    expect(await mod.convertPaths([])).toEqual({});
    expect(await mod.convertPaths(['', ''] as string[])).toEqual({});

    expect(app.query).not.toHaveBeenCalled();
  });

  it('解析 data.result 信封，path → semanticPath', async () => {
    const { mod, app } = await loadTrim();

    const map = await mod.convertPaths(['/vol1/1000/音乐'], 'zh-CN');

    expect(map).toEqual({ '/vol1/1000/音乐': '存储空间1/admin 的文件/音乐' });
    expect(app.query).toHaveBeenCalledWith({
      req: 'trim.file.convertPath',
      data: { path: ['/vol1/1000/音乐'], language: 'zh-CN' },
    });
  });

  it('兼容 result / data.data.result / payload.result / data 数组等信封', async () => {
    const envelopes: AnyApp[] = [
      { result: [{ path: '/a', semanticPath: 'A' }] },
      { data: { data: { result: [{ path: '/a', semanticPath: 'A' }] } } },
      { payload: { result: [{ path: '/a', semanticPath: 'A' }] } },
      { data: [{ path: '/a', semanticPath: 'A' }] },
    ];

    for (const env of envelopes) {
      const app = makeApp({ query: vi.fn(async () => env) });
      const { mod } = await loadTrim({ app });

      expect(await mod.convertPaths(['/a'], 'zh-CN')).toEqual({ '/a': 'A' });
    }
  });

  it('缺 semanticPath 时回退为原路径；缺 path 的条目跳过', async () => {
    const app = makeApp({
      query: vi.fn(async () => ({
        data: { result: [{ path: '/a' }, { semanticPath: 'no-path' }, null] },
      })),
    });
    const { mod } = await loadTrim({ app });

    expect(await mod.convertPaths(['/a'], 'zh-CN')).toEqual({ '/a': '/a' });
  });

  it('language 缺省时取平台配置语言', async () => {
    const { mod, app } = await loadTrim();

    await mod.convertPaths(['/a']);

    expect(app.query).toHaveBeenCalledWith({
      req: 'trim.file.convertPath',
      data: { path: ['/a'], language: 'en-US' },
    });
  });

  it('language 为空白时同样回退到平台配置', async () => {
    const { mod, app } = await loadTrim();

    await mod.convertPaths(['/a'], '   ');

    expect(app.query).toHaveBeenCalledWith(
      expect.objectContaining({
        data: expect.objectContaining({ language: 'en-US' }),
      }),
    );
  });

  it('未授权（errno 10000002）后永久停用', async () => {
    const query = vi.fn(async () => {
      throw errnoError('forbidden', 10000002);
    });
    const app = makeApp({ query });
    const { mod } = await loadTrim({ app });

    expect(await mod.convertPaths(['/a'], 'zh-CN')).toEqual({});
    expect(await mod.convertPaths(['/b'], 'zh-CN')).toEqual({});

    // 第二次不再请求 SDK
    expect(query).toHaveBeenCalledTimes(1);
  });

  it('普通错误不禁用，下次仍会重试', async () => {
    const query = vi.fn(async () => {
      throw new Error('transient');
    });
    const app = makeApp({ query });
    const { mod } = await loadTrim({ app });

    await mod.convertPaths(['/a'], 'zh-CN');
    await mod.convertPaths(['/a'], 'zh-CN');

    expect(query).toHaveBeenCalledTimes(2);
  });

  it('并发调用合并为一次请求', async () => {
    let resolveQuery: (v: unknown) => void = () => {};
    const query = vi.fn(
      () =>
        new Promise((resolve) => {
          resolveQuery = resolve;
        }),
    );
    const app = makeApp({ query });
    const { mod } = await loadTrim({ app });

    const p1 = mod.convertPaths(['/a'], 'zh-CN');
    const p2 = mod.convertPaths(['/a'], 'zh-CN');

    expect(query).toHaveBeenCalledTimes(1);

    resolveQuery({ data: { result: [{ path: '/a', semanticPath: 'A' }] } });

    const [r1, r2] = await Promise.all([p1, p2]);

    expect(r1).toEqual({ '/a': 'A' });
    expect(r2).toEqual({ '/a': 'A' });
  });
});
