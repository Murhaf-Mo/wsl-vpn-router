# wsl-vpn-router documentation

Per-host VPN routing on Windows, powered by WSL. The tool runs OpenConnect inside a WSL distro, exposes its egress as an HTTP proxy, and points Windows' system proxy at a PAC file that decides — per request URL — whether to send the traffic through the VPN or out your home network.

## Start here

| If you want to… | Read |
|---|---|
| Install on a fresh Windows machine | [install.md](install.md) |
| Understand what gets installed where | [architecture.md](architecture.md) |
| Get the tunnel up after install | [quickstart.md](quickstart.md) |
| Add or remove hosts from VPN / DIRECT routing | [lists.md](lists.md) |
| Edit your credentials, distro, ports, routing mode | [configuration.md](configuration.md) |
| Use the CLI day-to-day | [usage.md](usage.md) |
| Use the tray app | [tray.md](tray.md) |
| Use the web dashboard | [dashboard.md](dashboard.md) |
| Understand how updates work | [updates.md](updates.md) |
| Debug something that isn't working | [troubleshooting.md](troubleshooting.md) |
| Know what's stored where, what's encrypted, what isn't | [security.md](security.md) |
| Cleanly remove the tool | [uninstall.md](uninstall.md) |
| Run from a checkout / build from source / contribute | [development.md](development.md) |

## In one sentence

`irm https://raw.githubusercontent.com/Murhaf-Mo/wsl-vpn-router/main/install.ps1 | iex` — UAC prompts, MSI installs, an interactive wizard collects your VPN credentials, the tunnel comes up, and a tray icon appears that survives reboots.

## What this tool is

A **split-routing** VPN client. Some hosts go through your corporate VPN, others go straight out your home network. Lists drive the split; a runtime "default mode" decides what happens for everything not explicitly listed.

If you only need a regular always-on VPN, the official Cisco AnyConnect client is simpler. This project exists because flipping AnyConnect on/off twenty times a day is miserable.

## What this tool is NOT

- A general-purpose VPN. It only does OpenConnect (Cisco AnyConnect-compatible).
- A system-wide packet capture. It only routes traffic from apps that honor either a PAC file or `HTTP_PROXY` env vars. Native game launchers, Steam, and things using WinHTTP without PAC are out of scope. See [architecture.md](architecture.md#why-pac--env-vars-instead-of-a-tap-driver).
- An IPv6 router. `tun0` is IPv4 only.
- A credential vault. `config.toml` stores your VPN password as plaintext. See [security.md](security.md).

## License

Personal-use. Fork freely.
