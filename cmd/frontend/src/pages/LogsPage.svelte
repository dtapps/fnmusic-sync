<script lang="ts">
  import { _ } from 'svelte-i18n';
  import { getLogs } from '$lib/api';
  import { Button, Card } from '$lib/components';

  type LogLevel = 'all' | 'debug' | 'info' | 'warn' | 'error';

  let allLines = $state<string[]>([]);
  let fileEnabled = $state(true);
  let filter = $state<LogLevel>('all');
  let loading = $state(false);

  async function refresh() {
    loading = true;
    try {
      const data: any = await getLogs();
      allLines = data.lines || [];
      fileEnabled = data.file_enabled !== false;
    } catch (e: any) {
      allLines = [e.message];
      fileEnabled = true;
    } finally {
      loading = false;
    }
  }

  // 初始化加载
  $effect(() => {
    refresh();
  });

  // 从 slog TextHandler 日志行中提取等级
  // 格式: time=2026-09-11T10:30:00Z level=INFO msg="..." key=value
  function extractLevel(line: string): string {
    const m = line.match(/\blevel=(\w+)/i);
    if (m) return m[1].toLowerCase();
    // 兼容非标准格式
    if (/error|err/i.test(line)) return 'error';
    if (/warn/i.test(line)) return 'warn';
    if (/debug|dbg/i.test(line)) return 'debug';
    return 'info';
  }

  let filteredLines = $derived.by(() => {
    if (filter === 'all') return allLines;
    return allLines.filter((line) => extractLevel(line) === filter);
  });

  // 按等级给日志行着色
  function lineClass(line: string): string {
    const level = extractLevel(line);
    if (level === 'error') return 'text-red-500';
    if (level === 'warn') return 'text-amber-500';
    if (level === 'debug') return 'text-muted-foreground';
    return '';
  }

  const filterOptions: { value: LogLevel; label: string }[] = [
    { value: 'all', label: 'logs.filter_all' },
    { value: 'debug', label: 'logs.filter_debug' },
    { value: 'info', label: 'logs.filter_info' },
    { value: 'warn', label: 'logs.filter_warn' },
    { value: 'error', label: 'logs.filter_error' },
  ];
</script>

<section class="flex flex-col gap-3 sm:gap-4">
  <Card>
    <div class="flex items-center justify-between gap-2 mb-4 flex-wrap">
      <h2 class="text-base font-semibold">{$_('tabs.logs')}</h2>
      <Button size="sm" onclick={refresh} disabled={loading}>
        {loading ? '...' : $_('common.refresh')}
      </Button>
    </div>

    {#if !fileEnabled}
      <div
        class="px-3 py-2.5 rounded-md text-xs mb-3 bg-amber-50 text-amber-700 border border-amber-200 dark:bg-amber-500/10 dark:text-amber-400 dark:border-amber-500/20"
      >
        {$_('logs.file_disabled')}
      </div>
    {:else}
      <!-- 等级过滤 -->
      <div class="flex gap-1 mb-3 flex-wrap">
        {#each filterOptions as opt (opt.value)}
          <button
            class="px-2.5 py-1 rounded-md text-xs font-medium transition-colors cursor-pointer
              {filter === opt.value ? 'bg-primary text-primary-foreground' : 'text-muted-foreground hover:bg-muted'}"
            onclick={() => (filter = opt.value)}
          >
            {$_(opt.label)}
          </button>
        {/each}
      </div>

      {#if filteredLines.length === 0}
        <p class="text-center text-muted-foreground text-sm py-8">
          {$_('logs.no_data')}
        </p>
      {:else}
        <pre
          class="log-content"><!-- prettier-ignore -->{#each filteredLines as line, i (i)}<span class={lineClass(line)}>{line}</span>
{/each}</pre>
      {/if}
    {/if}
  </Card>
</section>
