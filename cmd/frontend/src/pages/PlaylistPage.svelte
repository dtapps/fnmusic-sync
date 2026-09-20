<script lang="ts">
  import { onMount } from 'svelte';
  import { _ } from 'svelte-i18n';
  import { state as stateStore, currentUser } from '$lib/stores';
  import { getPlaylist } from '$lib/api';
  import { Button, Card, Badge } from '$lib/components';
  import { formatDateTime, formatDuration } from '$lib/utils';
  import type { PlaybackEntry } from '$lib/types';

  let loading = $state(false);
  let entries = $state<PlaybackEntry[]>([]);
  let filterUser = $state('');

  // 管理员可切换用户（含"全部"）；普通用户只看自己，隐藏选择器。
  let userOptions = $derived($currentUser.isAdmin ? Object.keys($stateStore.users || {}) : [$currentUser.username]);

  async function refresh() {
    loading = true;
    try {
      const user = $currentUser.isAdmin ? filterUser || undefined : undefined;
      const data = await getPlaylist(user, 200);
      entries = data.entries || [];
    } catch {
      entries = [];
    } finally {
      loading = false;
    }
  }

  // 切换筛选用户（仅管理员）后重新加载。
  function onUserChange() {
    refresh();
  }

  onMount(refresh);

  // 进行中（无结束时间）= 当前正在播放。
  function isPlaying(e: PlaybackEntry): boolean {
    return !e.ended_at;
  }
</script>

<section class="flex flex-col gap-3 sm:gap-4">
  <Card>
    <div class="flex items-center justify-between gap-2 mb-4 flex-wrap">
      <h2 class="text-base font-semibold">{$_('playlist.title')}</h2>
      <div class="flex items-center gap-2">
        {#if $currentUser.isAdmin && userOptions.length > 0}
          <select
            class="rounded-md border border-border bg-background px-2 py-1.5 text-xs sm:text-sm"
            bind:value={filterUser}
            onchange={onUserChange}
          >
            <option value="">{$_('playlist.all_users')}</option>
            {#each userOptions as u (u)}
              <option value={u}>{u}</option>
            {/each}
          </select>
        {/if}
        <Button size="sm" onclick={refresh} disabled={loading}>
          {loading ? '...' : $_('common.refresh')}
        </Button>
      </div>
    </div>

    {#if entries.length === 0}
      <p class="text-center text-muted-foreground text-sm py-8">
        {$_('playlist.empty')}
      </p>
    {:else}
      <div class="overflow-x-auto -mx-1 px-1">
        <table class="w-full text-sm">
          <thead>
            <tr class="text-left text-xs text-muted-foreground border-b border-border">
              <th class="py-2 pr-3 font-medium">{$_('playlist.col_title')}</th>
              <th class="py-2 pr-3 font-medium">{$_('playlist.col_artist')}</th>
              <th class="py-2 pr-3 font-medium hidden sm:table-cell">{$_('playlist.col_album')}</th>
              <th class="py-2 pr-3 font-medium hidden sm:table-cell">{$_('playlist.col_duration')}</th>
              <th class="py-2 pr-3 font-medium">{$_('playlist.col_started')}</th>
              <th class="py-2 pr-3 font-medium">{$_('playlist.col_status')}</th>
            </tr>
          </thead>
          <tbody>
            {#each entries as e (e.id)}
              <tr class="border-b border-border/60 hover:bg-muted/40">
                <td class="py-2 pr-3">
                  <div class="font-medium truncate max-w-[16rem]">{e.title}</div>
                  <div class="text-xs text-muted-foreground sm:hidden">
                    {[e.artist, e.album].filter(Boolean).join(' · ')}
                  </div>
                </td>
                <td class="py-2 pr-3 text-muted-foreground truncate max-w-[10rem]">{e.artist || '—'}</td>
                <td class="py-2 pr-3 text-muted-foreground truncate max-w-[12rem] hidden sm:table-cell">
                  {e.album || '—'}
                </td>
                <td class="py-2 pr-3 text-muted-foreground tabular-nums hidden sm:table-cell">
                  {formatDuration(e.duration_ms) || '—'}
                </td>
                <td class="py-2 pr-3 text-muted-foreground tabular-nums whitespace-nowrap">
                  {formatDateTime(e.started_at)}
                </td>
                <td class="py-2 pr-3">
                  {#if isPlaying(e)}
                    <Badge variant="on">{$_('playlist.playing')}</Badge>
                  {:else}
                    <span class="text-xs text-muted-foreground">{formatDateTime(e.ended_at)}</span>
                  {/if}
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  </Card>
</section>
