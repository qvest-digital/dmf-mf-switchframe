const token = new URLSearchParams(location.search).get('token') || localStorage.getItem('sf_token') || '';
if (token) localStorage.setItem('sf_token', token);
const authHeader = token ? { 'Authorization': 'Bearer ' + token } : {};

async function fetchJSON(path) {
  const r = await fetch(path, { headers: authHeader });
  if (!r.ok) throw new Error(path + ': ' + r.status);
  return r.json();
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

  const blocked = document.getElementById('blocked');
  const blockedItems = items.filter(i => i.status === 'blocked');
  blocked.innerHTML = blockedItems.length
    ? '<h3>Blocked</h3>' + blockedItems.map(it => `<div class="card"><div class="id">${it.id}</div><div class="meta">${it.title}</div></div>`).join('')
    : '';
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
