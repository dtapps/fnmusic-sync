<script lang="ts">
  import type { HTMLSelectAttributes } from 'svelte/elements';
  import { cn } from '$lib/utils';

  let {
    class: className = '',
    value = $bindable(),
    children,
    ...restProps
  }: HTMLSelectAttributes & { value?: any } = $props();
</script>

<select
  bind:value
  class={cn(
    'form-select flex-1 px-3 py-2 border border-input rounded-md text-sm bg-card text-foreground outline-none transition-colors cursor-pointer focus:border-primary',
    className,
  )}
  {...restProps}
>
  {@render children?.()}
</select>

<style>
  /* 用 CSS 变量动态适配箭头颜色 */
  .form-select {
    appearance: none;
    -webkit-appearance: none;
    -moz-appearance: none;
    background-repeat: no-repeat;
    background-position: right 8px center;
    padding-right: 28px;
  }

  /* option 下拉菜单背景适配（Webkit / Blink 浏览器） */
  .form-select option {
    background-color: hsl(var(--card));
    color: hsl(var(--foreground));
  }

  /* 浅色模式箭头 */
  :root .form-select {
    background-image: url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='12' height='12' viewBox='0 0 12 12'%3E%3Cpath fill='%2364748b' d='M6 8L2 4h8z'/%3E%3C/svg%3E");
  }

  /* 深色模式箭头 */
  [data-theme='dark'] .form-select {
    background-image: url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='12' height='12' viewBox='0 0 12 12'%3E%3Cpath fill='%2394a3b8' d='M6 8L2 4h8z'/%3E%3C/svg%3E");
  }
</style>
