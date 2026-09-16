<script lang="ts">
  import { cn } from '$lib/utils';
  import type { Snippet } from 'svelte';

  let { open = $bindable(false), children }: { open?: boolean; children?: Snippet } = $props();
</script>

{#if open}
  <div class="fixed inset-0 z-[1000] flex items-center justify-center">
    <!-- 遮罩层 -->
    <div class="absolute inset-0 bg-black/40" onclick={() => (open = false)}></div>
    <!-- 对话框 -->
    <div
      class="relative bg-card rounded-lg p-4 sm:p-6 w-[92%] max-w-md shadow-xl border border-border max-h-[90vh] overflow-y-auto"
    >
      {@render children?.()}
    </div>
  </div>
{/if}
