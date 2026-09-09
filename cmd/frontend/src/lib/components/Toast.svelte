<script lang="ts">
  import { toasts } from '$lib/stores';
  import { cn } from '$lib/utils';
</script>

{#each $toasts as toast (toast.id)}
  <div
    class={cn(
      'fixed bottom-6 right-6 px-6 py-3 rounded-lg text-white text-sm z-[9999] transition-all duration-300 pointer-events-none',
      toast.type === 'success' ? 'bg-green-600' : 'bg-red-600',
      'opacity-100 translate-y-0',
    )}
  >
    {toast.message}
  </div>
{/each}
