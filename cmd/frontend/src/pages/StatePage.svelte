<script lang="ts">
  import { onMount } from 'svelte';
  import { _ } from 'svelte-i18n';
  import { state as stateStore, config } from '$lib/stores';
  import { getState } from '$lib/api';
  import { Button, Card, Badge } from '$lib/components';
  import { formatDateTime, lastfmProfileUrl, listenbrainzProfileUrl } from '$lib/utils';

  let loading = $state(false);

  async function refresh() {
    loading = true;
    try {
      const s = await getState();
      stateStore.set(s);
    } catch {
      // 静默
    } finally {
      loading = false;
    }
  }

  // 每次进入该 tab（组件重新挂载）时自动刷新，无需手动点刷新。
  onMount(refresh);

  let users = $derived(Object.entries($stateStore.users || {}));
</script>

<section class="flex flex-col gap-3 sm:gap-4">
  <Card>
    <div class="flex items-center justify-between gap-2 mb-4">
      <h2 class="text-base font-semibold">{$_('tabs.state')}</h2>
      <Button size="sm" onclick={refresh} disabled={loading}>
        {loading ? '...' : $_('common.refresh')}
      </Button>
    </div>

    {#if users.length === 0}
      <p class="text-center text-muted-foreground text-sm py-8">
        {$_('state.no_data')}
      </p>
    {:else}
      <div class="flex flex-col gap-3">
        {#each users as [name, info] (name)}
          {@const userCfg = $config.users?.[name]}
          {@const lfUrl = lastfmProfileUrl(userCfg?.lastfm?.username)}
          {@const lbUrl = listenbrainzProfileUrl(userCfg?.listenbrainz?.username)}
          <div class="rounded-lg border border-border bg-background/50 p-3 sm:p-4">
            <!-- 用户名 -->
            <div class="flex items-center justify-between gap-2 mb-3">
              <h3 class="text-sm font-semibold truncate">{name}</h3>
              <div class="flex gap-1.5 shrink-0">
                {#if lfUrl}
                  <a href={lfUrl} target="_blank" rel="noopener noreferrer">
                    <Badge variant={info.lastfm_enabled ? 'on' : 'off'}>
                      Last.fm {info.lastfm_enabled ? '✓' : '✗'}
                    </Badge>
                  </a>
                {:else}
                  <Badge variant={info.lastfm_enabled ? 'on' : 'off'}>
                    Last.fm {info.lastfm_enabled ? '✓' : '✗'}
                  </Badge>
                {/if}
                {#if lbUrl}
                  <a href={lbUrl} target="_blank" rel="noopener noreferrer">
                    <Badge variant={info.listenbrainz_enabled ? 'on' : 'off'}>
                      ListenBrainz {info.listenbrainz_enabled ? '✓' : '✗'}
                    </Badge>
                  </a>
                {:else}
                  <Badge variant={info.listenbrainz_enabled ? 'on' : 'off'}>
                    ListenBrainz {info.listenbrainz_enabled ? '✓' : '✗'}
                  </Badge>
                {/if}
              </div>
            </div>

            <!-- 统计信息 -->
            <div class="grid grid-cols-2 gap-3">
              <div class="rounded-md bg-muted/50 px-3 py-2">
                <div class="text-xs text-muted-foreground mb-0.5">
                  {$_('state.scrobble_total')}
                </div>
                <div class="text-lg font-semibold tabular-nums">
                  {info.scrobbles ?? 0}
                </div>
              </div>
              <div class="rounded-md bg-muted/50 px-3 py-2">
                <div class="text-xs text-muted-foreground mb-0.5">
                  {$_('state.last_scrobble')}
                </div>
                <div class="text-sm font-medium tabular-nums">
                  {info.last_scrobbled_at ? formatDateTime(info.last_scrobbled_at) : $_('state.no_record')}
                </div>
              </div>
            </div>
          </div>
        {/each}
      </div>
    {/if}
  </Card>
</section>
