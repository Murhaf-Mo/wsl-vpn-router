# Architecture

## The big picture

```
Windows app
  │
  ├─ PAC-aware (Chrome / Edge / IE / WinINet) ─────▶ reads AutoConfigURL ─▶ PAC
  │                                                                          │
  ├─ env-var-aware (git / curl / npm / JetBrains) ─▶ reads HTTP_PROXY ──────┤
  │                                                                          │
  │                                                  DIRECT  or  PROXY <wsl_ip>:8888
  │                                                                          │
  │                                                                tinyproxy (in WSL)
  │                                                                          │
  │                                                      default route → tun0 → openconnect → VPN
  │
  └─ everything else (Steam, native game launchers, etc.) ─▶ unaffected
```

## Components

| Component      | Where                                                        | What                                                                                                                                                           |
|----------------|--------------------------------------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `vpn.exe`      | Windows, `C:\Program Files\wsl-vpn-router\`                  | CLI controller. Knows how to start/stop the daemon, set Windows registry/env, talk to the daemon's REST API.                                                   |
| `vpn-tray.exe` | Windows, same dir                                            | Tray UI. Polls daemon status, exposes a small menu. Uses the same code paths as `vpn.exe`.                                                                     |
| `vpnctl.py`    | inside WSL distro (read from Program Files via `/mnt/c/...`) | The daemon. Runs `openconnect` and `tinyproxy`, serves `/proxy.pac`, the REST API, and the dashboard SPA on `:8889`. Single-file, stdlib-only Python.          |
| `openconnect`  | inside WSL distro                                            | The actual VPN client. Creates `tun0`.                                                                                                                         |
| `tinyproxy`    | inside WSL distro                                            | HTTP proxy listening on `:8888` of the WSL VM. Its outbound socket exits via `tun0` (the default route), so any HTTP traffic it forwards goes through the VPN. |
| MSI            | install-time only                                            | Bundles everything, writes `HKLM\Software\wsl-vpn-router\InstallDir`, runs WSL provisioning, kicks off the config wizard.                                      |

## Install vs Data split

Two roots, by design:

```
C:\Program Files\wsl-vpn-router\         ← INSTALL_DIR (admin to write)
  vpn.exe, vpn-tray.exe, vpnctl.py, static/, setup-wsl.sh, …

%LOCALAPPDATA%\wsl-vpn-router\           ← DATA_DIR (per-user, no admin)
  config.toml, vpn.list, direct.list, runtime/
```

Why split:

- **Updates don't blow away config.** MSI replaces files in INSTALL_DIR; DATA_DIR is never touched.
- **Daily ops never need admin.** Editing `vpn.list` to add a host, flipping mode, viewing logs — all happen in DATA_DIR.
- **Per-user credentials.** Each Windows user has their own `config.toml`, their own `vpn.list` edits, their own VPN session.
- **Read-only assets stay read-only.** `static/` (dashboard JS/CSS) lives in INSTALL_DIR, untampered by the user.

The MSI writes the install path to `HKLM\Software\wsl-vpn-router\InstallDir`. The Go controller and the Python daemon both read this to find `static/` and to know where their sibling scripts live. The
daemon receives DATA_DIR as a `--data-dir` argument from the launcher.

### Dev mode

When `vpn.exe` runs from a checkout (no HKLM key present), INSTALL_DIR and DATA_DIR collapse to the same directory — the project checkout. This means `git clone && go build && ./vpn.exe up` works
without an MSI install, useful for hacking.

## PAC decision order

Implemented in `vpnctl.py:build_pac`. For each `(url, host)`:

1. URL matches `vpn.list` → `PROXY <wsl_ip>:8888`
2. URL matches `direct.list` → `DIRECT`
3. Otherwise per `[routing] mode`:
    - `vpn-default` → `PROXY <wsl_ip>:8888`
    - `direct-default` → `DIRECT`

`vpn.list` is checked first so a host pinned to VPN can never be accidentally leaked via DIRECT — even if a broader CIDR rule in `direct.list` would match its resolved IP.

## Why PAC AND env vars?

| Tool family                            | Honors PAC? | Honors `HTTP_PROXY`?             |
|----------------------------------------|-------------|----------------------------------|
| Chrome, Edge, IE, anything via WinINet | yes         | sort of (via WinINet, after PAC) |
| git, curl, npm, pip                    | no          | yes                              |
| JetBrains IDEs (default)               | yes         | yes (configurable)               |
| Steam, game launchers                  | no          | no                               |

In `vpn-default` mode, the controller sets both:

- `HKCU\…\AutoConfigURL = http://<wsl_ip>:8889/proxy.pac` (for PAC-aware apps)
- User-level `HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY` env vars + `git config --global http.proxy` (for env-aware apps)

Env vars only model "always proxy with NO_PROXY exceptions" — which fits `vpn-default` cleanly. In `direct-default`, env vars are cleared (they'd break too many things), and only PAC-aware apps get
per-host VPN routing.

## Why PAC + env vars instead of a TAP driver?

A TAP driver (or Wintun, or WireGuard kernel) intercepts at the IP layer. That covers Steam, native launchers, anything that doesn't respect PAC or env vars. Trade-offs:

- Requires admin to install + maintain the driver
- Routing-table acrobatics to do split-tunneling cleanly
- Way more brittle: AV / EDR sometimes flag arbitrary TAP devices
- A bug routes ALL your traffic the wrong way (security-relevant)

For a per-user tool whose users predominantly use browsers + dev tools + IDEs (all of which honor PAC and/or env vars), the app-layer approach is dramatically simpler, requires no kernel-mode
anything, and fails safe.

## How the daemon stays alive

WSL2 idle-times-out the VM ~60s after the last process exits. If we `wsl.exe -e python3 vpnctl.py` and then return, `wsl.exe` exits, the VM idle-shuts-down, and the daemon dies with it.

Workaround: `vpn up` launches `vpn.exe -e python3 vpnctl.py ...` via `ShellExecuteExW` with `SW_HIDE`, then captures the launched **wsl.exe** PID into `runtime\wsl-keepalive.pid`. The Windows-side
`wsl.exe` process stays alive (because it's blocking on the python3 in WSL), and WSL2 sees the connection as live → no idle shutdown.

`vpn down` reads the PID, kills `wsl.exe`, which propagates SIGTERM into WSL, which the daemon's signal handler catches to tear down openconnect + tinyproxy cleanly.

## TCP MSS clamp

When a TLS handshake to a `vpn.list` host stalls forever despite the tunnel being up, the cause is almost always a TCP MSS mismatch: the corporate path-MTU through openconnect is smaller than your
home network's, and the server's SYN-ACK can't be fragmented.

The daemon installs an `iptables -t mangle … TCPMSS --clamp-mss-to-pmtu` rule on `tun0` automatically. Verify:

```bash
wsl -d Ubuntu -- sudo iptables -t mangle -L OUTPUT -v -n | grep tun0
```

## Why a daemon at all?

PAC is a JavaScript function the browser evaluates per request. The function needs to know "what's the current mode", "what's in vpn.list", "what's in direct.list". Hardcoding into a static file means
edits don't propagate without rewrites + browser cache flushes.

By serving the PAC dynamically, we:

- Re-render the PAC body from current `mode` + lists on every request
- Reload lists from disk on mtime change (~1s lag)
- Surface a REST API (`/api/status`, `/api/lists/*`, `/api/mode`) for the tray + dashboard
- Serve the dashboard SPA from `/`

That's why `vpnctl.py` is a ~900-line stdlib-only Python HTTP server, not a static file.
