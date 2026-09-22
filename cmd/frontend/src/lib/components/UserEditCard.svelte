<script lang="ts">
  import { _ } from 'svelte-i18n';
  import { showToast, currentUser } from '$lib/stores';
  import { startLastFmAuth, pollLastFmAuth } from '$lib/api';
  import type { UserAccount } from '$lib/types';
  import { errMessage } from '$lib/utils';
  import { Button, Card, Input, Select, Switch, Collapsible, PasswordInput } from './index';

  // 导出 AuthState 类型
  export interface AuthState {
    status: 'idle' | 'pending' | 'success' | 'error' | 'timeout';
    message: string;
  }

  interface Props {
    name: string;
    user: UserAccount;
    onSave: () => void;
    canAuth: boolean;
    authState?: AuthState;
  }

  let {
    name,
    user = $bindable(),
    onSave,
    canAuth = false,
    authState = { status: 'idle', message: '' } as AuthState,
  }: Props = $props();

  // 内部授权状态（如果未从外部传入）
  let localAuthState = $state<AuthState>({ status: 'idle', message: '' });
  let currentAuthState = $derived(authState || localAuthState);

  // 折叠面板状态
  let lfmOpen = $state(false);
  let lbOpen = $state(false);

  // 授权按钮文字
  let authBtnText = $derived.by(() => {
    if (currentAuthState.status === 'pending') return $_('lastfm.authing');
    if (currentAuthState.status === 'success') return $_('lastfm.authed');
    return $_('lastfm.auth');
  });

  let authDisabled = $derived(!canAuth && currentAuthState.status !== 'pending');

  // 内部授权处理
  async function handleAuth() {
    const apiKey = user.lastfm?.api_key?.trim() || '';
    const apiSecret = user.lastfm?.api_secret?.trim() || '';

    if (!apiKey || !apiSecret) {
      showToast($_('lastfm.auth_need_credentials'), 'error');
      return;
    }

    localAuthState = { status: 'pending', message: $_('lastfm.auth_pending') };

    try {
      const resp = await startLastFmAuth(apiKey, apiSecret, name);
      window.open(resp.auth_url, '_blank');
      localAuthState = { status: 'pending', message: $_('lastfm.auth_opened') };
      pollAuth(apiKey, apiSecret);
    } catch (e) {
      localAuthState = {
        status: 'error',
        message: $_('lastfm.auth_failed', { values: { message: errMessage(e) } }),
      };
    }
  }

  function pollAuth(apiKey: string, apiSecret: string) {
    let attempts = 0;
    const maxAttempts = 60;

    const poll = async () => {
      attempts++;
      if (attempts > maxAttempts) {
        localAuthState = {
          status: 'timeout',
          message: $_('lastfm.auth_timeout'),
        };
        return;
      }

      try {
        const resp = await pollLastFmAuth(apiKey, apiSecret, name);
        if (resp.authorized) {
          localAuthState = {
            status: 'success',
            message: $_('lastfm.auth_success', {
              values: { username: resp.lastfm_username || '' },
            }),
          };
          if (user.lastfm) {
            user.lastfm.session_key = resp.session_key || '';
            user.lastfm.username = resp.lastfm_username || '';
            user = { ...user };
          }
          showToast($_('lastfm.auth_success_toast'));
          return;
        }
        if (resp.expired) {
          localAuthState = {
            status: 'error',
            message: $_('lastfm.auth_expired'),
          };
          return;
        }
        const waitMsg = $_('lastfm.auth_waiting', {
          values: { current: String(attempts), max: String(maxAttempts) },
        });
        localAuthState = { status: 'pending', message: waitMsg };
        setTimeout(poll, 3000);
      } catch (e) {
        if (attempts >= maxAttempts) {
          localAuthState = {
            status: 'error',
            message: $_('lastfm.auth_poll_failed', {
              values: { message: errMessage(e) },
            }),
          };
          return;
        }
        setTimeout(poll, 3000);
      }
    };

    setTimeout(poll, 3000);
  }
</script>

<!-- 表单行通用样式：移动端垂直，桌面端水平 -->
<Card class="border border-border">
  <!-- Last.fm 区域 -->
  <Collapsible bind:open={lfmOpen} title={$_('lastfm.title')}>
    {#snippet headerExtra()}
      <Switch bind:checked={user.lastfm.enabled} />
    {/snippet}

    <div class="flex flex-col sm:flex-row sm:items-center gap-2 sm:gap-3 mb-3">
      <label class="text-xs font-medium sm:min-w-[100px]">{$_('lastfm.api_key')}</label>
      <Input bind:value={user.lastfm.api_key} placeholder={$_('lastfm.api_key')} />
    </div>
    <div class="flex flex-col sm:flex-row sm:items-center gap-2 sm:gap-3 mb-3">
      <label class="text-xs font-medium sm:min-w-[100px]">{$_('lastfm.api_secret')}</label>
      <PasswordInput bind:value={user.lastfm.api_secret} placeholder={$_('lastfm.api_secret')} />
    </div>
    <div class="flex flex-col sm:flex-row sm:items-center gap-2 sm:gap-3 mb-3">
      <label class="text-xs font-medium sm:min-w-[100px]">{$_('lastfm.session_key')}</label>
      <div class="flex gap-2 flex-1">
        <PasswordInput bind:value={user.lastfm.session_key} placeholder={$_('lastfm.session_placeholder')} />
        <Button variant="auth" disabled={authDisabled} onclick={handleAuth}>{authBtnText}</Button>
      </div>
    </div>

    {#if currentAuthState.status !== 'idle'}
      <div
        class="px-3 py-2 rounded-md text-xs mb-3
          {currentAuthState.status === 'pending' ? 'bg-amber-50 text-amber-700 border border-amber-200' : ''}
          {currentAuthState.status === 'success' ? 'bg-green-50 text-green-600 border border-green-200' : ''}
          {currentAuthState.status === 'error' || currentAuthState.status === 'timeout'
          ? 'bg-red-50 text-red-600 border border-red-200'
          : ''}"
      >
        {currentAuthState.message}
      </div>
    {/if}

    <div class="flex flex-col sm:flex-row sm:items-center gap-2 sm:gap-3 mb-3">
      <label class="text-xs font-medium sm:min-w-[100px]">{$_('lastfm.username')}</label>
      <Input bind:value={user.lastfm.username} placeholder={$_('lastfm.username_placeholder')} />
    </div>

    <!-- Last.fm 歌单同步 -->
    <div class="mt-3 p-3 bg-muted/50 rounded-md">
      <h5 class="text-xs text-muted-foreground mb-2">
        {$_('lastfm.smart_playlist')}
      </h5>

      <div class="flex flex-wrap items-center gap-2 mb-2">
        <label class="text-xs font-medium min-w-[80px]">{$_('lastfm.top_tracks')}</label>
        <Switch bind:checked={user.lastfm.playlist.top_tracks.enabled} />
        <Input
          bind:value={user.lastfm.playlist.top_tracks.name}
          placeholder={$_('lastfm.top_tracks_placeholder')}
          class="min-w-[100px] flex-1"
        />
        <Select bind:value={user.lastfm.playlist.top_tracks.period} class="min-w-[90px]">
          <option value="overall">{$_('lastfm.period_overall')}</option>
          <option value="7day">{$_('lastfm.period_7day')}</option>
          <option value="1month">{$_('lastfm.period_1month')}</option>
          <option value="3month">{$_('lastfm.period_3month')}</option>
          <option value="6month">{$_('lastfm.period_6month')}</option>
          <option value="12month">{$_('lastfm.period_12month')}</option>
        </Select>
        <Input type="number" bind:value={user.lastfm.playlist.top_tracks.limit} placeholder="50" min="0" class="w-16" />
      </div>

      <div class="flex flex-wrap items-center gap-2 mb-2">
        <label class="text-xs font-medium min-w-[80px]">{$_('lastfm.loved_tracks')}</label>
        <Switch bind:checked={user.lastfm.playlist.loved_tracks.enabled} />
        <Input
          bind:value={user.lastfm.playlist.loved_tracks.name}
          placeholder={$_('lastfm.loved_tracks_placeholder')}
          class="min-w-[100px] flex-1"
        />
        <Input
          type="number"
          bind:value={user.lastfm.playlist.loved_tracks.limit}
          placeholder="50"
          min="0"
          class="w-16"
        />
      </div>

      <div class="flex flex-wrap items-center gap-2 mb-2">
        <label class="text-xs font-medium min-w-[80px]">{$_('lastfm.recent_tracks')}</label>
        <Switch bind:checked={user.lastfm.playlist.recent_tracks.enabled} />
        <Input
          bind:value={user.lastfm.playlist.recent_tracks.name}
          placeholder={$_('lastfm.recent_tracks_placeholder')}
          class="min-w-[100px] flex-1"
        />
        <Input
          type="number"
          bind:value={user.lastfm.playlist.recent_tracks.limit}
          placeholder="50"
          min="0"
          class="w-16"
        />
      </div>

      <div class="flex flex-wrap items-center gap-2 mb-2">
        <label class="text-xs font-medium min-w-[80px]">{$_('lastfm.weekly_charts')}</label>
        <Switch bind:checked={user.lastfm.playlist.weekly_charts.enabled} />
        <Input
          bind:value={user.lastfm.playlist.weekly_charts.name}
          placeholder={$_('lastfm.weekly_charts_placeholder')}
          class="min-w-[100px] flex-1"
        />
        <Input
          type="number"
          bind:value={user.lastfm.playlist.weekly_charts.limit}
          placeholder="50"
          min="0"
          class="w-16"
        />
      </div>
    </div>
  </Collapsible>

  <!-- ListenBrainz 区域 -->
  <Collapsible bind:open={lbOpen} title={$_('listenbrainz.title')}>
    {#snippet headerExtra()}
      <Switch bind:checked={user.listenbrainz.enabled} />
    {/snippet}

    <div class="flex flex-col sm:flex-row sm:items-center gap-2 sm:gap-3 mb-3">
      <label class="text-xs font-medium sm:min-w-[100px]">{$_('listenbrainz.token')}</label>
      <PasswordInput bind:value={user.listenbrainz.token} placeholder={$_('listenbrainz.token_placeholder')} />
    </div>
    <div class="flex flex-col sm:flex-row sm:items-center gap-2 sm:gap-3 mb-3">
      <label class="text-xs font-medium sm:min-w-[100px]">{$_('listenbrainz.username')}</label>
      <Input bind:value={user.listenbrainz.username} placeholder={$_('listenbrainz.username_placeholder')} />
    </div>

    <!-- LB 歌单同步 -->
    <div class="mt-3 p-3 bg-muted/50 rounded-md">
      <h5 class="text-xs text-muted-foreground mb-2">
        {$_('listenbrainz.playlist_sync')}
      </h5>

      <div class="flex flex-wrap items-center gap-2 mb-2">
        <label class="text-xs font-medium min-w-[80px]">{$_('listenbrainz.daily_jams')}</label>
        <Switch bind:checked={user.listenbrainz.playlist.daily_jams.enabled} />
        <Input
          bind:value={user.listenbrainz.playlist.daily_jams.name}
          placeholder={$_('listenbrainz.daily_jams_placeholder')}
          class="min-w-[100px] flex-1"
        />
        <Input
          type="number"
          bind:value={user.listenbrainz.playlist.daily_jams.limit}
          placeholder="0"
          min="0"
          class="w-16"
        />
      </div>

      <div class="flex flex-wrap items-center gap-2 mb-2">
        <label class="text-xs font-medium min-w-[80px]">{$_('listenbrainz.weekly_jams')}</label>
        <Switch bind:checked={user.listenbrainz.playlist.weekly_jams.enabled} />
        <Input
          bind:value={user.listenbrainz.playlist.weekly_jams.name}
          placeholder={$_('listenbrainz.weekly_jams_placeholder')}
          class="min-w-[100px] flex-1"
        />
        <Input
          type="number"
          bind:value={user.listenbrainz.playlist.weekly_jams.limit}
          placeholder="0"
          min="0"
          class="w-16"
        />
      </div>

      <div class="flex flex-wrap items-center gap-2 mb-2">
        <label class="text-xs font-medium min-w-[80px]">{$_('listenbrainz.weekly_exploration')}</label>
        <Switch bind:checked={user.listenbrainz.playlist.weekly_exploration.enabled} />
        <Input
          bind:value={user.listenbrainz.playlist.weekly_exploration.name}
          placeholder={$_('listenbrainz.weekly_exploration_placeholder')}
          class="min-w-[100px] flex-1"
        />
        <Input
          type="number"
          bind:value={user.listenbrainz.playlist.weekly_exploration.limit}
          placeholder="0"
          min="0"
          class="w-16"
        />
      </div>

      <div class="flex flex-wrap items-center gap-2 mb-2">
        <label class="text-xs font-medium min-w-[80px]">{$_('listenbrainz.year_discoveries')}</label>
        <Switch bind:checked={user.listenbrainz.playlist.year_discoveries.enabled} />
        <Input
          bind:value={user.listenbrainz.playlist.year_discoveries.name}
          placeholder={$_('listenbrainz.year_discoveries_placeholder')}
          class="min-w-[100px] flex-1"
        />
        <Input
          type="number"
          bind:value={user.listenbrainz.playlist.year_discoveries.limit}
          placeholder="0"
          min="0"
          class="w-16"
        />
      </div>

      <div class="flex flex-wrap items-center gap-2 mb-2">
        <label class="text-xs font-medium min-w-[80px]">{$_('listenbrainz.year_missed')}</label>
        <Switch bind:checked={user.listenbrainz.playlist.year_missed.enabled} />
        <Input
          bind:value={user.listenbrainz.playlist.year_missed.name}
          placeholder={$_('listenbrainz.year_missed_placeholder')}
          class="min-w-[100px] flex-1"
        />
        <Input
          type="number"
          bind:value={user.listenbrainz.playlist.year_missed.limit}
          placeholder="0"
          min="0"
          class="w-16"
        />
      </div>

      <div class="flex flex-wrap items-center gap-2 mb-2">
        <label class="text-xs font-medium min-w-[80px]">{$_('listenbrainz.top_recordings')}</label>
        <Switch bind:checked={user.listenbrainz.playlist.top_recordings.enabled} />
        <Input
          bind:value={user.listenbrainz.playlist.top_recordings.name}
          placeholder={$_('listenbrainz.top_recordings_placeholder')}
          class="min-w-[100px] flex-1"
        />
        <Select bind:value={user.listenbrainz.playlist.top_recordings.range} class="min-w-[90px]">
          <option value="all_time">{$_('listenbrainz.range_all_time')}</option>
          <option value="this_year">{$_('listenbrainz.range_this_year')}</option>
          <option value="year">{$_('listenbrainz.range_year')}</option>
          <option value="half_year">{$_('listenbrainz.range_half_year')}</option>
          <option value="quarter">{$_('listenbrainz.range_quarter')}</option>
          <option value="month">{$_('listenbrainz.range_month')}</option>
          <option value="week">{$_('listenbrainz.range_week')}</option>
        </Select>
        <Input
          type="number"
          bind:value={user.listenbrainz.playlist.top_recordings.limit}
          placeholder="0"
          min="0"
          class="w-16"
        />
      </div>

      <div class="flex flex-wrap items-center gap-2">
        <label class="text-xs font-medium min-w-[80px]">{$_('listenbrainz.recently_played')}</label>
        <Switch bind:checked={user.listenbrainz.playlist.recently_played.enabled} />
        <Input
          bind:value={user.listenbrainz.playlist.recently_played.name}
          placeholder={$_('listenbrainz.recently_played_placeholder')}
          class="min-w-[100px] flex-1"
        />
        <Input
          type="number"
          bind:value={user.listenbrainz.playlist.recently_played.limit}
          placeholder="0"
          min="0"
          class="w-16"
        />
      </div>
    </div>
  </Collapsible>

  <Button variant="primary" class="w-full mt-4" onclick={onSave}
    >{$currentUser.isAdmin ? $_('users.save') : $_('common.save')}</Button
  >
</Card>
