// vpn dashboard — vanilla JS, no build step.

const $ = (sel, root = document) => root.querySelector(sel);
const $$ = (sel, root = document) => Array.from(root.querySelectorAll(sel));

const state = {
  status: null,
  logName: 'oc',
  follow: true,
  statusTimer: null,
  logTimer: null,
};

// ---------- api ----------
const api = {
  async status() { return (await fetch('/api/status')).json(); },
  async setMode(mode) {
    const r = await fetch('/api/mode', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ mode }),
    });
    if (!r.ok) throw new Error((await r.json()).error || r.statusText);
    return r.json();
  },
  async getList(which) { return (await fetch(`/api/lists/${which}`)).json(); },
  async addToList(which, entry) {
    const r = await fetch(`/api/lists/${which}`, {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ add: [entry] }),
    });
    if (!r.ok) throw new Error((await r.json()).error || r.statusText);
    return r.json();
  },
  async removeFromList(which, entry) {
    const r = await fetch(`/api/lists/${which}/${encodeURIComponent(entry)}`, { method: 'DELETE' });
    if (!r.ok) throw new Error((await r.json()).error || r.statusText);
    return r.json();
  },
  async reload() {
    const r = await fetch('/api/reload', { method: 'POST' });
    return r.json();
  },
  async restartOpenconnect() {
    const r = await fetch('/api/restart-openconnect', { method: 'POST' });
    if (!r.ok) throw new Error((await r.json()).error || r.statusText);
    return r.json();
  },
  async logTail(name, n = 300) {
    const r = await fetch(`/api/logs/${name}?tail=${n}`);
    return r.text();
  },
};

// ---------- toast ----------
let toastTimer;
function toast(msg, kind = 'ok') {
  const t = $('#toast');
  t.textContent = msg;
  t.className = `toast show ${kind}`;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => t.classList.remove('show'), 2400);
}

// ---------- status rendering ----------
function setDot(el, kind) {
  el.classList.remove('ok', 'warn', 'err');
  if (kind) el.classList.add(kind);
}

function renderStatus(s) {
  state.status = s;

  // brand
  const brandDot = $('#brandDot'), brandSub = $('#brandSub');
  const tunOk   = !!s.tun0_ip;
  const pacOk   = !!s.pac_pid;
  const proxyOk = !!s.tinyproxy_pid;

  if (tunOk && pacOk && proxyOk) { setDot(brandDot, 'ok');   brandSub.textContent = 'connected'; }
  else if (pacOk && proxyOk)     { setDot(brandDot, 'warn'); brandSub.textContent = 'tunnel down'; }
  else if (pacOk)                { setDot(brandDot, 'warn'); brandSub.textContent = 'proxy down'; }
  else                           { setDot(brandDot, 'err');  brandSub.textContent = 'daemon down'; }

  // tunnel
  setDot($('#dotTun'), tunOk ? 'ok' : 'err');
  $('#valTun').textContent = s.tun0_ip || '— offline —';
  $('#subTun').textContent = tunOk ? `openconnect pid ${s.oc_pid}` : 'openconnect not running';

  // proxy
  setDot($('#dotProxy'), proxyOk ? 'ok' : 'err');
  $('#valProxy').textContent = proxyOk ? `${s.wsl_ip || '?'}:${s.proxy_port}` : '—';
  $('#subProxy').textContent = proxyOk ? `tinyproxy pid ${s.tinyproxy_pid}` : 'tinyproxy not running';

  // daemon
  setDot($('#dotPac'), pacOk ? 'ok' : 'err');
  $('#valPac').textContent = pacOk ? `pid ${s.pac_pid}` : '—';
  $('#subPac').textContent = pacOk ? 'serving PAC + API' : 'vpnctl.py not running';

  // mode
  $$('.mode-btn').forEach(b => b.classList.toggle('active', b.dataset.mode === s.mode));
  $('#modeHint').textContent = s.mode === 'vpn-default'
    ? 'all traffic via VPN; direct.list bypasses'
    : 'all traffic direct; vpn.list goes via VPN';

  // counts
  $('#vpnCount').textContent = (s.vpn_list?.domains || 0) + (s.vpn_list?.cidrs || 0) + (s.vpn_list?.urls || 0);
  $('#directCount').textContent = (s.direct_list?.domains || 0) + (s.direct_list?.cidrs || 0) + (s.direct_list?.urls || 0);

  // updated stamp
  $('#updatedAt').textContent = 'updated ' + new Date().toLocaleTimeString();
}

async function refreshStatus() {
  try {
    const s = await api.status();
    renderStatus(s);
  } catch (e) {
    // daemon unreachable
    const brandDot = $('#brandDot');
    setDot(brandDot, 'err');
    $('#brandSub').textContent = 'unreachable';
    ['#dotTun', '#dotProxy', '#dotPac'].forEach(s => setDot($(s), 'err'));
    ['#valTun', '#valProxy', '#valPac'].forEach(s => $(s).textContent = '—');
  }
}

// ---------- lists ----------
function listItem(entry, which) {
  const li = document.createElement('li');
  li.className = 'list-item' + (entry.startsWith('#') ? ' is-comment' : '');
  const text = document.createElement('span');
  text.textContent = entry;
  const btn = document.createElement('button');
  btn.className = 'remove';
  btn.title = 'Remove';
  btn.innerHTML = `<svg viewBox="0 0 24 24" width="14" height="14"><path fill="currentColor" d="M9 3v1H4v2h16V4h-5V3H9Zm-3 5v12a2 2 0 0 0 2 2h8a2 2 0 0 0 2-2V8H6Zm3 3h2v8H9v-8Zm4 0h2v8h-2v-8Z"/></svg>`;
  btn.addEventListener('click', async () => {
    btn.disabled = true;
    try {
      await api.removeFromList(which, entry);
      toast(`removed from ${which}.list`);
      await Promise.all([refreshList('vpn'), refreshList('direct'), refreshStatus()]);
    } catch (e) {
      toast('remove failed: ' + e.message, 'err');
      btn.disabled = false;
    }
  });
  li.append(text, btn);
  return li;
}

async function refreshList(which) {
  const ul = $(`#${which}List`);
  try {
    const data = await api.getList(which);
    ul.innerHTML = '';
    const entries = (data.entries || []).filter(l => l.trim() !== '');
    if (!entries.length) {
      const li = document.createElement('li');
      li.className = 'empty';
      li.textContent = `${which}.list is empty`;
      ul.append(li);
      return;
    }
    for (const e of entries) ul.append(listItem(e, which));
  } catch (e) {
    ul.innerHTML = `<li class="empty">failed to load: ${e.message}</li>`;
  }
}

// ---------- logs ----------
async function refreshLog() {
  const v = $('#logView');
  try {
    const text = await api.logTail(state.logName, 400);
    const wasAtBottom = v.scrollTop + v.clientHeight >= v.scrollHeight - 20;
    v.textContent = text || '(empty)';
    if (state.follow && wasAtBottom) v.scrollTop = v.scrollHeight;
  } catch (e) {
    v.textContent = 'failed to load: ' + e.message;
  }
}

function setLogTab(name) {
  state.logName = name;
  $$('.tab').forEach(t => t.classList.toggle('active', t.dataset.log === name));
  refreshLog();
}

// ---------- wiring ----------
function wireForms() {
  // add to list
  $$('.add-row').forEach(form => {
    form.addEventListener('submit', async (e) => {
      e.preventDefault();
      const which = form.dataset.list;
      const input = form.querySelector('input');
      const entry = input.value.trim();
      if (!entry) return;
      try {
        await api.addToList(which, entry);
        input.value = '';
        toast(`added to ${which}.list: ${entry}`);
        await Promise.all([refreshList(which), refreshStatus()]);
      } catch (err) {
        toast('add failed: ' + err.message, 'err');
      }
    });
  });

  // mode toggle
  $$('.mode-btn').forEach(b => {
    b.addEventListener('click', async () => {
      const m = b.dataset.mode;
      if (state.status?.mode === m) return;
      try {
        await api.setMode(m);
        toast(`mode → ${m}`);
        await refreshStatus();
      } catch (e) {
        toast('mode change failed: ' + e.message, 'err');
      }
    });
  });

  // log tabs
  $$('.tab').forEach(t => t.addEventListener('click', () => setLogTab(t.dataset.log)));
  $('#follow').addEventListener('change', e => { state.follow = e.target.checked; });

  // action bar
  $$('[data-action]').forEach(b => {
    b.addEventListener('click', async () => {
      const action = b.dataset.action;
      try {
        b.disabled = true;
        if (action === 'reload') {
          const r = await api.reload();
          toast(r.reloaded ? 'lists reloaded' : 'no changes');
        } else if (action === 'restart-oc') {
          toast('restarting openconnect…');
          const r = await api.restartOpenconnect();
          toast(r.tun0_ip ? `reconnected: tun0 ${r.tun0_ip}` : 'tunnel did not come up', r.tun0_ip ? 'ok' : 'warn');
          await refreshStatus();
        }
      } catch (e) {
        toast(`${action} failed: ${e.message}`, 'err');
      } finally {
        b.disabled = false;
      }
    });
  });
}

// ---------- boot ----------
function applyHash() {
  const m = /(?:^|[#&])log=(oc|pac|proxy)\b/.exec(location.hash || '');
  if (m) setLogTab(m[1]);
}

async function boot() {
  wireForms();
  applyHash();
  window.addEventListener('hashchange', applyHash);
  await Promise.all([refreshStatus(), refreshList('vpn'), refreshList('direct'), refreshLog()]);
  state.statusTimer = setInterval(() => { refreshStatus(); refreshList('vpn'); refreshList('direct'); }, 4000);
  state.logTimer = setInterval(refreshLog, 2000);
}
boot();
