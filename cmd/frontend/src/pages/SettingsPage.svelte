<script lang="ts">
  import { _ } from 'svelte-i18n';
  import {
    config,
    showToast,
    reloadConfig,
    themeMode,
    languageMode,
    setThemeMode,
    setLanguageMode,
    currentUser,
  } from '$lib/stores';
  import type { ThemeMode, LanguageMode } from '$lib/stores';
  import { saveSettings, checkUpgrade, doUpgrade, getLibrary, saveLibrary } from '$lib/api';
  import { errMessage } from '$lib/utils';
  import { Card, Select, Switch, Input, Button, Badge } from '$lib/components';
  import { pickDirectory, isHostEnvironment, convertPaths } from '$lib/trim';
  import { get } from 'svelte/store';
  import { platformConfig } from '$lib/stores';

  let scrobbleThreshold = $state('auto');
  let playlistEnabled = $state(true);
  let syncInterval = $state('30m');
  let logEnabled = $state(true);
  let logLevel = $state('info');
  let logMaxSize = $state(3);
  let logMaxBackups = $state(5);
  let logMaxAge = $state(7);
  let logCompress = $state(true);

  let saving = $state(false);
  let saveTimer: ReturnType<typeof setTimeout> | null = null;

  // ===== 音乐目录（library.directories）状态 =====
  let directories = $state<string[]>([]);
  let libraryScanInterval = $state('6h');
  let libraryMbidOnlineLookup = $state(false);
  let manualPath = $state('');
  let libSaving = $state(false);
  let libSaveTimer: ReturnType<typeof setTimeout> | null = null;
  const canPick = isHostEnvironment();

  // 内部路径 -> 语义化展示路径（飞牛 trim.file.convertPath）映射；转换失败/非宿主时按原路径展示
  let dirDisplay = $state<Record<string, string>>({});

  // 当前界面语言对应的 API 语言码（convertPath 必填 zh-CN / en-US）
  function effectiveLang(): string {
    const mode = get(languageMode);
    if (mode === 'auto') return get(platformConfig).language || 'zh-CN';
    return mode === 'en' ? 'en-US' : 'zh-CN';
  }

  // 把当前目录列表批量转换为语义路径用于展示
  async function refreshDirDisplay() {
    dirDisplay = await convertPaths(directories, effectiveLang());
  }

  // 界面语言变化时，重新转换展示路径（目录变化由上方配置加载处触发）
  $effect(() => {
    void $languageMode;
    if (directories.length) refreshDirDisplay();
  });

  // ===== 升级相关状态 =====
  let upgradeChecking = $state(false);
  let upgradeInfo = $state<{ current_version: string; latest_version: string; has_update: boolean } | null>(null);
  let upgrading = $state(false);

  // 从 config store 同步到本地状态（仅初始化和配置外部变更时执行）
  $effect(() => {
    const cfg = $config;
    scrobbleThreshold = cfg.playback?.scrobble_threshold || 'auto';
    playlistEnabled = cfg.playlist?.enabled ?? true;
    syncInterval = cfg.playlist?.sync_interval || '30m';
    const log = cfg.logging || {};
    logEnabled = log.enabled ?? true;
    logLevel = log.level || 'info';
    logMaxSize = log.max_size ?? 3;
    logMaxBackups = log.max_backups ?? 5;
    logMaxAge = log.max_age ?? 7;
    logCompress = log.compress ?? true;
    directories = cfg.library?.directories || [];
    libraryScanInterval = cfg.library?.scan_interval || '6h';
    libraryMbidOnlineLookup = cfg.library?.mbid_online_lookup ?? false;
    // 目录来自后端内部路径，拉取后转换为语义化展示路径
    refreshDirDisplay();
  });

  // 用户修改任意设置时触发防抖保存（通过 onchange/oninput 事件，不会因 config 同步而触发）
  function scheduleSave() {
    if (saveTimer) clearTimeout(saveTimer);
    saveTimer = setTimeout(doSave, 800);
  }

  async function doSave() {
    if (saving) return;
    saving = true;
    try {
      const settings = {
        playback: { scrobble_threshold: scrobbleThreshold },
        playlist: {
          enabled: playlistEnabled,
          sync_interval: syncInterval.trim() || '30m',
        },
        logging: {
          enabled: logEnabled,
          level: logLevel,
          max_size: parseInt(String(logMaxSize)) || 0,
          max_backups: parseInt(String(logMaxBackups)) || 0,
          max_age: parseInt(String(logMaxAge)) || 0,
          compress: logCompress,
        },
      };
      await saveSettings(settings);
      await reloadConfig();
    } catch (e) {
      showToast($_('settings.save_failed', { values: { message: errMessage(e) } }), 'error');
    } finally {
      saving = false;
    }
  }

  // ===== 音乐目录相关方法 =====
  function scheduleLibSave() {
    if (libSaveTimer) clearTimeout(libSaveTimer);
    libSaveTimer = setTimeout(doLibSave, 800);
  }

  async function doLibSave() {
    if (libSaving) return;
    libSaving = true;
    try {
      const res = await saveLibrary(directories, libraryScanInterval.trim() || '6h', libraryMbidOnlineLookup);
      directories = res.directories;
      await reloadConfig();
    } catch (e) {
      showToast($_('library.save_failed', { values: { message: errMessage(e) } }), 'error');
    } finally {
      libSaving = false;
    }
  }

  // 通过 fnOS 原生文件夹选择器选目录（宿主环境）
  async function handlePick() {
    const paths = await pickDirectory();
    if (!paths || !paths.length) return;
    let changed = false;
    for (const p of paths) {
      if (!directories.includes(p)) {
        directories = [...directories, p];
        changed = true;
      }
    }
    if (changed) scheduleLibSave();
  }

  // 手动输入路径添加（非宿主环境 / 开发调试）
  function handleAddManual() {
    const p = manualPath.trim();
    if (!p) return;
    if (directories.includes(p)) {
      manualPath = '';
      return;
    }
    directories = [...directories, p];
    manualPath = '';
    scheduleLibSave();
  }

  function removeDir(idx: number) {
    directories = directories.filter((_, i) => i !== idx);
    scheduleLibSave();
  }

  // ===== 升级相关方法 =====
  async function handleCheckUpgrade() {
    upgradeChecking = true;
    upgradeInfo = null;
    try {
      upgradeInfo = await checkUpgrade();
    } catch (e) {
      showToast($_('upgrade.check_failed', { values: { message: errMessage(e) } }), 'error');
    } finally {
      upgradeChecking = false;
    }
  }

  async function handleUpgrade() {
    if (!upgradeInfo?.latest_version) return;
    if (!confirm($_('upgrade.confirm_upgrade', { values: { version: upgradeInfo.latest_version } }))) return;
    upgrading = true;
    try {
      await doUpgrade();
      showToast($_('upgrade.upgrade_success'), 'success');
      // 升级后应用会重启，延迟刷新页面
      setTimeout(() => window.location.reload(), 5000);
    } catch (e) {
      showToast($_('upgrade.upgrade_failed', { values: { message: errMessage(e) } }), 'error');
    } finally {
      upgrading = false;
    }
  }
</script>

<section>
  <Card>
    <h2 class="text-base font-semibold mb-4">{$_('appearance.title')}</h2>
    <div class="flex flex-col sm:flex-row sm:items-center gap-2 sm:gap-3 mb-3">
      <label class="text-xs font-medium sm:min-w-[120px]">{$_('appearance.theme')}</label>
      <Select value={$themeMode} onchange={(e) => setThemeMode(e.currentTarget.value as ThemeMode)}>
        <option value="auto">{$_('appearance.theme_auto')}</option>
        <option value="light">{$_('appearance.theme_light')}</option>
        <option value="dark">{$_('appearance.theme_dark')}</option>
      </Select>
    </div>
    <div class="flex flex-col sm:flex-row sm:items-center gap-2 sm:gap-3">
      <label class="text-xs font-medium sm:min-w-[120px]">{$_('appearance.language')}</label>
      <Select value={$languageMode} onchange={(e) => setLanguageMode(e.currentTarget.value as LanguageMode)}>
        <option value="auto">{$_('appearance.language_auto')}</option>
        <option value="zh-CN">{$_('appearance.language_zh_cn')}</option>
        <option value="en">{$_('appearance.language_en')}</option>
      </Select>
    </div>
  </Card>

  <Card>
    <div class="flex items-center justify-between gap-2 mb-4">
      <h2 class="text-base font-semibold">{$_('settings.playback')}</h2>
      {#if saving}<span class="text-xs text-muted-foreground">{$_('settings.auto_saving')}</span>{/if}
    </div>
    <div class="flex flex-col sm:flex-row sm:items-center gap-2 sm:gap-3">
      <label class="text-xs font-medium sm:min-w-[120px]">{$_('settings.scrobble_threshold')}</label>
      <Select bind:value={scrobbleThreshold} onchange={scheduleSave}>
        <option value="auto">{$_('settings.threshold_auto')}</option>
        <option value="30s">30s</option>
        <option value="2m">2m</option>
        <option value="50%">50%</option>
        <option value="off">off</option>
      </Select>
    </div>
  </Card>

  <Card>
    <h2 class="text-base font-semibold mb-4">{$_('settings.playlist_sync')}</h2>
    <div class="flex flex-col sm:flex-row sm:items-center gap-2 sm:gap-3 mb-3">
      <label class="text-xs font-medium sm:min-w-[120px]">{$_('settings.sync_enabled')}</label>
      <Switch bind:checked={playlistEnabled} onchange={scheduleSave} />
    </div>
    <div class="flex flex-col sm:flex-row sm:items-center gap-2 sm:gap-3">
      <label class="text-xs font-medium sm:min-w-[120px]">{$_('settings.sync_interval')}</label>
      <Input bind:value={syncInterval} oninput={scheduleSave} placeholder="30m" />
    </div>
  </Card>

  <Card>
    <h2 class="text-base font-semibold mb-4">{$_('settings.logging')}</h2>
    <div class="flex flex-col sm:flex-row sm:items-center gap-2 sm:gap-3 mb-3">
      <div class="flex flex-col sm:flex-row sm:items-center gap-2 sm:gap-3">
        <label class="text-xs font-medium sm:min-w-[120px]">{$_('settings.log_enabled')}</label>
        <Switch bind:checked={logEnabled} onchange={scheduleSave} />
      </div>
      <span class="text-xs text-muted-foreground">{$_('settings.log_enabled_hint')}</span>
    </div>
    {#if logEnabled}
      <div class="flex flex-col sm:flex-row sm:items-center gap-2 sm:gap-3 mb-3">
        <label class="text-xs font-medium sm:min-w-[120px]">{$_('settings.log_level')}</label>
        <Select bind:value={logLevel} onchange={scheduleSave}>
          <option value="info">info</option>
          <option value="debug">debug</option>
          <option value="warn">warn</option>
          <option value="error">error</option>
        </Select>
      </div>
      <div class="flex flex-col sm:flex-row sm:items-center gap-2 sm:gap-3 mb-3">
        <label class="text-xs font-medium sm:min-w-[120px]">{$_('settings.log_max_size')}</label>
        <Input type="number" bind:value={logMaxSize} oninput={scheduleSave} min="0" />
      </div>
      <div class="flex flex-col sm:flex-row sm:items-center gap-2 sm:gap-3 mb-3">
        <label class="text-xs font-medium sm:min-w-[120px]">{$_('settings.log_max_backups')}</label>
        <Input type="number" bind:value={logMaxBackups} oninput={scheduleSave} min="0" />
      </div>
      <div class="flex flex-col sm:flex-row sm:items-center gap-2 sm:gap-3 mb-3">
        <label class="text-xs font-medium sm:min-w-[120px]">{$_('settings.log_max_age')}</label>
        <Input type="number" bind:value={logMaxAge} oninput={scheduleSave} min="0" />
      </div>
      <div class="flex flex-col sm:flex-row sm:items-center gap-2 sm:gap-3">
        <label class="text-xs font-medium sm:min-w-[120px]">{$_('settings.log_compress')}</label>
        <Switch bind:checked={logCompress} onchange={scheduleSave} />
      </div>
    {/if}
  </Card>

  {#if $currentUser.isAdmin}
    <Card>
      <div class="flex items-center justify-between gap-2 mb-4">
        <h2 class="text-base font-semibold">{$_('library.title')}</h2>
        {#if libSaving}<span class="text-xs text-muted-foreground">{$_('settings.auto_saving')}</span>{/if}
      </div>
      <p class="text-xs text-muted-foreground mb-4">{$_('library.desc')}</p>
      {#if directories.length > 0}
        <ul class="flex flex-col gap-2 mb-4">
          {#each directories as dir, i (dir)}
            <li class="flex items-center gap-2 rounded-md border border-border bg-muted/40 px-3 py-2">
              <code class="text-xs flex-1 break-all">{dirDisplay[dir] ?? dir}</code>
              <Button variant="ghost" size="sm" onclick={() => removeDir(i)}>{$_('library.remove')}</Button>
            </li>
          {/each}
        </ul>
      {:else}
        <p class="text-xs text-muted-foreground mb-4">{$_('library.empty')}</p>
      {/if}
      <div class="flex flex-col sm:flex-row gap-2">
        {#if canPick}
          <Button variant="primary" size="sm" onclick={handlePick}>{$_('library.pick')}</Button>
        {/if}
        <div class="flex flex-1 gap-2">
          <Input
            bind:value={manualPath}
            onkeydown={(e: KeyboardEvent) => {
              if (e.key === 'Enter') handleAddManual();
            }}
            placeholder={$_('library.placeholder')}
          />
          <Button variant="outline" size="sm" onclick={handleAddManual}>{$_('library.add')}</Button>
        </div>
      </div>
      <div class="flex flex-col sm:flex-row sm:items-center gap-2 sm:gap-3 mt-4">
        <label class="text-xs font-medium sm:min-w-[120px]">{$_('library.scan_interval')}</label>
        <Input
          bind:value={libraryScanInterval}
          oninput={scheduleLibSave}
          placeholder={$_('library.scan_interval_placeholder')}
        />
      </div>
      <p class="text-xs text-muted-foreground mt-1">{$_('library.scan_interval_desc')}</p>
      <div class="flex flex-col sm:flex-row sm:items-center gap-2 sm:gap-3 mt-4">
        <label class="text-xs font-medium sm:min-w-[120px]">{$_('library.mbid_online_lookup')}</label>
        <Switch bind:checked={libraryMbidOnlineLookup} onchange={scheduleLibSave} />
        <span class="text-xs text-muted-foreground">{$_('library.mbid_online_lookup_desc')}</span>
      </div>
    </Card>

    <Card>
      <h2 class="text-base font-semibold mb-4">{$_('upgrade.title')}</h2>
      <div class="flex flex-col gap-3">
        <div class="flex flex-col sm:flex-row sm:items-center gap-2 sm:gap-3">
          <label class="text-xs font-medium sm:min-w-[120px]">{$_('upgrade.current_version')}</label>
          <span class="text-sm text-muted-foreground"
            >{upgradeInfo?.current_version || (upgradeInfo === null && '...') || '-'}</span
          >
        </div>
        {#if upgradeInfo}
          <div class="flex flex-col sm:flex-row sm:items-center gap-2 sm:gap-3">
            <label class="text-xs font-medium sm:min-w-[120px]">{$_('upgrade.latest_version')}</label>
            <div class="flex items-center gap-2">
              <span class="text-sm text-muted-foreground">{upgradeInfo.latest_version}</span>
              {#if upgradeInfo.has_update}
                <Badge variant="off">{$_('upgrade.has_update')}</Badge>
              {:else}
                <Badge variant="on">{$_('upgrade.up_to_date')}</Badge>
              {/if}
            </div>
          </div>
        {/if}
        <div class="flex gap-2 mt-2">
          <Button variant="outline" size="sm" onclick={handleCheckUpgrade} disabled={upgradeChecking || upgrading}>
            {upgradeChecking ? $_('upgrade.checking') : $_('common.refresh')}
          </Button>
          {#if upgradeInfo?.has_update}
            <Button variant="primary" size="sm" onclick={handleUpgrade} disabled={upgrading}>
              {upgrading ? $_('upgrade.upgrading') : $_('upgrade.upgrade_button')}
            </Button>
          {/if}
        </div>
      </div>
    </Card>
  {/if}
</section>
