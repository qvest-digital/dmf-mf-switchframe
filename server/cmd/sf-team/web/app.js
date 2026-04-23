const token = new URLSearchParams(location.search).get('token') || localStorage.getItem('sf_token') || '';
if (token) localStorage.setItem('sf_token', token);
const authHeader = token ? { 'Authorization': 'Bearer ' + token } : {};

const state = {
  drawerItem: null,        // current item id shown in drawer
  drawerOldestTs: null,    // cursor for "load older"
  headerRefetchTimer: null,
};

async function fetchJSON(path) {
  const r = await fetch(path, { headers: authHeader });
  if (!r.ok) throw new Error(path + ': ' + r.status);
  return r.json();
}

function renderMarkdown(md) {
  const lines = (md || '').split('\n');
  let html = '';
  let inList = false;
  const esc = s => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
  const inline = s => esc(s)
    .replace(/`([^`]+)`/g, '<code>$1</code>')
    .replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>')
    .replace(/\[([^\]]+)\]\(([^)]+)\)/g, '<a href="$2" target="_blank" rel="noopener">$1</a>');
  for (const raw of lines) {
    const line = raw.trimEnd();
    if (/^- /.test(line)) {
      if (!inList) { html += '<ul>'; inList = true; }
      html += '<li>' + inline(line.slice(2)) + '</li>';
      continue;
    }
    if (inList) { html += '</ul>'; inList = false; }
    if (line === '') continue;
    if (/^#### /.test(line)) { html += '<h5>' + inline(line.slice(5)) + '</h5>'; continue; }
    if (/^### /.test(line))  { html += '<h5>' + inline(line.slice(4)) + '</h5>'; continue; }
    html += '<p>' + inline(line) + '</p>';
  }
  if (inList) html += '</ul>';
  return html;
}

function splitBodySections(md) {
  const sections = [];
  const re = /^##\s+(.+)$/gm;
  let match, lastEnd = 0, lastTitle = null;
  while ((match = re.exec(md)) !== null) {
    if (lastTitle !== null) sections.push({ title: lastTitle, content: md.slice(lastEnd, match.index).trim() });
    lastTitle = match[1].trim();
    lastEnd = re.lastIndex;
  }
  if (lastTitle !== null) sections.push({ title: lastTitle, content: md.slice(lastEnd).trim() });
  return sections;
}

function fmtTs(unix) {
  return new Date(unix * 1000).toTimeString().slice(0, 5);
}

function escapeHtml(s) {
  return String(s || '').replace(/[&<>"']/g, c => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
  })[c]);
}

async function refreshAgents() {
  const list = document.getElementById('agents-list');
  const agents = await fetchJSON('/api/agents');
  list.innerHTML = '';
  for (const a of agents) {
    const li = document.createElement('li');
    const dot = a.Status === 'active' ? 'active' : 'stopped';
    li.innerHTML = `<span class="agent-dot ${dot}"></span>${a.DisplayName || a.AgentID}${a.ClaimItem ? ' → ' + a.ClaimItem : ''}`;
    li.addEventListener('click', () => openAgentDrawer(a));
    list.appendChild(li);
  }
}

async function refreshBoard() {
  const [items, claims] = await Promise.all([fetchJSON('/api/items'), fetchJSON('/api/claims')]);
  const claimed = new Set(claims.map(c => c.item_id));

  const ip = document.getElementById('in-progress');
  ip.innerHTML = '<h3>In Progress</h3>' + claims.map(c => {
    const it = items.find(i => i.id === c.item_id) || { priority: 'P3', title: '—' };
    const pct = it.progress_pct || 0;
    return `<div class="card ${(it.priority || '').toLowerCase()}">
      <div class="id">${c.item_id}</div>
      <div class="meta">${c.agent_id} · ${it.priority} · ${it.title}</div>
      <div class="meta">${c.intent || ''}</div>
      <div class="progress"><div class="progress-bar" style="width:${pct}%"></div></div>
    </div>`;
  }).join('') || '<p>(none)</p>';

  const ready = document.getElementById('ready');
  const readyItems = items.filter(i => !claimed.has(i.id) && i.status !== 'done' && i.status !== 'blocked');
  const byPri = {};
  for (const it of readyItems) (byPri[it.priority] = byPri[it.priority] || []).push(it);
  ready.innerHTML = Object.keys(byPri).sort().map(p =>
    `<h3>Ready — ${p}</h3>` + byPri[p].map(it =>
      `<div class="card ${p.toLowerCase()}"><div class="id">${it.id}</div><div class="meta">${it.title} (${it.estimate})</div></div>`
    ).join('')
  ).join('');

  ready.querySelectorAll('.card').forEach(el => {
    const id = el.querySelector('.id').textContent;
    el.addEventListener('click', () => openItemDrawer(id));
  });

  const blocked = document.getElementById('blocked');
  const blockedItems = items.filter(i => i.status === 'blocked');
  blocked.innerHTML = blockedItems.length
    ? '<h3>Blocked</h3>' + blockedItems.map(it => `<div class="card"><div class="id">${it.id}</div><div class="meta">${it.title}</div></div>`).join('')
    : '';

  const select = document.getElementById('thread-select');
  const current = select.value;
  select.innerHTML = '<option value="global">#global</option>' + claims.map(c => `<option value="${c.item_id}">#${c.item_id}</option>`).join('');
  select.value = claims.find(c => c.item_id === current) ? current : 'global';
}

function appendMessage(m) {
  const ul = document.getElementById('messages');
  const ts = new Date((m.ts || m.TS || 0) * 1000).toTimeString().slice(0, 8);
  const cls = m.kind === 'knock' ? 'knock' : '';
  const li = document.createElement('li');
  li.innerHTML = `<span class="ts">${ts}</span><span class="agent">${m.agent_id || m.AgentID}</span> <span class="${cls}">${m.body || m.Body || ''}</span>`;
  ul.appendChild(li);
  ul.scrollTop = ul.scrollHeight;
}

async function refreshMessages() {
  const ul = document.getElementById('messages');
  const thread = document.getElementById('thread-select').value;
  const messages = await fetchJSON('/api/messages?thread=' + encodeURIComponent(thread) + '&limit=50');
  ul.innerHTML = '';
  for (const m of messages.reverse()) appendMessage(m);
}

function openAgentDrawer(agent) {
  const body = document.getElementById('drawer-body');
  body.innerHTML = `
    <h3>${agent.DisplayName || agent.AgentID}</h3>
    <p>Status: ${agent.Status}</p>
    <p>Claim: ${agent.ClaimItem || '(none)'}</p>
    <p>Intent: ${agent.Intent || '—'}</p>
    <p>Files touched: ${agent.TouchCount}</p>
    <form class="knock-form" data-agent="${agent.AgentID}">
      <input placeholder="Knock message..."/>
      <button>Knock</button>
    </form>
  `;
  document.getElementById('drawer').hidden = false;
  body.querySelector('form').addEventListener('submit', async e => {
    e.preventDefault();
    const input = e.target.querySelector('input');
    await fetch('/api/knock', {
      method: 'POST',
      headers: { ...authHeader, 'Content-Type': 'application/json' },
      body: JSON.stringify({ to_agent: agent.AgentID, body: input.value }),
    });
    input.value = '';
    document.getElementById('drawer').hidden = true;
  });
}

async function openItemDrawer(itemID) {
  state.drawerItem = itemID;
  state.drawerOldestTs = null;

  const sel = document.getElementById('thread-select');
  if (sel.value !== 'global') { sel.value = 'global'; refreshMessages(); }

  const body = document.getElementById('drawer-body');
  body.innerHTML = '<p>Loading…</p>';
  document.getElementById('drawer').hidden = false;

  try {
    const [it, activity] = await Promise.all([
      fetchJSON('/api/items/' + encodeURIComponent(itemID)),
      fetchJSON('/api/items/' + encodeURIComponent(itemID) + '/activity?limit=50'),
    ]);
    renderDrawer(it, activity);
  } catch (err) {
    body.innerHTML = '<p>Error: ' + escapeHtml(err.message) + '</p>';
  }
}

function renderDrawer(it, activity) {
  const body = document.getElementById('drawer-body');
  const pri = (it.priority || '').toLowerCase();
  const claimHtml = it.current_claim
    ? `<div class="claim-banner">● <strong>${escapeHtml(it.current_claim.agent_id)}</strong> — ${escapeHtml(it.current_claim.intent || '(no intent)')}</div>`
    : '<div class="claim-banner" style="opacity:0.6">unclaimed</div>';

  const acHtml = (it.ac || []).map(a =>
    `<li class="${a.checked ? 'checked' : ''}"><span class="glyph">${a.checked ? '☒' : '☐'}</span>${escapeHtml(a.text).replace(/\n/g, '<br>')}</li>`
  ).join('') || '<li style="opacity:0.6">(no acceptance criteria)</li>';

  const depsHtml =
    (it.blocked_by || []).map(id => `<span>blocked by</span> <span class="chip" data-id="${escapeHtml(id)}">${escapeHtml(id)}</span>`).join(' ') +
    (it.relates_to || []).map(id => `<span>relates to</span> <span class="chip" data-id="${escapeHtml(id)}">${escapeHtml(id)}</span>`).join(' ');

  const sections = splitBodySections(it.body_markdown || '');
  const sectionsHtml = sections.map((s, i) =>
    `<details class="body-section"${i === 0 ? ' open' : ''}><summary>${escapeHtml(s.title)}</summary><div class="md">${renderMarkdown(s.content)}</div></details>`
  ).join('');

  body.innerHTML = `
    <h3>${escapeHtml(it.id)} · ${escapeHtml(it.title || '')}</h3>
    <div class="tags">
      <span class="priority ${pri}">${escapeHtml(it.priority || '')}</span> ·
      ${escapeHtml(it.type || '')} · ${escapeHtml(it.area || '')} · est ${escapeHtml(it.estimate || '—')} · risk ${escapeHtml(it.risk || '—')}
    </div>
    ${claimHtml}
    <div class="progress"><div class="progress-bar" style="width:${it.progress_pct || 0}%"></div></div>
    ${depsHtml ? `<div class="deps">${depsHtml}</div>` : ''}
    <h4>Acceptance (${it.ac_checked}/${it.ac_total})</h4>
    <ul class="ac">${acHtml}</ul>
    <h4>Body</h4>
    ${sectionsHtml}
    <h4>Activity</h4>
    <div class="activity" id="drawer-activity"></div>
    <button class="load-older" id="drawer-load-older">Load older</button>
    <form class="drawer-chat" data-thread="${escapeHtml(it.id)}">
      <input placeholder="Message #${escapeHtml(it.id)}…" />
      <button>Send</button>
    </form>
  `;

  state.drawerOldestTs = null;
  renderActivity(activity);

  body.querySelectorAll('.deps .chip').forEach(c => {
    c.addEventListener('click', () => openItemDrawer(c.dataset.id));
  });
  body.querySelector('#drawer-load-older').addEventListener('click', loadOlderActivity);
  body.querySelector('form.drawer-chat').addEventListener('submit', async e => {
    e.preventDefault();
    const input = e.target.querySelector('input');
    const msg = input.value.trim();
    if (!msg) return;
    await fetch('/api/say', {
      method: 'POST',
      headers: { ...authHeader, 'Content-Type': 'application/json' },
      body: JSON.stringify({ thread: e.target.dataset.thread, body: msg }),
    });
    input.value = '';
  });
}

function renderActivity(events) {
  const list = document.getElementById('drawer-activity');
  if (!list) return;
  for (const e of events) {
    list.appendChild(activityRow(e));
    if (state.drawerOldestTs === null || e.ts < state.drawerOldestTs) state.drawerOldestTs = e.ts;
  }
}

function activityRow(e) {
  const div = document.createElement('div');
  div.className = 'row';
  const kindClass = e.kind === 'say' ? 'say chat' : e.kind;
  const detail = {
    claim: e.detail ? '"' + e.detail + '"' : '',
    release: e.outcome || '',
    progress: (e.pct != null ? e.pct + '% ' : '') + (e.detail || ''),
    touch: e.path || '',
    untouch: e.path || '',
    say: e.body || '',
    chat: e.body || '',
    knock: e.body || '',
  }[e.kind] || e.body || e.detail || '';
  div.innerHTML = `<span class="ts">${fmtTs(e.ts)}</span><span class="agent">${escapeHtml(e.agent_id)}</span><span class="kind ${kindClass}">${escapeHtml(e.kind)}</span><span>${escapeHtml(detail)}</span>`;
  return div;
}

async function loadOlderActivity() {
  if (!state.drawerItem || !state.drawerOldestTs) return;
  const more = await fetchJSON(
    '/api/items/' + encodeURIComponent(state.drawerItem) +
    '/activity?limit=50&before=' + state.drawerOldestTs
  );
  renderActivity(more);
}

document.getElementById('drawer-close').addEventListener('click', () => {
  document.getElementById('drawer').hidden = true;
});

function connectSSE() {
  const tokenParam = token ? '?token=' + encodeURIComponent(token) : '';
  const es = new EventSource('/api/events' + tokenParam);
  es.addEventListener('claim', () => { refreshBoard(); refreshAgents(); });
  es.addEventListener('release', () => { refreshBoard(); refreshAgents(); });
  es.addEventListener('done', () => { refreshBoard(); refreshAgents(); });
  es.addEventListener('blocked', () => { refreshBoard(); });
  es.addEventListener('message', e => {
    const data = JSON.parse(e.data);
    const thread = document.getElementById('thread-select').value;
    if (data.payload.thread === thread) appendMessage(data.payload);
  });
  es.addEventListener('touch', () => { refreshAgents(); });
  es.addEventListener('progress', () => { refreshBoard(); });
  es.addEventListener('item', () => { refreshBoard(); });
  es.onerror = () => console.warn('SSE disconnected; will retry');
}

document.getElementById('thread-select').addEventListener('change', refreshMessages);
document.getElementById('compose').addEventListener('submit', async e => {
  e.preventDefault();
  const input = document.getElementById('compose-input');
  const body = input.value.trim();
  if (!body) return;
  await fetch('/api/say', {
    method: 'POST',
    headers: { ...authHeader, 'Content-Type': 'application/json' },
    body: JSON.stringify({ thread: document.getElementById('thread-select').value, body }),
  });
  input.value = '';
});

refreshAgents();
refreshBoard();
refreshMessages();
connectSSE();
