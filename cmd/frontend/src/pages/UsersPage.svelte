<script lang="ts">
  import { _ } from 'svelte-i18n';
  import { currentUser, config, showToast, reloadConfig } from '$lib/stores';
  import { saveUser, deleteUser as apiDeleteUser } from '$lib/api';
  import { createEmptyUser, type UserAccount } from '$lib/types';
  import { Button, Card, Badge, Dialog, Input } from '$lib/components';
  import UserEditCard from '../lib/components/UserEditCard.svelte';
  import type { AuthState } from '../lib/components/UserEditCard.svelte';

  // 本地编辑状态
  let users = $state<Record<string, UserAccount>>({});
  let expandedRows = $state<Record<string, boolean>>({});
  let showAddUserModal = $state(false);
  let newUserName = $state('');
  let authStates = $state<Record<string, AuthState>>({});

  // 标记是否正在本地修改 users（避免 $effect 覆盖）
  let localDirty = $state(false);

  // 当 config store 变化时，更新本地 users（仅当没有本地未保存修改时）
  $effect(() => {
    const cfgUsers = $config.users || {};
    if (!localDirty) {
      users = JSON.parse(JSON.stringify(cfgUsers));
      const validNames = Object.keys(cfgUsers);
      let needsCleanup = false;
      for (const n of Object.keys(expandedRows)) {
        if (!validNames.includes(n)) {
          delete expandedRows[n];
          needsCleanup = true;
        }
      }
      if (needsCleanup) expandedRows = { ...expandedRows };
    }
  });

  let userEntries = $derived(Object.entries(users));
  let isAdmin = $derived($currentUser.isAdmin);

  function confirmAddUser() {
    const name = newUserName.trim();
    if (!name) {
      showToast($_('users.name_required'), 'error');
      return;
    }
    if (name in users) {
      showToast($_('users.name_exists', { values: { name } }), 'error');
      return;
    }
    localDirty = true;
    users[name] = createEmptyUser();
    users = { ...users };
    newUserName = '';
    showAddUserModal = false;
    if (isAdmin) {
      expandedRows = { ...expandedRows, [name]: true };
    }
    showToast($_('users.added'));
  }

  async function handleSave(name: string) {
    try {
      const userData = users[name];
      await saveUser(name, userData);
      localDirty = false;
      await reloadConfig();
      showToast($_('users.saved'));
    } catch (e: any) {
      showToast($_('users.save_failed', { values: { message: e.message } }), 'error');
    }
  }

  async function handleDelete(name: string) {
    if (!confirm($_('users.confirm_delete', { values: { name } }))) return;
    try {
      await apiDeleteUser(name);
      localDirty = false;
      await reloadConfig();
      showToast($_('users.deleted'));
    } catch (e: any) {
      showToast($_('users.delete_failed', { values: { message: e.message } }), 'error');
    }
  }

  function updateAuthBtnState(name: string): boolean {
    const user = users[name];
    if (!user) return false;
    const apiKey = user.lastfm?.api_key?.trim() || '';
    const apiSecret = user.lastfm?.api_secret?.trim() || '';
    return !!(apiKey && apiSecret && name in ($config.users || {}));
  }
</script>

<section>
  <div class="flex items-center gap-3 mb-4 flex-wrap">
    {#if isAdmin}
      <Button
        variant="primary"
        onclick={() => {
          newUserName = '';
          showAddUserModal = true;
        }}>{$_('users.add_user')}</Button
      >
      <span class="text-xs text-muted-foreground hidden sm:inline">{$_('users.username_hint')}</span>
    {/if}
  </div>

  {#if isAdmin}
    <!-- 管理员模式 -->
    {#if userEntries.length === 0}
      <Card>
        <p class="text-center text-muted-foreground py-4">{$_('users.no_users')}</p>
      </Card>
    {:else}
      <!-- 移动端：卡片列表 -->
      <div class="sm:hidden flex flex-col gap-3">
        {#each userEntries as [name, user] (name)}
          <Card class="p-4">
            <div class="flex items-center justify-between gap-2 mb-2">
              <span class="text-sm font-medium truncate">{name}</span>
              <div class="flex gap-1.5 shrink-0">
                <Button
                  size="sm"
                  onclick={() => {
                    expandedRows = { ...expandedRows, [name]: !expandedRows[name] };
                  }}>{expandedRows[name] ? $_('users.collapse') : $_('users.edit')}</Button
                >
                <Button variant="destructive" size="sm" onclick={() => handleDelete(name)}>{$_('users.delete')}</Button>
              </div>
            </div>
            <div class="flex gap-3 text-xs">
              <span class="flex items-center gap-1">
                <Badge variant={user.lastfm?.enabled ? 'on' : 'off'}>{user.lastfm?.enabled ? '✓' : '✗'}</Badge>
                Last.fm
              </span>
              <span class="flex items-center gap-1">
                <Badge variant={user.listenbrainz?.enabled ? 'on' : 'off'}
                  >{user.listenbrainz?.enabled ? '✓' : '✗'}</Badge
                >
                ListenBrainz
              </span>
            </div>
            {#if expandedRows[name]}
              <div class="mt-3">
                <UserEditCard
                  {name}
                  bind:user={users[name]}
                  onSave={() => handleSave(name)}
                  canAuth={updateAuthBtnState(name)}
                  authState={authStates[name]}
                />
              </div>
            {/if}
          </Card>
        {/each}
      </div>

      <!-- 桌面端：表格模式 -->
      <table class="hidden sm:table w-full border-collapse bg-card border border-border rounded-lg overflow-hidden">
        <thead>
          <tr class="bg-muted">
            <th class="px-4 py-2.5 text-left text-xs font-semibold text-muted-foreground">{$_('users.username')}</th>
            <th class="px-4 py-2.5 text-left text-xs font-semibold text-muted-foreground">{$_('users.lastfm')}</th>
            <th class="px-4 py-2.5 text-left text-xs font-semibold text-muted-foreground">{$_('users.listenbrainz')}</th
            >
            <th class="px-4 py-2.5 text-left text-xs font-semibold text-muted-foreground">{$_('users.actions')}</th>
          </tr>
        </thead>
        <tbody>
          {#each userEntries as [name, user] (name)}
            <tr class="border-t border-border hover:bg-muted/50">
              <td class="px-4 py-2.5 text-sm">{name}</td>
              <td class="px-4 py-2.5 text-sm">
                {#if user.lastfm?.enabled}
                  <Badge variant="on">✓</Badge>
                  {#if user.lastfm?.username}
                    <span class="text-xs text-muted-foreground ml-1">{user.lastfm.username}</span>
                  {/if}
                {:else}
                  <Badge variant="off">✗</Badge>
                {/if}
              </td>
              <td class="px-4 py-2.5 text-sm">
                {#if user.listenbrainz?.enabled}
                  <Badge variant="on">✓</Badge>
                  {#if user.listenbrainz?.username}
                    <span class="text-xs text-muted-foreground ml-1">{user.listenbrainz.username}</span>
                  {/if}
                {:else}
                  <Badge variant="off">✗</Badge>
                {/if}
              </td>
              <td class="px-4 py-2.5 text-sm whitespace-nowrap">
                <div class="flex gap-1.5">
                  <Button
                    size="sm"
                    onclick={() => {
                      expandedRows = { ...expandedRows, [name]: !expandedRows[name] };
                    }}>{expandedRows[name] ? $_('users.collapse') : $_('users.edit')}</Button
                  >
                  <Button variant="destructive" size="sm" onclick={() => handleDelete(name)}
                    >{$_('users.delete')}</Button
                  >
                </div>
              </td>
            </tr>
            {#if expandedRows[name]}
              <tr class="border-t border-border bg-muted/30">
                <td colspan="4" class="px-4 py-3">
                  <UserEditCard
                    {name}
                    bind:user={users[name]}
                    onSave={() => handleSave(name)}
                    canAuth={updateAuthBtnState(name)}
                    authState={authStates[name]}
                  />
                </td>
              </tr>
            {/if}
          {/each}
        </tbody>
      </table>
    {/if}
  {:else}
    <!-- 普通用户：卡片模式 -->
    {#if userEntries.length === 0 && $currentUser.username}
      {@const name = $currentUser.username}
      {@const user = createEmptyUser()}
      <UserEditCard {name} {user} onSave={() => handleSave(name)} canAuth={false} />
    {:else}
      {#each userEntries as [name] (name)}
        <UserEditCard
          {name}
          bind:user={users[name]}
          onSave={() => handleSave(name)}
          canAuth={updateAuthBtnState(name)}
          authState={authStates[name]}
        />
      {/each}
    {/if}
  {/if}
</section>

<!-- 添加用户模态框 -->
<Dialog bind:open={showAddUserModal}>
  <h2 class="text-base font-semibold mb-2">{$_('users.add_dialog_title')}</h2>
  <p class="text-xs text-muted-foreground mb-3">{$_('users.add_dialog_desc')}</p>
  <div class="flex flex-col sm:flex-row sm:items-center gap-2 sm:gap-3 mb-4">
    <label class="text-xs font-medium sm:min-w-[80px]">{$_('users.fn_username')}</label>
    <Input bind:value={newUserName} placeholder={$_('users.placeholder_username')} />
  </div>
  <div class="flex justify-end gap-2 mt-4">
    <Button onclick={() => (showAddUserModal = false)}>{$_('users.cancel')}</Button>
    <Button variant="primary" onclick={confirmAddUser}>{$_('users.confirm_add')}</Button>
  </div>
</Dialog>
