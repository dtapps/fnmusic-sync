<script lang="ts">
  import { onMount } from 'svelte';
  import { _ } from 'svelte-i18n';
  import { getSockets } from '$lib/api';
  import { Button, Card, Badge } from '$lib/components';
  import type { SocketOverview, SocketStatus } from '$lib/types';

  let data = $state<SocketOverview | null>(null);
  let loading = $state(false);
  let error = $state('');

  async function refresh() {
    loading = true;
    error = '';
    try {
      data = await getSockets();
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      loading = false;
    }
  }

  // 每次进入该 tab（组件重新挂载）时自动刷新。
  onMount(refresh);

  function kindLabel(kind: string): string {
    switch (kind) {
      case 'trim':
        return $_('sockets.kind_trim');
      case 'proxy':
        return $_('sockets.kind_proxy');
      case 'stale':
        return $_('sockets.kind_stale');
      case 'none':
        return $_('sockets.kind_none');
      default:
        return kind;
    }
  }

  function typeLabel(s: SocketStatus): string {
    if (!s.exists) return $_('sockets.type_missing');
    if (!s.is_socket) return $_('sockets.type_nonsocket');
    return $_('sockets.type_socket');
  }
</script>

<section class="flex flex-col gap-3 sm:gap-4">
  <Card>
    <div class="flex items-center justify-between gap-2 mb-4">
      <h2 class="text-base font-semibold">{$_('sockets.title')}</h2>
      <Button size="sm" onclick={refresh} disabled={loading}>
        {loading ? '...' : $_('common.refresh')}
      </Button>
    </div>

    {#if error}
      <p class="text-center text-red-600 text-sm py-8">
        {$_('sockets.load_failed', { values: { message: error } })}
      </p>
    {:else if !data}
      <p class="text-center text-muted-foreground text-sm py-8">{$_('common.refresh')}...</p>
    {:else}
      <div class="flex flex-col gap-3">
        {#each [data.listen, data.upstream] as s, i (i)}
          <div class="rounded-lg border border-border bg-background/50 p-3 sm:p-4">
            <div class="flex items-center justify-between gap-2 mb-3">
              <h3 class="text-sm font-semibold truncate">
                {i === 0 ? $_('sockets.listen') : $_('sockets.upstream')}
              </h3>
              <Badge variant={s.healthy ? 'on' : 'off'}>
                {s.healthy ? $_('sockets.normal') : $_('sockets.abnormal')}
              </Badge>
            </div>

            <div class="grid grid-cols-2 gap-3 text-sm">
              <div>
                <div class="text-xs text-muted-foreground mb-0.5">{$_('sockets.type')}</div>
                <div class="font-medium">{typeLabel(s)}</div>
              </div>
              <div>
                <div class="text-xs text-muted-foreground mb-0.5">{$_('sockets.perm')}</div>
                <div class="font-medium">{s.perm || '-'}</div>
              </div>
              <div>
                <div class="text-xs text-muted-foreground mb-0.5">{$_('sockets.owner')}</div>
                <div class="font-medium">{s.uid}:{s.gid}</div>
              </div>
              <div>
                <div class="text-xs text-muted-foreground mb-0.5">{$_('sockets.kind')}</div>
                <div class="font-medium">{kindLabel(s.kind)}</div>
              </div>
              <div>
                <div class="text-xs text-muted-foreground mb-0.5">{$_('sockets.connectable')}</div>
                <div class="font-medium">{s.connectable ? '✓' : '✗'}</div>
              </div>
              <div>
                <div class="text-xs text-muted-foreground mb-0.5">{$_('sockets.pid')}</div>
                <div class="font-medium">{s.peer_pid ? s.peer_pid : '-'}</div>
              </div>
            </div>

            {#if s.detail}
              <p class="text-xs text-muted-foreground mt-2">{$_('sockets.detail')}: {s.detail}</p>
            {/if}
            <p class="text-xs text-muted-foreground mt-1 break-all">{s.path}</p>
          </div>
        {/each}
      </div>

      <div
        class="mt-4 rounded-lg border p-3 text-sm {data.all_healthy
          ? 'border-green-500/30 bg-green-500/10 text-green-700 dark:text-green-400'
          : 'border-red-500/30 bg-red-500/10 text-red-700 dark:text-red-400'}"
      >
        <span class="font-semibold">{$_('sockets.conclusion')}:</span>
        {data.all_healthy ? $_('sockets.all_ok') : data.message}
      </div>
    {/if}
  </Card>
</section>
