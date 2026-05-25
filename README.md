# wsl-vpn-router

Per-host VPN routing on Windows, powered by WSL.

OpenConnect runs inside a WSL distro; its egress is exposed as an HTTP proxy; Windows' system proxy points at a PAC file that decides — per request URL — whether to send the traffic through the VPN or out your home network. Two plain-text lists (`vpn.list`, `direct.list`) plus a runtime-switchable default mode drive the split. Hot-reloaded, ~1s lag on edits.

## Install

From any PowerShell window:

```powershell
irm https://raw.githubusercontent.com/Murhaf-Mo/wsl-vpn-router/main/install.ps1 | iex
```

UAC prompts. MSI installs to `C:\Program Files\wsl-vpn-router\`. An interactive wizard collects your VPN host / user / password / group + WSL distro. The tunnel comes up. Tray icon appears.

Updates: the tray pings GitHub once a day and shows **↑ Update to X** when there's a newer release. Or run `vpn upgrade`. Or just re-run the irm one-liner.

## What this is

A **split-routing** VPN client for developers who:

- need to reach a corporate network over OpenConnect (Cisco AnyConnect-compatible VPN),
- want certain sites/apps to **not** route through that VPN (because it blocks them, slows them, or geo-locates them),
- don't want to flip an "enable / disable VPN" toggle a hundred times a day.

If you just need an always-on, all-traffic VPN, the official Cisco AnyConnect client is simpler. This project exists for split-routing.

## Documentation

Everything lives under [`docs/`](docs/README.md):

| Topic | Page |
|---|---|
| **Installing** | [docs/install.md](docs/install.md) |
| **First-time setup** | [docs/quickstart.md](docs/quickstart.md) |
| **Daily CLI usage** | [docs/usage.md](docs/usage.md) |
| **Routing lists** | [docs/lists.md](docs/lists.md) |
| **Configuration schema** | [docs/configuration.md](docs/configuration.md) |
| **How it works inside** | [docs/architecture.md](docs/architecture.md) |
| **Tray app** | [docs/tray.md](docs/tray.md) |
| **Web dashboard** | [docs/dashboard.md](docs/dashboard.md) |
| **Updates** | [docs/updates.md](docs/updates.md) |
| **Troubleshooting** | [docs/troubleshooting.md](docs/troubleshooting.md) |
| **Security model** | [docs/security.md](docs/security.md) |
| **Uninstalling** | [docs/uninstall.md](docs/uninstall.md) |
| **Building from source / contributing** | [docs/development.md](docs/development.md) |

## License

Personal-use. Fork freely.
