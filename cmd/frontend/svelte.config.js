import { vitePreprocess } from '@sveltejs/vite-plugin-svelte';

export default {
  // Svelte 5 runes 模式
  preprocess: vitePreprocess(),
  compilerOptions: {
    runes: true,
  },
};
