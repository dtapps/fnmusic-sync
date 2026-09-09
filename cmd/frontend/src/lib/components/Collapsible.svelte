<script lang="ts">
  import { cn } from '$lib/utils';

  let {
    open = $bindable(false),
    title = '',
    children,
    headerExtra,
    class: className = '',
  }: {
    open?: boolean;
    title?: string;
    children?: any;
    headerExtra?: any;
    class?: string;
  } = $props();
</script>

<div class={cn('mt-4 border-t border-border pt-4', className)}>
  <div class="flex items-center gap-2 cursor-pointer font-semibold text-sm select-none" onclick={() => (open = !open)}>
    <span class="text-[10px] transition-transform" style={open ? 'transform: rotate(90deg);' : ''}>▶</span>
    {title}
    {#if headerExtra}
      <span onclick={(e) => e.stopPropagation()} class="ml-auto">
        {@render headerExtra?.()}
      </span>
    {/if}
  </div>
  {#if open}
    <div class="px-3 sm:px-6 py-2 sm:py-3">
      {@render children?.()}
    </div>
  {/if}
</div>
