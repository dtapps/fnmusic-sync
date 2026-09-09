<script lang="ts">
  import { cn } from '$lib/utils';

  let {
    value = $bindable(''),
    placeholder = '',
    class: className = '',
  }: { value?: string; placeholder?: string; class?: string } = $props();

  let show = $state(false);
</script>

<div class={cn('flex-1 flex items-center relative', className)}>
  <input
    type={show ? 'text' : 'password'}
    bind:value
    {placeholder}
    class="flex-1 w-full px-3 py-2 pr-9 border border-input rounded-md text-sm bg-card text-foreground outline-none transition-colors focus:border-primary"
  />
  <button
    type="button"
    class="absolute right-1.5 top-1/2 -translate-y-1/2 bg-transparent border-none cursor-pointer text-base opacity-60 hover:opacity-100"
    onclick={() => (show = !show)}
  >
    {show ? '🙈' : '👁'}
  </button>
</div>
