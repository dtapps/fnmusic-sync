import { describe, it, expect } from 'vitest';
import { createEmptyUser } from '$lib/types';

describe('createEmptyUser', () => {
  it('返回禁用的空配置骨架', () => {
    const user = createEmptyUser();
    expect(user.lastfm.enabled).toBe(false);
    expect(user.lastfm.api_key).toBe('');
    expect(user.lastfm.api_secret).toBe('');
    expect(user.lastfm.session_key).toBe('');
    expect(user.lastfm.username).toBe('');
    expect(user.listenbrainz.enabled).toBe(false);
    expect(user.listenbrainz.token).toBe('');
    expect(user.listenbrainz.username).toBe('');
  });

  it('Last.fm 四个歌单默认禁用、limit 50、period overall', () => {
    const { playlist } = createEmptyUser().lastfm;
    expect(playlist.top_tracks).toEqual({ enabled: false, name: 'LF {period} 最常听', period: 'overall', limit: 50 });
    expect(playlist.loved_tracks).toEqual({ enabled: false, name: 'LF 红心收藏', limit: 50 });
    expect(playlist.recent_tracks).toEqual({ enabled: false, name: 'LF 最近播放', limit: 50 });
    expect(playlist.weekly_charts).toEqual({ enabled: false, name: 'LF 本周榜单', limit: 50 });
  });

  it('ListenBrainz 歌单默认禁用、limit 50、top_recordings range all_time', () => {
    const { playlist } = createEmptyUser().listenbrainz;
    expect(playlist.daily_jams).toEqual({ enabled: false, name: 'LB 每日推荐', limit: 50 });
    expect(playlist.weekly_jams).toEqual({ enabled: false, name: 'LB 每周推荐', limit: 50 });
    expect(playlist.weekly_exploration).toEqual({ enabled: false, name: 'LB 每周探索', limit: 50 });
    expect(playlist.year_discoveries).toEqual({ enabled: false, name: 'LB {year} 年度发现', limit: 50 });
    expect(playlist.year_missed).toEqual({ enabled: false, name: 'LB {year} 年度遗珠', limit: 50 });
    expect(playlist.top_recordings).toEqual({ enabled: false, name: 'LB 最常听', limit: 50, range: 'all_time' });
    expect(playlist.recently_played).toEqual({ enabled: false, name: 'LB 最近在听', limit: 50 });
  });

  it('每次调用返回独立对象（互不影响）', () => {
    const a = createEmptyUser();
    const b = createEmptyUser();
    expect(a).not.toBe(b);
    a.lastfm.api_key = 'mutated';
    expect(b.lastfm.api_key).toBe('');
  });
});
