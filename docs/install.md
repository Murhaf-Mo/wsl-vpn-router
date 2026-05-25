# Install

## One-liner

From any PowerShell window (elevation handled automatically with one UAC prompt):

```powershell
irm https://raw.githubusercontent.com/Murhaf-Mo/wsl-vpn-router/main/install.ps1 | iex
```

What you see, in order, **all in the same console with live output** (no silent waits):

1. **Self-elevate.** If you ran the irm in a non-admin PowerShell, the installer downloads itself to `%TEMP%` and re-launches via `Start-Process -Verb RunAs`. Single UAC prompt. The original window
   closes; the elevated one takes over.
2. **Download MSI + SHA-256 sidecar** from the latest GitHub Release into `%TEMP%`.
3. **Verify SHA-256.**
4. **`msiexec /i ... /qb /norestart`** — fast file copy + registry, ~10 seconds, visible MSI progress dialog. Installs binaries + assets to `C:\Program Files\wsl-vpn-router\` and writes
   `HKLM\Software\wsl-vpn-router\InstallDir`.
5. **`wslprovision.cmd`** runs directly in the same console — you see DISM, `wsl --update`, `wsl --install -d Ubuntu`, and apt output as it happens. Enables WSL features if needed, installs Ubuntu if
   no non-`docker-desktop` distro is present, then apt-installs `openconnect tinyproxy python3 iptables`.
6. **`vpn.exe configure`** — the interactive wizard prompts for your VPN host / user / password / group + WSL distro + sudo password. Writes `%LOCALAPPDATA%\wsl-vpn-router\config.toml`.

After all of that completes, the tunnel is **not** automatically brought up — final step:

```powershell
vpn up                 # bring the tunnel up
vpn autostart enable   # tray runs at every login
```

(See [quickstart.md](quickstart.md).)

### Why is this multi-step?

Earlier MSI versions tried to do steps 5 + 6 inside the MSI via custom actions. That made them run **silently** (WixQuietExec hides stdout), so users saw the MSI progress bar stuck at 75% for the 5+
minutes that `wsl --install` and apt take on a fresh machine — and the Cancel button couldn't interrupt the external process. Moving those steps out into the wrapper trades a slightly longer-feeling
install for full visibility.

## Prerequisites

| Thing                           | Why                                                                                                               |
|---------------------------------|-------------------------------------------------------------------------------------------------------------------|
| Windows 10 v2004+ or Windows 11 | WSL2 baseline.                                                                                                    |
| Admin rights on this machine    | **One-time**, during MSI install + WSL feature enable + distro install. After that, daily operation is non-admin. |
| Internet access                 | To download the MSI, pull the WSL kernel, fetch the Ubuntu rootfs, and `apt install` runtime deps.                |
| Your VPN credentials            | Host (e.g. `vpn.example.com`), username, password. Optional: auth group if your server uses one.                  |

You do **not** need a clone of this repo or Git installed.

## Locations after install

| Path                               | Contents                                                                                                                                                                | Writable by   |
|------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------|---------------|
| `C:\Program Files\wsl-vpn-router\` | `vpn.exe`, `vpn-tray.exe`, `vpnctl.py`, `static/`, `setup-wsl.sh`, `wslprovision.cmd`, `uninstall.ps1`, the default `vpn.list` / `direct.list` shipped with the release | Admin only    |
| `%LOCALAPPDATA%\wsl-vpn-router\`   | `config.toml` (your credentials), `vpn.list` (your edits), `direct.list` (your edits), `runtime/` (logs, pid files, generated tinyproxy.conf)                           | You, no admin |

The split is intentional: see [architecture.md](architecture.md#install-vs-data-split).

## Per-user behavior

The MSI installs per-machine (Program Files), but each Windows user on the machine gets their own `%LOCALAPPDATA%\wsl-vpn-router\` with their own credentials and lists. The first-run wizard fires the
first time each user runs `vpn` from a fresh shell.

## Verifying the install

Open a **new** PowerShell window (so PATH picks up `C:\Program Files\wsl-vpn-router\`), then:

```powershell
vpn --version       # prints the embedded release tag, e.g. "vpn 1.0.0"
vpn status          # daemon status; if first-run wizard ran, will show tunnel up
```

Or via the tray icon in the system tray — health dot, connected IP, and a menu for up/down/restart/mode toggle.

## Reinstall / update

Re-running the same one-liner upgrades in place. The MSI's `MajorUpgrade` rule replaces binaries and rewrites the HKLM key while preserving your `%LOCALAPPDATA%\wsl-vpn-router\` contents (config,
lists, logs). See [updates.md](updates.md) for the in-app `vpn upgrade` flow that the tray surfaces automatically.

## Uninstall

Add/Remove Programs → "wsl-vpn-router" → Uninstall. Preserves `%LOCALAPPDATA%\wsl-vpn-router\` by default. Full clean removal is in [uninstall.md](uninstall.md).
