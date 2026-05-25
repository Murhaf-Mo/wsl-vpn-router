# Install

## One-liner

From any PowerShell window (no need to elevate beforehand — the installer will UAC-prompt itself):

```powershell
irm https://raw.githubusercontent.com/Murhaf-Mo/wsl-vpn-router/main/install.ps1 | iex
```

What this does:

1. Queries GitHub for the latest release tag.
2. Downloads `wsl-vpn-router-<version>.msi` and its `.sha256` sidecar into `%TEMP%`.
3. Verifies SHA-256 of the MSI against the sidecar.
4. Runs `msiexec /i …` — **Windows prompts for UAC** at this point.

What the MSI does:

1. Installs binaries + assets to `C:\Program Files\wsl-vpn-router\`.
2. Writes the install path to `HKLM\Software\wsl-vpn-router\InstallDir` (so `vpn.exe` knows where it is regardless of how it's invoked).
3. Runs `wslprovision.cmd`, which:
   - Enables the `Microsoft-Windows-Subsystem-Linux` + `VirtualMachinePlatform` Windows features (if disabled).
   - Installs Ubuntu via `wsl --install -d Ubuntu --no-launch --web-download` (only if no non-`docker-desktop` distro is registered).
   - Runs `setup-wsl.sh` inside the chosen distro to `apt install openconnect tinyproxy python3 iptables`.
4. Launches the **first-run configuration wizard** (`vpn.exe configure`) — see [configuration.md](configuration.md).
5. Adds a Start Menu entry and an Add/Remove Programs entry.

## Prerequisites

| Thing | Why |
|---|---|
| Windows 10 v2004+ or Windows 11 | WSL2 baseline. |
| Admin rights on this machine | **One-time**, during MSI install + WSL feature enable + distro install. After that, daily operation is non-admin. |
| Internet access | To download the MSI, pull the WSL kernel, fetch the Ubuntu rootfs, and `apt install` runtime deps. |
| Your VPN credentials | Host (e.g. `vpn.example.com`), username, password. Optional: auth group if your server uses one. |

You do **not** need a clone of this repo or Git installed.

## Locations after install

| Path | Contents | Writable by |
|---|---|---|
| `C:\Program Files\wsl-vpn-router\` | `vpn.exe`, `vpn-tray.exe`, `vpnctl.py`, `static/`, `setup-wsl.sh`, `wslprovision.cmd`, `uninstall.ps1`, the default `vpn.list` / `direct.list` shipped with the release | Admin only |
| `%LOCALAPPDATA%\wsl-vpn-router\` | `config.toml` (your credentials), `vpn.list` (your edits), `direct.list` (your edits), `runtime/` (logs, pid files, generated tinyproxy.conf) | You, no admin |

The split is intentional: see [architecture.md](architecture.md#install-vs-data-split).

## Per-user behavior

The MSI installs per-machine (Program Files), but each Windows user on the machine gets their own `%LOCALAPPDATA%\wsl-vpn-router\` with their own credentials and lists. The first-run wizard fires the first time each user runs `vpn` from a fresh shell.

## Verifying the install

Open a **new** PowerShell window (so PATH picks up `C:\Program Files\wsl-vpn-router\`), then:

```powershell
vpn --version       # prints the embedded release tag, e.g. "vpn 1.0.0"
vpn status          # daemon status; if first-run wizard ran, will show tunnel up
```

Or via the tray icon in the system tray — health dot, connected IP, and a menu for up/down/restart/mode toggle.

## Reinstall / update

Re-running the same one-liner upgrades in place. The MSI's `MajorUpgrade` rule replaces binaries and rewrites the HKLM key while preserving your `%LOCALAPPDATA%\wsl-vpn-router\` contents (config, lists, logs). See [updates.md](updates.md) for the in-app `vpn upgrade` flow that the tray surfaces automatically.

## Uninstall

Add/Remove Programs → "wsl-vpn-router" → Uninstall. Preserves `%LOCALAPPDATA%\wsl-vpn-router\` by default. Full clean removal is in [uninstall.md](uninstall.md).
