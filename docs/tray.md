# Tray app

`vpn-tray.exe` is a windowsgui-mode twin of `vpn.exe` (no console window) that sits in the Windows system tray.

## Launching

```powershell
.\vpn-tray.exe              # one-off, current session only
vpn autostart enable        # add HKCU Run entry — auto-launches at every login
```

`autostart enable` writes `HKCU\Software\Microsoft\Windows\CurrentVersion\Run\VpnTray` and starts the tray immediately so you don't have to log out / back in.

## Menu

```
● connected · 172.31.x.x · vpn-default     ← live status (poll every 3s)
↑ Update to 1.2.0                          ← only when an update is available
─────────────
Switch to direct-default                   ← mode toggle (label updates)
─────────────
Up
Down
Restart
─────────────
Dashboard
Quit
```

| Item                | What it does                                                                                                               |
|---------------------|----------------------------------------------------------------------------------------------------------------------------|
| Status row          | Polls `/api/status` every 3 seconds. `●` = connected, `○` = down, with WSL IP and mode. Click is no-op.                    |
| `↑ Update to X`     | Only appears when the daily-check sees a newer GitHub release. Click launches `vpn upgrade`. See [updates.md](updates.md). |
| Switch mode         | Flips `vpn-default` ↔ `direct-default`. Label changes to show what you'd switch *to*.                                      |
| Up / Down / Restart | Same as `vpn up` / `vpn down` / `vpn restart` from the CLI.                                                                |
| Dashboard           | Opens the web dashboard in your default browser.                                                                           |
| Quit                | Exits the tray. Does **NOT** bring the VPN down — useful if you want to keep the tunnel but stop watching the tray.        |

## Icons

| Icon       | State                                               |
|------------|-----------------------------------------------------|
| green dot  | tunnel + proxy both up                              |
| yellow dot | proxy up, tunnel down (or some other partial state) |
| grey dot   | tunnel + proxy both down, or daemon unreachable     |

Tooltip on hover gives the full state string.

## Survives reboots, doesn't auto-start the VPN

After Windows reboot, the WSL VM gets a new internal IP, so the PAC URL and env vars go stale. The tray:

- Launches at login (if `autostart enable` was run)
- Polls the daemon — finds it's down → grey dot
- Shows tooltip "vpn — daemon unreachable"

You click **Up** to bring the tunnel back. We deliberately don't auto-run `vpn up` at login: Duo push at 9am every workday with no context feels broken, and `vpn up` requires WSL to already be running
which isn't always true right after a reboot.

If you want true auto-up-at-login, see [troubleshooting.md](troubleshooting.md#auto-up-at-windows-login).

## Daily update check

The tray's update goroutine:

1. On startup, sleeps 30s (so it doesn't slow down login).
2. Reads `%LOCALAPPDATA%\wsl-vpn-router\.update-check` (Unix timestamp of last check).
3. If the last check is older than 24h (or the file doesn't exist), GETs `https://api.github.com/repos/Murhaf-Mo/wsl-vpn-router/releases/latest`.
4. If the returned tag is newer than the locally-embedded version, reveals the **↑ Update to X** menu item.
5. Writes the current timestamp back to `.update-check`.
6. Sleeps 24h, repeats.

Failure (network blip, GitHub rate limit, GitHub down) is silent — the goroutine just retries on the next cycle.

Network footprint: one HTTPS GET to api.github.com every 24 hours per running tray. Each request is ~1KB.
