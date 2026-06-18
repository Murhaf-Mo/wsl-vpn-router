// vpn dashboard — vanilla JS, no build step.

const $ = (sel, root = document) => root.querySelector(sel);
const $$ = (sel, root = document) => Array.from(root.querySelectorAll(sel));

const state = {
    status: null,
    page: 'overview',
    logName: 'oc',
    logLevel: '',
    follow: true,
    statusTimer: null,
    listTimer: null,
    logTimer: null,
};

// ---------- api ----------
const api = {
    async status() {
        return (await fetch('/api/status')).json();
    },
    async setMode(mode) {
        const r = await fetch('/api/mode', {
            method: 'POST', headers: {'Content-Type': 'application/json'},
            body: JSON.stringify({mode}),
        });
        if (!r.ok) throw new Error((await r.json()).error || r.statusText);
        return r.json();
    },
    async getList(which) {
        return (await fetch(`/api/lists/${which}`)).json();
    },
    async addToList(which, entry) {
        const r = await fetch(`/api/lists/${which}`, {
            method: 'POST', headers: {'Content-Type': 'application/json'},
            body: JSON.stringify({add: [entry]}),
        });
        if (!r.ok) throw new Error((await r.json()).error || r.statusText);
        return r.json();
    },
    async removeFromList(which, entry) {
        const r = await fetch(`/api/lists/${which}/${encodeURIComponent(entry)}`, {method: 'DELETE'});
        if (!r.ok) throw new Error((await r.json()).error || r.statusText);
        return r.json();
    },
    async reload() {
        const r = await fetch('/api/reload', {method: 'POST'});
        return r.json();
    },
    async restartOpenconnect() {
        const r = await fetch('/api/restart-openconnect', {method: 'POST'});
        if (!r.ok) throw new Error((await r.json()).error || r.statusText);
        return r.json();
    },
    async componentAction(name, verb) {
        const r = await fetch(`/api/components/${name}/${verb}`, {method: 'POST'});
        if (!r.ok) throw new Error((await r.json()).error || r.statusText);
        return r.json();
    },
    async logTail(name, n = 300, level = '') {
        const q = level ? `&level=${encodeURIComponent(level)}` : '';
        const r = await fetch(`/api/logs/${name}?tail=${n}${q}`);
        return r.text();
    },
    async getConfig() {
        return (await fetch('/api/config')).json();
    },
    async saveConfig(body) {
        const r = await fetch('/api/config', {
            method: 'POST', headers: {'Content-Type': 'application/json'},
            body: JSON.stringify(body),
        });
        if (!r.ok) throw new Error((await r.json()).error || r.statusText);
        return r.json();
    },
    async replaceList(which, text) {
        const r = await fetch(`/api/lists/${which}`, {
            method: 'POST', headers: {'Content-Type': 'application/json'},
            body: JSON.stringify({replace: text}),
        });
        if (!r.ok) throw new Error((await r.json()).error || r.statusText);
        return r.json();
    },
    async addManyToList(which, entries) {
        const r = await fetch(`/api/lists/${which}`, {
            method: 'POST', headers: {'Content-Type': 'application/json'},
            body: JSON.stringify({add: entries}),
        });
        if (!r.ok) throw new Error((await r.json()).error || r.statusText);
        return r.json();
    },
    async hostsFile() {
        return (await fetch('/api/hosts-file')).json();
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
    el.classList.remove('ok', 'warn', 'err', 'paused');
    if (kind) el.classList.add(kind);
}

// comp returns the per-component view, falling back to the flat pid fields when
// talking to an older daemon that doesn't send `components`.
function comp(s, name) {
    if (s.components && s.components[name]) return s.components[name];
    return {pid: name === 'openconnect' ? s.oc_pid : s.tinyproxy_pid, desired: 'up'};
}

// dotFor maps a component's (desired,pid) to a status-dot kind.
function dotFor(c, extraOk = true) {
    if (c.desired === 'down') return 'paused';
    if (c.pid > 0) return extraOk ? 'ok' : 'warn';
    return 'err';
}

function renderStatus(s) {
    state.status = s;

    // dashboard-only mode banner
    $('#liteBanner').hidden = !s.no_connect;

    const oc = comp(s, 'openconnect');
    const tp = comp(s, 'tinyproxy');
    const tunOk = !!s.tun0_ip;
    const pacOk = !!s.pac_pid;
    const proxyOk = tp.pid > 0;
    const anyPaused = oc.desired === 'down' || tp.desired === 'down';

    // brand
    const brandDot = $('#brandDot'), brandSub = $('#brandSub');
    if (anyPaused) {
        setDot(brandDot, 'paused');
        brandSub.textContent = 'paused';
    } else if (tunOk && pacOk && proxyOk) {
        setDot(brandDot, 'ok');
        brandSub.textContent = 'connected';
    } else if (pacOk && proxyOk) {
        setDot(brandDot, 'warn');
        brandSub.textContent = 'tunnel down';
    } else if (pacOk) {
        setDot(brandDot, 'warn');
        brandSub.textContent = 'proxy down';
    } else {
        setDot(brandDot, 'err');
        brandSub.textContent = 'daemon down';
    }

    // tunnel — "ok" only when tun0 actually has an IP
    setDot($('#dotTun'), oc.desired === 'down' ? 'paused' : (tunOk ? 'ok' : (oc.pid > 0 ? 'warn' : 'err')));
    $('#valTun').textContent = s.tun0_ip || (oc.desired === 'down' ? '— paused —' : '— offline —');
    $('#subTun').textContent = oc.desired === 'down' ? 'paused — held down by supervisor'
        : (oc.pid > 0 ? `openconnect pid ${oc.pid}` : 'openconnect recovering…');

    // proxy
    setDot($('#dotProxy'), dotFor(tp));
    $('#valProxy').textContent = proxyOk ? `${s.wsl_ip || '?'}:${s.proxy_port}` : (tp.desired === 'down' ? '— paused —' : '—');
    $('#subProxy').textContent = tp.desired === 'down' ? 'paused — held down by supervisor'
        : (proxyOk ? `tinyproxy pid ${tp.pid}` : 'tinyproxy recovering…');

    // daemon
    setDot($('#dotPac'), pacOk ? 'ok' : 'err');
    $('#valPac').textContent = pacOk ? `pid ${s.pac_pid}` : '—';
    $('#subPac').textContent = pacOk ? 'serving PAC + API' : 'vpnctl.py not running';

    // per-component control buttons (disable the redundant verb)
    updateCompControls('openconnect', oc);
    updateCompControls('tinyproxy', tp);

    // topology
    renderTopo(s, oc, tp);

    // mode
    $$('.mode-btn').forEach(b => b.classList.toggle('active', b.dataset.mode === s.mode));
    $('#modeHint').textContent = s.mode === 'vpn-default'
        ? 'all traffic via VPN; direct.list bypasses'
        : 'all traffic direct; vpn.list goes via VPN';

    // counts
    $('#vpnCount').textContent = (s.vpn_list?.domains || 0) + (s.vpn_list?.cidrs || 0) + (s.vpn_list?.urls || 0);
    $('#directCount').textContent = (s.direct_list?.domains || 0) + (s.direct_list?.cidrs || 0) + (s.direct_list?.urls || 0);

    // version
    if (s.version) $('#brandVer').textContent = 'v' + s.version;

    // updated stamp
    $('#updatedAt').textContent = 'updated ' + new Date().toLocaleTimeString();
}

// updateCompControls disables the redundant verb: Pause when already paused,
// Start when already running (use Restart to bounce a running component).
function updateCompControls(name, c) {
    const root = $(`.stat-controls[data-comp="${name}"]`);
    if (!root) return;
    const paused = c.desired === 'down';
    const pause = root.querySelector('[data-verb="pause"]');
    const start = root.querySelector('[data-verb="start"]');
    if (pause) pause.disabled = paused;
    if (start) start.disabled = !paused && c.pid > 0;
}

// ---------- topology ----------
function setNode(id, kind) {
    const g = $(`#node-${id}`);
    if (!g) return;
    g.classList.remove('ok', 'warn', 'err', 'paused');
    if (kind) g.classList.add(kind);
}

function setEdge(id, active) {
    const e = $(`#${id}`);
    if (e) e.classList.toggle('edge-active', !!active);
}

function renderTopo(s, oc, tp) {
    setNode('win', 'ok');
    setNode('pac', s.pac_pid ? 'ok' : 'err');
    setNode('direct', 'ok');
    setNode('proxy', dotFor(tp));
    setNode('oc', oc.desired === 'down' ? 'paused' : (oc.pid > 0 ? 'ok' : 'warn'));
    setNode('tun', s.tun0_ip ? 'ok' : (oc.desired === 'down' ? 'paused' : 'err'));
    setNode('corp', s.tun0_ip ? 'ok' : 'err');

    // Highlight the branch the default mode sends unlisted traffic down.
    const vpnDefault = s.mode !== 'direct-default';
    setEdge('e-win-pac', true);
    setEdge('e-pac-direct', !vpnDefault);
    setEdge('e-pac-proxy', vpnDefault);
    setEdge('e-proxy-oc', vpnDefault);
    setEdge('e-oc-tun', vpnDefault);
    setEdge('e-tun-corp', vpnDefault);
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
        ['win', 'pac', 'direct', 'proxy', 'oc', 'tun', 'corp'].forEach(n => setNode(n, 'err'));
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

// ---------- configuration ----------
async function loadConfig() {
    try {
        const c = await api.getConfig();
        $('#cfgHost').value = c.vpn?.host || '';
        $('#cfgUser').value = c.vpn?.user || '';
        $('#cfgGroup').value = c.vpn?.group || '';
        $('#cfgPassword').value = '';
        $('#cfgPassword').placeholder = c.vpn?.password_set ? '(unchanged)' : '(not set)';
        $('#cfgDistro').value = c.wsl?.distro || '';
        $('#cfgSudo').value = '';
        $('#cfgSudo').placeholder = c.wsl?.sudo_password_set ? '(unchanged)' : '(not set / NOPASSWD)';
        $('#cfgHttpPort').value = c.proxy?.http_port ?? 8888;
        $('#cfgPacPort').value = c.proxy?.pac_port ?? 8889;
        $('#cfgBindAddr').value = c.proxy?.bind_addr || '0.0.0.0';
        $('#cfgMode').value = c.routing?.mode || 'vpn-default';
    } catch (e) {
        $('#cfgSaveHint').textContent = 'failed to load config: ' + e.message;
    }
}

function wireConfigForm() {
    $('#cfgForm').addEventListener('submit', async (e) => {
        e.preventDefault();
        const body = {
            vpn: {
                host: $('#cfgHost').value.trim(),
                user: $('#cfgUser').value.trim(),
                group: $('#cfgGroup').value.trim(),
            },
            wsl: {distro: $('#cfgDistro').value.trim()},
            proxy: {
                http_port: parseInt($('#cfgHttpPort').value, 10),
                pac_port: parseInt($('#cfgPacPort').value, 10),
                bind_addr: $('#cfgBindAddr').value.trim(),
            },
            routing: {mode: $('#cfgMode').value},
        };
        // Only send secrets when the user typed a new value (blank = keep).
        const pw = $('#cfgPassword').value;
        if (pw) body.vpn.password = pw;
        const sudo = $('#cfgSudo').value;
        if (sudo) body.wsl.sudo_password = sudo;

        const btn = $('#cfgForm button[type=submit]');
        btn.disabled = true;
        try {
            await api.saveConfig(body);
            toast('configuration saved');
            $('#cfgSaveHint').textContent = 'saved - run `vpn restart` (or Reconnect) for non-mode changes to take effect';
            await Promise.all([loadConfig(), refreshStatus()]);
        } catch (err) {
            toast('save failed: ' + err.message, 'err');
            $('#cfgSaveHint').textContent = 'save failed: ' + err.message;
        } finally {
            btn.disabled = false;
        }
    });
}

// ---------- import / export ----------
function wireListTools() {
    // Export: download the raw list file.
    $$('[data-export]').forEach(b => {
        b.addEventListener('click', () => {
            const which = b.dataset.export;
            const a = document.createElement('a');
            a.href = `/api/lists/${which}?format=raw`;
            a.download = `${which}.list`;
            document.body.appendChild(a);
            a.click();
            a.remove();
            toast(`downloading ${which}.list`);
        });
    });
    // Import: pick a file, replace the whole list with its contents.
    $$('[data-import]').forEach(b => {
        b.addEventListener('click', () => {
            const which = b.dataset.import;
            const inp = document.createElement('input');
            inp.type = 'file';
            inp.accept = '.list,.txt,text/plain';
            inp.addEventListener('change', async () => {
                const file = inp.files?.[0];
                if (!file) return;
                if (!confirm(`Replace the entire ${which}.list with the contents of "${file.name}"?`)) return;
                try {
                    const text = await file.text();
                    const r = await api.replaceList(which, text);
                    toast(`${which}.list replaced (${r.count} entries)`);
                    await Promise.all([refreshList(which), refreshStatus()]);
                } catch (e) {
                    toast('import failed: ' + e.message, 'err');
                }
            });
            inp.click();
        });
    });
}

// ---------- logs ----------
async function refreshLog() {
    const v = $('#logView');
    try {
        const text = await api.logTail(state.logName, 400, state.logLevel);
        const wasAtBottom = v.scrollTop + v.clientHeight >= v.scrollHeight - 20;
        renderLogLines(v, text || '(empty)');
        if (state.follow && wasAtBottom) v.scrollTop = v.scrollHeight;
    } catch (e) {
        v.textContent = 'failed to load: ' + e.message;
    }
}

// renderLogLines colourises level + component tags client-side. The structured
// daemon lines look like "[HH:MM:SS] [level] [comp] msg"; other logs
// (openconnect, tinyproxy) pass through untouched.
function renderLogLines(v, text) {
    const frag = document.createDocumentFragment();
    for (const line of text.split('\n')) {
        const div = document.createElement('div');
        div.className = 'log-line';
        const m = /^\[\d\d:\d\d:\d\d\] \[(\w+)\] \[([\w-]+)\]/.exec(line);
        if (m) {
            div.classList.add('lvl-' + m[1]);
        }
        div.textContent = line;
        frag.append(div);
    }
    v.replaceChildren(frag);
}

function setLogTab(name) {
    state.logName = name;
    $$('.tab').forEach(t => t.classList.toggle('active', t.dataset.log === name));
    refreshLog();
}

// ---------- wiring ----------
function wireForms() {
    // unified add-host control with a VPN / Direct target toggle
    let addTarget = 'vpn';
    $$('.addhost-target .seg').forEach(seg => {
        seg.addEventListener('click', () => {
            addTarget = seg.dataset.target;
            $$('.addhost-target .seg').forEach(s => s.classList.toggle('active', s === seg));
        });
    });
    $('#addHostForm').addEventListener('submit', async (e) => {
        e.preventDefault();
        const input = $('#addHostInput');
        const entry = input.value.trim();
        if (!entry) return;
        try {
            await api.addToList(addTarget, entry);
            input.value = '';
            toast(`added to ${addTarget}.list: ${entry}`);
            await Promise.all([refreshList(addTarget), refreshStatus()]);
        } catch (err) {
            toast('add failed: ' + err.message, 'err');
        }
    });

    // import hostnames from the Windows hosts file into the selected list
    $('#addFromHosts').addEventListener('click', async () => {
        const btn = $('#addFromHosts');
        btn.disabled = true;
        try {
            const data = await api.hostsFile();
            const names = data.hostnames || [];
            if (!names.length) {
                toast('no hostnames found in Windows hosts file', 'warn');
                return;
            }
            const preview = names.slice(0, 8).join(', ') + (names.length > 8 ? `, +${names.length - 8} more` : '');
            if (!confirm(`Add ${names.length} hostname(s) from your Windows hosts file to ${addTarget}.list?\n\n${preview}`)) return;
            const r = await api.addManyToList(addTarget, names);
            toast(`added ${r.added.length} to ${addTarget}.list (${r.skipped.length} already present)`);
            await Promise.all([refreshList(addTarget), refreshStatus()]);
        } catch (err) {
            toast('hosts-file import failed: ' + err.message, 'err');
        } finally {
            btn.disabled = false;
        }
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
    $('#follow').addEventListener('change', e => {
        state.follow = e.target.checked;
    });
    $('#logLevel').addEventListener('change', e => {
        state.logLevel = e.target.value;
        refreshLog();
    });

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

// ---------- component controls ----------
async function runComponent(name, verb, btn) {
    if (btn) btn.disabled = true;
    try {
        const r = await api.componentAction(name, verb);
        const desc = r.desired === 'down' ? 'paused' : (r.pid ? `running (pid ${r.pid})` : 'starting…');
        toast(`${name} ${verb} → ${desc}`);
        await refreshStatus();
    } catch (e) {
        toast(`${name} ${verb} failed: ${e.message}`, 'err');
    } finally {
        if (btn) btn.disabled = false;
    }
}

// wireComponentControls binds every [data-comp][data-verb] button (status-card
// controls and the topology control bar) to a component action.
function wireComponentControls() {
    $$('[data-comp][data-verb]').forEach(b => {
        b.addEventListener('click', () => runComponent(b.dataset.comp, b.dataset.verb, b));
    });
}

// wireTopo makes the controllable nodes select their component into the
// control bar under the diagram.
function wireTopo() {
    $$('#topo .topo-node.controllable').forEach(g => {
        g.addEventListener('click', () => selectTopoComponent(g.dataset.comp));
    });
}

function selectTopoComponent(name) {
    const bar = $('#topoControls');
    if (!bar) return;
    $('#topoControlsLabel').textContent = name;
    bar.querySelectorAll('[data-verb]').forEach(b => { b.dataset.comp = name; });
    bar.hidden = false;
    // Highlight every node mapped to the selected component (oc + tun0 share one).
    $$('#topo .topo-node').forEach(g => g.classList.toggle('selected', g.dataset.comp === name));
}

// ---------- router ----------
const PAGES = ['overview', 'routing', 'config', 'logs'];

function showPage(page) {
    if (!PAGES.includes(page)) page = 'overview';
    state.page = page;
    $$('.page').forEach(p => p.classList.toggle('active', p.dataset.page === page));
    $$('.nav-tab').forEach(t => t.classList.toggle('active', t.dataset.page === page));
    // Refresh the entered page's data immediately (its poller only runs while visible).
    if (page === 'routing') { refreshList('vpn'); refreshList('direct'); }
    if (page === 'logs') refreshLog();
}

function router() {
    const h = location.hash || '';
    // Legacy deep link: #log=oc → Logs page with that tab selected.
    const mLog = /(?:^|[#&/])log=(oc|pac|proxy|events)\b/.exec(h);
    const mPage = /^#\/?(overview|routing|config|logs)\b/.exec(h);
    let page = 'overview';
    if (mPage) page = mPage[1];
    if (mLog) { page = 'logs'; setLogTab(mLog[1]); }
    showPage(page);
}

// ---------- boot ----------
async function boot() {
    wireForms();
    wireConfigForm();
    wireListTools();
    wireComponentControls();
    wireTopo();
    router();
    window.addEventListener('hashchange', router);
    await Promise.all([refreshStatus(), refreshList('vpn'), refreshList('direct'), refreshLog(), loadConfig()]);
    // Status always polls (drives nav badge + topology). List/log pollers only
    // do work while their page is visible.
    state.statusTimer = setInterval(refreshStatus, 3000);
    state.listTimer = setInterval(() => {
        if (state.page === 'routing') { refreshList('vpn'); refreshList('direct'); }
    }, 4000);
    state.logTimer = setInterval(() => {
        if (state.page === 'logs') refreshLog();
    }, 2000);
}

boot();
