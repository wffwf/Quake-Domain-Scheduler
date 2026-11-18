'use strict';
/* 空探平台前端（原生 JS，零依赖零构建） */

const CRED_KEY = 'kongtan_api_key_v1';
const THEME_KEY = 'kongtan_theme_v1';
const TITLES = {
  dashboard: '仪表盘', apis: 'API 管理', groups: '分组与域名',
  blacklist: '黑名单', tasks: '任务管理', data: '数据查看', config: '配置查看', schedule: '计划任务',
};

const state = {
  view: 'dashboard',
  groups: [],
  apis: [],
  blacklist: [],
  tasks: [],
  overview: null,
  config: null,
  dataTab: 'urls',
  filters: { q: '', group: '', domain: '', host: '', icp: '' },
  selectedGroup: null,
  dataItems: [],
  dataTotal: 0,
  dataPage: 1,
  dataPageSize: 50,
  pollTimer: null,
};

const $ = (id) => document.getElementById(id);

function esc(s) {
  return String(s == null ? '' : s).replace(/[&<>"']/g, (c) => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
  }[c]));
}

function toast(msg, type) {
  const el = document.createElement('div');
  el.className = 'toast ' + (type || 'info');
  el.textContent = msg;
  $('toasts').appendChild(el);
  setTimeout(() => el.remove(), 3500);
}

/* ---------- API 封装 ---------- */
async function api(path, opts) {
  opts = opts || {};
  const h = Object.assign({}, opts.headers || {});
  const k = localStorage.getItem(CRED_KEY);
  if (k) h['Authorization'] = 'Bearer ' + k;
  if (opts.body) h['Content-Type'] = 'application/json';
  const r = await fetch('/api/' + path, Object.assign({}, opts, { headers: h }));
  if (r.status === 401) {
    const key = await promptKey();
    if (key) {
      localStorage.setItem(CRED_KEY, key);
      return api(path, opts);
    }
    throw new Error('未授权');
  }
  let d = {};
  try { d = await r.json(); } catch (e) { /* ignore */ }
  if (!r.ok) throw new Error(d.error || ('HTTP ' + r.status));
  return d.data;
}

function promptKey() {
  return new Promise((resolve) => {
    openModal({
      title: '需要鉴权', hint: '请输入访问 /api 所需的 API Key',
      body: '<div class="field"><label>API Key</label><input type="password" id="cred-input"></div>',
      actions: [
        { label: '取消', cls: 'ghost', onClick: () => { closeModal(); resolve(null); } },
        { label: '确认', cls: 'primary', onClick: () => { const v = $('cred-input').value.trim(); closeModal(); resolve(v || null); } },
      ],
    });
  });
}

/* ---------- 主题 ---------- */
function applyTheme() {
  const pref = localStorage.getItem(THEME_KEY) || 'auto';
  let t = pref;
  if (pref === 'auto') {
    t = (window.matchMedia && window.matchMedia('(prefers-color-scheme: light)').matches) ? 'light' : 'dark';
  }
  document.documentElement.setAttribute('data-theme', t);
}
function cycleTheme() {
  const cur = localStorage.getItem(THEME_KEY) || 'auto';
  const next = cur === 'auto' ? 'light' : (cur === 'light' ? 'dark' : 'auto');
  localStorage.setItem(THEME_KEY, next);
  applyTheme();
}

/* ---------- 视图切换 ---------- */
function go(view) {
  state.view = view;
  document.querySelectorAll('.view').forEach((s) => { s.hidden = (s.dataset.view !== view); });
  document.querySelectorAll('#nav-list a').forEach((a) => { a.classList.toggle('on', a.dataset.view === view); });
  $('view-title').textContent = TITLES[view] || '';
  location.hash = view;
  $('sidebar').classList.remove('open');
  renderCurrent();
}
function renderCurrent() {
  switch (state.view) {
    case 'dashboard': renderOverview(); break;
    case 'apis': renderAPIs(); break;
    case 'groups': renderGroups(); break;
    case 'blacklist': renderBlacklist(); break;
    case 'tasks': renderTasks(); break;
    case 'data': renderData(); break;
    case 'config': renderConfig(); break;
    case 'schedule': renderSchedule(); break;
  }
}

/* ---------- 渲染：仪表盘 ---------- */
async function renderOverview() {
  try {
    const ov = await api('overview');
    state.overview = ov;
    setNavState(true);
    const creditVal = ov.credits_updated_at ? String(ov.credits_total) : '-';
    const cards = [
      { v: creditVal, k: '当前剩余点数', tone: ov.credits_updated_at ? 'ok' : '' },
      { v: ov.group_count, k: '分组', tone: '' },
      { v: ov.domain_count, k: '域名', tone: '' },
      { v: ov.apikey_enabled + '/' + ov.apikey_count, k: '启用 API / 总数', tone: '' },
      { v: ov.url_count, k: 'URL 记录', tone: '' },
      { v: ov.subdomain_count, k: '子域名记录', tone: '' },
      { v: ov.running_tasks, k: '运行中任务', tone: ov.running_tasks > 0 ? 'ok' : '' },
      { v: ov.last_task_at || '-', k: '上次任务时间', tone: '' },
    ];
    $('dash-stats').innerHTML = cards.map((c) =>
      `<div class="stat-card ${c.tone ? 'tone-' + c.tone : ''}"><div class="v">${esc(c.v)}</div><div class="k">${esc(c.k)}</div></div>`
    ).join('');
    $('credits-updated').textContent = ov.credits_updated_at ? ('最近更新：' + ov.credits_updated_at) : '尚未查询（点击左侧按钮查询）';

    const tasks = await api('tasks?limit=6');
    $('dash-tasks').innerHTML = renderTaskRows(tasks.items);
  } catch (e) {
    setNavState(false);
    toast('加载仪表盘失败: ' + e.message, 'error');
  }
}

function renderTaskRows(items) {
  if (!items || !items.length) return '<tr><td colspan="7" class="empty"><div class="t">暂无任务</div><div class="d">在「任务管理」中点击立即执行</div></td></tr>';
  return items.map((t) => `<tr>
    <td class="num">${t.id}</td><td>${esc(t.group_name)}</td><td>${t.trigger_type === 'manual' ? '手动' : '定时'}</td>
    <td>${statusTag(t.status)}</td><td>${esc(t.started_at)}</td><td class="num">${t.total_records}</td><td class="num">${t.consumed_points}</td>
  </tr>`).join('');
}

function statusTag(s) {
  const map = { running: 'tag-ok', success: 'tag-ok', partial: 'tag-warn', failed: 'tag-bad', pending: 'tag-neutral' };
  const label = { running: '运行中', success: '成功', partial: '部分成功', failed: '失败', pending: '等待' };
  return `<span class="tag ${map[s] || 'tag-neutral'}">${label[s] || s}</span>`;
}

function statusText(s) {
  const label = { running: '运行中', success: '成功', partial: '部分成功', failed: '失败', pending: '等待' };
  return label[s] || s;
}

function fmtDur(sec) {
  if (sec == null || sec < 0) return '-';
  if (sec < 60) return sec + ' 秒';
  if (sec < 3600) return Math.floor(sec / 60) + ' 分 ' + (sec % 60) + ' 秒';
  return Math.floor(sec / 3600) + ' 时 ' + Math.floor((sec % 3600) / 60) + ' 分';
}

/* ---------- 渲染：API ---------- */
async function renderAPIs() {
  try {
    state.apis = await api('apikeys');
    const body = $('apis-body');
    if (!state.apis.length) {
      body.innerHTML = '<tr><td colspan="5" class="empty"><div class="t">暂无 API</div><div class="d">新增 360Quake Token 后即可探测</div></td></tr>';
      return;
    }
    body.innerHTML = state.apis.map((k, i) => `<tr>
      <td class="num">${i}</td>
      <td>${esc(k.name)}</td>
      <td><code>${esc(k.token_masked)}</code></td>
      <td><button class="xs ghost" data-api-toggle="${k.id}">${k.enabled ? '启用' : '禁用'}</button></td>
      <td class="col-actions">
        <button class="xs ghost" data-api-up="${k.id}">↑</button>
        <button class="xs ghost" data-api-down="${k.id}">↓</button>
        <button class="xs ghost" data-api-test="${k.id}">测试</button>
        <button class="xs ghost" data-api-edit="${k.id}">编辑</button>
        <button class="xs danger" data-api-del="${k.id}">删除</button>
      </td></tr>`).join('');
  } catch (e) { toast('加载 API 失败: ' + e.message, 'error'); }
}

/* ---------- 渲染：分组与域名 ---------- */
async function renderGroups() {
  try {
    state.groups = await api('groups');
    const sel = $('group-select');
    sel.innerHTML = state.groups.map((g) => `<option value="${g.id}">${esc(g.name)}（${g.domain_count}）</option>`).join('')
      || '<option value="">（无分组）</option>';
    if (state.selectedGroup == null && state.groups.length) state.selectedGroup = state.groups[0].id;
    if (state.groups.length && !state.groups.some((g) => g.id === state.selectedGroup)) state.selectedGroup = state.groups[0].id;
    sel.value = state.selectedGroup ? String(state.selectedGroup) : '';
    renderGroupDesc();
    await renderDomains();
  } catch (e) { toast('加载分组失败: ' + e.message, 'error'); }
}

function renderGroupDesc() {
  const g = state.groups.find((x) => x.id === state.selectedGroup);
  $('group-desc').textContent = g ? ('分组说明：' + (g.description || '（无）')) : '请先新建分组';
}

async function renderDomains() {
  const body = $('domains-body');
  if (!state.selectedGroup) {
    body.innerHTML = '<tr><td colspan="5" class="empty"><div class="t">暂无分组</div><div class="d">先新建分组再添加域名</div></td></tr>';
    return;
  }
  try {
    const ds = await api('groups/' + state.selectedGroup + '/domains');
    if (!ds.length) {
      body.innerHTML = '<tr><td colspan="5" class="empty"><div class="t">该分组暂无域名</div><div class="d">点击「添加域名」批量录入</div></td></tr>';
      return;
    }
    body.innerHTML = ds.map((d) => `<tr>
      <td class="num">${d.id}</td><td>${esc(d.domain)}</td>
      <td><button class="xs ghost" data-dom-toggle="${d.id}">${d.enabled ? '启用' : '禁用'}</button></td>
      <td>${esc(d.created_at)}</td>
      <td class="col-actions"><button class="xs danger" data-dom-del="${d.id}">删除</button></td></tr>`).join('');
  } catch (e) { toast('加载域名失败: ' + e.message, 'error'); }
}

/* ---------- 渲染：黑名单 ---------- */
async function renderBlacklist() {
  try {
    state.blacklist = await api('blacklist');
    const body = $('blacklist-body');
    if (!state.blacklist.length) {
      body.innerHTML = '<tr><td colspan="4" class="empty"><div class="t">暂无黑名单</div><div class="d">可添加关键词或正则</div></td></tr>';
      return;
    }
    body.innerHTML = state.blacklist.map((b) => `<tr>
      <td class="num">${b.id}</td>
      <td>${b.type === 'keyword' ? '关键词' : '正则'}</td>
      <td><code>${esc(b.value)}</code></td>
      <td class="col-actions"><button class="xs danger" data-bl-del="${b.id}">删除</button></td></tr>`).join('');
  } catch (e) { toast('加载黑名单失败: ' + e.message, 'error'); }
}

/* ---------- 渲染：任务 ---------- */
async function renderTasks() {
  try {
    const r = await api('tasks?limit=100');
    state.tasks = r.items;
    const body = $('tasks-body');
    if (!state.tasks.length) {
      body.innerHTML = '<tr><td colspan="9" class="empty"><div class="t">暂无任务</div><div class="d">选择分组后点击「立即执行」</div></td></tr>';
    } else {
      body.innerHTML = state.tasks.map((t) => `<tr>
        <td class="num">${t.id}</td><td>${esc(t.group_name)}</td>
        <td>${t.trigger_type === 'manual' ? '手动' : '定时'}</td>
        <td>${statusTag(t.status)}</td>
        <td>${esc(t.started_at)}</td><td>${esc(t.finished_at || '-')}</td>
        <td class="num">${t.total_records}</td><td class="num">${t.consumed_points}</td>
        <td title="${esc(t.error_msg)}">${esc((t.error_msg || '').slice(0, 60))}</td></tr>`).join('');
    }
    // 填充「立即执行」分组下拉与数据查看任务下拉。
    if (!state.groups.length) state.groups = await api('groups');
    const opts = state.groups.map((g) => `<option value="${g.id}">${esc(g.name)}</option>`).join('');
    $('run-group-select').innerHTML = opts || '<option value="">（无分组）</option>';
    $('data-task-select').innerHTML = state.tasks.map((t) => `<option value="${t.id}">#${t.id} ${esc(t.group_name)} ${esc(t.started_at)}</option>`).join('')
      || '<option value="">（无任务）</option>';
  } catch (e) { toast('加载任务失败: ' + e.message, 'error'); }
}

/* ---------- 渲染：数据查看 ---------- */
async function renderData() {
  const offset = (state.dataPage - 1) * state.dataPageSize;
  const params = new URLSearchParams({
    q: state.filters.q, group: state.filters.group, domain: state.filters.domain,
    host: state.filters.host, icp: state.filters.icp, limit: state.dataPageSize, offset: offset,
  });
  try {
    const r = await api((state.dataTab === 'urls' ? 'urls?' : 'subdomains?') + params.toString());
    state.dataItems = r.items; state.dataTotal = r.total;
    const pages = Math.max(1, Math.ceil(r.total / state.dataPageSize));
    if (state.dataPage > pages) { state.dataPage = pages; return renderData(); }
    renderDataTable();
    $('data-count').textContent = '共 ' + r.total + ' 条';
    $('data-page-info').textContent = '第 ' + state.dataPage + ' / ' + pages + ' 页（每页 ' + state.dataPageSize + ' 条）';
    $('data-prev').disabled = state.dataPage <= 1;
    $('data-next').disabled = state.dataPage >= pages;
    document.querySelectorAll('.tab').forEach((t) => t.classList.toggle('on', t.dataset.dtab === state.dataTab));
    if (!state.tasks.length) { try { state.tasks = (await api('tasks?limit=100')).items; } catch (e) {} }
    $('data-task-select').innerHTML = state.tasks.map((t) => `<option value="${t.id}">#${t.id} ${esc(t.group_name)} ${esc(t.started_at)}</option>`).join('')
      || '<option value="">（无任务）</option>';
  } catch (e) { toast('加载数据失败: ' + e.message, 'error'); }
}

function renderDataTable() {
  let head, rows;
  if (state.dataTab === 'urls') {
    head = '<tr><th>分组</th><th>任务</th><th>执行时间</th><th>URL</th><th>host</th><th>IP</th><th>端口</th><th>标题</th><th>域名</th><th>ICP单位</th><th>省市</th></tr>';
    rows = state.dataItems.map((r) => `<tr>
      <td>${esc(r.group_name)}</td>
      <td title="${esc(r.task_name)}">${esc(r.task_name)}</td>
      <td>${esc(r.task_started_at || '-')}</td>
      <td>${esc(r.url)}</td><td>${esc(r.host)}</td><td>${esc(r.ip)}</td><td class="num">${r.port}</td>
      <td>${esc(r.title)}</td><td>${esc(r.domain)}</td><td>${esc(r.icp_unit)}</td><td>${esc(r.province_cn)} ${esc(r.city_cn)}</td></tr>`).join('');
  } else {
    head = '<tr><th>分组</th><th>任务</th><th>执行时间</th><th>子域名</th><th>域名</th><th>host</th><th>ICP单位</th><th>省市</th><th>ISP</th></tr>';
    rows = state.dataItems.map((r) => `<tr>
      <td>${esc(r.group_name)}</td>
      <td title="${esc(r.task_name)}">${esc(r.task_name)}</td>
      <td>${esc(r.task_started_at || '-')}</td>
      <td>${esc(r.subdomain)}</td><td>${esc(r.domain)}</td><td>${esc(r.host)}</td>
      <td>${esc(r.icp_unit)}</td><td>${esc(r.province_cn)} ${esc(r.city_cn)}</td><td>${esc(r.isp)}</td></tr>`).join('');
  }
  $('data-head').innerHTML = head;
  const emptyCols = state.dataTab === 'urls' ? 11 : 9;
  $('data-body').innerHTML = rows || '<tr><td colspan="' + emptyCols + '" class="empty"><div class="t">暂无数据</div></td></tr>';
}

/* ---------- 渲染：配置 ---------- */
async function renderConfig() {
  try {
    state.config = await api('config');
    $('config-view').textContent = JSON.stringify(state.config, null, 2);
  } catch (e) { toast('加载配置失败: ' + e.message, 'error'); }
}

/* ---------- 渲染：计划任务 ---------- */
async function renderSchedule() {
  try {
    const d = await api('schedule');
    const stateBadge = d.enabled
      ? '<span class="tag tag-ok">已开启</span>'
      : '<span class="tag tag-bad">已关闭</span>';
    $('schedule-notice').innerHTML = d.enabled ? '' : '<div class="banner warn">自动调度已关闭</div>';

    const monthCell = d.month_executed
      ? ('是 · 任务 #' + d.month_task_id + ' @ ' + esc(d.month_task_time || d.month_executed_at || '-'))
      : '否';
    const lastCell = d.last_auto_task
      ? ('#' + d.last_auto_task.id + ' ' + esc(d.last_auto_task.group_name) + ' · ' + statusText(d.last_auto_task.status)
        + ' · ' + esc(d.last_auto_task.started_at) + ' · 耗时 ' + fmtDur(d.last_auto_task.duration_sec)
        + ' · ' + d.last_auto_task.consumed_points + ' 点')
      : '暂无';

    const rows = [
      ['调度开关', stateBadge],
      ['每月执行窗口', '每月最后 ' + d.window_hours + ' 小时'],
      ['时区', esc(d.timezone)],
      ['下一次预计执行时间（窗口起点）', esc(d.next_run_at || '-')],
      ['本月是否已自动执行', monthCell],
      ['最近一次自动任务', lastCell],
    ];
    $('schedule-info').innerHTML = rows.map((r) =>
      `<div class="kv"><span class="k">${esc(r[0])}</span><span class="v">${r[1]}</span></div>`
    ).join('');

    const hist = d.history || [];
    $('schedule-history').innerHTML = hist.length
      ? hist.map((t) => `<tr>
          <td class="num">${t.id}</td><td>${esc(t.group_name)}</td><td>${statusTag(t.status)}</td>
          <td>${esc(t.started_at)}</td><td>${fmtDur(t.duration_sec)}</td><td class="num">${t.consumed_points}</td>
        </tr>`).join('')
      : '<tr><td colspan="6" class="empty"><div class="t">暂无自动任务</div></td></tr>';
  } catch (e) { toast('加载计划任务失败: ' + e.message, 'error'); }
}

/* ---------- 弹窗 ---------- */
function openModal({ title, hint, body, actions, width }) {
  const root = $('modal-root');
  root.innerHTML = '';
  const dlg = document.createElement('div');
  dlg.className = 'dialog' + (width === 'wide' ? ' wide' : '');
  const head = document.createElement('header');
  head.innerHTML = '<h3>' + esc(title) + '</h3>' + (hint ? '<div class="hint">' + esc(hint) + '</div>' : '');
  const bd = document.createElement('div');
  bd.className = 'body';
  bd.innerHTML = body;
  const ft = document.createElement('footer');
  ft.innerHTML = '<span class="grow"></span>';
  actions.forEach((a) => {
    const btn = document.createElement('button');
    btn.className = a.cls || 'primary';
    btn.textContent = a.label;
    btn.addEventListener('click', a.onClick);
    ft.appendChild(btn);
  });
  dlg.appendChild(head); dlg.appendChild(bd); dlg.appendChild(ft);
  root.appendChild(dlg);
  root.hidden = false;
}
function closeModal() { $('modal-root').hidden = true; $('modal-root').innerHTML = ''; }
function confirmModal(message, onYes) {
  openModal({
    title: '确认操作', hint: message,
    body: '',
    actions: [
      { label: '取消', cls: 'ghost', onClick: closeModal },
      { label: '确认', cls: 'danger', onClick: () => { closeModal(); onYes(); } },
    ],
  });
}

/* ---------- 各操作弹窗 ---------- */
function modalAddAPI() {
  openModal({
    title: '新增 API', hint: 'Token 仅用于调用 Quake，界面脱敏展示',
    body: '<div class="field"><label>名称</label><input id="m-name"></div>'
      + '<div class="field"><label>Token</label><input id="m-token" type="password"></div>'
      + '<div class="field"><label>启用</label><select id="m-enabled"><option value="1">启用</option><option value="0">禁用</option></select></div>'
      + '<div class="err" id="m-err"></div>',
    actions: [
      { label: '取消', cls: 'ghost', onClick: closeModal },
      {
        label: '保存', cls: 'primary', onClick: async () => {
          const name = $('m-name').value.trim();
          const token = $('m-token').value.trim();
          const enabled = $('m-enabled').value === '1';
          if (!token) { $('m-err').textContent = 'Token 不能为空'; return; }
          try { await api('apikeys', { method: 'POST', body: JSON.stringify({ name, token, enabled }) }); closeModal(); toast('已新增', 'success'); renderAPIs(); }
          catch (e) { $('m-err').textContent = e.message; }
        },
      },
    ],
  });
}

function modalEditAPI(k) {
  openModal({
    title: '编辑 API', hint: 'Token 留空表示保持不变',
    body: '<div class="field"><label>名称</label><input id="m-name" value="' + esc(k.name) + '"></div>'
      + '<div class="field"><label>Token（留空不变）</label><input id="m-token" type="password" placeholder="留空保持不变"></div>'
      + '<div class="field"><label>启用</label><select id="m-enabled"><option value="1"' + (k.enabled ? ' selected' : '') + '>启用</option><option value="0"' + (k.enabled ? '' : ' selected') + '>禁用</option></select></div>'
      + '<div class="err" id="m-err"></div>',
    actions: [
      { label: '取消', cls: 'ghost', onClick: closeModal },
      {
        label: '保存', cls: 'primary', onClick: async () => {
          const name = $('m-name').value.trim();
          const token = $('m-token').value.trim();
          const enabled = $('m-enabled').value === '1';
          try { await api('apikeys/' + k.id, { method: 'PUT', body: JSON.stringify({ name, token, enabled }) }); closeModal(); toast('已保存', 'success'); renderAPIs(); }
          catch (e) { $('m-err').textContent = e.message; }
        },
      },
    ],
  });
}

function modalAddGroup() {
  openModal({
    title: '新建分组',
    body: '<div class="field"><label>名称</label><input id="m-name"></div>'
      + '<div class="field"><label>说明</label><textarea id="m-desc"></textarea></div>'
      + '<div class="err" id="m-err"></div>',
    actions: [
      { label: '取消', cls: 'ghost', onClick: closeModal },
      {
        label: '保存', cls: 'primary', onClick: async () => {
          const name = $('m-name').value.trim();
          if (!name) { $('m-err').textContent = '名称不能为空'; return; }
          try { await api('groups', { method: 'POST', body: JSON.stringify({ name, description: $('m-desc').value }) }); closeModal(); toast('已新建', 'success'); renderGroups(); }
          catch (e) { $('m-err').textContent = e.message; }
        },
      },
    ],
  });
}

function modalEditGroup() {
  const g = state.groups.find((x) => x.id === state.selectedGroup);
  if (!g) { toast('请先选择分组', 'error'); return; }
  openModal({
    title: '编辑分组',
    body: '<div class="field"><label>名称</label><input id="m-name" value="' + esc(g.name) + '"></div>'
      + '<div class="field"><label>说明</label><textarea id="m-desc">' + esc(g.description) + '</textarea></div>'
      + '<div class="err" id="m-err"></div>',
    actions: [
      { label: '取消', cls: 'ghost', onClick: closeModal },
      {
        label: '保存', cls: 'primary', onClick: async () => {
          const name = $('m-name').value.trim();
          if (!name) { $('m-err').textContent = '名称不能为空'; return; }
          try { await api('groups/' + g.id, { method: 'PUT', body: JSON.stringify({ name, description: $('m-desc').value }) }); closeModal(); toast('已保存', 'success'); renderGroups(); }
          catch (e) { $('m-err').textContent = e.message; }
        },
      },
    ],
  });
}

function modalAddDomain() {
  if (!state.selectedGroup) { toast('请先新建分组', 'error'); return; }
  openModal({
    title: '添加域名', hint: '支持批量粘贴，每行一个域名；同分组内自动去重',
    width: 'wide',
    body: '<div class="field"><label>域名（批量：每行一个）</label><textarea id="m-domains" placeholder="example.com&#10;wap.js.10086.cn"></textarea></div>'
      + '<div class="err" id="m-err"></div>',
    actions: [
      { label: '取消', cls: 'ghost', onClick: closeModal },
      {
        label: '添加', cls: 'primary', onClick: async () => {
          const raw = $('m-domains').value;
          const domains = raw.split(/\r?\n/).map((s) => s.trim()).filter(Boolean);
          if (!domains.length) { $('m-err').textContent = '请输入至少一个域名'; return; }
          try {
            const r = await api('domains', { method: 'POST', body: JSON.stringify({ group_id: state.selectedGroup, domains }) });
            closeModal(); toast('新增 ' + r.added + ' 个域名', 'success'); renderGroups();
          } catch (e) { $('m-err').textContent = e.message; }
        },
      },
    ],
  });
}

function modalAddBlacklist() {
  openModal({
    title: '新增黑名单规则',
    body: '<div class="field"><label>类型</label><select id="m-type"><option value="keyword">关键词（包含）</option><option value="regex">正则</option></select></div>'
      + '<div class="field"><label>值</label><input id="m-value"></div>'
      + '<div class="err" id="m-err"></div>',
    actions: [
      { label: '取消', cls: 'ghost', onClick: closeModal },
      {
        label: '添加', cls: 'primary', onClick: async () => {
          const type = $('m-type').value;
          const value = $('m-value').value.trim();
          if (!value) { $('m-err').textContent = '值不能为空'; return; }
          try { await api('blacklist', { method: 'POST', body: JSON.stringify({ type, value }) }); closeModal(); toast('已添加', 'success'); renderBlacklist(); }
          catch (e) { $('m-err').textContent = e.message; }
        },
      },
    ],
  });
}

/* ---------- CSV 下载 ---------- */
function downloadCSV() {
  const taskId = $('data-task-select').value;
  if (!taskId) { toast('请先选择任务', 'error'); return; }
  const kind = state.dataTab;
  const a = document.createElement('a');
  a.href = '/api/export/' + kind + '?task_id=' + taskId;
  a.download = '';
  document.body.appendChild(a);
  a.click();
  a.remove();
  toast('已开始下载', 'success');
}

/* ---------- 事件绑定 ---------- */
function bind() {
  // 视图切换
  document.querySelectorAll('#nav-list a').forEach((a) => {
    a.addEventListener('click', (e) => { e.preventDefault(); go(a.dataset.view); });
  });
  document.querySelectorAll('[data-go]').forEach((el) => {
    el.addEventListener('click', () => go(el.dataset.go));
  });

  // 顶栏
  document.querySelector('[data-theme-toggle]').addEventListener('click', cycleTheme);
  document.querySelector('[data-refresh]').addEventListener('click', renderCurrent);
  document.querySelector('[data-navbtn]').addEventListener('click', () => $('sidebar').classList.toggle('open'));

  // 全局按钮（data-act）
  document.body.addEventListener('click', async (e) => {
    const t = e.target.closest('[data-act]');
    if (t) {
      const act = t.dataset.act;
      if (act === 'add-api') modalAddAPI();
      else if (act === 'add-group') modalAddGroup();
      else if (act === 'edit-group') modalEditGroup();
      else if (act === 'delete-group') {
        const g = state.groups.find((x) => x.id === state.selectedGroup);
        if (!g) { toast('请先选择分组', 'error'); return; }
        confirmModal('删除分组「' + g.name + '」及其下所有域名？', async () => {
          await api('groups/' + g.id, { method: 'DELETE' }); toast('已删除', 'success'); state.selectedGroup = null; renderGroups();
        });
      }
      else if (act === 'add-domain') modalAddDomain();
      else if (act === 'add-blacklist') modalAddBlacklist();
      else if (act === 'reset-data') { state.filters = { q: '', group: '', domain: '', host: '', icp: '' }; document.querySelectorAll('.filter-bar input[data-filter]').forEach((i) => { i.value = ''; }); renderData(); }
      else if (act === 'download-data' || act === 'download-task') downloadCSV();
      else if (act === 'run-task') runTask();
      else if (act === 'refresh-credits') refreshCredits();
      else if (act === 'data-prev') { state.dataPage = Math.max(1, state.dataPage - 1); renderData(); }
      else if (act === 'data-next') { state.dataPage += 1; renderData(); }
    }

    // API 操作
    const up = e.target.closest('[data-api-up]');
    if (up) { moveAPI(up.dataset.apiUp, -1); return; }
    const down = e.target.closest('[data-api-down]');
    if (down) { moveAPI(down.dataset.apiDown, 1); return; }
    const test = e.target.closest('[data-api-test]');
    if (test) { testAPI(test.dataset.apiTest); return; }
    const ed = e.target.closest('[data-api-edit]');
    if (ed) { const k = state.apis.find((x) => x.id == ed.dataset.apiEdit); if (k) modalEditAPI(k); return; }
    const del = e.target.closest('[data-api-del]');
    if (del) { confirmModal('删除该 API？', async () => { await api('apikeys/' + del.dataset.apiDel, { method: 'DELETE' }); toast('已删除', 'success'); renderAPIs(); }); return; }
    const tg = e.target.closest('[data-api-toggle]');
    if (tg) { toggleAPI(tg.dataset.apiToggle); return; }

    // 域名操作
    const ddel = e.target.closest('[data-dom-del]');
    if (ddel) { confirmModal('删除该域名？', async () => { await api('domains/' + ddel.dataset.domDel, { method: 'DELETE' }); toast('已删除', 'success'); renderGroups(); }); return; }
    const dtg = e.target.closest('[data-dom-toggle]');
    if (dtg) { toggleDomain(dtg.dataset.domToggle); return; }

    // 黑名单删除
    const bdel = e.target.closest('[data-bl-del]');
    if (bdel) { confirmModal('删除该规则？', async () => { await api('blacklist/' + bdel.dataset.blDel, { method: 'DELETE' }); toast('已删除', 'success'); renderBlacklist(); }); return; }
  });

  // 分组下拉切换
  $('group-select').addEventListener('change', (e) => { state.selectedGroup = parseInt(e.target.value) || null; renderGroupDesc(); renderDomains(); });

  // 数据查看：tab 与筛选
  document.querySelectorAll('.tab').forEach((t) => {
    t.addEventListener('click', () => { state.dataTab = t.dataset.dtab; state.dataPage = 1; renderData(); });
  });
  document.querySelectorAll('.filter-bar input[data-filter]').forEach((i) => {
    i.addEventListener('input', debounce((e) => {
      if (e.target.closest('[data-view="data"]') || state.view === 'data') {
        state.filters[e.target.dataset.filter] = e.target.value;
        state.dataPage = 1;
        renderData();
      }
    }, 350));
  });

  // 遮罩点击关闭弹窗
  $('modal-root').addEventListener('click', (e) => { if (e.target === $('modal-root')) closeModal(); });

  // hash 路由
  window.addEventListener('hashchange', () => {
    const v = location.hash.slice(1);
    if (TITLES[v]) go(v);
  });
}

/* ---------- 操作辅助 ---------- */
function debounce(fn, ms) {
  let t;
  return function (...args) { clearTimeout(t); t = setTimeout(() => fn.apply(this, args), ms); };
}

async function moveAPI(id, dir) {
  const idx = state.apis.findIndex((x) => x.id == id);
  const j = idx + dir;
  if (idx < 0 || j < 0 || j >= state.apis.length) return;
  const arr = state.apis.slice();
  const tmp = arr[idx]; arr[idx] = arr[j]; arr[j] = tmp;
  await api('apikeys/reorder', { method: 'POST', body: JSON.stringify({ ids: arr.map((x) => x.id) }) });
  state.apis = arr;
  renderAPIs();
}

async function toggleAPI(id) {
  const k = state.apis.find((x) => x.id == id);
  await api('apikeys/' + id, { method: 'PUT', body: JSON.stringify({ name: k.name, enabled: !k.enabled }) });
  renderAPIs();
}

async function toggleDomain(id) {
  const ds = await api('groups/' + state.selectedGroup + '/domains');
  const d = ds.find((x) => x.id == id);
  await api('domains/' + id, { method: 'PUT', body: JSON.stringify({ domain: d.domain, enabled: !d.enabled }) });
  renderDomains();
}

async function testAPI(id) {
  toast('正在测试 Token…');
  try {
    const r = await api('apikeys/' + id + '/test', { method: 'POST' });
    toast(r.valid ? 'Token 有效' : ('Token 无效: ' + (r.error || '')), r.valid ? 'success' : 'error');
  } catch (e) { toast('测试失败: ' + e.message, 'error'); }
}

async function runTask() {
  const gid = $('run-group-select').value;
  if (!gid) { toast('请选择分组', 'error'); return; }
  try {
    const r = await api('tasks/run', { method: 'POST', body: JSON.stringify({ group_id: parseInt(gid) }) });
    toast('已开始执行「' + r.group_name + '」', 'success');
    renderTasks();
  } catch (e) { toast('执行失败: ' + e.message, 'error'); }
}

async function refreshCredits() {
  toast('正在查询账号剩余点数…');
  try {
    const r = await api('credits/refresh', { method: 'POST' });
    const accounts = r.accounts || [];
    const rows = accounts.map((a) => `<tr>
        <td>${esc(a.name)}</td>
        <td class="num">${a.error ? '-' : a.total}</td>
        <td class="num">${a.error ? '-' : a.month_remaining}</td>
        <td>${a.error ? '<span class="tag tag-bad">失败</span> ' + esc(a.error) : '<span class="tag tag-ok">正常</span>'}</td>
      </tr>`).join('');
    openModal({
      title: '账号剩余点数',
      hint: '查询时间：' + esc(r.updated_at || '-'),
      body: '<div class="credits-total">剩余总点数：<b>' + r.total + '</b>（本月剩余 ' + r.month_remaining + '）</div>'
        + '<div class="table-wrap"><table class="data-table">'
        + '<thead><tr><th>账号</th><th>剩余点数</th><th>本月剩余</th><th>状态</th></tr></thead>'
        + '<tbody>' + (rows || '<tr><td colspan="4" class="empty">无启用账号</td></tr>') + '</tbody>'
        + '</table></div>',
      actions: [{ label: '关闭', cls: 'ghost', onClick: closeModal }],
    });
    renderOverview();
  } catch (e) { toast('查询剩余点数失败: ' + e.message, 'error'); }
}

function setNavState(ok) {
  $('nav-pulse').classList.toggle('err', !ok);
  $('nav-state').textContent = ok ? '后端运行中' : '后端连接失败';
}

/* ---------- 启动 ---------- */
function startPolling() {
  if (state.pollTimer) clearInterval(state.pollTimer);
  state.pollTimer = setInterval(() => {
    if (state.view === 'dashboard' || state.view === 'tasks') renderCurrent();
  }, 4000);
}

(function init() {
  applyTheme();
  bind();
  const v = location.hash.slice(1);
  go(TITLES[v] ? v : 'dashboard');
  startPolling();
})();
