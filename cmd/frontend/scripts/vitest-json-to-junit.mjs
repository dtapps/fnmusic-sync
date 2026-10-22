#!/usr/bin/env node
// 将 Vitest 的 JSON reporter 输出（默认 test-results.json）转换为 JUnit XML（默认 test-report.xml），
// 供 codecov/test-results-action 上传测试分析结果（Test Analytics）。
//
// 用法：
//   node scripts/vitest-json-to-junit.mjs [input.json] [output.xml]
//
// 为什么需要它：Vitest 5.0.5 没有内置 junit reporter，社区 @vitest/junit 包在 npm 上也已不可得；
// 但 Vitest 有内置 json reporter，这里用一份零依赖脚本完成 json -> junit 的转换。
import { readFileSync, writeFileSync } from "node:fs";

const input = process.argv[2] || "test-results.json";
const output = process.argv[3] || "test-report.xml";

const data = JSON.parse(readFileSync(input, "utf8"));
const suites = Array.isArray(data.testResults) ? data.testResults : [];

const escapeXml = (s) =>
  String(s ?? "")
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&apos;");

let total = 0;
let failures = 0;
let skipped = 0;
let totalTime = 0;
const suitesXml = [];

for (const suite of suites) {
  const name = suite.name || "unknown";
  const cases = Array.isArray(suite.assertionResults) ? suite.assertionResults : [];
  let sFail = 0;
  let sSkip = 0;
  let sTime = 0;
  const casesXml = [];

  for (const t of cases) {
    const status = t.status || "unknown";
    const dur = (typeof t.duration === "number" ? t.duration : 0) / 1000;
    sTime += dur;
    total++;
    totalTime += dur;

    const testName = t.fullName || t.title || "unknown";
    if (status === "failed") {
      failures++;
      sFail++;
    }
    if (status === "skipped" || status === "pending" || status === "todo") {
      skipped++;
      sSkip++;
    }

    let inner = "";
    if (status === "failed") {
      const msg = (t.failureMessages || []).join("\n");
      const head = msg.split("\n")[0] || "failed";
      inner = `<failure message="${escapeXml(head)}">${escapeXml(msg)}</failure>`;
    } else if (status === "skipped" || status === "pending" || status === "todo") {
      inner = "<skipped/>";
    }

    casesXml.push(
      `    <testcase name="${escapeXml(testName)}" classname="${escapeXml(name)}" time="${dur.toFixed(3)}">${inner}</testcase>`
    );
  }

  // 整 suite 运行期错误（无 assertionResults 但有 message）
  if (suite.status && suite.status !== "passed" && cases.length === 0) {
    failures++;
    total++;
    sFail++;
    const msg = suite.message || "suite failed";
    casesXml.push(
      `    <testcase name="${escapeXml(name)}" classname="${escapeXml(name)}" time="0"><failure message="${escapeXml(
        msg.split("\n")[0]
      )}">${escapeXml(msg)}</failure></testcase>`
    );
  }

  suitesXml.push(
    `  <testsuite name="${escapeXml(name)}" tests="${cases.length}" failures="${sFail}" skipped="${sSkip}" time="${sTime.toFixed(3)}">\n${casesXml.join(
      "\n"
    )}\n  </testsuite>`
  );
}

const xml =
  `<?xml version="1.0" encoding="UTF-8"?>\n` +
  `<testsuites name="vitest" tests="${total}" failures="${failures}" skipped="${skipped}" time="${totalTime.toFixed(3)}">\n` +
  `${suitesXml.join("\n")}\n` +
  `</testsuites>\n`;

writeFileSync(output, xml);
console.log(
  `JUnit XML 已写出 ${output}：${total} 用例，${failures} 失败，${skipped} 跳过`
);
