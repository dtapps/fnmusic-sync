<script lang="ts">
  import type { HTMLAttributes } from 'svelte/elements';
  import { cn } from '$lib/utils';

  type Variant = 'on' | 'off' | 'default';

  let {
    variant = 'default' as Variant,
    class: className = '',
    children,
    ...restProps
  }: HTMLAttributes<HTMLSpanElement> & { variant?: Variant } = $props();

  const variants: Record<Variant, string> = {
    on: 'bg-green-100 text-green-700 dark:bg-green-500/15 dark:text-green-400',
    off: 'bg-red-100 text-red-700 dark:bg-red-500/15 dark:text-red-400',
    default: 'bg-slate-100 text-slate-700 dark:bg-slate-500/15 dark:text-slate-400',
  };
</script>

<span class={cn('inline-block px-2 py-0.5 rounded text-xs font-medium', variants[variant], className)} {...restProps}>
  {@render children?.()}
</span>
