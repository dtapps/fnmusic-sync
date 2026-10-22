# Node 单元测试覆盖率实现文案（fnmusic-sync 前端实战：Svelte 5 + vitest + CNB）

> 按 fnmusic-sync 项目 `cmd/frontend` 实际实现整理：Svelte 5 + Vite + TypeScript + pnpm，vitest 5，5 个测试文件 57 个用例，lines 基线 45.41%。
> 方案可复制到其他 Vite 系项目（Vue/React/Svelte），按项目实际调整目录、入口排除项与环境桩。

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

用 `mergeConfig` 复用 vite.config 的 svelte 插件与 `$lib` 别名，仅额外声明测试环境与覆盖率：

```ts
import { defineConfig, mergeConfig } from "vitest/config";
import viteConfig from "./vite.config";

// 复用 vite.config 的插件（svelte）与别名（$lib），仅额外声明测试环境与覆盖率。
export default mergeConfig(
  viteConfig,
  defineConfig({
    test: {
      environment: "node", // 纯函数/store 测试无需 DOM；后续组件测试再切 jsdom
      setupFiles: ["./src/test/setup.ts"],
      coverage: {
        provider: "v8",
        reporter: ["lcov", "text"], // lcov 供 CI 插件解析，text 供本地阅读
        reportsDirectory: "./coverage",
        // 只度量逻辑代码（.ts）。.svelte 视图组件暂不写测试，计入分母会让指标失去意义，
        // 组件测试（@testing-library/svelte + jsdom）作为第二步再纳入。
        include: ["src/**/*.ts"],
        exclude: [
          "src/test/**", // 测试自身不计入
          "src/app.ts", // 入口：导入期即挂载 DOM，node 下会崩
          "src/**/*.d.ts",
        ],
        all: true, // 全量度量：未测试文件按 0% 计入分母，指标不虚高
      },
    },
  }),
);
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

本项目 5 个测试文件共 57 个用例：

| 文件           | 用例数 | 覆盖内容                                                                 |
| -------------- | ------ | ------------------------------------------------------------------------ |
| utils.test.ts  | 22     | 日期/时长格式化、Profile URL、`cn`、`errMessage`、`hasSessionKey`        |
| stores.test.ts | 18     | toast 定时移除、Tab hash 同步、主题/语言持久化与 auto 跟随、reloadConfig |
| logger.test.ts | 8      | 五级日志过滤、非法级别回落 info、空 tag                                  |
| types.test.ts  | 4      | `createEmptyUser` 默认值与对象独立性                                     |
| i18n.test.ts   | 5      | `mapLanguage` 飞牛语言 → i18n locale 映射表                              |

实践技巧：

- Svelte 用 `writable` store + `get()` 断言即可，**不需要 pinia**（Vue 项目才需要 `setActivePinia(createPinia())`）。
- toast 的 3 秒自动移除：`vi.useFakeTimers()` + `vi.advanceTimersByTime(3000)`。
- `reloadConfig` 的 fetch：`vi.stubGlobal('fetch', vi.fn().mockResolvedValue(...))`，收尾 `vi.unstubAllGlobals()`。
- 失败文案走 `t()` 翻译：测试文件需 `import '$lib/i18n'` 注册消息并 init，否则断言只能拿到 key（如 `common.load_failed`）。
- 每个测试文件按需重置：`localStorage.clear()` / `config.set({})` / `vi.restoreAllMocks()`。

### 第 5 步：package.json 增加脚本

```json
"scripts": {
  "test": "vitest run",
  "test:coverage": "vitest run --coverage"
}
```

### 第 6 步：.gitignore 增加

```
/cmd/frontend/coverage/
```

### 第 7 步：流水线配置（push 与 PR 各用独立并行流水线，不并入既有 CI）

CI 直接调用 `pnpm --dir cmd/frontend test:coverage`，**不引入 make coverage 包装**（与 Go 侧策略一致：仅 CI 使用的命令不进 Makefile，流水线自解释）。Makefile 只保留本地便利目标 `make test-frontend`（等价 `pnpm test`，带依赖安装检查），覆盖率本地复现直接 `pnpm test:coverage`。

CNB 语义：事件键下的值是 Pipeline 数组，同事件多个条目 = 多流水线并行；并入同一 pipeline 的 stages 会串行，不要这样做。`.cnb/Dockerfile` 已预装 Node + pnpm，挂 pnpm store 缓存卷加速依赖安装。

```yaml
"**":
  push:
    # ...既有流水线保持不动...
    # 新增：Node 覆盖率流水线（与既有流水线并行）
    - runner:
        tags: cnb:arch:amd64
      docker:
        build: .cnb/Dockerfile # 使用自定义 Dockerfile（预装 Node + pnpm）
        volumes:
          - /root/.local/share/pnpm/store:copy-on-write # pnpm 全局存储 - 加速前端依赖安装
      stages:
        - name: 安装前端依赖
          script: pnpm --dir cmd/frontend install --frozen-lockfile
        - name: 生成前端测试覆盖率
          script: pnpm --dir cmd/frontend test:coverage # 产出 cmd/frontend/coverage/lcov.info
        - name: 上报覆盖率（全量红线）
          type: testing:coverage
          options:
            key: node # 多报告数据键：与 Go 侧（key: go）区分，各自写入 meta map 互不覆盖
            name: Node Coverage # 徽章显示名，与 key 一对一对应
            lines: 40 # 全量覆盖率红线（%），低于此值阻断流水线；当前基线约 45%，留余量后逐步上调
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
        build: .cnb/Dockerfile
        volumes:
          - /root/.local/share/pnpm/store:copy-on-write
      stages:
        - name: 安装前端依赖
          script: pnpm --dir cmd/frontend install --frozen-lockfile
        - name: 生成前端测试覆盖率
          script: pnpm --dir cmd/frontend test:coverage
        - name: 上报覆盖率（增量红线）
          type: testing:coverage
          options:
            key: node # 多报告数据键：与 Go 侧（key: go）区分，各自写入 meta map 互不覆盖
            name: Node Coverage # 徽章显示名，与 key 一对一对应
            diffLines: 50 # 增量覆盖率红线（%）：本次 PR 变更代码的覆盖率，低于此值阻断流水线
            lines: 40 # 全量覆盖率底线，与 push 流水线一致，防止总量被单个 PR 拉低
            pattern: "cmd/frontend/coverage/lcov.info"
            breakIfNoCoverage: true
          exports:
            lines: NODE_COVERAGE_LINES
            diff_pct: NODE_COVERAGE_DIFF_PCT
        - name: 输出覆盖率
          script: 'echo "node coverage: lines=$NODE_COVERAGE_LINES diff=$NODE_COVERAGE_DIFF_PCT"'
```

## 覆盖率实况（首次实现基线，57 个用例）

| 文件                | % Stmts   | % Lines   | 说明                                         |
| ------------------- | --------- | --------- | -------------------------------------------- |
| **All files**       | **45.36** | **45.41** | 全量度量（all: true）                        |
| types.ts            | 100       | 100       | `createEmptyUser` 纯工厂函数                 |
| i18n/index.ts       | 100       | 100       | `mapLanguage`                                |
| utils.ts            | 96.87     | 100       |                                              |
| stores.ts           | 94.44     | 98.43     |                                              |
| logger.ts           | 94.73     | 93.33     |                                              |
| api.ts              | 21.95     | 24.32     | 纯 fetch 封装，未写全量 mock 测试            |
| trim.ts             | 0         | 0         | 依赖 fnOS 宿主 SDK，模块级单例状态，留待后续 |
| components/index.ts | 0         | 0         | 组件导出，随组件测试纳入                     |

红线设定：`lines: 40`（基线 45.41% 往下留约 5 个百分点余量）；`diffLines: 50`（变更代码过半覆盖）。

## 关键决策（实现时遵循，有冲突按项目实际调整并说明）

1. **vitest 而非 jest**：Vite/Svelte/TS 原生集成；`mergeConfig` 复用 vite.config，别名与插件不丢。
2. **`all: true` 但 include 只含 `src/**/*.ts`**：未测试文件按 0% 计入分母，指标诚实。.svelte 视图组件暂不写测试（需要 `@testing-library/svelte` + jsdom，作为第二步），现在计入会把 45% 稀释到个位数，指标失去意义。
3. **排除入口文件**（`src/app.ts`）：导入期即 `mount(App)`，node 环境导入即崩。
4. **环境桩四件套**（localStorage/window/navigator/document）：见第 3 步，缺任何一个都会在导入期崩；其中 svelte-i18n 读 `window.navigator` 而非全局 navigator，是最容易漏的坑。
5. **不设 `lang`**：`lang=go` 仅 golang 格式报告需要；lcov 由插件自动识别。
6. **红线棘轮**：`lines` 初始贴基线下沿（40）；`diffLines: 50`（变更代码过半覆盖）长期不动或逐步上调。
7. **多报告 key/name**：同一仓库同时上报 Go/Node 覆盖率时，必须为每个 `testing:coverage` 指定不同 `key`（`go`/`node`）与一一对应的 `name`。key 决定数据写入 meta map 的键，重复会互相覆盖、最终只剩一个徽章；`name` 即徽章显示名（如 `Node Coverage`）。
8. **测试从纯函数/store 开始**：组件测试需要 `@testing-library/svelte` + jsdom/happy-dom，作为第二步。
9. **YAML 细节**：script 含 `": "` 等特殊字符时整体用单引号包裹；独立并行流水线，勿并入既有 stages。
10. **CI 直接调 pnpm，不引入 make coverage 包装**：与 Go 侧策略一致，仅 CI 使用的命令不进 Makefile；本地跑测试用 `make test-frontend` 或 `pnpm test`，本地复现覆盖率直接 `pnpm test:coverage`。
11. **Codecov 并存**：本仓库在 GitHub Actions（`.github/workflows/coverage.yml` + `.github/codecov.yml`）另设 Codecov 上报（Node flag: node），与 CNB `testing:coverage` 的硬性红线门禁相互独立——Codecov 仅做趋势/PR 评论/徽章，其状态检查默认 `informational` 不阻塞。

## 验收标准

- `pnpm test` 全绿（57 个用例）；`pnpm test:coverage` 产出 `cmd/frontend/coverage/lcov.info`，total 与上表一致。
- `tsc --noEmit` 与 oxlint 对新测试文件零报错（测试文件在 tsconfig include 范围内）；prettier / yamlfmt 通过。
- YAML 解析正确；首次推送后确认徽章「Node Coverage」生成、pattern 解析正常、多流水线并行启动。
