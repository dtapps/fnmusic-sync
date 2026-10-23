import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest';
import {
  apiCall,
  BASE_URL,
  getVersion,
  getPlaylist,
  getSyncLog,
  saveUser,
  deleteUser,
  saveSettings,
  saveLibrary,
  startLastFmAuth,
  pollLastFmAuth,
  checkUpgrade,
  doUpgrade,
  getSockets,
  getActiveUsers,
} from '$lib/api';

// fetch 桩：按需返回指定状态码与 JSON 体，并记录调用参数
function stubFetch(status = 200, payload: unknown = { ok: true }) {
  const fn = vi.fn(async () => ({
    ok: status >= 200 && status < 300,
    status,
    json: async () => payload,
  })) as unknown as typeof fetch;

  globalThis.fetch = fn;

  return fn as unknown as ReturnType<typeof vi.fn>;
}

describe('BASE_URL', () => {
  it('取 window.BaseURL（Go 模板注入），保证子路径部署可用', () => {
    expect(BASE_URL).toBe('/app/fnmusic-sync');
  });
});

describe('apiCall', () => {
  let fetchMock: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    fetchMock = stubFetch();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('默认 GET，拼上 BASE_URL + /api 前缀', async () => {
    await apiCall('/version');

    expect(fetchMock).toHaveBeenCalledWith('/app/fnmusic-sync/api/version', {
      method: 'GET',
      headers: {},
    });
  });

  it('有 body 时序列化为 JSON 并带 Content-Type', async () => {
    await apiCall('/user/a', 'POST', { name: 'a' });

    const [url, opts] = fetchMock.mock.calls[0];

    expect(url).toBe('/app/fnmusic-sync/api/user/a');
    expect(opts.method).toBe('POST');
    expect(opts.headers).toEqual({ 'Content-Type': 'application/json' });
    expect(opts.body).toBe(JSON.stringify({ name: 'a' }));
  });

  it('body 为空 / undefined 时不发送 body', async () => {
    await apiCall('/x', 'POST');

    const [, opts] = fetchMock.mock.calls[0];
    expect(opts.body).toBeUndefined();
    expect(opts.headers).toEqual({});
  });

  it('返回解析后的 JSON', async () => {
    fetchMock = stubFetch(200, { value: 42 });
    await expect(apiCall('/x')).resolves.toEqual({ value: 42 });
  });

  it('非 2xx 时抛错，优先使用后端 error 文案', async () => {
    fetchMock = stubFetch(400, { error: '目录不存在' });

    await expect(apiCall('/x')).rejects.toThrow('目录不存在');
  });

  it('非 2xx 且无 error 字段时退化为 HTTP <status>', async () => {
    fetchMock = stubFetch(500, {});

    await expect(apiCall('/x')).rejects.toThrow('HTTP 500');
  });

  it('401 同样抛错（调用方据此跳转登录）', async () => {
    fetchMock = stubFetch(401, { error: '未登录' });

    await expect(apiCall('/x')).rejects.toThrow('未登录');
  });
});

describe('列表类接口的 query 拼装', () => {
  let fetchMock: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    fetchMock = stubFetch();
  });

  it('getPlaylist 同时带 user 与 limit', async () => {
    await getPlaylist('alice', 50);

    expect(fetchMock.mock.calls[0][0]).toBe('/app/fnmusic-sync/api/playlist?user=alice&limit=50');
  });

  it('getPlaylist 无 user 时只带 limit（管理员看全部）', async () => {
    await getPlaylist();

    expect(fetchMock.mock.calls[0][0]).toBe('/app/fnmusic-sync/api/playlist?limit=100');
  });

  it('getPlaylist limit<=0 时不带 limit', async () => {
    await getPlaylist('bob', 0);

    expect(fetchMock.mock.calls[0][0]).toBe('/app/fnmusic-sync/api/playlist?user=bob');
  });

  it('getSyncLog 默认 limit=200', async () => {
    await getSyncLog();

    expect(fetchMock.mock.calls[0][0]).toBe('/app/fnmusic-sync/api/synclog?limit=200');
  });

  it('getSyncLog 带 user 时按 URL 编码', async () => {
    await getSyncLog('a b/c', 10);

    const url = fetchMock.mock.calls[0][0] as string;
    expect(url).toContain('user=a+b%2Fc');
    expect(url).toContain('limit=10');
  });

  it('getPlaylist / getSyncLog 均为 GET', async () => {
    await getPlaylist('x', 1);
    await getSyncLog('x', 1);

    expect(fetchMock.mock.calls[0][1].method).toBe('GET');
    expect(fetchMock.mock.calls[1][1].method).toBe('GET');
  });
});

describe('写操作接口', () => {
  let fetchMock: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    fetchMock = stubFetch();
  });

  it('saveUser 对用户名做 URL 编码', async () => {
    await saveUser('a/b c', { username: 'a/b c' } as never);

    expect(fetchMock.mock.calls[0][0]).toBe('/app/fnmusic-sync/api/user/a%2Fb%20c');
    expect(fetchMock.mock.calls[0][1].method).toBe('POST');
  });

  it('deleteUser 用 DELETE 且不带 body', async () => {
    await deleteUser('alice');

    const [url, opts] = fetchMock.mock.calls[0];
    expect(url).toBe('/app/fnmusic-sync/api/user/alice');
    expect(opts.method).toBe('DELETE');
    expect(opts.body).toBeUndefined();
  });

  it('saveSettings 用 PUT 发送配置', async () => {
    const settings = { logging: { level: 'debug' } };
    await saveSettings(settings as never);

    const [url, opts] = fetchMock.mock.calls[0];
    expect(url).toBe('/app/fnmusic-sync/api/settings');
    expect(opts.method).toBe('PUT');
    expect(JSON.parse(opts.body)).toEqual(settings);
  });

  it('saveLibrary 发送 snake_case 字段（与 Go 侧 JSON tag 对齐）', async () => {
    await saveLibrary(['/vol1/music'], '6h', true);

    const [url, opts] = fetchMock.mock.calls[0];
    expect(url).toBe('/app/fnmusic-sync/api/library');
    expect(opts.method).toBe('PUT');
    expect(JSON.parse(opts.body)).toEqual({
      directories: ['/vol1/music'],
      scan_interval: '6h',
      mbid_online_lookup: true,
    });
  });

  it('startLastFmAuth 发送 api_key / api_secret / username', async () => {
    await startLastFmAuth('k', 's', 'alice');

    const [url, opts] = fetchMock.mock.calls[0];
    expect(url).toBe('/app/fnmusic-sync/api/lastfm/auth');
    expect(opts.method).toBe('POST');
    expect(JSON.parse(opts.body)).toEqual({
      api_key: 'k',
      api_secret: 's',
      username: 'alice',
    });
  });

  it('pollLastFmAuth 走 /lastfm/poll', async () => {
    fetchMock = stubFetch(200, { authorized: true, session_key: 'sk' });

    await expect(pollLastFmAuth('k', 's', 'alice')).resolves.toEqual({
      authorized: true,
      session_key: 'sk',
    });

    expect(fetchMock.mock.calls[0][0]).toBe('/app/fnmusic-sync/api/lastfm/poll');
  });
});

describe('升级接口', () => {
  let fetchMock: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    fetchMock = stubFetch();
  });

  it('checkUpgrade 为 GET', async () => {
    await checkUpgrade();

    expect(fetchMock.mock.calls[0][0]).toBe('/app/fnmusic-sync/api/upgrade/check');
    expect(fetchMock.mock.calls[0][1].method).toBe('GET');
  });

  it('doUpgrade 为 POST 且无 body', async () => {
    await doUpgrade();

    const [url, opts] = fetchMock.mock.calls[0];
    expect(url).toBe('/app/fnmusic-sync/api/upgrade');
    expect(opts.method).toBe('POST');
    expect(opts.body).toBeUndefined();
  });
});

describe('只读接口路径', () => {
  it('getVersion / getSockets / getActiveUsers 走各自路径', async () => {
    const fetchMock = stubFetch();

    await getVersion();
    await getSockets();
    await getActiveUsers();

    expect(fetchMock.mock.calls.map((c) => c[0])).toEqual([
      '/app/fnmusic-sync/api/version',
      '/app/fnmusic-sync/api/sockets',
      '/app/fnmusic-sync/api/users',
    ]);
  });
});
