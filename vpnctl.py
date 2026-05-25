#!/usr/bin/env python3
"""
vpnctl.py — runs inside WSL. Brings up openconnect + tinyproxy and serves a
hot-reloading PAC file plus a JSON REST API and a dashboard SPA on :PAC_PORT.
Watches vpn.list / direct.list for file-mtime changes and reloads transparently.

Invoked by vpn.exe (the Windows-side controller) via `wsl -d <distro> ...`.
The dashboard lives at /, the REST API under /api/, and the legacy PAC URL at
/proxy.pac.

Single file, stdlib only (Python 3.8 compatible).
"""
import json
import os
import re
import shlex
import signal
import subprocess
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

# INSTALL_DIR holds read-only assets shipped by the MSI: vpnctl.py itself and
# static/ (the dashboard). It's always the directory containing this script.
INSTALL_DIR = os.path.dirname(os.path.abspath(__file__))
STATIC_DIR = os.path.join(INSTALL_DIR, "static")


def _resolve_data_dir():
    """Pick the writable per-user directory holding config + lists + runtime/.

    Resolution order (first hit wins):
      1. `--data-dir <path>` on argv  (how vpn.exe launches us)
      2. $VPN_DATA_DIR env var
      3. $XDG_RUNTIME_DIR/wsl-vpn-router  (not used by Windows-side launcher
         but lets the daemon be run by hand inside WSL for debugging)
      4. INSTALL_DIR  (pre-1.0 single-directory layout; what dev-mode
         vpn.exe falls back to when no MSI is installed)
    """
    argv = sys.argv
    for i, a in enumerate(argv):
        if a == "--data-dir" and i + 1 < len(argv):
            return os.path.abspath(argv[i + 1])
        if a.startswith("--data-dir="):
            return os.path.abspath(a.split("=", 1)[1])
    if os.environ.get("VPN_DATA_DIR"):
        return os.path.abspath(os.environ["VPN_DATA_DIR"])
    return INSTALL_DIR


DATA_DIR = _resolve_data_dir()
os.makedirs(DATA_DIR, exist_ok=True)

CONFIG_PATH = os.path.join(DATA_DIR, "config.toml")
DIRECT_PATH = os.path.join(DATA_DIR, "direct.list")
VPN_LIST_PATH = os.path.join(DATA_DIR, "vpn.list")

# Runtime artifacts (logs, pid files, generated tinyproxy.conf) live under
# RUNTIME_DIR. Created here so cleanup paths exist before the daemon's first run.
RUNTIME_DIR = os.path.join(DATA_DIR, "runtime")
os.makedirs(RUNTIME_DIR, exist_ok=True)

TINYPROXY_CONF = f"{RUNTIME_DIR}/tinyproxy.conf"
OC_PID = f"{RUNTIME_DIR}/oc.pid"
OC_LOG = f"{RUNTIME_DIR}/oc.log"
TP_PID = "/tmp/tinyproxy.pid"
TP_LOG = "/tmp/tinyproxy.log"
TP_PID_MIRROR = f"{RUNTIME_DIR}/proxy.pid"
TP_LOG_MIRROR = f"{RUNTIME_DIR}/proxy.log"
PAC_PID = f"{RUNTIME_DIR}/pac.pid"
PAC_LOG = f"{RUNTIME_DIR}/pac.log"

LOG_PATHS = {"oc": OC_LOG, "pac": PAC_LOG, "proxy": TP_LOG_MIRROR}

# Module-level lock to serialize list-file writes across HTTP threads.
_lists_write_lock = threading.Lock()


def log(msg):
    line = f"[{time.strftime('%H:%M:%S')}] {msg}\n"
    sys.stdout.write(line)
    sys.stdout.flush()


# ---------- Minimal TOML parser (handles our simple config) ----------
def parse_toml(text):
    cfg, section = {}, None
    for raw in text.splitlines():
        line = raw.split("#", 1)[0].strip()
        if not line:
            continue
        if line.startswith("[") and line.endswith("]"):
            section = line[1:-1].strip()
            cfg.setdefault(section, {})
            continue
        if "=" not in line:
            continue
        k, v = line.split("=", 1)
        k, v = k.strip(), v.strip()
        if v.startswith('"') and v.endswith('"'):
            v = v[1:-1]
        elif v.lower() in ("true", "false"):
            v = v.lower() == "true"
        else:
            try:
                v = int(v)
            except ValueError:
                pass
        if section:
            cfg[section][k] = v
    return cfg


def load_config():
    with open(CONFIG_PATH) as f:
        return parse_toml(f.read())


# ---------- routing list parsing ----------
class Rules:
    def __init__(self, path, label):
        self.path = path
        self.label = label
        self.domains = []   # list[str]   shell-pattern host
        self.cidrs = []     # list[(net_str, mask_str)]  for PAC isInNet
        self.urls = []      # list[str]   shell-pattern url
        self.mtime = 0
        self.lock = threading.Lock()
        self.last_reload = 0

    @staticmethod
    def cidr_to_mask(prefix):
        bits = (0xFFFFFFFF << (32 - prefix)) & 0xFFFFFFFF
        return ".".join(str((bits >> (8 * (3 - i))) & 0xFF) for i in range(4))

    def parse(self, text):
        domains, cidrs, urls = [], [], []
        for raw in text.splitlines():
            line = raw.split("#", 1)[0].strip()
            if not line:
                continue
            if "://" in line or line.endswith("/*") or "/*" in line:
                urls.append(line)
                continue
            m = re.match(r"^(\d+\.\d+\.\d+\.\d+)(?:/(\d+))?$", line)
            if m:
                ip = m.group(1)
                prefix = int(m.group(2)) if m.group(2) else 32
                cidrs.append((ip, self.cidr_to_mask(prefix)))
                continue
            if ":" in line:  # ipv6 - skip in PAC
                continue
            domains.append(line)
        return domains, cidrs, urls

    def reload_if_changed(self):
        try:
            mt = os.stat(self.path).st_mtime
        except FileNotFoundError:
            with self.lock:
                if self.mtime != 0:
                    self.domains, self.cidrs, self.urls = [], [], []
                    self.mtime = 0
            return False
        if mt == self.mtime:
            return False
        with open(self.path) as f:
            text = f.read()
        d, c, u = self.parse(text)
        with self.lock:
            self.domains, self.cidrs, self.urls = d, c, u
            self.mtime = mt
            self.last_reload = time.time()
        log(f"{self.label} reloaded: {len(d)} domains, {len(c)} cidrs, {len(u)} urls")
        return True

    def snapshot(self):
        with self.lock:
            return list(self.domains), list(self.cidrs), list(self.urls)


DIRECT_RULES = Rules(DIRECT_PATH, "direct.list")
VPN_RULES = Rules(VPN_LIST_PATH, "vpn.list")


def get_eth0_ip():
    """Returns the WSL VM's eth0 IPv4 (reachable from Windows host)."""
    try:
        out = subprocess.check_output(
            ["bash", "-c", "ip -4 -br addr show eth0 | awk '{print $3}' | cut -d/ -f1"],
            text=True,
        ).strip()
        return out
    except Exception:
        return "127.0.0.1"


def _emit_rules(parts, rules, action, need_dns_block):
    domains, cidrs, urls = rules.snapshot()
    for u in urls:
        parts.append(f'  if (shExpMatch(url, {json.dumps(u)})) return {action};')
    for d in domains:
        d_l = d.lower()
        if "*" in d_l or "?" in d_l:
            parts.append(f'  if (shExpMatch(host, {json.dumps(d_l)})) return {action};')
        else:
            parts.append(f'  if (host == {json.dumps(d_l)} || dnsDomainIs(host, {json.dumps("." + d_l)})) return {action};')
    if cidrs:
        if not need_dns_block["emitted"]:
            parts.append("  var ip = null;")
            parts.append("  try { ip = dnsResolve(host); } catch(e) {}")
            need_dns_block["emitted"] = True
        parts.append("  if (ip) {")
        for net, mask in cidrs:
            parts.append(f'    if (isInNet(ip, "{net}", "{mask}")) return {action};')
        parts.append("  }")


def build_pac(proxy_host, proxy_port, mode):
    proxy_action  = f'"PROXY {proxy_host}:{proxy_port}"'
    direct_action = '"DIRECT"'
    default_action = direct_action if mode == "direct-default" else proxy_action

    parts = []
    parts.append(f"// mode={mode}")
    parts.append("function FindProxyForURL(url, host) {")
    parts.append("  host = host.toLowerCase();")
    need_dns = {"emitted": False}
    # Precedence: vpn.list (force VPN) wins over direct.list (force home),
    # because corporate hosts pinned to VPN must never accidentally leak DIRECT.
    _emit_rules(parts, VPN_RULES,    proxy_action,  need_dns)
    _emit_rules(parts, DIRECT_RULES, direct_action, need_dns)
    parts.append(f"  return {default_action};")
    parts.append("}")
    return "\n".join(parts) + "\n"


def current_mode():
    """Re-read config.toml each call so /proxy.pac reflects live mode."""
    try:
        with open(CONFIG_PATH) as f:
            cfg = parse_toml(f.read())
        m = cfg.get("routing", {}).get("mode", "vpn-default")
        return m if m in ("vpn-default", "direct-default") else "vpn-default"
    except Exception:
        return "vpn-default"


# ---------- Process management ----------
def is_alive(pid_file):
    try:
        with open(pid_file) as f:
            pid = int(f.read().strip())
    except (FileNotFoundError, ValueError):
        return 0
    try:
        os.kill(pid, 0)
        return pid
    except ProcessLookupError:
        return 0
    except PermissionError:
        # process exists but we can't signal it (root-owned). Still "alive".
        return pid


def kill_pid_file(pid_file, with_sudo=False, sudo_pw=""):
    pid = is_alive(pid_file)
    if not pid:
        return
    try:
        if with_sudo:
            subprocess.run(
                ["sudo", "-S", "kill", str(pid)],
                input=sudo_pw + "\n", text=True, check=False,
                stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
            )
        else:
            os.kill(pid, signal.SIGTERM)
    except Exception as e:
        log(f"kill {pid} failed: {e}")
    try:
        os.remove(pid_file)
    except FileNotFoundError:
        pass


def _eth0_gateway():
    """Heuristic: WSL's eth0 default gateway is the .1 host of the eth0
    subnet (e.g., 172.23.58.50/20 → 172.23.48.1)."""
    try:
        out = subprocess.check_output(
            ["ip", "-4", "-br", "addr", "show", "eth0"], text=True
        ).split()
        cidr = out[2]  # "172.23.58.50/20"
        import ipaddress
        net = ipaddress.IPv4Network(cidr, strict=False)
        return str(net.network_address + 1)
    except Exception:
        return None


def ensure_pre_vpn_network(sudo_pw):
    """openconnect's vpnc-script rewrites /etc/resolv.conf, replaces the
    default route, and reconfigures systemd-resolved. If openconnect dies
    uncleanly (DPD, killed process, etc.) none of those get restored — so
    on the next connect attempt DNS resolves against unreachable corp DNS,
    or fails entirely with 'Network is unreachable' because the default
    route is gone. Detect both failure modes and fix them in place."""
    fixes = []

    # 1. resolv.conf
    try:
        with open("/etc/resolv.conf") as f:
            head = f.read(200)
    except FileNotFoundError:
        head = ""
    resolv_dirty = "@VPNC_GENERATED@" in head

    # 2. default route
    try:
        rt = subprocess.check_output(
            ["ip", "-4", "route", "show", "default"], text=True
        ).strip()
    except Exception:
        rt = ""
    default_missing = not rt

    if not resolv_dirty and not default_missing:
        return

    script_parts = []
    if resolv_dirty:
        fallback = "nameserver 8.8.8.8\nnameserver 1.1.1.1\n"
        script_parts.append(f"printf {shlex.quote(fallback)} > /etc/resolv.conf")
        script_parts.append("resolvectl flush-caches 2>/dev/null")
        script_parts.append("resolvectl revert eth0 2>/dev/null")
        script_parts.append("systemctl restart systemd-resolved 2>/dev/null")
        fixes.append("resolv.conf+resolved")
    if default_missing:
        gw = _eth0_gateway()
        if gw:
            script_parts.append(f"ip route add default via {gw} dev eth0 2>/dev/null")
            fixes.append(f"default route via {gw}")
        else:
            fixes.append("default route missing (could not derive gateway)")
    script_parts.append("true")
    script = "; ".join(script_parts)

    if sudo_pw:
        full = f"echo {shlex.quote(sudo_pw)} | sudo -S bash -c {shlex.quote(script)}"
    else:
        full = f"sudo bash -c {shlex.quote(script)}"
    subprocess.run(["bash", "-c", full], check=False,
                   stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    log("pre-vpn fixups: " + ", ".join(fixes))


def start_openconnect(cfg):
    if is_alive(OC_PID):
        log(f"openconnect already running (pid {is_alive(OC_PID)})")
        return
    vpn = cfg["vpn"]
    sudo_pw = cfg.get("wsl", {}).get("sudo_password", "")
    ensure_pre_vpn_network(sudo_pw)
    user = vpn["user"]
    host = vpn["host"]
    password = vpn["password"]
    group = vpn.get("group", "")
    extra = []
    if group:
        extra += ["--authgroup", group]
    oc_cmd = (
        "echo {pw} | setsid nohup openconnect "
        "--user={user} --passwd-on-stdin --background "
        "--pid-file={pid} {extra} {host} "
        ">{log} 2>&1"
    ).format(
        pw=shlex.quote(password),
        user=shlex.quote(user),
        pid=shlex.quote(OC_PID),
        extra=" ".join(shlex.quote(x) for x in extra),
        host=shlex.quote(host),
        log=shlex.quote(OC_LOG),
    )
    if sudo_pw:
        full = f"echo {shlex.quote(sudo_pw)} | sudo -S bash -c {shlex.quote(oc_cmd)}"
    else:
        full = f"sudo bash -c {shlex.quote(oc_cmd)}"
    log("starting openconnect")
    subprocess.run(["bash", "-c", full], check=False)
    # wait up to 15s for tun0 to appear
    for _ in range(30):
        if os.path.exists("/sys/class/net/tun0"):
            log("tun0 is up")
            ensure_mss_clamp(sudo_pw)
            return
        time.sleep(0.5)
    log("WARNING: tun0 not detected after 15s — check oc.log")


def ensure_mss_clamp(sudo_pw):
    """Clamp TCP MSS to tun0's PMTU. Without this, TLS ClientHello packets
    from proxied clients (Windows MTU 1500) get silently dropped when they
    cross the openconnect tunnel (PMTU often ~1358), so TLS handshakes hang
    until they time out."""
    rules = [
        "OUTPUT -o tun0 -p tcp --tcp-flags SYN,RST SYN -j TCPMSS --clamp-mss-to-pmtu",
        "FORWARD -o tun0 -p tcp --tcp-flags SYN,RST SYN -j TCPMSS --clamp-mss-to-pmtu",
    ]
    cmds = []
    for r in rules:
        # idempotent: delete-if-present then add
        cmds.append(f"iptables -t mangle -D {r} 2>/dev/null ; iptables -t mangle -A {r}")
    script = " ; ".join(cmds)
    if sudo_pw:
        full = f"echo {shlex.quote(sudo_pw)} | sudo -S bash -c {shlex.quote(script)}"
    else:
        full = f"sudo bash -c {shlex.quote(script)}"
    r = subprocess.run(["bash", "-c", full], capture_output=True, text=True)
    if r.returncode == 0:
        log("MSS clamp on tun0 installed")
    else:
        log(f"MSS clamp failed: {r.stderr.strip()}")


def write_tinyproxy_conf(cfg):
    port = cfg["proxy"]["http_port"]
    bind = cfg["proxy"]["bind_addr"]
    conf = f"""# generated by vpnctl.py - do not edit by hand
# Stay as root: avoids drvfs permission edge-cases under privilege drop.
User root
Group root
Port {port}
Listen {bind}
Timeout 600
DefaultErrorFile "/usr/share/tinyproxy/default.html"
StatFile "/usr/share/tinyproxy/stats.html"
# Log+pid on a tmpfs path that tinyproxy can always open;
# we tail-copy to /mnt/c/.../proxy.log from the PAC daemon for visibility.
LogFile "/tmp/tinyproxy.log"
LogLevel Info
PidFile "/tmp/tinyproxy.pid"
MaxClients 100
StartServers 5
MinSpareServers 5
MaxSpareServers 20
Allow 127.0.0.1
Allow 10.0.0.0/8
Allow 172.16.0.0/12
Allow 192.168.0.0/16
ViaProxyName "vpnctl"
DisableViaHeader Yes
# Allow CONNECT to all common TLS ports
ConnectPort 443
ConnectPort 563
ConnectPort 8443
"""
    with open(TINYPROXY_CONF, "w") as f:
        f.write(conf)


def tinyproxy_pid():
    try:
        out = subprocess.check_output(["pgrep", "-x", "tinyproxy"], text=True).strip()
        return int(out.splitlines()[0]) if out else 0
    except (subprocess.CalledProcessError, ValueError):
        return 0


def start_tinyproxy(cfg):
    if tinyproxy_pid():
        log(f"tinyproxy already running (pid {tinyproxy_pid()})")
        return
    write_tinyproxy_conf(cfg)
    sudo_pw = cfg.get("wsl", {}).get("sudo_password", "")
    cmd = f"tinyproxy -c {shlex.quote(TINYPROXY_CONF)}"
    if sudo_pw:
        full = f"echo {shlex.quote(sudo_pw)} | sudo -S {cmd}"
    else:
        full = f"sudo {cmd}"
    log("starting tinyproxy")
    r = subprocess.run(["bash", "-c", full], capture_output=True, text=True)
    if r.returncode != 0:
        log(f"tinyproxy failed (rc={r.returncode}): {r.stderr.strip() or r.stdout.strip()}")
        return
    time.sleep(0.7)
    pid = tinyproxy_pid()
    log(f"tinyproxy pid {pid}")
    if pid:
        try:
            with open(TP_PID_MIRROR, "w") as f:
                f.write(str(pid))
        except Exception:
            pass


def log_mirror_thread():
    """Continuously copy /tmp/tinyproxy.log -> Desktop\\vpn\\proxy.log."""
    last_size = 0
    while True:
        try:
            if os.path.exists(TP_LOG):
                sz = os.path.getsize(TP_LOG)
                if sz != last_size:
                    with open(TP_LOG, "rb") as src, open(TP_LOG_MIRROR, "wb") as dst:
                        dst.write(src.read())
                    last_size = sz
        except Exception:
            pass
        time.sleep(2)


# ---------- Mutation helpers ----------
def set_config_mode(new_mode):
    """Edit routing.mode in config.toml in place."""
    if new_mode not in ("vpn-default", "direct-default"):
        raise ValueError("mode must be 'vpn-default' or 'direct-default'")
    with open(CONFIG_PATH) as f:
        text = f.read()
    if re.search(r"(?ms)^\s*\[routing\][^\[]*?^\s*mode\s*=", text):
        text = re.sub(r"(?m)^\s*mode\s*=.*$", f'mode = "{new_mode}"', text)
    elif re.search(r"(?m)^\s*\[routing\]", text):
        text = re.sub(r"(?m)^\s*\[routing\].*$",
                      f'[routing]\nmode = "{new_mode}"', text)
    else:
        text = text.rstrip() + f'\n\n[routing]\nmode = "{new_mode}"\n'
    with open(CONFIG_PATH, "w") as f:
        f.write(text)


def read_list_lines(path):
    try:
        with open(path) as f:
            return [ln.rstrip("\n") for ln in f.readlines()]
    except FileNotFoundError:
        return []


def write_list_lines(path, lines):
    # Always end with a newline; collapse multiple trailing blanks.
    body = "\n".join(ln.rstrip() for ln in lines).rstrip() + "\n"
    with open(path, "w") as f:
        f.write(body)


def apply_list_edits(path, add=None, remove=None):
    """Apply add/remove edits to a list file. Adds are appended if not present
    (case-insensitive). Removes drop any matching lines (case-insensitive,
    ignoring inline comments). Returns (added, removed, skipped) tuples."""
    add = add or []
    remove = remove or []
    with _lists_write_lock:
        lines = read_list_lines(path)
        existing_norm = set()
        for ln in lines:
            stripped = ln.split("#", 1)[0].strip().lower()
            if stripped:
                existing_norm.add(stripped)

        added, skipped = [], []
        for entry in add:
            e = entry.strip()
            if not e:
                continue
            if e.lower() in existing_norm:
                skipped.append(e)
            else:
                lines.append(e)
                existing_norm.add(e.lower())
                added.append(e)

        removed = []
        if remove:
            rm_set = {r.strip().lower() for r in remove if r.strip()}
            kept = []
            for ln in lines:
                content = ln.split("#", 1)[0].strip().lower()
                if content and content in rm_set:
                    removed.append(ln.strip())
                else:
                    kept.append(ln)
            lines = kept

        write_list_lines(path, lines)
        return added, removed, skipped


def read_log_tail(path, n):
    if not os.path.exists(path):
        return ""
    try:
        with open(path, "rb") as f:
            f.seek(0, os.SEEK_END)
            size = f.tell()
            chunk = min(size, max(n * 250, 8192))
            f.seek(max(0, size - chunk))
            data = f.read().decode("utf-8", errors="replace")
        lines = data.splitlines()
        return "\n".join(lines[-n:]) + ("\n" if lines else "")
    except Exception as e:
        return f"<error reading {path}: {e}>\n"


def get_tun0_ip():
    try:
        out = subprocess.check_output(
            ["bash", "-c", "ip -4 -br addr show tun0 2>/dev/null | awk '{print $3}'"],
            text=True,
        ).strip()
        return out
    except Exception:
        return ""


def build_status():
    VPN_RULES.reload_if_changed()
    DIRECT_RULES.reload_if_changed()
    vd, vc, vu = VPN_RULES.snapshot()
    dd, dc, du = DIRECT_RULES.snapshot()
    return {
        "mode": current_mode(),
        "oc_pid": is_alive(OC_PID),
        "tinyproxy_pid": tinyproxy_pid(),
        "pac_pid": os.getpid(),
        "tun0_ip": get_tun0_ip(),
        "wsl_ip": PacHandler.proxy_host,
        "proxy_port": PacHandler.proxy_port,
        "vpn_list":    {"domains": len(vd), "cidrs": len(vc), "urls": len(vu),
                        "mtime": VPN_RULES.mtime, "last_reload": VPN_RULES.last_reload},
        "direct_list": {"domains": len(dd), "cidrs": len(dc), "urls": len(du),
                        "mtime": DIRECT_RULES.mtime, "last_reload": DIRECT_RULES.last_reload},
    }


# ---------- HTTP server ----------
_STATIC_TYPES = {
    ".html": "text/html; charset=utf-8",
    ".js":   "application/javascript; charset=utf-8",
    ".css":  "text/css; charset=utf-8",
    ".svg":  "image/svg+xml",
    ".png":  "image/png",
    ".ico":  "image/x-icon",
    ".json": "application/json",
    ".woff2": "font/woff2",
}


class PacHandler(BaseHTTPRequestHandler):
    proxy_host = "127.0.0.1"
    proxy_port = 8888

    def log_message(self, format, *args):  # noqa: A002 - matches base class
        log("http " + (format % args))

    # ---- response helpers ----
    def _send(self, code, ctype, body, *, cache=False):
        b = body.encode("utf-8") if isinstance(body, str) else body
        self.send_response(code)
        self.send_header("Content-Type", ctype)
        if not cache:
            self.send_header("Cache-Control", "no-cache, no-store, must-revalidate")
            self.send_header("Pragma", "no-cache")
        # CORS: dashboard might be opened via http://localhost too if a tray
        # forwarder is added later; harmless for a LAN-bound daemon.
        self.send_header("Access-Control-Allow-Origin", "*")
        self.send_header("Access-Control-Allow-Methods", "GET,POST,DELETE,OPTIONS")
        self.send_header("Access-Control-Allow-Headers", "Content-Type")
        self.send_header("Content-Length", str(len(b)))
        self.end_headers()
        if self.command != "HEAD":
            self.wfile.write(b)

    def _json(self, obj, code=200):
        self._send(code, "application/json", json.dumps(obj, indent=2))

    def _err(self, code, msg):
        self._json({"error": msg}, code=code)

    def _read_json(self):
        n = int(self.headers.get("Content-Length") or 0)
        if not n:
            return {}
        try:
            return json.loads(self.rfile.read(n).decode("utf-8"))
        except Exception as e:
            raise ValueError(f"invalid JSON: {e}")

    # ---- static dashboard ----
    def _serve_static(self, rel):
        # Default index for "/" or "" → index.html.
        if rel in ("", "/"):
            rel = "index.html"
        rel = rel.lstrip("/")
        # No directory traversal.
        full = os.path.normpath(os.path.join(STATIC_DIR, rel))
        if not full.startswith(STATIC_DIR + os.sep) and full != STATIC_DIR:
            return self._err(403, "forbidden")
        if not os.path.isfile(full):
            return self._err(404, "not found")
        ext = os.path.splitext(full)[1].lower()
        ctype = _STATIC_TYPES.get(ext, "application/octet-stream")
        with open(full, "rb") as f:
            data = f.read()
        # Cache assets but never index.html (so dashboard updates land instantly).
        cache = ext in (".css", ".js", ".svg", ".png", ".ico", ".woff2")
        self._send(200, ctype, data, cache=cache)

    # ---- routing ----
    def do_OPTIONS(self):  # noqa: N802
        self._send(204, "text/plain", "")

    def do_GET(self):  # noqa: N802
        path = self.path.split("?", 1)[0]
        qs = {}
        if "?" in self.path:
            from urllib.parse import parse_qs
            qs = parse_qs(self.path.split("?", 1)[1])

        # PAC
        if path in ("/proxy.pac", "/wpad.dat"):
            VPN_RULES.reload_if_changed()
            DIRECT_RULES.reload_if_changed()
            pac = build_pac(self.proxy_host, self.proxy_port, current_mode())
            return self._send(200, "application/x-ns-proxy-autoconfig", pac)

        # API
        if path in ("/api/status", "/status"):
            return self._json(build_status())
        if path == "/api/mode":
            return self._json({"mode": current_mode()})
        if path.startswith("/api/lists/"):
            which = path.split("/")[-1]
            if which not in ("vpn", "direct"):
                return self._err(404, "unknown list")
            target = VPN_LIST_PATH if which == "vpn" else DIRECT_PATH
            entries = [ln for ln in read_list_lines(target) if ln.strip()]
            return self._json({"list": which, "entries": entries})
        if path.startswith("/api/logs/"):
            name = path.split("/")[-1]
            if name not in LOG_PATHS:
                return self._err(404, "unknown log")
            tail = 200
            try:
                tail = max(1, min(2000, int(qs.get("tail", ["200"])[0])))
            except ValueError:
                pass
            return self._send(200, "text/plain; charset=utf-8",
                              read_log_tail(LOG_PATHS[name], tail))

        # Dashboard / static
        if path == "/" or path.startswith("/static/") or path in ("/favicon.ico",):
            rel = "" if path in ("/", "") else path[len("/static/"):] if path.startswith("/static/") else path[1:]
            return self._serve_static(rel)

        # Backwards-compat aliases
        if path == "/reload":
            c1 = VPN_RULES.reload_if_changed()
            c2 = DIRECT_RULES.reload_if_changed()
            return self._send(200, "text/plain",
                              "reloaded\n" if (c1 or c2) else "no change\n")

        self._err(404, "not found")

    def do_POST(self):  # noqa: N802
        path = self.path.split("?", 1)[0]
        try:
            if path == "/api/mode":
                body = self._read_json()
                new = body.get("mode", "")
                set_config_mode(new)
                VPN_RULES.reload_if_changed()
                DIRECT_RULES.reload_if_changed()
                log(f"api: mode -> {new}")
                return self._json({"mode": current_mode()})

            if path == "/api/lists/vpn" or path == "/api/lists/direct":
                which = "vpn" if path.endswith("vpn") else "direct"
                target = VPN_LIST_PATH if which == "vpn" else DIRECT_PATH
                body = self._read_json()
                added, removed, skipped = apply_list_edits(
                    target, add=body.get("add"), remove=body.get("remove"))
                # Touch reload so PAC reflects immediately.
                rules = VPN_RULES if which == "vpn" else DIRECT_RULES
                rules.reload_if_changed()
                log(f"api: {which}.list edits +{len(added)} -{len(removed)}")
                return self._json({"list": which, "added": added,
                                   "removed": removed, "skipped": skipped})

            if path in ("/api/reload", "/reload"):
                c1 = VPN_RULES.reload_if_changed()
                c2 = DIRECT_RULES.reload_if_changed()
                return self._json({"reloaded": bool(c1 or c2)})

            if path == "/api/restart-openconnect":
                cfg = load_config()
                sudo_pw = cfg.get("wsl", {}).get("sudo_password", "")
                kill_pid_file(OC_PID, with_sudo=True, sudo_pw=sudo_pw)
                # tear down stray openconnects too
                subprocess.run(
                    ["bash", "-c",
                     f"echo {shlex.quote(sudo_pw)} | sudo -S pkill openconnect 2>/dev/null; true"],
                    check=False, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                )
                start_openconnect(cfg)
                return self._json({"oc_pid": is_alive(OC_PID), "tun0_ip": get_tun0_ip()})

            return self._err(404, "not found")
        except ValueError as e:
            return self._err(400, str(e))
        except Exception as e:
            log(f"api error on {path}: {e}")
            return self._err(500, str(e))

    def do_DELETE(self):  # noqa: N802
        # DELETE on a list is sugar for POST {remove:[...]} with the entry in
        # the path: /api/lists/vpn/sourcecontrol.diyarme.com
        path = self.path.split("?", 1)[0]
        if path.startswith("/api/lists/"):
            parts = path.split("/", 4)  # ['', 'api', 'lists', '<which>', '<entry>']
            if len(parts) < 5:
                return self._err(400, "missing entry")
            which, entry = parts[3], parts[4]
            if which not in ("vpn", "direct"):
                return self._err(404, "unknown list")
            target = VPN_LIST_PATH if which == "vpn" else DIRECT_PATH
            from urllib.parse import unquote
            entry = unquote(entry)
            try:
                added, removed, skipped = apply_list_edits(target, remove=[entry])
                rules = VPN_RULES if which == "vpn" else DIRECT_RULES
                rules.reload_if_changed()
                return self._json({"removed": removed})
            except Exception as e:
                return self._err(500, str(e))
        return self._err(404, "not found")


def watcher_thread():
    while True:
        try:
            VPN_RULES.reload_if_changed()
            DIRECT_RULES.reload_if_changed()
        except Exception as e:
            log(f"watcher error: {e}")
        time.sleep(1)


# ---------- Main ----------
def redirect_logs_to_pac_log():
    """Redirect stdout/stderr (and OS-level fds 1/2) to PAC_LOG.

    We launch the daemon via `Start-Process wsl.exe -e python3 vpnctl.py`
    from PowerShell with no bash wrapper, because a `bash -c` wrapper around
    this exits prematurely when wsl.exe is launched detached on Windows. That
    leaves no shell to apply `>pac.log 2>&1`, so vpnctl.py has to redirect
    its own output instead.
    """
    fd = os.open(PAC_LOG, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o644)
    os.dup2(fd, 1)
    os.dup2(fd, 2)
    os.close(fd)
    sys.stdout = os.fdopen(1, "w", buffering=1)
    sys.stderr = os.fdopen(2, "w", buffering=1)


def main():
    redirect_logs_to_pac_log()
    cfg = load_config()
    proxy_port = int(cfg["proxy"]["http_port"])
    pac_port = int(cfg["proxy"]["pac_port"])
    bind = cfg["proxy"]["bind_addr"]
    # Embed the WSL eth0 IP in PAC so Windows browsers reach the proxy
    # directly. (Re-running `vpn up` after a WSL restart refreshes this.)
    PacHandler.proxy_host = get_eth0_ip()
    PacHandler.proxy_port = proxy_port
    log(f"PAC will return PROXY {PacHandler.proxy_host}:{proxy_port}")
    # write our own pid
    with open(PAC_PID, "w") as f:
        f.write(str(os.getpid()))

    def shutdown(*_):
        log("shutting down")
        sudo_pw = cfg.get("wsl", {}).get("sudo_password", "")
        tp = tinyproxy_pid()
        if tp:
            subprocess.run(
                ["sudo", "-S", "kill", str(tp)],
                input=sudo_pw + "\n", text=True, check=False,
                stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
            )
        kill_pid_file(OC_PID, with_sudo=True, sudo_pw=sudo_pw)
        for f in (PAC_PID, TP_PID_MIRROR):
            try:
                os.remove(f)
            except FileNotFoundError:
                pass
        os._exit(0)

    signal.signal(signal.SIGTERM, shutdown)
    signal.signal(signal.SIGINT, shutdown)

    start_openconnect(cfg)
    start_tinyproxy(cfg)
    VPN_RULES.reload_if_changed()
    DIRECT_RULES.reload_if_changed()

    threading.Thread(target=watcher_thread, daemon=True).start()
    threading.Thread(target=log_mirror_thread, daemon=True).start()
    log(f"PAC server on {bind}:{pac_port}")
    srv = ThreadingHTTPServer((bind, pac_port), PacHandler)
    srv.daemon_threads = True
    srv.serve_forever()


if __name__ == "__main__":
    main()
