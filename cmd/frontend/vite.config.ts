import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import path from 'path';

export default defineConfig({
  // 静态资源引用前缀，与飞牛网关路径一致
  base: '/app/fnmusic-sync/',
  plugins: [svelte()],
  build: {
    // 输出到 Go embed 目录
    outDir: '../../internal/webui/www',
    // 不自动清空目录（build.sh 控制清理，保护 index.html 和 images/）
    emptyOutDir: false,
    sourcemap: false,
    rollupOptions: {
      // 直接用 app.ts 作为入口，不使用 index.html
      input: 'src/app.ts',
      output: {
        // 固定文件名，不用 hash，保持 Go embed 引用稳定
        entryFileNames: 'app.js',
        chunkFileNames: 'chunks/[name].js',
        assetFileNames: '[name][extname]',
      },
    },
  },
  resolve: {
    alias: {
      $lib: path.resolve('./src/lib'),
    },
  },
  server: {
    port: 5173,
    // 开发时代理 API 请求到 Go 后端
    proxy: {
      '/app/fnmusic-sync/api': {
        target: 'http://localhost:8080',
        changeOrigin: true,
      },
    },
  },
});
