<script lang="ts">
  import type { HTMLInputAttributes } from 'svelte/elements';
  import { cn } from '$lib/utils';

  let { class: className = '', value = $bindable(), ...restProps }: HTMLInputAttributes = $props();
</script>

<input
  bind:value
  class={cn(
    'flex-1 px-3 py-2 border border-input rounded-md text-sm bg-card text-foreground outline-none transition-colors focus:border-primary',
    className,
  )}
  {...restProps}
/>
