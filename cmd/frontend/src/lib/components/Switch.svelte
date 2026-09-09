<script lang="ts">
  import { cn } from '$lib/utils';

  let {
    checked = $bindable(false),
    class: className = '',
    onchange,
  }: { checked?: boolean; class?: string; onchange?: (e: Event) => void } = $props();
</script>

<label class={cn('relative inline-block w-10 h-[22px] flex-shrink-0', className)}>
  <input type="checkbox" bind:checked {onchange} class="opacity-0 w-0 h-0" />
  <span
    class="absolute inset-0 rounded-full transition-colors cursor-pointer"
    class:bg-primary={checked}
    class:bg-muted={!checked}
  >
    <span
      class="absolute top-[2px] left-[2px] w-[18px] h-[18px] bg-white rounded-full transition-transform"
      style={checked ? 'transform: translateX(18px);' : ''}
    ></span>
  </span>
</label>
