import { defineConfig, mergeConfig } from 'vitest/config';
import viteConfig from './vite.config';

// 复用 vite.config 的插件（svelte）与别名（$lib），仅额外声明测试环境与覆盖率。
export default mergeConfig(
  viteConfig,
  defineConfig({
    test: {
      environment: 'node', // 纯函数/store 测试无需 DOM；后续组件测试再切 jsdom
      setupFiles: ['./src/test/setup.ts'],
      coverage: {
        provider: 'v8',
        reporter: ['lcov', 'text'], // lcov 供 CI 插件解析，text 供本地阅读
        reportsDirectory: './coverage',
        // 只度量逻辑代码（.ts）。.svelte 视图组件暂不写测试，计入分母会让指标失去意义，
        // 组件测试（@testing-library/svelte + jsdom）作为第二步再纳入。
        include: ['src/**/*.ts'],
        exclude: [
          'src/test/**', // 测试自身不计入
          'src/app.ts', // 入口：导入期即挂载 DOM，node 下会崩
          'src/**/*.d.ts',
        ],
        all: true, // 全量度量：未测试文件按 0% 计入分母，指标不虚高
      },
    },
  }),
);
