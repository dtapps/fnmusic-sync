<script lang="ts">
  import type { HTMLButtonAttributes } from 'svelte/elements';
  import { cn } from '$lib/utils';

  type Variant = 'default' | 'primary' | 'destructive' | 'outline' | 'ghost' | 'auth';
  type Size = 'default' | 'sm' | 'icon';

  interface Props extends HTMLButtonAttributes {
    variant?: Variant;
    size?: Size;
    class?: string;
  }

  let { variant = 'default', size = 'default', class: className = '', children, ...restProps }: Props = $props();

  const variants: Record<Variant, string> = {
    default: 'border border-border bg-card text-foreground hover:bg-muted',
    primary: 'bg-primary text-primary-foreground border-primary hover:bg-primary-hover',
    destructive: 'bg-destructive text-destructive-foreground border-destructive hover:bg-destructive/90',
    outline: 'border border-input bg-transparent hover:bg-accent hover:text-accent-foreground',
    ghost: 'hover:bg-accent hover:text-accent-foreground',
    auth: 'bg-[#e11d48] text-white border-[#e11d48] hover:bg-[#be123c] whitespace-nowrap disabled:opacity-60 disabled:cursor-not-allowed',
  };

  const sizes: Record<Size, string> = {
    default: 'px-5 py-2 text-sm',
    sm: 'px-3 py-1 text-xs',
    icon: 'h-8 w-8',
  };
</script>

<button
  class={cn(
    'inline-flex items-center justify-center rounded-md font-medium transition-colors cursor-pointer disabled:pointer-events-none',
    variants[variant],
    sizes[size],
    className,
  )}
  {...restProps}
>
  {@render children?.()}
</button>
