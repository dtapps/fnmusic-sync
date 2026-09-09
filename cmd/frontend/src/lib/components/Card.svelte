<script lang="ts">
  import type { HTMLAttributes } from 'svelte/elements';
  import { cn } from '$lib/utils';

  let { class: className = '', children, ...restProps }: HTMLAttributes<HTMLDivElement> = $props();
</script>

<div
  class={cn('rounded-lg border border-border bg-card text-card-foreground p-4 sm:p-5 mb-3 sm:mb-4', className)}
  {...restProps}
>
  {@render children?.()}
</div>
