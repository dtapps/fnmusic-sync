import js from '@eslint/js'
import tseslint from 'typescript-eslint'
import svelte from 'eslint-plugin-svelte'
import globals from 'globals'
import prettier from 'eslint-config-prettier'

export default tseslint.config(
  // 忽略构建产物与自动生成的静态资源（www 由 format 单独处理）
  { ignores: ['dist/**', 'build/**', 'node_modules/**', '../../internal/webui/www/**'] },

  // Svelte 文件：用 svelte-eslint-parser，其 <script> 块再交给 TS 解析器
  {
    files: ['**/*.svelte'],
    languageOptions: {
      parser: svelte.parser,
      parserOptions: {
        parser: tseslint.parser,
        extraFileExtensions: ['.svelte'],
      },
    },
  },

  js.configs.recommended,
  ...tseslint.configs.recommended,
  ...svelte.configs['flat/recommended'],
  prettier, // 关闭与 prettier 冲突的规则，格式交给 prettier --check

  {
    files: ['**/*.ts', '**/*.js', '**/*.svelte'],
    languageOptions: {
      globals: { ...globals.browser },
    },
    rules: {
      // 未使用变量告警（以 _ 开头的参数/变量视为有意忽略）
      '@typescript-eslint/no-unused-vars': [
        'warn',
        { argsIgnorePattern: '^_', varsIgnorePattern: '^_' },
      ],
      // 外部 API 响应常用 any，按 stylistic 类告警而非阻塞（如需严格可改 'error'）
      '@typescript-eslint/no-explicit-any': 'warn',
      // 开发期允许 console
      'no-console': 'off',
      // 生产代码若需禁止 console，可改为 'error'
    },
  },
)
