<script lang="ts">
  import { onMount } from 'svelte';
  import { _ } from 'svelte-i18n';
  import { state as stateStore, currentUser } from '$lib/stores';
  import { getSyncLog } from '$lib/api';
  import { Button, Card, Badge } from '$lib/components';
  import { formatDateTime } from '$lib/utils';
  import type { SyncLogEntry } from '$lib/types';

  let loading = $state(false);
  let entries = $state<SyncLogEntry[]>([]);
  let filterUser = $state('');

  // 管理员可切换用户（含"全部"）；普通用户只看自己，隐藏选择器与用户名列。
  let userOptions = $derived($currentUser.isAdmin ? Object.keys($stateStore.users || {}) : [$currentUser.username]);
  let showUserColumn = $derived($currentUser.isAdmin);

  async function refresh() {
    loading = true;
    try {
      const user = $currentUser.isAdmin ? filterUser || undefined : undefined;
      const data = await getSyncLog(user, 200);
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

  function providerLabel(e: SyncLogEntry): string {
    return e.provider === 'lastfm' ? $_('synclog.provider_lastfm') : $_('synclog.provider_listenbrainz');
  }

  function triggerLabel(e: SyncLogEntry): string {
    switch (e.trigger) {
      case 'user_identified':
        return $_('synclog.trigger_user_identified');
      case 'manual':
        return $_('synclog.trigger_manual');
      default:
        return $_('synclog.trigger_interval');
    }
  }
</script>

<section class="flex flex-col gap-3 sm:gap-4">
  <Card>
    <div class="flex items-center justify-between gap-2 mb-4 flex-wrap">
      <h2 class="text-base font-semibold">{$_('synclog.title')}</h2>
      <div class="flex items-center gap-2">
        {#if showUserColumn && userOptions.length > 0}
          <select
            class="rounded-md border border-border bg-background px-2 py-1.5 text-xs sm:text-sm"
            bind:value={filterUser}
            onchange={onUserChange}
          >
            <option value="">{$_('synclog.all_users')}</option>
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
        {$_('synclog.empty')}
      </p>
    {:else}
      <div class="overflow-x-auto -mx-1 px-1">
        <table class="w-full text-sm">
          <thead>
            <tr class="text-left text-xs text-muted-foreground border-b border-border">
              {#if showUserColumn}
                <th class="py-2 pr-3 font-medium">{$_('synclog.col_user')}</th>
              {/if}
              <th class="py-2 pr-3 font-medium">{$_('synclog.col_provider')}</th>
              <th class="py-2 pr-3 font-medium">{$_('synclog.col_status')}</th>
              <th class="py-2 pr-3 font-medium hidden sm:table-cell">{$_('synclog.col_playlists')}</th>
              <th class="py-2 pr-3 font-medium hidden sm:table-cell">{$_('synclog.col_tracks')}</th>
              <th class="py-2 pr-3 font-medium hidden md:table-cell">{$_('synclog.col_trigger')}</th>
              <th class="py-2 pr-3 font-medium">{$_('synclog.col_finished')}</th>
            </tr>
          </thead>
          <tbody>
            {#each entries as e (e.id)}
              <tr class="border-b border-border/60 hover:bg-muted/40">
                {#if showUserColumn}
                  <td class="py-2 pr-3 text-muted-foreground truncate max-w-[8rem]">{e.username}</td>
                {/if}
                <td class="py-2 pr-3 font-medium">{providerLabel(e)}</td>
                <td class="py-2 pr-3">
                  {#if e.status === 'success'}
                    <Badge variant="on">{$_('synclog.success')}</Badge>
                  {:else}
                    <span class="inline-flex items-center" title={e.message || ''}>
                      <Badge variant="off">{$_('synclog.failed')}</Badge>
                    </span>
                  {/if}
                </td>
                <td class="py-2 pr-3 text-muted-foreground tabular-nums hidden sm:table-cell">{e.playlists}</td>
                <td class="py-2 pr-3 text-muted-foreground tabular-nums hidden sm:table-cell">{e.tracks}</td>
                <td class="py-2 pr-3 text-muted-foreground whitespace-nowrap hidden md:table-cell">
                  {triggerLabel(e)}
                </td>
                <td class="py-2 pr-3 text-muted-foreground tabular-nums whitespace-nowrap">
                  {formatDateTime(e.finished_at)}
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  </Card>
</section>
