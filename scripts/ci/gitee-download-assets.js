'use strict';
/**
 * 把上游 Release 的附件搬到本地 bin/，并把上游正文落成 release_body.md。
 *
 * 这份是 /Users/liguangchun/Desktop/GolandProject/certflow/.workflow/release.yml 里
 * 那段下载逻辑的移植：同一个 request/downloadOnce/downloadFile 结构、同一组镜像回退、
 * 同样的断点续传与速度守护、同样的 3 路并发。别在这里另发明一套 —— 那段代码是在
 * plugin-24 容器里跑通过的（Gitee 镜像里没有 python3 / jq，所以全用 node 内置模块）。
 *
 * 与 certflow 唯一的实质差异：**整条链只认一个源**，由 DOWNLOAD_SOURCE 决定
 * （cnb 默认 / github）。包里的二进制注入了 buildinfo.RepoSource，客户端按它决定
 * 从哪个平台查更新；同一个 Release 混两个来源，等于给用户随机分配升级通道。
 * 所以连「更新内容」也不跨平台：cnb 源的附件名固定、大小向 CNB 现问、正文本地生成，
 * 全程不碰 GitHub 的接口；github 源才去读 GitHub 的 release 元数据与 body。
 *
 * 输入（环境变量）：
 *   DOWNLOAD_SOURCE  cnb（默认）/ github
 *   VERSION          可选，带不带 v 都行；空则由 --resolve-tag 或上游最新 Release 判定
 *   OUT_DIR          落盘目录，默认 bin
 *   CNB_REPO         CNB 仓库路径，默认 dtapp/fnmusic-sync
 *   GH_REPO          GitHub 仓库路径，默认 dtapps/fnmusic-sync
 *   GH_TOKEN         可选，避开 api.github.com 匿名限额（60 次/小时）
 *
 * 输出：
 *   bin/<上游附件原名>    附件原样落地，不做改名（下游 assertFiles 按名字找）
 *   release_body.md       上游正文（markdown），由 gitee-release-body.js 转成单行 HTML
 */

const fs = require('fs');
const path = require('path');
const http = require('http');
const https = require('https');

const VERSION_IN = (process.env.VERSION || '').trim();
const OUT_DIR = (process.env.OUT_DIR || 'bin').trim();
const CNB_REPO = (process.env.CNB_REPO || 'dtapp/fnmusic-sync').trim();
const GH_REPO = (process.env.GH_REPO || 'dtapps/fnmusic-sync').trim();
const GH_TOKEN = (process.env.GH_TOKEN || '').trim();
const SOURCE = (process.env.DOWNLOAD_SOURCE || 'cnb').trim().toLowerCase();

const GH_API = 'https://api.github.com/repos/' + GH_REPO;
const CONCURRENCY = 3;

let VER = VERSION_IN.replace(/^v/i, '');
let TAG = VER ? 'v' + VER : '';

function fail(msg) {
  console.error('错误：' + msg);
  process.exit(1);
}
function mb(n) {
  return (n / 1048576).toFixed(1) + ' MB';
}
function rate(bps) {
  if (bps >= 1048576) {
    return mb(bps) + '/s';
  }
  if (bps >= 1024) {
    return (bps / 1024).toFixed(0) + ' KB/s';
  }
  return bps.toFixed(0) + ' B/s';
}
function sleep(ms) {
  return new Promise(function (r) {
    setTimeout(r, ms);
  });
}

// ---------- HTTP ----------
// 断点续传要带 Range 头，重定向到签名地址时也得一路带着，否则续传变成从头下
function request(url, redirects, rangeFrom) {
  return new Promise(function (resolve, reject) {
    const lib = url.indexOf('https:') === 0 ? https : http;
    const headers = { 'User-Agent': 'fnmusic-sync-gitee-release' };
    if (rangeFrom > 0) {
      headers.Range = 'bytes=' + rangeFrom + '-';
    }
    const req = lib.get(url, { headers: headers }, function (res) {
      if (res.statusCode >= 300 && res.statusCode < 400 && res.headers.location) {
        res.resume();
        if (!redirects) {
          return reject(new Error('重定向次数过多: ' + url));
        }
        return resolve(request(new URL(res.headers.location, url).href, redirects - 1, rangeFrom));
      }
      if (res.statusCode !== 200 && res.statusCode !== 206) {
        let body = '';
        res.on('data', function (c) {
          body += c;
        });
        res.on('end', function () {
          reject(new Error('HTTP ' + res.statusCode + ' ' + url + '\n' + body.slice(0, 300)));
        });
        return;
      }
      resolve(res);
    });
    req.on('error', reject);
    req.setTimeout(60000, function () {
      req.destroy(new Error('请求超时: ' + url));
    });
  });
}

function readText(url, withAuth) {
  return new Promise(function (resolve, reject) {
    const lib = https;
    const headers = { 'User-Agent': 'fnmusic-sync-gitee-release', Accept: 'application/vnd.github+json' };
    if (GH_TOKEN && withAuth) {
      headers.Authorization = 'Bearer ' + GH_TOKEN;
    }
    const req = lib.get(url, { headers: headers }, function (res) {
      if (res.statusCode >= 300 && res.statusCode < 400 && res.headers.location) {
        res.resume();
        return resolve(readText(new URL(res.headers.location, url).href, withAuth));
      }
      if (res.statusCode !== 200) {
        let body = '';
        res.on('data', function (c) {
          body += c;
        });
        res.on('end', function () {
          reject(new Error('HTTP ' + res.statusCode + ' ' + url + '\n' + body.slice(0, 300)));
        });
        return;
      }
      let body = '';
      res.setEncoding('utf8');
      res.on('data', function (c) {
        body += c;
      });
      res.on('end', function () {
        resolve(body);
      });
      res.on('error', reject);
    });
    req.on('error', reject);
    req.setTimeout(60000, function () {
      req.destroy(new Error('请求超时: ' + url));
    });
  });
}

// ---------- 下载（结构与 certflow 一致）----------
// 附件字节的来源只有一个：cnb 源按附件名拼 CNB 的下载地址；github 源用上游的
// browser_download_url，慢的时候走镜像 —— 镜像里还是 GitHub 那个文件，字节相同。
const MIRRORS = [
  { name: 'daocloud', wrap: function (u) { return 'https://files.m.daocloud.io/' + u.replace(/^https?:\/\//, ''); } },
  { name: 'ghfast', wrap: function (u) { return 'https://ghfast.top/' + u; } },
  { name: 'gh-proxy', wrap: function (u) { return 'https://gh-proxy.com/' + u; } },
  { name: '直连', wrap: function (u) { return u; } },
];

function urlFor(asset) {
  if (SOURCE === 'cnb') {
    return 'https://cnb.cool/' + CNB_REPO + '/-/releases/download/' + TAG + '/' + asset.name;
  }
  return asset.browser_download_url;
}

// 大小从哪来：github 源直接用上游元数据里的 asset.size（就是同一个文件）；
// cnb 源不能用它 —— 每个平台各自编译，同版本的两个包字节数和内容都不一样
// （实测 v0.1.30 的 amd64.fpk：GitHub 7176234 / CNB 7176009），拿 GitHub 的
// 尺寸去卡 CNB 的文件会一直「大小不符」然后 416。所以 cnb 源现探一次：
// 发一个 Range: bytes=1- 的请求，从 Content-Range 尾部读真实总长度。
function probeSize(url) {
  return request(url, 5, 1).then(function (res) {
    const cr = res.headers['content-range'];
    res.resume();
    if (cr) {
      const m = /\/(\d+)\s*$/.exec(cr);
      if (m) {
        return Number(m[1]);
      }
    }
    const cl = Number(res.headers['content-length']);
    return res.statusCode === 200 && cl > 0 ? cl : 0;
  });
}

// 落盘校验：网关/反代拿 200 回一个 HTML 错误页时，大小可能对得上，内容却全是文本。
// deb 必须是 ar 归档（!<arch>），fpk 必须是 gzip（1f 8b）。
function looksRight(dest, name) {
  const fd = fs.openSync(dest, 'r');
  const buf = Buffer.alloc(8);
  fs.readSync(fd, buf, 0, 8, 0);
  fs.closeSync(fd);
  if (/\.deb$/.test(name)) {
    return buf.slice(0, 8).toString('ascii') === '!<arch>\n';
  }
  if (/\.fpk$/.test(name)) {
    return buf[0] === 0x1f && buf[1] === 0x8b;
  }
  return true;
}

function candidatesFor(asset) {
  const base = urlFor(asset);
  if (SOURCE === 'cnb') {
    return [{ name: 'CNB', wrap: function (u) { return u; } }];
  }
  return MIRRORS;
}

function downloadOnce(url, dest, from) {
  return request(url, 5, from).then(function (res) {
    return new Promise(function (resolve, reject) {
      const append = from > 0 && res.statusCode === 206;
      const out = fs.createWriteStream(dest, { flags: append ? 'a' : 'w' });
      let written = append ? from : 0;
      let lastAt = Date.now();
      let lastBytes = written;
      let lowStreak = 0;
      let done = false;

      function abort(err) {
        if (done) {
          return;
        }
        done = true;
        out.destroy();
        res.destroy();
        reject(err);
      }

      res.on('data', function (chunk) {
        written += chunk.length;
        const now = Date.now();
        if (now - lastAt < 5000) {
          return;
        }
        const bps = (written - lastBytes) / ((now - lastAt) / 1000);
        console.log('    ' + mb(written) + '  ' + rate(bps));
        // 15 秒持续低于 30 KB/s 就判定这条源废了，主动断开换源。
        // 只在「连接断了」时才换源的话，慢而不断的连接会一直磨下去
        // （实测 GitHub 直连 13 KB/s，508MB 按这速度要十几小时）。
        lowStreak = bps < 30 * 1024 ? lowStreak + 1 : 0;
        if (lowStreak >= 3) {
          const slowErr = new Error('速度过低（' + rate(bps) + '）');
          slowErr.slow = true;
          abort(slowErr);
          return;
        }
        lastAt = now;
        lastBytes = written;
      });
      res.on('error', abort);
      res.on('aborted', function () {
        abort(new Error('连接被中断'));
      });
      out.on('error', abort);
      out.on('finish', function () {
        if (done) {
          return;
        }
        done = true;
        resolve(written);
      });
      res.pipe(out);
    });
  });
}

// 单个文件：累计重试 8 次，每次从已落盘的字节续传；速度过低立刻换源，
// 普通中断两轮换一次，避免在同一个慢源上反复消耗次数
function downloadFile(asset, dest, expectedSize) {
  const chain = candidatesFor(asset);
  let idx = 0;
  let tries = 0;

  function step() {
    const from = fs.existsSync(dest) ? fs.statSync(dest).size : 0;
    if (expectedSize > 0 && from === expectedSize) {
      console.log('  已完整（' + mb(from) + '）');
      return Promise.resolve();
    }
    if (expectedSize > 0 && from > expectedSize) {
      fs.unlinkSync(dest); // 上次写了脏数据，重来
    }
    return downloadOnce(chain[idx].wrap(urlFor(asset)), dest, from)
      .then(function (written) {
        if (expectedSize > 0 && written !== expectedSize) {
          throw new Error('大小不符：' + written + ' != ' + expectedSize);
        }
        console.log('  完成 ' + mb(written) + '（' + chain[idx].name + '）');
      })
      .catch(function (err) {
        tries++;
        const now = fs.existsSync(dest) ? fs.statSync(dest).size : 0;
        console.log(
          '  中断：' + (err && err.message ? err.message : err) + '，已下 ' + mb(now) + '，第 ' + tries + ' 次'
        );
        if (tries >= 8) {
          return Promise.reject(new Error('下载失败（重试 ' + tries + ' 次）：' + urlFor(asset)));
        }
        if ((err && err.slow) || tries % 2 === 0) {
          if (idx < chain.length - 1) {
            idx++;
            console.log('  切换下载源 → ' + chain[idx].name);
          }
        }
        return sleep(1500).then(step);
      });
  }
  return step();
}

// ---------- 主流程 ----------
// 与另外四个平台逐字一致的四个产物。cnb 源不查任何元数据接口：名字是固定的，
// 大小用 probeSize 向 CNB 现问，正文本地生成 —— 全程不碰 GitHub 的 API，
// 免得出现「附件从 CNB 来、更新内容从 GitHub 来」这种自相矛盾的发布。
const CNB_FILES = [
  { name: 'fnmusic-sync_amd64.deb', arch: 'amd64', desc: 'Debian / Ubuntu' },
  { name: 'fnmusic-sync_arm64.deb', arch: 'arm64', desc: 'Debian / Ubuntu' },
  { name: 'fnmusic-sync-amd64.fpk', arch: 'amd64', desc: '飞牛 fnOS' },
  { name: 'fnmusic-sync-arm64.fpk', arch: 'arm64', desc: '飞牛 fnOS' },
];

// 正文格式与 CNB / GitHub 那份 release_body.md 保持一致（同四行发布信息 + 同五列下载表）
function localBody(sizes) {
  const pre = (process.env.PRERELEASE || 'false').trim() === 'true';
  const dl = 'https://gitee.com/dtapps/fnmusic-sync/releases/download/' + TAG;
  const now = new Date().toISOString().replace('T', ' ').slice(0, 19) + ' UTC';
  const lines = [];
  if (pre) {
    lines.push('## ⚠️ 预发布版本', '', '> 这是预发布版本，可能包含未充分测试的功能。', '');
  }
  lines.push('## 发布信息', '', '| 项目 | 内容 |', '| --- | --- |');
  lines.push('| 版本号 | ' + TAG + ' |');
  lines.push('| 发布类型 | ' + (pre ? '🚧 预发布版本 (Pre-release)' : '✅ 正式发布版本 (Stable)') + ' |');
  lines.push('| 是否预发布 | ' + (pre ? 'true' : 'false') + ' |');
  lines.push('| 构建时间 | ' + now + ' |');
  lines.push('', '## 下载', '', '| 平台 | 架构 | 文件 | 大小 | 说明 |', '|------|------|------|------|------|');
  for (const f of CNB_FILES) {
    lines.push(
      '| Linux | ' + f.arch + ' | [' + f.name + '](' + dl + '/' + f.name + ') | ' +
        mb(sizes[f.name] || 0) + ' | ' + f.desc + ' |'
    );
  }
  lines.push('', '## 更新内容', '');
  lines.push('- 详见 [CHANGELOG.md](https://gitee.com/dtapps/fnmusic-sync/blob/' + TAG + '/CHANGELOG.md)');
  lines.push('');
  return lines.join('\n');
}

async function runCnb() {
  console.log('附件来源：CNB（固定，不混用；正文也在本地生成，不查 GitHub）');
  console.log('Release ' + TAG + '：' + CNB_FILES.length + ' 个固定产物');

  const sizes = {};
  let seq = 0;
  const queue = CNB_FILES.slice();

  async function downloadOne(item) {
    // 序号在出队时就定，别等 probeSize 回来再取 —— 否则并发时日志里会出现两个 [2/4]
    const n = ++seq;
    const dest = path.join(OUT_DIR, item.name);
    const url = urlFor({ name: item.name });
    const expected = await probeSize(url).catch(function () {
      return 0; // 探不到就只靠 magic 校验，不因此失败
    });
    console.log('[' + n + '/' + CNB_FILES.length + '] ' + item.name + '（' + mb(expected) + '）');
    await downloadFile({ name: item.name }, dest, expected);
    if (!looksRight(dest, item.name)) {
      fs.unlinkSync(dest);
      throw new Error(item.name + ' 落盘内容不像 ' + path.extname(item.name) + '，可能是错误页');
    }
    sizes[item.name] = fs.statSync(dest).size;
  }

  async function worker() {
    while (queue.length) {
      await downloadOne(queue.shift());
    }
  }

  // 并发 3 路：2 核 4G 的容器也扛得住，单流几十 MB/s 时没必要再开
  const pool = [];
  for (let i = 0; i < Math.min(CONCURRENCY, queue.length); i++) {
    pool.push(worker());
  }
  await Promise.all(pool);

  fs.writeFileSync('release_body.md', localBody(sizes), 'utf8');
  console.log('正文：本地生成（上游正文要 CNB API，匿名 401 拿不到，不混用 GitHub 的）');

  const all = fs.readdirSync(OUT_DIR).sort();
  console.log('');
  console.log('准备就绪：' + OUT_DIR + '/ 下 ' + all.length + ' 个附件');
  all.forEach(function (f) {
    console.log('  - ' + f + '  ' + mb(fs.statSync(path.join(OUT_DIR, f)).size));
  });
}

async function runGithub() {
  const relUrl = TAG ? GH_API + '/releases/tags/' + encodeURIComponent(TAG) : GH_API + '/releases/latest';
  console.log('版本号: ' + (TAG || '未指定，取 GitHub 最新 Release'));
  const rel = JSON.parse(await readText(relUrl, true));

  if (!TAG) {
    TAG = String(rel.tag_name || '').trim();
    if (!TAG) {
      fail('最新 Release 没有 tag_name，无法判定版本');
    }
    VER = TAG.replace(/^v/i, '');
    console.log('自动判定版本：' + TAG);
  }

  const assets = rel.assets || [];
  if (!assets.length) {
    fail('Release ' + TAG + ' 没有附件');
  }

  const body = String(rel.body || '').replace(/\r\n/g, '\n').trim();
  fs.writeFileSync('release_body.md', body || '版本 ' + TAG, 'utf8');
  console.log('正文：取自 GitHub Release（' + (body ? body.split('\n').length + ' 行' : '正文为空，用占位') + '）');

  console.log('');
  console.log('附件来源：GitHub（固定，不混用）');
  console.log('Release ' + TAG + ' 共 ' + assets.length + ' 个附件');

  let seq = 0;
  const queue = assets.slice();

  async function downloadOne(asset) {
    const n = ++seq;
    const dest = path.join(OUT_DIR, asset.name);
    console.log('[' + n + '/' + assets.length + '] ' + asset.name + '（' + mb(asset.size || 0) + '）');
    await downloadFile(asset, dest, asset.size || 0);
    if (!looksRight(dest, asset.name)) {
      fs.unlinkSync(dest);
      throw new Error(asset.name + ' 落盘内容不像 ' + path.extname(asset.name) + '，可能是错误页');
    }
  }

  async function worker() {
    while (queue.length) {
      await downloadOne(queue.shift());
    }
  }

  const pool = [];
  for (let i = 0; i < Math.min(CONCURRENCY, queue.length); i++) {
    pool.push(worker());
  }
  await Promise.all(pool);

  const all = fs.readdirSync(OUT_DIR).sort();
  console.log('');
  console.log('准备就绪：' + OUT_DIR + '/ 下 ' + all.length + ' 个附件');
  all.forEach(function (f) {
    console.log('  - ' + f + '  ' + mb(fs.statSync(path.join(OUT_DIR, f)).size));
  });
}

async function main() {
  // 容器启动时工作目录可能已被删除（日志里的 shell-init getcwd 报错），先兜一下
  try {
    process.cwd();
  } catch (e) {
    process.chdir('/');
  }

  if (SOURCE !== 'cnb' && SOURCE !== 'github') {
    fail('DOWNLOAD_SOURCE 只能是 cnb 或 github，收到：' + JSON.stringify(SOURCE));
  }
  if (!TAG) {
    fail(
      'VERSION 为空：' +
        (SOURCE === 'cnb'
          ? 'CNB 的 API 匿名访问返回 401，推不出最新 tag，请填 VERSION（全局参数或 Webhook 的 payload.params）'
          : '查 GitHub 最新 Release 也失败了，请填 VERSION')
    );
  }

  // 只解析版本用：打印后退出，不下载、不写正文
  if (process.argv.indexOf('--resolve-tag') !== -1) {
    if (SOURCE === 'github' && !VERSION_IN) {
      // 没给 VERSION 时用上游最新 tag 回填
      const rel = JSON.parse(await readText(GH_API + '/releases/latest', true));
      if (rel.tag_name) {
        TAG = String(rel.tag_name).trim();
        process.stdout.write(TAG + '\n');
        return;
      }
    }
    process.stdout.write(TAG + '\n');
    return;
  }

  if (!fs.existsSync(OUT_DIR)) {
    fs.mkdirSync(OUT_DIR, { recursive: true });
  }

  if (SOURCE === 'cnb') {
    await runCnb();
  } else {
    await runGithub();
  }
}

main().catch(function (err) {
  fail(err && err.message ? err.message : String(err));
});
