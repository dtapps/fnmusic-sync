import { defineConfig, mergeConfig } from 'vitest/config';
import viteConfig from './vite.config';

// 扫描 / 排除清单由 Makefile「测试覆盖率」节通过环境变量注入（唯一来源），
// 新增目录只改 Makefile；不经过 make 直接跑 vitest 时回退到默认值，行为不变。
// 多个 glob 用空格或逗号分隔。
const list = (value: string | undefined, fallback: string[]) =>
  value ? value.split(/[,\s]+/).filter(Boolean) : fallback;

// 复用 vite.config 的插件（svelte）与别名（$lib），仅额外声明测试环境与覆盖率。
// 配置说明见 docs/coverage-node.md。
export default mergeConfig(
  viteConfig,
  defineConfig({
    test: {
      environment: 'node', // 纯函数/store 测试无需 DOM；后续组件测试再切 jsdom
      setupFiles: ['./src/test/setup.ts'],
      include: list(process.env.VITEST_TEST_INCLUDE, ['src/test/**/*.test.ts']),
      coverage: {
        provider: 'v8',
        // lcov 供 CI 插件（CNB testing:coverage / Coveralls）解析，text 供本地阅读。
        reporter: ['lcov', 'text'],
        reportsDirectory: './coverage',
        // 只度量逻辑代码（.ts）。.svelte 视图组件暂不写测试，计入分母会让指标失去意义，
        // 组件测试（@testing-library/svelte + jsdom）作为第二步再纳入。
        include: list(process.env.VITEST_COVER_INCLUDE, ['src/**/*.ts']),
        exclude: list(process.env.VITEST_COVER_EXCLUDE, ['src/test/**', 'src/app.ts', 'src/**/*.d.ts']),
        // 全量度量：include 显式列出源码目录，未被测试 import 的文件也按 0% 计入分母，
        // 指标不虚高（Vitest 5 已移除 coverage.all，全量由 coverage.include 实现）。
      },
    },
  }),
);
