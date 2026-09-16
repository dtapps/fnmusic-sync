<script lang="ts">
  import './app.css';
  import './lib/i18n';
  import { onMount } from 'svelte';
  import { _ } from 'svelte-i18n';
  import {
    activeTab,
    setActiveTab,
    currentUser,
    versionInfo,
    config,
    state,
    showToast,
    reloadConfig,
    resolveTheme,
    resolveLanguage,
    onPlatformConfigUpdate,
    platformConfig,
  } from '$lib/stores';
  import { getVersion, getCurrentUser, getConfig, getState, getLogs } from '$lib/api';
  import { formatBuildTime } from '$lib/utils';
  import { Toast } from '$lib/components';
  import UsersPage from './pages/UsersPage.svelte';
  import SettingsPage from './pages/SettingsPage.svelte';
  import StatePage from './pages/StatePage.svelte';
  import LogsPage from './pages/LogsPage.svelte';
  import UserListPage from './pages/UserListPage.svelte';
  import { BASE_URL } from '$lib/api';
  import { getPlatformConfig, onThemeChange, onLanguageChange, isHostEnvironment } from '$lib/trim';

  let versionDisplay = $derived.by(() => {
    const v = $versionInfo;
    if (!v) return $_('app.version_loading');
    if (!v.version || v.version === 'failed') return $_('app.version_failed');
    const time = formatBuildTime(v.buildTime);
    return `${v.version} (${v.gitCommit}) ${time}`;
  });

  onMount(async () => {
    // ===== 1. 先应用用户保存的偏好（从 localStorage 读取的）=====
    // auto 模式下用默认值（浅色+zh-CN），SDK 加载后再更新
    resolveTheme();
    resolveLanguage();

    // ===== 2. 初始化飞牛 SDK：获取宿主主题/语言/系统版本 =====
    // 仅在飞牛宿主环境中使用 SDK 的配置
    const isHost = isHostEnvironment();
    if (isHost) {
      try {
        const cfg = await getPlatformConfig();
        // 更新宿主配置，resolveTheme/resolveLanguage 会根据用户选择的 mode
        // 决定是否使用宿主值（auto）还是用户手动选择的值
        onPlatformConfigUpdate(cfg);
      } catch {
        // 降级：使用默认值
      }
    }

    // 监听主题/语言变化（仅 Web 宿主环境有效）
    // 仅当用户选择 auto 模式时才响应宿主变化
    onThemeChange((t) => {
      platformConfig.update((p) => ({ ...p, theme: t }));
      resolveTheme();
    });

    onLanguageChange((lang) => {
      platformConfig.update((p) => ({ ...p, language: lang }));
      resolveLanguage();
    });

    // ===== 2. 加载版本信息 =====
    try {
      const v = await getVersion();
      versionInfo.set(v);
    } catch {
      versionInfo.set({ version: 'failed', gitCommit: '', buildTime: '', binaryName: '' });
    }

    // ===== 3. 加载用户身份 =====
    try {
      const user = await getCurrentUser();
      currentUser.set(user);
    } catch {
      currentUser.set({ uid: '0', username: 'local', isAdmin: true });
    }

    // ===== 4. 加载配置/状态/日志 =====
    await reloadConfig();
    await loadState();
    if ($currentUser.isAdmin) {
      await loadLogs();
    }
  });

  async function loadState() {
    try {
      const s = await getState();
      state.set(s);
    } catch {
      // 静默失败
    }
  }

  async function loadLogs() {
    // LogsPage 自己加载
  }
</script>

<div id="app">
  <header class="bg-card border-b border-border px-3 sm:px-6 py-3 sm:py-4">
    <div class="flex items-center justify-between gap-3 mb-2 sm:mb-3">
      <div class="flex items-center gap-2 sm:gap-3 min-w-0">
        <img
          src="{BASE_URL}/images/icon_256.png"
          alt="logo"
          class="w-8 h-8 sm:w-10 sm:h-10 rounded-lg shrink-0"
          onerror={(e) => {
            (e.target as HTMLImageElement).style.display = 'none';
          }}
        />
        <div class="min-w-0">
          <h1 class="text-base sm:text-lg font-semibold truncate">{$_('app.title')}</h1>
          <p class="text-xs text-muted-foreground truncate hidden sm:block">{versionDisplay}</p>
        </div>
      </div>
    </div>
    <nav class="flex gap-1 overflow-x-auto -mx-1 px-1 pb-0.5" style="scrollbar-width: none;">
      <button
        class="px-3 sm:px-4 py-1.5 sm:py-2 rounded-md text-xs sm:text-sm whitespace-nowrap transition-colors cursor-pointer shrink-0
          {$activeTab === 'users' ? 'bg-primary text-primary-foreground' : 'text-muted-foreground hover:bg-muted'}"
        onclick={() => setActiveTab('users')}>{$_('tabs.users')}</button
      >
      {#if $currentUser.isAdmin}
        <button
          class="px-3 sm:px-4 py-1.5 sm:py-2 rounded-md text-xs sm:text-sm whitespace-nowrap transition-colors cursor-pointer shrink-0
            {$activeTab === 'users_list'
            ? 'bg-primary text-primary-foreground'
            : 'text-muted-foreground hover:bg-muted'}"
          onclick={() => setActiveTab('users_list')}>{$_('tabs.users_list')}</button
        >
      {/if}
      {#if $currentUser.isAdmin}
        <button
          class="px-3 sm:px-4 py-1.5 sm:py-2 rounded-md text-xs sm:text-sm whitespace-nowrap transition-colors cursor-pointer shrink-0
            {$activeTab === 'settings' ? 'bg-primary text-primary-foreground' : 'text-muted-foreground hover:bg-muted'}"
          onclick={() => setActiveTab('settings')}>{$_('tabs.settings')}</button
        >
      {/if}
      <button
        class="px-3 sm:px-4 py-1.5 sm:py-2 rounded-md text-xs sm:text-sm whitespace-nowrap transition-colors cursor-pointer shrink-0
          {$activeTab === 'state' ? 'bg-primary text-primary-foreground' : 'text-muted-foreground hover:bg-muted'}"
        onclick={() => setActiveTab('state')}>{$_('tabs.state')}</button
      >
      {#if $currentUser.isAdmin}
        <button
          class="px-3 sm:px-4 py-1.5 sm:py-2 rounded-md text-xs sm:text-sm whitespace-nowrap transition-colors cursor-pointer shrink-0
            {$activeTab === 'logs' ? 'bg-primary text-primary-foreground' : 'text-muted-foreground hover:bg-muted'}"
          onclick={() => setActiveTab('logs')}>{$_('tabs.logs')}</button
        >
      {/if}
    </nav>
  </header>

  <main class="p-3 sm:p-6">
    {#if $activeTab === 'users'}
      <UsersPage />
    {:else if $activeTab === 'users_list'}
      <UserListPage />
    {:else if $activeTab === 'settings'}
      <SettingsPage />
    {:else if $activeTab === 'state'}
      <StatePage />
    {:else if $activeTab === 'logs'}
      <LogsPage />
    {/if}
  </main>
</div>

<Toast />
