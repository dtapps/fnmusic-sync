#!/usr/bin/env node
// 将 Vitest 的 JSON reporter 输出（默认 test-results.json）转换为 JUnit XML（默认 test-report.xml），
// 供 codecov/test-results-action 上传测试分析结果（Test Analytics）。
//
// 用法：
//   node scripts/vitest-json-to-junit.mjs [input.json] [output.xml]
//
// 为什么需要它：Vitest 5.0.5 没有内置 junit reporter，社区 @vitest/junit 包在 npm 上也已不可得；
// 但 Vitest 有内置 json reporter，这里用一份零依赖脚本完成 json -> junit 的转换。
import { existsSync, readFileSync, statSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const input = process.argv[2] || 'test-results.json';
const output = process.argv[3] || 'test-report.xml';

// ---------------------------------------------------------------------------
// classname 归一化（Codecov Tests 面板必需）
//
// Codecov Test Analytics 只解析 JUnit XML，且只用 testcase 的 classname 属性定位
// 用例归属文件（官方 Jest 示例即 JEST_JUNIT_CLASSNAME="{filepath}"）；file/filepath
// 属性文档未提及，不可依赖。classname 同时用于「文件分组、跨 run 关联（flaky 判定）、
// 失败位置跳转」。
//
// 但 Vitest json reporter 的 testResults[].name 是**测试文件绝对路径**，直接写进
// classname 就会带上各 CI 的 runner 前缀：
//   GitHub Actions 容器   /__w/<repo>/<repo>/...
//   GitHub Actions 非容器 /home/runner/work/<repo>/<repo>/...
//   GitLab                /builds/<group>/<repo>/...
//   CNB                   /workspace/<repo>/...
// 结果同名用例在不同 run 变成不同记录，flaky 关联与跳转全部失效。
//
// 正确做法是在生成端归一化为「相对仓库根 + POSIX 分隔符」，不要用 codecov.yml 的
// fixes: 打补丁（那是覆盖率侧路径修补，且只能写死某个 CI 的前缀）。
// ---------------------------------------------------------------------------

// 仓库根：从脚本自身位置向上找 go.mod / .git。
// 不能用 process.cwd() 推断——CI 里转换步骤的 working-directory 是 cmd/frontend，
// cwd 不是仓库根，会算出 ../src/... 这种以 .. 开头的路径。
function findRepoRoot(start) {
  let dir = start;

  for (;;) {
    if (existsSync(path.join(dir, 'go.mod')) || existsSync(path.join(dir, '.git'))) return dir;

    const parent = path.dirname(dir);
    if (parent === dir) return start; // 到根仍未找到，退回脚本目录

    dir = parent;
  }
}

const repoRoot = findRepoRoot(path.dirname(fileURLToPath(import.meta.url)));

// 必须用 isFile 而不是 existsSync：后者会命中 frontend/src/test 这类同名目录，
// 让兜底层把一个目录当成测试文件返回。
function isFileInRepo(relPath) {
  try {
    return statSync(path.join(repoRoot, relPath)).isFile();
  } catch {
    return false;
  }
}

const toRepoRelative = (p) => {
  if (!p) return '';

  // 相对路径按 cwd 解析（cwd 只用于解析输入，不用于推仓库根）
  const abs = path.isAbsolute(p) ? p : path.resolve(process.cwd(), p);

  // 第 1 层（常规，覆盖 99% 场景）：JSON 与脚本在同一份 checkout 树内，直接相减。
  // 与绝对前缀具体是什么无关：/__w、/home/runner/work、/workspace、/builds 都能吃掉。
  const rel = path.relative(repoRoot, abs);
  if (rel !== '' && !rel.startsWith('..') && !path.isAbsolute(rel)) {
    return rel.split(path.sep).join('/'); // Windows 反斜杠统一成 /
  }

  // 第 2 层（兜底）：路径不在这棵树里。例如测试在容器内跑（/__w/...）、转换在宿主上跑；
  // 或本机复现别的 CI（/home/runner/work/...）产出的 JSON。
  // 改为从尾部取最长的「在仓库里真实存在」的后缀，结果同样与前缀无关。
  const segs = abs.split(/[\\/]+/).filter(Boolean);
  for (let i = segs.length; i >= 2; i--) {
    // i >= 2：避免误命中仓库根的同名单文件（go.mod、package.json 等）
    const candidate = segs.slice(segs.length - i).join('/');
    if (isFileInRepo(candidate)) return candidate; // cmd/frontend/src/test/a.test.ts
  }

  return p; // 认不出来就原样返回，绝不凭空编造路径
};

const data = JSON.parse(readFileSync(input, 'utf8'));
const suites = Array.isArray(data.testResults) ? data.testResults : [];

const escapeXml = (s) =>
  String(s ?? '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&apos;');

let total = 0;
let failures = 0;
let skipped = 0;
let totalTime = 0;
let stillAbsolute = 0;
const suitesXml = [];

for (const suite of suites) {
  const name = toRepoRelative(suite.name) || 'unknown';
  if (path.isAbsolute(name)) stillAbsolute++;

  const cases = Array.isArray(suite.assertionResults) ? suite.assertionResults : [];
  let sFail = 0;
  let sSkip = 0;
  let sTime = 0;
  const casesXml = [];

  for (const t of cases) {
    const status = t.status || 'unknown';
    const dur = (typeof t.duration === 'number' ? t.duration : 0) / 1000;
    sTime += dur;
    total++;
    totalTime += dur;

    const testName = t.fullName || t.title || 'unknown';
    if (status === 'failed') {
      failures++;
      sFail++;
    }
    if (status === 'skipped' || status === 'pending' || status === 'todo') {
      skipped++;
      sSkip++;
    }

    let inner = '';
    if (status === 'failed') {
      const msg = (t.failureMessages || []).join('\n');
      const head = msg.split('\n')[0] || 'failed';
      inner = `<failure message="${escapeXml(head)}">${escapeXml(msg)}</failure>`;
    } else if (status === 'skipped' || status === 'pending' || status === 'todo') {
      inner = '<skipped/>';
    }

    casesXml.push(
      `    <testcase name="${escapeXml(testName)}" classname="${escapeXml(name)}" time="${dur.toFixed(3)}">${inner}</testcase>`,
    );
  }

  // 整 suite 运行期错误（无 assertionResults 但有 message）
  if (suite.status && suite.status !== 'passed' && cases.length === 0) {
    failures++;
    total++;
    sFail++;
    const msg = suite.message || 'suite failed';
    casesXml.push(
      `    <testcase name="${escapeXml(name)}" classname="${escapeXml(name)}" time="0"><failure message="${escapeXml(
        msg.split('\n')[0],
      )}">${escapeXml(msg)}</failure></testcase>`,
    );
  }

  suitesXml.push(
    `  <testsuite name="${escapeXml(name)}" tests="${cases.length}" failures="${sFail}" skipped="${sSkip}" time="${sTime.toFixed(3)}">\n${casesXml.join(
      '\n',
    )}\n  </testsuite>`,
  );
}

const xml =
  `<?xml version="1.0" encoding="UTF-8"?>\n` +
  `<testsuites name="vitest" tests="${total}" failures="${failures}" skipped="${skipped}" time="${totalTime.toFixed(3)}">\n` +
  `${suitesXml.join('\n')}\n` +
  `</testsuites>\n`;

writeFileSync(output, xml);
console.log(`JUnit XML 已写出 ${output}：${total} 用例，${failures} 失败，${skipped} 跳过`);
console.log(`classname 基准目录（仓库根）：${repoRoot}`);

if (stillAbsolute > 0) {
  console.warn(
    `⚠️  仍有 ${stillAbsolute} 个 suite 的 classname 是绝对路径：两层归一化都没能把它落到仓库根 ` +
      `${repoRoot} 之内（文件不存在于此 checkout）。Codecov Tests 面板的跨 run 关联会失效。`,
  );
}
