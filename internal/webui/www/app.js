// 飞牛音乐 Scrobble 代理 Web UI

const API = (window.BaseURL || '/app/fnmusic-sync') + '/api';

// ===== 工具函数 =====

function showToast(msg, type = 'success') {
  const toast = document.getElementById('toast');
  toast.textContent = msg;
  toast.className = `toast ${type} show`;
  setTimeout(() => { toast.className = 'toast'; }, 3000);
}

async function apiCall(path, method = 'GET', body = null) {
  const opts = { method, headers: {} };
  if (body) {
    opts.headers['Content-Type'] = 'application/json';
    opts.body = JSON.stringify(body);
  }
  const resp = await fetch(API + path, opts);
  const data = await resp.json();
  if (!resp.ok) {
    throw new Error(data.error || `HTTP ${resp.status}`);
  }
  return data;
}

// ===== Tab 切换 =====

document.querySelectorAll('.tab').forEach(btn => {
  btn.addEventListener('click', () => {
    document.querySelectorAll('.tab').forEach(b => b.classList.remove('active'));
    document.querySelectorAll('.tab-content').forEach(c => c.classList.remove('active'));
    btn.classList.add('active');
    document.getElementById('tab-' + btn.dataset.tab).classList.add('active');
  });
});

// ===== 版本信息 =====

async function loadVersion() {
  try {
    const v = await apiCall('/version');
    // 版本号本身已含 v 前缀，无需额外拼接
    // 构建时间为 UTC，转为本地时区显示，格式与 Go 一致：YYYY-MM-DD HH:mm:ss
    let timeStr = v.buildTime || '';
    if (timeStr) {
      const d = new Date(timeStr + ' UTC');
      if (!isNaN(d.getTime())) {
        const pad = (n) => String(n).padStart(2, '0');
        timeStr = `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
      }
    }
    document.getElementById('version').textContent =
      `${v.version} (${v.gitCommit}) ${timeStr}`;
  } catch (e) {
    document.getElementById('version').textContent = '版本获取失败';
  }
}

// ===== 用户配置 =====

let savedConfig = null;
let currentUser = null; // { uid, username, isAdmin }

// ===== 用户身份 =====

async function loadCurrentUser() {
  try {
    currentUser = await apiCall('/me');
    applyPermissions();
  } catch (e) {
    // 非网关环境或接口不可用时，默认管理员权限（本地调试）
    currentUser = { uid: '0', username: 'local', isAdmin: true };
    applyPermissions();
  }
}

function applyPermissions() {
  const isAdmin = currentUser?.isAdmin === true;
  const username = currentUser?.username || '';

  // 管理员独占的 tab 和功能区
  // CSS 默认 .admin-only { display: none !important }
  // 管理员加 .visible class 覆盖为 display: revert，恢复元素默认 display
  document.querySelectorAll('.admin-only').forEach(el => {
    if (isAdmin) {
      el.classList.add('visible');
    } else {
      el.classList.remove('visible');
    }
  });

  // 管理员表格 / 普通用户列表的显隐
  const userTable = document.getElementById('userTable');
  const userList = document.getElementById('userList');
  const saveMyConfig = document.getElementById('saveMyConfig');
  if (isAdmin) {
    if (userTable) userTable.style.display = '';
    if (userList) userList.style.display = 'none';
    if (saveMyConfig) saveMyConfig.style.display = 'none';
  } else {
    if (userTable) userTable.style.display = 'none';
    if (userList) userList.style.display = '';
    // saveMyConfig 的显隐由 renderUsers 控制
  }

  // 非管理员在用户名输入框上设为只读（不能改用户名）
  if (!isAdmin && username) {
    document.querySelectorAll('.user-name').forEach(input => {
      input.readOnly = true;
    });
  }
}

// ===== 加载配置 =====

async function loadConfig() {
  try {
    const cfg = await apiCall('/config');
    savedConfig = cfg;
    renderUsers(cfg.users || {});
    renderSettings(cfg);
    // 重新应用权限（renderUsers 重建了 DOM，需要重新设置只读等）
    applyPermissions();
  } catch (e) {
    showToast('加载配置失败: ' + e.message, 'error');
  }
}

function renderUsers(users) {
  const list = document.getElementById('userList');
  const tableBody = document.getElementById('userTableBody');
  list.innerHTML = '';
  tableBody.innerHTML = '';

  const userEntries = Object.entries(users || {});
  const isAdmin = currentUser?.isAdmin === true;

  if (isAdmin) {
    // 管理员：表格模式
    // 如果没有用户，显示空提示
    if (userEntries.length === 0) {
      tableBody.innerHTML = '<tr><td colspan="4" style="text-align:center;color:var(--text-light)">暂无用户，点击「+ 添加用户」创建</td></tr>';
      return;
    }

    userEntries.forEach(([name, user]) => {
      const lfm = user.lastfm || {};
      const lb = user.listenbrainz || {};
      const lfmBadge = lfm.enabled
        ? `<span class="badge badge-on">✓</span>${lfm.username || lfMsk(lfm) ? '' : ''}`
        : '<span class="badge badge-off">✗</span>';
      const lbBadge = lb.enabled
        ? '<span class="badge badge-on">✓</span>'
        : '<span class="badge badge-off">✗</span>';

      // 行 ID 用于关联详情行
      const rowId = 'user-row-' + name.replace(/[^a-zA-Z0-9]/g, '_');

      const tr = document.createElement('tr');
      tr.innerHTML = `
        <td>${name}</td>
        <td>${lfmBadge} ${lfm.username ? '<span class="hint">'+lfm.username+'</span>' : ''}</td>
        <td>${lbBadge} ${lb.username ? '<span class="hint">'+lb.username+'</span>' : ''}</td>
        <td class="col-actions">
          <button class="btn btn-sm btn-edit-user">编辑</button>
          <button class="btn btn-danger btn-sm btn-del-user">删除</button>
        </td>
      `;
      tr.dataset.userName = name;
      tr.id = rowId;

      // 详情行（默认隐藏）
      const detailTr = document.createElement('tr');
      detailTr.className = 'detail-row';
      detailTr.style.display = 'none';
      detailTr.id = rowId + '-detail';
      const detailTd = document.createElement('td');
      detailTd.colSpan = 4;
      detailTr.appendChild(detailTd);

      // 点击编辑按钮：展开/收起详情
      tr.querySelector('.btn-edit-user').addEventListener('click', () => {
        if (detailTr.style.display === 'none') {
          // 首次展开时创建详情卡片
          if (!detailTd.children.length) {
            const card = createUserCard(name, user);
            card.dataset.userName = name;
            detailTd.appendChild(card);
            // 重新应用权限到新卡片
            applyPermissions();
          }
          detailTr.style.display = '';
          tr.querySelector('.btn-edit-user').textContent = '收起';
        } else {
          detailTr.style.display = 'none';
          tr.querySelector('.btn-edit-user').textContent = '编辑';
        }
      });

      // 删除按钮
      tr.querySelector('.btn-del-user').addEventListener('click', () => {
        if (confirm('确定删除用户 ' + name + '？')) {
          deleteUserByName(name);
        }
      });

      tableBody.appendChild(tr);
      tableBody.appendChild(detailTr);
    });
  } else {
    // 普通用户：卡片模式（保持现有方式）
    // 如果配置中没有自己的用户配置，自动添加一个空白卡片
    if (currentUser && currentUser.username) {
      const hasOwn = userEntries.some(([name]) => name === currentUser.username);
      if (!hasOwn) {
        const card = createUserCard(currentUser.username, {});
        card.classList.add('simple-mode');
        list.appendChild(card);
        showSimpleSaveBar(card);
        return;
      }
    }

    userEntries.forEach(([name, user]) => {
      const card = createUserCard(name, user);
      card.classList.add('simple-mode');
      list.appendChild(card);
    });

    // 普通用户模式：保存按钮已在卡片底部，无需 toolbar 按钮
    const saveBtn = document.getElementById('saveMyConfig');
    if (saveBtn) {
      saveBtn.style.display = 'none';
    }
  }
}

// lfMsk 判断 lastfm 是否有 session_key
function lfMsk(lfm) {
  return lfm.session_key && lfm.session_key.length > 0;
}

// deleteUserByName 按用户名删除（管理员从表格行删除）
async function deleteUserByName(name) {
  if (!name) {
    showToast('无法确定要删除的用户名', 'error');
    return;
  }
  try {
    await apiCall('/user/' + encodeURIComponent(name), 'DELETE');
    showToast('用户已删除');
    await loadConfig();
  } catch (e) {
    showToast('删除失败: ' + e.message, 'error');
  }
}

// showSimpleSaveBar 为普通用户模式的卡片初始化（保存按钮已在卡片内）
function showSimpleSaveBar(card) {
  // 普通用户模式保存按钮已在卡片底部显示，无需额外操作
}

// updateAuthBtnState 根据卡片的 api_key + api_secret 是否同时有值，
// 以及该用户是否已保存到配置文件，决定授权按钮的 disabled 状态。
// 授权前必须先保存配置（确保后端拿到正确的凭证）。
function updateAuthBtnState(card) {
  const apiKey = card.querySelector('.lfm-api-key').value.trim();
  const apiSecret = card.querySelector('.lfm-api-secret').value.trim();
  const saved = card.dataset.userName && savedConfig?.users && (card.dataset.userName in savedConfig.users);
  const btn = card.querySelector('.lfm-auth-btn');
  if (!btn) return;

  // 只有 api_key + api_secret 都填写了，且用户已保存到配置文件，才可点击授权
  if (apiKey && apiSecret && saved) {
    btn.disabled = false;
    if (!btn.classList.contains('auth-done')) {
      btn.textContent = '授权';
    }
  } else {
    btn.disabled = true;
    btn.textContent = '授权';
    btn.classList.remove('auth-done');
  }
}

function createUserCard(name, user) {
  const tpl = document.getElementById('userTemplate');
  // cloneNode(true) 返回 DocumentFragment，appendChild 后 fragment 会变空，
  // 导致后续 querySelector 找不到元素。取 firstElementChild 得到实际 DOM 节点。
  const card = tpl.content.firstElementChild.cloneNode(true);
  card.querySelector('.user-name').value = name;
  card.querySelector('.user-title').textContent = `用户: ${name}`;
  // 存储原始用户名，用于删除时查找
  card.dataset.userName = name;

  const lfm = user.lastfm || {};
  card.querySelector('.lfm-enabled').checked = lfm.enabled || false;
  card.querySelector('.lfm-api-key').value = lfm.api_key || '';
  card.querySelector('.lfm-api-secret').value = lfm.api_secret || '';
  card.querySelector('.lfm-session-key').value = lfm.session_key || '';
  card.querySelector('.lfm-username').value = lfm.username || '';

  // 根据已保存的 api_key + api_secret 是否同时有值，决定授权按钮初始状态
  updateAuthBtnState(card);

  const lfmPl = lfm.playlist || {};
  const tt = lfmPl.top_tracks || {};
  card.querySelector('.lfm-top-enabled').checked = tt.enabled || false;
  card.querySelector('.lfm-top-name').value = tt.name || 'LF {period} 最常听';
  // period 是 select，设置 value 会自动选中对应 option
  card.querySelector('.lfm-top-period').value = tt.period || 'overall';
  card.querySelector('.lfm-top-limit').value = tt.limit || 50;

  const lt = lfmPl.loved_tracks || {};
  card.querySelector('.lfm-loved-enabled').checked = lt.enabled || false;
  card.querySelector('.lfm-loved-name').value = lt.name || 'LF 红心收藏';
  card.querySelector('.lfm-loved-limit').value = lt.limit || 50;

  const rt = lfmPl.recent_tracks || {};
  card.querySelector('.lfm-recent-enabled').checked = rt.enabled || false;
  card.querySelector('.lfm-recent-name').value = rt.name || 'LF 最近播放';
  card.querySelector('.lfm-recent-limit').value = rt.limit || 50;

  const lb = user.listenbrainz || {};
  card.querySelector('.lb-enabled').checked = lb.enabled || false;
  card.querySelector('.lb-token').value = lb.token || '';
  card.querySelector('.lb-username').value = lb.username || '';

  const lbPl = lb.playlist || {};
  const dj = lbPl.daily_jams || {};
  card.querySelector('.lb-daily-enabled').checked = dj.enabled || false;
  card.querySelector('.lb-daily-name').value = dj.name || 'LB 每日推荐';
  card.querySelector('.lb-daily-limit').value = dj.limit || 50;

  const wj = lbPl.weekly_jams || {};
  card.querySelector('.lb-weekly-enabled').checked = wj.enabled || false;
  card.querySelector('.lb-weekly-name').value = wj.name || 'LB 每周推荐';
  card.querySelector('.lb-weekly-limit').value = wj.limit || 50;

  const we = lbPl.weekly_exploration || {};
  card.querySelector('.lb-exploration-enabled').checked = we.enabled || false;
  card.querySelector('.lb-exploration-name').value = we.name || 'LB 每周探索';
  card.querySelector('.lb-exploration-limit').value = we.limit || 50;

  const yd = lbPl.year_discoveries || {};
  card.querySelector('.lb-discoveries-enabled').checked = yd.enabled || false;
  card.querySelector('.lb-discoveries-name').value = yd.name || 'LB {year} 年度发现';
  card.querySelector('.lb-discoveries-limit').value = yd.limit || 50;

  const ym = lbPl.year_missed || {};
  card.querySelector('.lb-missed-enabled').checked = ym.enabled || false;
  card.querySelector('.lb-missed-name').value = ym.name || 'LB {year} 年度遗珠';
  card.querySelector('.lb-missed-limit').value = ym.limit || 50;

  // 折叠
  card.querySelectorAll('.section-toggle').forEach(toggle => {
    toggle.addEventListener('click', () => {
      const body = toggle.nextElementSibling;
      const expanded = body.style.display !== 'none';
      body.style.display = expanded ? 'none' : 'block';
      toggle.classList.toggle('expanded', !expanded);
    });
  });

  // 密码切换显示/隐藏
  card.querySelectorAll('.btn-toggle-pwd').forEach(btn => {
    btn.addEventListener('click', () => {
      const selector = btn.dataset.target;
      const input = card.querySelector(selector);
      if (input) {
        input.type = input.type === 'password' ? 'text' : 'password';
        btn.textContent = input.type === 'password' ? '👁' : '🙈';
      }
    });
  });

  // 监听 api_key/api_secret 输入变化，动态启用/禁用授权按钮
  card.querySelector('.lfm-api-key').addEventListener('input', () => updateAuthBtnState(card));
  card.querySelector('.lfm-api-secret').addEventListener('input', () => updateAuthBtnState(card));

  // 保存单用户：管理员和普通用户都在卡片底部显示保存按钮
  const saveBtn = card.querySelector('.user-save');
  saveBtn.style.display = '';
  saveBtn.addEventListener('click', () => saveUserCard(card));

  // Last.fm 授权按钮
  card.querySelector('.lfm-auth-btn').addEventListener('click', () => startLastFMAuth(card));

  // 删除按钮：管理员通过表格行删除，卡片内不需要
  // 普通用户也不需要删除按钮
  const deleteBtn = card.querySelector('.user-delete');
  deleteBtn.style.display = 'none';
  deleteBtn.addEventListener('click', () => {
    if (confirm('确定删除此用户配置？')) {
      deleteUser(card);
    }
  });

  // 管理员模式下卡片在表格详情行中展开，不需要显示用户标题行
  // （表格行已有用户名和操作按钮）
  if (currentUser?.isAdmin) {
    const header = card.querySelector('.user-header');
    if (header) header.style.display = 'none';
  }

  return card;
}

function collectUserData(card) {
  const name = card.querySelector('.user-name').value.trim();
  if (!name) throw new Error('用户名不能为空');

  return {
    lastfm: {
      enabled: card.querySelector('.lfm-enabled').checked,
      api_key: card.querySelector('.lfm-api-key').value.trim(),
      api_secret: card.querySelector('.lfm-api-secret').value.trim(),
      session_key: card.querySelector('.lfm-session-key').value.trim(),
      username: card.querySelector('.lfm-username').value.trim(),
      playlist: {
        top_tracks: {
          enabled: card.querySelector('.lfm-top-enabled').checked,
          name: card.querySelector('.lfm-top-name').value.trim(),
          period: card.querySelector('.lfm-top-period').value.trim() || 'overall',
          limit: parseInt(card.querySelector('.lfm-top-limit').value) || 50,
        },
        loved_tracks: {
          enabled: card.querySelector('.lfm-loved-enabled').checked,
          name: card.querySelector('.lfm-loved-name').value.trim(),
          limit: parseInt(card.querySelector('.lfm-loved-limit').value) || 50,
        },
        recent_tracks: {
          enabled: card.querySelector('.lfm-recent-enabled').checked,
          name: card.querySelector('.lfm-recent-name').value.trim(),
          limit: parseInt(card.querySelector('.lfm-recent-limit').value) || 50,
        },
      },
    },
    listenbrainz: {
      enabled: card.querySelector('.lb-enabled').checked,
      token: card.querySelector('.lb-token').value.trim(),
      username: card.querySelector('.lb-username').value.trim(),
      playlist: {
        daily_jams: {
          enabled: card.querySelector('.lb-daily-enabled').checked,
          name: card.querySelector('.lb-daily-name').value.trim(),
          limit: parseInt(card.querySelector('.lb-daily-limit').value) || 0,
        },
        weekly_jams: {
          enabled: card.querySelector('.lb-weekly-enabled').checked,
          name: card.querySelector('.lb-weekly-name').value.trim(),
          limit: parseInt(card.querySelector('.lb-weekly-limit').value) || 0,
        },
        weekly_exploration: {
          enabled: card.querySelector('.lb-exploration-enabled').checked,
          name: card.querySelector('.lb-exploration-name').value.trim(),
          limit: parseInt(card.querySelector('.lb-exploration-limit').value) || 0,
        },
        year_discoveries: {
          enabled: card.querySelector('.lb-discoveries-enabled').checked,
          name: card.querySelector('.lb-discoveries-name').value.trim(),
          limit: parseInt(card.querySelector('.lb-discoveries-limit').value) || 0,
        },
        year_missed: {
          enabled: card.querySelector('.lb-missed-enabled').checked,
          name: card.querySelector('.lb-missed-name').value.trim(),
          limit: parseInt(card.querySelector('.lb-missed-limit').value) || 0,
        },
      },
    },
  };
}

async function saveUserCard(card) {
  try {
    const userData = collectUserData(card);
    const userName = card.querySelector('.user-name').value.trim();
    const oldName = card.dataset.userName || '';

    // 如果用户名变了，且旧用户确实存在于配置文件中，才先删除旧用户
    if (oldName && oldName !== userName) {
      const oldExists = savedConfig?.users && (oldName in savedConfig.users);
      if (oldExists) {
        await apiCall('/user/' + encodeURIComponent(oldName), 'DELETE');
      }
    }

    // 通过细粒度 API 保存单个用户
    await apiCall('/user/' + encodeURIComponent(userName), 'POST', userData);
    card.dataset.userName = userName;
    showToast('用户配置已保存');
    await loadConfig();
    // 保存成功后重新检查授权按钮状态（此时 savedConfig 已更新）
    updateAuthBtnState(card);
  } catch (e) {
    showToast('保存失败: ' + e.message, 'error');
  }
}

async function deleteUser(card) {
  const userName = card.dataset.userName || '';

  // 未保存的新用户卡片（配置文件中不存在），直接从 DOM 移除即可
  const existsInConfig = savedConfig?.users && (userName in savedConfig.users);
  if (!existsInConfig) {
    card.remove();
    showToast('已移除未保存的用户卡片');
    return;
  }

  if (!userName) {
    showToast('无法确定要删除的用户名', 'error');
    return;
  }

  try {
    // 通过细粒度 API 直接删除单个用户
    await apiCall('/user/' + encodeURIComponent(userName), 'DELETE');
    showToast('用户已删除');
    await loadConfig();
  } catch (e) {
    showToast('删除失败: ' + e.message, 'error');
  }
}

// ===== Last.fm 授权 =====

// startLastFMAuth 发起 Last.fm 授权流程：
// 1. 从卡片读取 api_key + api_secret + 飞牛用户名
// 2. 调用后端 /api/lastfm/auth 获取授权 URL
// 3. 在新标签页打开授权 URL
// 4. 开始轮询 /api/lastfm/poll，成功后自动写回 session_key
async function startLastFMAuth(card) {
  const apiKey = card.querySelector('.lfm-api-key').value.trim();
  const apiSecret = card.querySelector('.lfm-api-secret').value.trim();
  const username = card.querySelector('.user-name').value.trim() || card.dataset.userName || '';

  if (!apiKey || !apiSecret) {
    showToast('请先填写 API Key 和 API Secret', 'error');
    return;
  }
  if (!username) {
    showToast('请先填写飞牛用户名', 'error');
    return;
  }

  const btn = card.querySelector('.lfm-auth-btn');
  const statusEl = card.querySelector('.lfm-auth-status');
  btn.disabled = true;
  btn.textContent = '授权中...';
  statusEl.style.display = 'block';
  statusEl.className = 'auth-status lfm-auth-status auth-pending';
  statusEl.textContent = '正在申请授权令牌...';

  try {
    // 申请 token + 获取授权 URL
    const resp = await apiCall('/lastfm/auth', 'POST', {
      api_key: apiKey,
      api_secret: apiSecret,
      username: username,
    });

    // 在新标签页打开授权页面
    window.open(resp.auth_url, '_blank');

    statusEl.className = 'auth-status lfm-auth-status auth-pending';
    statusEl.textContent = '已打开授权页面，请在 Last.fm 点击「同意」后等待自动完成...';

    // 开始轮询
    pollLastFMAuth(card, apiKey, apiSecret, username);
  } catch (e) {
    btn.disabled = false;
    btn.textContent = '授权';
    statusEl.className = 'auth-status lfm-auth-status auth-error';
    statusEl.textContent = '授权失败: ' + e.message;
  }
}

// pollLastFMAuth 轮询后端检查授权状态
// 每 3 秒轮询一次，最多 60 次（3 分钟）
function pollLastFMAuth(card, apiKey, apiSecret, username) {
  const btn = card.querySelector('.lfm-auth-btn');
  const statusEl = card.querySelector('.lfm-auth-status');
  let attempts = 0;
  const maxAttempts = 60;

  const poll = async () => {
    attempts++;
    if (attempts > maxAttempts) {
      btn.disabled = false;
      btn.textContent = '授权';
      statusEl.className = 'auth-status lfm-auth-status auth-error';
      statusEl.textContent = '授权超时，请重试';
      return;
    }

    try {
      const resp = await apiCall('/lastfm/poll', 'POST', {
        api_key: apiKey,
        api_secret: apiSecret,
        username: username,
      });

      if (resp.authorized) {
        // 授权成功
        btn.disabled = false;
        btn.textContent = '已授权';
        btn.classList.add('auth-done');
        statusEl.className = 'auth-status lfm-auth-status auth-success';
        statusEl.textContent = `授权成功！Last.fm 用户: ${resp.lastfm_username}`;
        // 把 session_key 和用户名回填到表单
        card.querySelector('.lfm-session-key').value = resp.session_key || '';
        card.querySelector('.lfm-username').value = resp.lastfm_username || '';
        showToast('Last.fm 授权成功');
        // 重新加载配置以同步
        await loadConfig();
        return;
      }

      if (resp.expired) {
        btn.disabled = false;
        btn.textContent = '授权';
        statusEl.className = 'auth-status lfm-auth-status auth-error';
        statusEl.textContent = '授权会话已过期，请重新点击「授权」';
        return;
      }

      // 用户尚未点同意，继续等待
      if (resp.error) {
        statusEl.textContent = `等待授权...（${attempts}/${maxAttempts}）${resp.error}`;
      } else {
        statusEl.textContent = `等待授权...请在 Last.fm 页面点击「同意」（${attempts}/${maxAttempts}）`;
      }

      setTimeout(poll, 3000);
    } catch (e) {
      // 网络错误等，继续重试
      if (attempts >= maxAttempts) {
        btn.disabled = false;
        btn.textContent = '授权';
        statusEl.className = 'auth-status lfm-auth-status auth-error';
        statusEl.textContent = '轮询失败: ' + e.message;
        return;
      }
      setTimeout(poll, 3000);
    }
  };

  setTimeout(poll, 3000);
}

// ===== 全局设置 =====

function renderSettings(cfg) {
  document.getElementById('scrobbleThreshold').value = cfg.playback?.scrobble_threshold || 'auto';
  document.getElementById('playlistEnabled').checked = cfg.playlist?.enabled ?? true;
  document.getElementById('syncInterval').value = cfg.playlist?.sync_interval || '30m';

  const log = cfg.logging || {};
  document.getElementById('logLevel').value = log.level || 'info';
  document.getElementById('logMaxSize').value = log.max_size ?? 3;
  document.getElementById('logMaxBackups').value = log.max_backups ?? 5;
  document.getElementById('logMaxAge').value = log.max_age ?? 7;
  document.getElementById('logCompress').checked = log.compress ?? true;
}

document.getElementById('saveSettings').addEventListener('click', async () => {
  try {
    const settings = {
      playback: {
        scrobble_threshold: document.getElementById('scrobbleThreshold').value,
      },
      playlist: {
        enabled: document.getElementById('playlistEnabled').checked,
        sync_interval: document.getElementById('syncInterval').value.trim() || '30m',
      },
      logging: {
        level: document.getElementById('logLevel').value,
        max_size: parseInt(document.getElementById('logMaxSize').value) || 0,
        max_backups: parseInt(document.getElementById('logMaxBackups').value) || 0,
        max_age: parseInt(document.getElementById('logMaxAge').value) || 0,
        compress: document.getElementById('logCompress').checked,
      },
    };

    await apiCall('/settings', 'PUT', settings);
    showToast('全局设置已保存');
    // 重新加载配置以同步内存缓存
    await loadConfig();
  } catch (e) {
    showToast('保存失败: ' + e.message, 'error');
  }
});

// ===== 添加用户（模态框方式） =====

const addUserModal = document.getElementById('addUserModal');
const newUserNameInput = document.getElementById('newUserName');

document.getElementById('addUser').addEventListener('click', () => {
  newUserNameInput.value = '';
  addUserModal.style.display = 'flex';
  newUserNameInput.focus();
});

document.getElementById('cancelAddUser').addEventListener('click', () => {
  addUserModal.style.display = 'none';
});

// 点击遮罩层关闭
addUserModal.querySelector('.modal-overlay').addEventListener('click', () => {
  addUserModal.style.display = 'none';
});

// 回车确认
newUserNameInput.addEventListener('keydown', (e) => {
  if (e.key === 'Enter') {
    e.preventDefault();
    document.getElementById('confirmAddUser').click();
  }
});

document.getElementById('confirmAddUser').addEventListener('click', () => {
  const name = newUserNameInput.value.trim();
  if (!name) {
    showToast('用户名不能为空', 'error');
    return;
  }

  // 检查是否已存在
  const existing = savedConfig?.users || {};
  if (name in existing) {
    showToast('用户 ' + name + ' 已存在', 'error');
    return;
  }

  // 检查页面中是否已有同名条目
  const tableBody = document.getElementById('userTableBody');
  const list = document.getElementById('userList');
  const dupInTable = [...tableBody.querySelectorAll('tr[data-user-name]')].some(
    el => el.dataset.userName === name
  );
  const dupInList = [...list.querySelectorAll('.user-name')].some(
    el => el.value.trim() === name
  );
  if (dupInTable || dupInList) {
    showToast('页面已有同名用户', 'error');
    return;
  }

  const isAdmin = currentUser?.isAdmin === true;

  if (isAdmin) {
    // 管理员模式：在表格中新建一个可展开的行
    const rowId = 'user-row-' + name.replace(/[^a-zA-Z0-9]/g, '_');

    const tr = document.createElement('tr');
    tr.innerHTML = `
      <td>${name} <span class="badge badge-off" style="margin-left:4px">未保存</span></td>
      <td><span class="badge badge-off">✗</span></td>
      <td><span class="badge badge-off">✗</span></td>
      <td class="col-actions">
        <button class="btn btn-sm btn-edit-user">编辑</button>
        <button class="btn btn-danger btn-sm btn-del-user">删除</button>
      </td>
    `;
    tr.dataset.userName = name;
    tr.id = rowId;

    // 详情行
    const detailTr = document.createElement('tr');
    detailTr.className = 'detail-row';
    detailTr.id = rowId + '-detail';
    const detailTd = document.createElement('td');
    detailTd.colSpan = 4;
    detailTr.appendChild(detailTd);

    // 点击编辑：展开详情
    tr.querySelector('.btn-edit-user').addEventListener('click', () => {
      if (detailTr.style.display === 'none' || !detailTr.style.display) {
        if (!detailTd.children.length) {
          const card = createUserCard(name, {});
          card.dataset.userName = ''; // 标记为新建
          detailTd.appendChild(card);
          // 自动展开 Last.fm 区
          const lfmToggle = card.querySelector('.section-toggle[data-section="lastfm"]');
          if (lfmToggle) lfmToggle.click();
          applyPermissions();
        }
        detailTr.style.display = '';
        tr.querySelector('.btn-edit-user').textContent = '收起';
      } else {
        detailTr.style.display = 'none';
        tr.querySelector('.btn-edit-user').textContent = '编辑';
      }
    });

    // 删除按钮
    tr.querySelector('.btn-del-user').addEventListener('click', () => {
      if (confirm('确定删除用户 ' + name + '？')) {
        // 未保存的用户直接移除 DOM
        tr.remove();
        detailTr.remove();
        showToast('已移除未保存的用户');
      }
    });

    // 插入到表格末尾
    tableBody.appendChild(tr);
    tableBody.appendChild(detailTr);

    // 自动展开详情
    tr.querySelector('.btn-edit-user').click();
  } else {
    // 普通用户模式：添加卡片到列表
    const card = createUserCard(name, {});
    card.dataset.userName = '';
    card.classList.add('simple-mode');
    card.querySelector('.user-name').value = name;
    list.appendChild(card);

    // 自动展开 Last.fm 区
    const lfmToggle = card.querySelector('.section-toggle[data-section="lastfm"]');
    if (lfmToggle) lfmToggle.click();

    showSimpleSaveBar(card);
  }

  addUserModal.style.display = 'none';
  showToast('已添加用户，请填写配置后保存');
});

// ===== 运行状态 =====

async function loadState() {
  try {
    const state = await apiCall('/state');
    const el = document.getElementById('stateContent');
    const users = state.users || {};
    const userNames = Object.keys(users);

    if (userNames.length === 0) {
      el.innerHTML = '<p class="hint">暂无运行状态数据</p>';
      return;
    }

    let html = '';
    for (const [name, info] of Object.entries(users)) {
      const scrobbles = info.scrobbles ?? 0;
      const lastTime = info.last_scrobbled_at || '无记录';
      const lfm = info.lastfm_enabled ? '<span class="badge badge-on">Last.fm ✓</span>' : '<span class="badge badge-off">Last.fm ✗</span>';
      const lb = info.listenbrainz_enabled ? '<span class="badge badge-on">ListenBrainz ✓</span>' : '<span class="badge badge-off">ListenBrainz ✗</span>';

      html += `
        <div class="card state-card">
          <h3>${name}</h3>
          <div class="state-grid">
            <div class="state-item">
              <span class="state-label">Scrobble 总数</span>
              <span class="state-value">${scrobbles}</span>
            </div>
            <div class="state-item">
              <span class="state-label">最后 Scrobble</span>
              <span class="state-value">${lastTime}</span>
            </div>
            <div class="state-item">
              <span class="state-label">推送平台</span>
              <span class="state-value">${lfm} ${lb}</span>
            </div>
          </div>
        </div>`;
    }
    el.innerHTML = html;
  } catch (e) {
    document.getElementById('stateContent').textContent = '加载失败: ' + e.message;
  }
}

document.getElementById('refreshState').addEventListener('click', loadState);

// ===== 日志 =====

async function loadLogs() {
  try {
    const data = await apiCall('/logs');
    const lines = data.lines || [];
    document.getElementById('logContent').textContent = lines.join('\n') || '暂无日志';
    document.getElementById('logContent').scrollTop = 0;
  } catch (e) {
    document.getElementById('logContent').textContent = '加载失败: ' + e.message;
  }
}

document.getElementById('refreshLogs').addEventListener('click', loadLogs);

// ===== 初始化 =====

(async function init() {
  await loadVersion();
  await loadCurrentUser();
  await loadConfig();
  await loadState();
  // 日志仅管理员加载
  if (currentUser?.isAdmin) {
    await loadLogs();
  }
})();
