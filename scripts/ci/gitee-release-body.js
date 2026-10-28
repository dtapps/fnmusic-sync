'use strict';
/**
 * 把 release_body.md 压成 Gitee 能吃的「单行 HTML」，写进 GITEE_PARAMS 参数文件。
 *
 * 为什么不能直接给插件一段 markdown：
 *   1. release@gitee 的 description 只认文本，不渲染 markdown，得先转 HTML；
 *   2. 参数文件是 KEY=VALUE 逐行解析，所以正文必须是**单行**；
 *   3. 平台按 = 切 KEY=VALUE → 正文里不能有裸 =，全部转成 &#61;；
 *   4. 插件把值当相对路径 open，单段超过 255 字节直接 file name too long 崩掉
 *      → 每累计 200 字节插一个 <br/>，用它自带的 / 把路径切成短段，open 走 ENOENT
 *      分支降级成正文。
 *
 * 第 4 条踩过两次坑，别改回 <!--/-->：实测平台会把 HTML 注释删掉、把 // 压成 /，
 * 分隔符被清掉后整串又变成一个超长分量（8:30 那次就是这么崩的）。
 *
 * 用法：node scripts/ci/gitee-release-body.js <release_body.md>
 * 需要：能 require 到 marked。CI 里装在独立 prefix，靠 NODE_PATH 指过去
 *      （不在仓库根 npm install，避免改写 package.json）。
 */
const fs = require('fs');

// 参数文件：下游 description / prerelease / tagName 全靠它插值。
// GITEE_PARAMS 不是每个步骤都会注入（实测 build@nodejs 的 environ 里就没有它），
// 所以与 certflow 那份保持一致：没注入就写工作目录下名为 GITEE_PARAMS 的文件，
// 而不是直接退出。appendFileSync 会自己创建文件。
const PARAMS = process.env.GITEE_PARAMS || 'GITEE_PARAMS';

const MD_PATH = process.argv[2] || 'release_body.md';
if (!fs.existsSync(MD_PATH)) {
  console.error('错误：找不到正文文件 ' + MD_PATH);
  process.exit(1);
}

let marked;
try {
  marked = require('marked');
} catch (e) {
  console.error('错误：缺少 marked，构建步骤里要先 npm install marked');
  process.exit(1);
}
const toHtml = typeof marked.parse === 'function' ? marked.parse : marked;

const md = fs.readFileSync(MD_PATH, 'utf8');

// Gitee 插件存的是纯文本，链接会被平台剥掉，所以这里主动把 <a> 剥成文字，
// 让「文件名」留在原处，而不是让平台剥出一堆空标签。
let notesHtml = toHtml(md, { mangle: false, headerIds: false });
notesHtml = notesHtml.replace(/<a\b[^>]*>([\s\S]*?)<\/a>/g, '$1');
// 上面已经换成空串，这里清掉残留的空链接标签
notesHtml = notesHtml.replace(/<a\b[^>]*>\s*<\/a>/g, '');
notesHtml = notesHtml.replace(/\s+[a-zA-Z][\w:-]*="[^"]*"/g, '');
notesHtml = notesHtml.replace(/=/g, '&#61;');

/**
 * 按字节精确切分，每累计 LIMIT 字节插一个 <br/>。
 * 只落在「标签」与「文本」的边界上，不会把多字节汉字或标签切断。
 */
function withBreaks(block, LIMIT) {
  const tokens = block.match(/<[^>]*>|[^<]+/g) || [];
  let out = '';
  let size = 0;
  for (const t of tokens) {
    if (t.charAt(0) === '<') {
      out += t;
      size += Buffer.byteLength(t, 'utf8');
    } else {
      let buf = Buffer.from(t, 'utf8');
      while (buf.length > 0) {
        if (size >= LIMIT) {
          out += '<br/>';
          size = 0;
          continue;
        }
        let end = Math.min(LIMIT - size, buf.length);
        // 往回退到 UTF-8 字符边界，避免切出乱码
        while (end > 0 && (buf[end] & 0xc0) === 0x80) end--;
        out += buf.slice(0, end).toString('utf8');
        size += end;
        buf = buf.slice(end);
      }
    }
    if (size >= LIMIT) {
      out += '<br/>';
      size = 0;
    }
  }
  return out;
}

const oneLine = notesHtml
  .split('\n')
  .map(function (l) {
    return l.trim();
  })
  .filter(Boolean)
  .map(function (l) {
    return withBreaks(l, 200);
  })
  .join('');

if (oneLine.indexOf('=') !== -1) {
  throw new Error('正文里还有裸 =，会被参数解析截断');
}

// 兜底校验：任何一段都不能超过 255，否则插件 open 会 file name too long
const longest = Math.max.apply(
  null,
  oneLine.split('/').map(function (p) {
    return Buffer.byteLength(p, 'utf8');
  })
);
if (longest > 200) {
  throw new Error('正文分段后仍有 ' + longest + ' 字节的片段，会被插件当超长文件名');
}

// VERSION / PRERELEASE 由平台插值给下游 release-stage，正文走参数通道。
// 三个键必须在同一次写入里落盘，缺一个下游就是空值。
const version = (process.env.VERSION || '').trim();
const pre = (process.env.PRERELEASE || 'false').trim() === 'true' ? 'true' : 'false';

fs.appendFileSync(PARAMS, 'VERSION=' + version + '\n', 'utf8');
fs.appendFileSync(PARAMS, 'PRERELEASE=' + pre + '\n', 'utf8');
fs.appendFileSync(PARAMS, 'RELEASE_BODY=' + oneLine + '\n', 'utf8');

console.log(
  '正文：markdown ' +
    md.split('\n').length +
    ' 行 / ' +
    md.length +
    ' 字符 → 单行 HTML ' +
    oneLine.length +
    ' 字符（无裸 =，最长路径片段 ' +
    longest +
    ' 字节）'
);
console.log('写入参数文件：VERSION=' + version + ' PRERELEASE=' + pre);
