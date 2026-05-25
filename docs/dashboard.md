# Web dashboard

The daemon serves a small dark-themed SPA at `http://<wsl_ip>:8889/`. Pure HTML/JS/CSS, no external CDN — works fine inside an air-gapped corporate network.

## Opening it

```powershell
vpn dashboard            # opens default browser
```

Or click **Dashboard** in the tray. Or just paste `http://<wsl_ip>:8889/` — get `<wsl_ip>` from `vpn status`.

## What you can do

| Panel         | Action                                                                                                                  |
|---------------|-------------------------------------------------------------------------------------------------------------------------|
| Status header | Live health dot, mode badge, connected IP. Updates every 2s.                                                            |
| Mode toggle   | One click flips `vpn-default` ↔ `direct-default`. Daemon reloads PAC + env vars immediately.                            |
| Lists         | Inline add/remove for both `vpn.list` and `direct.list`. Same syntax as the files — see [lists.md](lists.md).           |
| Logs          | Tail viewer with tabs for `openconnect` / `vpnctl` / `tinyproxy`. Tail-only — for full files, use `vpn logs <name> -f`. |
| Reconnect     | Restart only `openconnect` (not the whole daemon). Useful after a DPD failure or laptop sleep.                          |
| Reload        | Force re-read of lists + flush wininet PAC cache. Same as `vpn reload`.                                                 |

## REST API (under the hood)

The dashboard calls these endpoints; you can hit them with `curl` too.

```
GET  /api/status                                   → JSON: tunnel/proxy/daemon health
GET  /api/lists/vpn                                → JSON array of entries
GET  /api/lists/direct
POST /api/lists/vpn       body: {"add": "host.com"}
POST /api/lists/vpn       body: {"remove": "host.com"}
POST /api/mode            body: {"mode": "vpn-default"}
POST /api/restart-openconnect
GET  /api/logs/{oc|pac|proxy}?tail=N               → text/plain, last N lines
GET  /proxy.pac                                    → text/plain, the actual PAC body
```

All endpoints are unauthenticated. They bind to `0.0.0.0` of the WSL VM, which is reachable from your Windows host. Other machines on your LAN can also reach them if they know the WSL VM's IP — but
the WSL VM's IP usually isn't routable from outside your machine.

If you want to lock this down further, change `[proxy] bind_addr` to `127.0.0.1` — then only the WSL VM itself can connect (and you'd lose the dashboard from Windows). Not generally recommended.

## Browser compatibility

Tested in current Chrome, Edge, Firefox. Uses standard fetch + ES modules.
