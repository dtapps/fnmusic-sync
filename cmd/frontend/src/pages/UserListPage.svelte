<script lang="ts">
  import { onMount } from 'svelte';
  import { _ } from 'svelte-i18n';
  import { Button, Card, Badge } from '$lib/components';
  import { getActiveUsers } from '$lib/api';
  import { errMessage } from '$lib/utils';
  import type { ActiveUser } from '$lib/types';

  let loading = $state(false);
  let error = $state('');
  let users = $state<ActiveUser[]>([]);

  async function load() {
    loading = true;
    error = '';
    try {
      const data = await getActiveUsers();
      users = data.users;
    } catch (e) {
      error = $_('users_list.load_failed', { values: { message: errMessage(e) } });
    } finally {
      loading = false;
    }
  }

  onMount(load);
</script>

<section class="flex flex-col gap-3 sm:gap-4">
  <Card>
    <div class="flex items-center justify-between gap-2 mb-4">
      <div class="min-w-0">
        <h2 class="text-base font-semibold">{$_('users_list.title')}</h2>
        <p class="text-xs text-muted-foreground mt-0.5">{$_('users_list.desc')}</p>
      </div>
      <Button size="sm" onclick={load} disabled={loading}>
        {loading ? '...' : $_('common.refresh')}
      </Button>
    </div>

    {#if error}
      <p class="text-center text-destructive text-sm py-8">{error}</p>
    {:else if users.length === 0 && !loading}
      <p class="text-center text-muted-foreground text-sm py-8">
        {$_('users_list.empty')}
      </p>
    {:else}
      <div class="overflow-x-auto -mx-1 px-1">
        <table class="w-full text-sm">
          <thead>
            <tr class="border-b border-border text-left text-muted-foreground">
              <th class="px-3 py-2 font-medium">{$_('users_list.username')}</th>
              <th class="px-3 py-2 font-medium">{$_('users_list.platform')}</th>
              <th class="px-3 py-2 font-medium">{$_('users_list.role')}</th>
              <th class="px-3 py-2 font-medium">{$_('users_list.client')}</th>
              <th class="px-3 py-2 font-medium">{$_('users_list.token')}</th>
              <th class="px-3 py-2 font-medium">{$_('users_list.first_seen')}</th>
              <th class="px-3 py-2 font-medium">{$_('users_list.last_seen')}</th>
            </tr>
          </thead>
          <tbody>
            {#each users as u (u.token)}
              <tr class="border-b border-border/50 align-top">
                <td class="px-3 py-2 font-medium whitespace-nowrap">{u.username}</td>
                <td class="px-3 py-2 whitespace-nowrap text-muted-foreground">{u.platform_username || '-'}</td>
                <td class="px-3 py-2 whitespace-nowrap">
                  {#if u.is_admin}
                    <Badge variant="on">{$_('users_list.admin')}</Badge>
                  {:else}
                    <span class="text-xs text-muted-foreground">{$_('users_list.normal')}</span>
                  {/if}
                </td>
                <td class="px-3 py-2 whitespace-nowrap text-muted-foreground">
                  {#if u.ua_system || u.ua_client}
                    {[u.ua_system, u.ua_client].filter(Boolean).join(' · ')}
                  {:else}-{/if}
                </td>
                <td class="px-3 py-2 font-mono text-xs text-muted-foreground truncate">{u.token}</td>
                <td class="px-3 py-2 whitespace-nowrap text-xs text-muted-foreground">{u.first_seen_at || '-'}</td>
                <td class="px-3 py-2 whitespace-nowrap text-xs text-muted-foreground">{u.last_seen_at || '-'}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
      <div class="mt-3 text-xs text-muted-foreground">
        {$_('users_list.online_count', { values: { count: users.length } })}
      </div>
    {/if}
  </Card>
</section>
