# Node 单元测试覆盖率实现文案（fnmusic-sync 前端实战：Svelte 5 + vitest + CNB）

> 按 fnmusic-sync 项目 `cmd/frontend` 实际实现整理：Svelte 5 + Vite + TypeScript + pnpm，vitest 5，7 个测试文件 110 个用例，lines 基线 97.32%。 扫描 / 排除清单唯一定义在 Makefile「测试覆盖率」节，由 `cmd/frontend/vitest.config.ts` 通过环境变量读取，CI 只调 `make cover-node` / `make junit-node`。 方案可复制到其他 Vite 系项目（Vue/React/Svelte），按项目实际调整目录、入口排除项与环境桩。

# 任务：为 Svelte 5 前端实现单元测试覆盖率上报与红线门禁（CNB 云原生构建）

## 背景与目标

- 构建平台是 CNB（cnb.cool），流水线配置在仓库根的 `.cnb.yml`（include `.cnb/workflows/*.yml`）。
- 使用 CNB 内置任务 `type: testing:coverage` 上报覆盖率：push 事件上报全量覆盖率并设红线（lines: 40），pull_request 事件额外计算增量覆盖率并设红线（diffLines: 50 + lines: 40，diffLines 仅 PR 类事件生效）。
- 与 Go 侧覆盖率共存于同一仓库：Node 侧用 `key: node` / `name: Node Coverage`，Go 侧用 `key: go` / `name: Go Coverage`，各自写入 meta map 互不覆盖，徽章分别显示。
- 红线采用「棘轮策略」：初始值贴着当前基线往下留余量，防止误伤，后续随覆盖率提升逐步上调。
- 技术栈：Svelte 5 + Vite + TypeScript + pnpm + svelte-i18n。测试框架选 **vitest**（不用 jest：jest 配 ESM + SFC 需要 babel/ts-jest 全套改造，vitest 与 Vite 原生集成、零 transform 配置）。

## 实现步骤

### 第 1 步：安装依赖

```bash
pnpm add -D vitest @vitest/coverage-v8
```

本项目锁定 vitest 5.0.5 / @vitest/coverage-v8 5.0.5（`pnpm-lock.yaml` 一并提交，CI 用 `--frozen-lockfile`）。

### 第 2 步：创建 vitest.config.ts（放在 cmd/frontend 根）

用 `mergeConfig` 复用 vite.config 的 svelte 插件与 `$lib` 别名，仅额外声明测试环境与覆盖率； **扫描 / 排除清单从环境变量读取**（由 Makefile 注入，是唯一来源；不经过 make 直接跑 vitest 时回退到默认值，行为不变）：

```ts
import { defineConfig, mergeConfig } from 'vitest/config';
import viteConfig from './vite.config';

// 扫描 / 排除清单由 Makefile「测试覆盖率」节通过环境变量注入（唯一来源），
// 新增目录只改 Makefile；不经过 make 直接跑 vitest 时回退到默认值，行为不变。
// 多个 glob 用空格或逗号分隔。
const list = (value: string | undefined, fallback: string[]) =>
  value ? value.split(/[,\s]+/).filter(Boolean) : fallback;

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
```

对应的 Makefile 清单（唯一来源，加目录只改这里）：

```make
NODE_TEST_SCAN = src/test/**/*.test.ts
NODE_COVER_SCAN = src/**/*.ts
NODE_COVER_EXCLUDE = src/test/** src/app.ts src/**/*.d.ts
NODE_VITEST_ENV = VITEST_TEST_INCLUDE="$(NODE_TEST_SCAN)" VITEST_COVER_INCLUDE="$(NODE_COVER_SCAN)" VITEST_COVER_EXCLUDE="$(NODE_COVER_EXCLUDE)"
```

### 第 3 步：创建测试环境桩 src/test/setup.ts

node 环境没有浏览器全局对象，而模块常在导入期读取它们，不补桩会加载即崩。本项目需要四件套：

- **localStorage**：`stores.ts` 导入期读取主题/语言偏好（`readStored`）。
- **window**：`api.ts` 导入期读 `window.BaseURL` 拼 API 前缀；`stores.ts` 读 `window.location.hash` 恢复 Tab 并监听 `hashchange`（需最小事件系统）。
- **navigator**：svelte-i18n 的 `getLocaleFromNavigator()` 读的是 `window.navigator.language`（不是全局 navigator）。
- **document**：svelte-i18n 的 locale store 变更时写 `document.documentElement` 的 lang 属性；`stores.applyTheme` 写 `dataset.theme`。

```ts
// vitest 环境桩：node 环境没有浏览器全局对象，而模块常在导入期读取它们，不补桩会加载即崩。
// 运行时机：setupFiles 在任何测试文件导入之前执行。

// ===== localStorage 桩（内存实现）=====
const memory = new Map<string, string>();

const localStorageStub: Storage = {
  get length() {
    return memory.size;
  },
  clear() {
    memory.clear();
  },
  getItem(key: string) {
    return memory.get(key) ?? null;
  },
  key(index: number) {
    return Array.from(memory.keys())[index] ?? null;
  },
  removeItem(key: string) {
    memory.delete(key);
  },
  setItem(key: string, value: string) {
    memory.set(key, value);
  },
};

Object.defineProperty(globalThis, "localStorage", {
  value: localStorageStub,
  configurable: true,
  writable: true,
});

// ===== window 桩（最小事件系统）=====
// api.ts 导入期读 window.BaseURL；stores.ts 读 window.location.hash 并监听 hashchange。
type Listener = () => void;
const windowListeners = new Map<string, Set<Listener>>();

const windowStub = {
  // 与 Go 模板注入的 window.BaseURL 对齐（api.ts 据此拼 API 前缀）
  BaseURL: "/app/fnmusic-sync",
  location: {
    hash: "",
    // svelte-i18n getLocaleFromNavigator 会读 location.search（query 参数覆盖语言）
    search: "",
  },
  // svelte-i18n 读 window.navigator（不是全局 navigator）
  navigator: { language: "zh-CN", languages: ["zh-CN"] },
  addEventListener(type: string, cb: Listener) {
    if (!windowListeners.has(type)) windowListeners.set(type, new Set());
    windowListeners.get(type)!.add(cb);
  },
  removeEventListener(type: string, cb: Listener) {
    windowListeners.get(type)?.delete(cb);
  },
  dispatchEvent(type: string) {
    windowListeners.get(type)?.forEach((cb) => cb());
  },
};

Object.defineProperty(globalThis, "window", {
  value: windowStub,
  configurable: true,
  writable: true,
});

// ===== navigator 桩 =====
// node 无 navigator；i18n 'auto' 模式解析系统语言时需要 navigator.language
Object.defineProperty(globalThis, "navigator", {
  value: { language: "zh-CN" },
  configurable: true,
  writable: true,
});

// ===== document 桩（最小 DOM）=====
// svelte-i18n 的 locale store 变更时写 document.documentElement 的 lang 属性；
// stores.applyTheme 写 dataset.theme。没有 document 会 ReferenceError。
const documentStub = {
  documentElement: {
    dataset: {} as Record<string, string>,
    setAttribute: (_key: string, _value: string) => {},
  },
};

Object.defineProperty(globalThis, "document", {
  value: documentStub,
  configurable: true,
  writable: true,
});

// 屏蔽 debug 噪音；console.error 保留可见，避免掩盖真实问题
console.debug = () => {};
```

### 第 4 步：编写测试（从纯函数与 store 入手，不要一上来写组件测试）

本项目 7 个测试文件共 110 个用例：

| 文件           | 用例数 | 覆盖内容                                                                 |
|----------------|--------|--------------------------------------------------------------------------|
| trim.test.ts   | 30     | fnOS 宿主 SDK（Trim）相关逻辑                                            |
| api.test.ts    | 23     | fetch 封装与 API 前缀拼接                                                |
| utils.test.ts  | 22     | 日期/时长格式化、Profile URL、`cn`、`errMessage`、`hasSessionKey`        |
| stores.test.ts | 18     | toast 定时移除、Tab hash 同步、主题/语言持久化与 auto 跟随、reloadConfig |
| logger.test.ts | 8      | 五级日志过滤、非法级别回落 info、空 tag                                  |
| i18n.test.ts   | 5      | `mapLanguage` 飞牛语言 → i18n locale 映射表                              |
| types.test.ts  | 4      | `createEmptyUser` 默认值与对象独立性                                     |

实践技巧：

- Svelte 用 `writable` store + `get()` 断言即可，**不需要 pinia**（Vue 项目才需要 `setActivePinia(createPinia())`）。
- toast 的 3 秒自动移除：`vi.useFakeTimers()` + `vi.advanceTimersByTime(3000)`。
- `reloadConfig` 的 fetch：`vi.stubGlobal('fetch', vi.fn().mockResolvedValue(...))`，收尾 `vi.unstubAllGlobals()`。
- 失败文案走 `t()` 翻译：测试文件需 `import '$lib/i18n'` 注册消息并 init，否则断言只能拿到 key（如 `common.load_failed`）。
- 每个测试文件按需重置：`localStorage.clear()` / `config.set({})` / `vi.restoreAllMocks()`。

### 第 5 步：package.json 增加脚本

```json
"scripts": {
  "test": "vitest run"
}
```

> 覆盖率**不**在这里留 `test:coverage` 入口：覆盖率命令与扫描清单统一由 Makefile 的 `cover-node` / `junit-node` 目标负责（清单要通过环境变量注入 vitest，裸 `vitest run --coverage` 会退回配置文件里的默认清单，与 CI 口径不一致）。本地要跑覆盖率用 `make cover-node`。

### 第 6 步：.gitignore 增加

```
/cmd/frontend/coverage/
```

### 第 7 步：流水线配置（push 与 PR 各用独立并行流水线，不并入既有 CI）

CI 统一调用 Makefile 目标 `make cover-node`（产出 `cmd/frontend/coverage/lcov.info`）与 `make junit-node`（产出 `cmd/frontend/coverage/junit.xml`），与 Go 侧共用同一份「清单配置」，口径不会漂移；本地复现也用 `make cover-node`（别直接跑 `vitest run --coverage`，那样会退回配置文件里的默认清单，与 CI 口径不一致）。 Makefile 里的清单通过 `VITEST_TEST_INCLUDE` / `VITEST_COVER_INCLUDE` / `VITEST_COVER_EXCLUDE` 注入给 vitest，新增目录只改 Makefile 一处。

CNB 语义：事件键下的值是 Pipeline 数组，同事件多个条目 = 多流水线并行；并入同一 pipeline 的 stages 会串行，不要这样做。统一镜像 `docker.cnb.cool/dtapp/golang/fnpack` 已预装 Node + pnpm（本仓库已无 Dockerfile，见 [ci-image.md](ci-image.md)），挂 pnpm store 缓存卷加速依赖安装。

```yaml
"**":
  push:
    # ...既有流水线保持不动...
    # 新增：Node 覆盖率流水线（与既有流水线并行）
    - runner:
        tags: cnb:arch:amd64
      docker:
        image: docker.cnb.cool/dtapp/golang/fnpack # 统一预置镜像（预装 Node + pnpm）
        volumes:
          - /root/.local/share/pnpm/store:copy-on-write # pnpm 全局存储 - 加速前端依赖安装
      stages:
        - name: 安装所有依赖
          script: make deps
        - name: 生成前端测试覆盖率
          script: make cover-node # 产出 cmd/frontend/coverage/lcov.info
        - name: 上报覆盖率（全量红线）
          type: testing:coverage
          options:
            key: node # 多报告数据键：与 Go 侧（key: go）区分，各自写入 meta map 互不覆盖
            name: Node Coverage # 徽章显示名，与 key 一对一对应
            lines: 90 # 全量覆盖率红线（%），低于此值阻断流水线；当前基线 97.32%，留约 7 个点缓冲
            pattern: "cmd/frontend/coverage/lcov.info"
            breakIfNoCoverage: true # 找不到覆盖率报告则报错终止，避免静默通过
          exports:
            lines: NODE_COVERAGE_LINES
            diff_pct: NODE_COVERAGE_DIFF_PCT
        - name: 输出覆盖率
          script: 'echo "node coverage: lines=$NODE_COVERAGE_LINES diff=$NODE_COVERAGE_DIFF_PCT"'
  pull_request:
    # ...既有 PR 流水线保持不动...
    - runner:
        tags: cnb:arch:amd64
      docker:
        image: docker.cnb.cool/dtapp/golang/fnpack
        volumes:
          - /root/.local/share/pnpm/store:copy-on-write
      stages:
        - name: 安装所有依赖
          script: make deps
        - name: 生成前端测试覆盖率
          script: make cover-node
        - name: 上报覆盖率（增量红线）
          type: testing:coverage
          options:
            key: node # 多报告数据键：与 Go 侧（key: go）区分，各自写入 meta map 互不覆盖
            name: Node Coverage # 徽章显示名，与 key 一对一对应
            diffLines: 80 # 增量覆盖率红线（%）：本次 PR 变更代码的覆盖率，低于此值阻断流水线
            lines: 90 # 全量覆盖率底线，与 push 流水线一致，防止总量被单个 PR 拉低
            pattern: "cmd/frontend/coverage/lcov.info"
            breakIfNoCoverage: true
          exports:
            lines: NODE_COVERAGE_LINES
            diff_pct: NODE_COVERAGE_DIFF_PCT
        - name: 输出覆盖率
          script: 'echo "node coverage: lines=$NODE_COVERAGE_LINES diff=$NODE_COVERAGE_DIFF_PCT"'
```

## 覆盖率实况（2026-11 实测，110 个用例）

| 文件                | % Stmts   | % Branch  | % Funcs   | % Lines   | 说明                          |
|---------------------|-----------|-----------|-----------|-----------|-------------------------------|
| **All files**       | **96.21** | **88.02** | **91.66** | **97.32** | 全量度量（coverage.include）  |
| types.ts            | 100       | 100       | 100       | 100       | `createEmptyUser` 纯工厂函数  |
| i18n/index.ts       | 100       | 83.33     | 100       | 100       | `mapLanguage`                 |
| utils.ts            | 96.87     | 95.45     | 100       | 100       |                               |
| trim.ts             | 98.34     | 97.01     | 88.23     | 98.23     | fnOS 宿主 SDK                 |
| stores.ts           | 94.44     | 67.74     | 100       | 98.43     |                               |
| logger.ts           | 100       | 88.88     | 100       | 100       |                               |
| api.ts              | 90.24     | 82.60     | 78.94     | 89.18     | 纯 fetch 封装，部分分支未覆盖 |
| components/index.ts | 0         | 0         | 0         | 0         | 组件导出，随组件测试纳入      |

红线设定：`lines: 90`（基线 97.32% 往下留约 7 个点缓冲）；`diffLines: 80`（变更代码高比例覆盖）。

## 关键决策（实现时遵循，有冲突按项目实际调整并说明）

1. **vitest 而非 jest**：Vite/Svelte/TS 原生集成；`mergeConfig` 复用 vite.config，别名与插件不丢。
2. **include 只含 `src/**/*.ts`**：未测试文件按 0% 计入分母，指标诚实（Vitest 5 已移除 `coverage.all`，全量由 `coverage.include` 实现）。.svelte 视图组件暂不写测试（需要 `@testing-library/svelte` + jsdom，作为第二步），现在计入会把指标稀释到个位数，指标失去意义。
3. **排除入口文件**（`src/app.ts`）：导入期即 `mount(App)`，node 环境导入即崩。
4. **环境桩四件套**（localStorage/window/navigator/document）：见第 3 步，缺任何一个都会在导入期崩；其中 svelte-i18n 读 `window.navigator` 而非全局 navigator，是最容易漏的坑。
5. **不设 `lang`**：`lang=go` 仅 golang 格式报告需要；lcov 由插件自动识别。
6. **红线棘轮**：`lines` 初始贴基线下沿（90）；`diffLines: 80`（变更代码高比例覆盖）长期不动或逐步上调。
7. **多报告 key/name**：同一仓库同时上报 Go/Node 覆盖率时，必须为每个 `testing:coverage` 指定不同 `key`（`go`/`node`）与一一对应的 `name`。key 决定数据写入 meta map 的键，重复会互相覆盖、最终只剩一个徽章；`name` 即徽章显示名（如 `Node Coverage`）。
8. **测试从纯函数/store 开始**：组件测试需要 `@testing-library/svelte` + jsdom/happy-dom，作为第二步。
9. **YAML 细节**：script 含 `": "` 等特殊字符时整体用单引号包裹；独立并行流水线，勿并入既有 stages。
10. **CI 统一调 Makefile 目标**：与 Go 侧一致，`make cover-node` / `make junit-node` 是唯一入口；扫描 / 排除清单在 Makefile「测试覆盖率」节，通过环境变量注入 vitest，新增目录只改一处。
11. **JUnit 用 vitest 内置 reporter**：vitest 5.1 已内置 `--reporter=junit`，无需再写 JSON→JUnit 转换脚本（原 `cmd/frontend/scripts/vitest-json-to-junit.mjs` 已删除）。
12. **不显式传 `--reporter` 才有 GitHub Actions 注解**：vitest 的默认 reporters = `[default, github-actions]`（`GITHUB_ACTIONS=true` 时追加），一旦显式传 `--reporter` 就会**整体替换**这个列表。所以 `make cover-node` 刻意不传 `--reporter`（失败用例注解与 Job Summary 的「Vitest Test Report」区块由它产出）；`make junit-node` 必须显式传 `--reporter=junit` 才能出 XML，那一次没有该 reporter，注解由前一步提供。`github-actions` reporter 在非 GHA 环境安全空转（无 `GITHUB_STEP_SUMMARY` 就跳过写文件）。
13. **Codecov 并存**：本仓库在 GitHub Actions（`.github/workflows/codecov.yml` + `.github/codecov.yml`）另设 Codecov 上报（Node flag: node），与 CNB `testing:coverage` 的硬性红线门禁相互独立——Codecov 仅做趋势/PR 评论/徽章，其状态检查默认 `informational` 不阻塞。

## 验收标准

- `pnpm --dir cmd/frontend exec vitest run` 全绿（110 个用例，`make check-node` 里也会跑）；`make cover-node` 产出 `cmd/frontend/coverage/lcov.info`，total 与上表一致。</new_string>
- `tsc --noEmit` 与 oxlint 对新测试文件零报错（测试文件在 tsconfig include 范围内）；prettier / yamlfmt 通过。
- YAML 解析正确；首次推送后确认徽章「Node Coverage」生成、pattern 解析正常、多流水线并行启动。
