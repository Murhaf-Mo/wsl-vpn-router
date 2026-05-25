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
    async logTail(name, n = 300) {
        const r = await fetch(`/api/logs/${name}?tail=${n}`);
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
    el.classList.remove('ok', 'warn', 'err');
    if (kind) el.classList.add(kind);
}

function renderStatus(s) {
    state.status = s;

    // brand
    const brandDot = $('#brandDot'), brandSub = $('#brandSub');
    const tunOk = !!s.tun0_ip;
    const pacOk = !!s.pac_pid;
    const proxyOk = !!s.tinyproxy_pid;

    if (tunOk && pacOk && proxyOk) {
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

    // version
    if (s.version) $('#brandVer').textContent = 'v' + s.version;

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
    wireConfigForm();
    wireListTools();
    applyHash();
    window.addEventListener('hashchange', applyHash);
    await Promise.all([refreshStatus(), refreshList('vpn'), refreshList('direct'), refreshLog(), loadConfig()]);
    state.statusTimer = setInterval(() => {
        refreshStatus();
        refreshList('vpn');
        refreshList('direct');
    }, 4000);
    state.logTimer = setInterval(refreshLog, 2000);
}

boot();
