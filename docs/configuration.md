# Configuration

All settings live in **one file**:

```
%LOCALAPPDATA%\wsl-vpn-router\config.toml
```

It's per-user, gitignored from the project repo, and contains plaintext secrets. See [security.md](security.md) for what's stored where.

## Re-running the wizard

The fastest way to change something is the interactive wizard:

```powershell
vpn configure
```

For each prompt, hitting **Enter** keeps the current value. Passwords show `(unchanged)` and stay as-is unless you type a new one. The wizard atomically writes a fresh `config.toml` (temp + rename) so a Ctrl-C mid-way can't leave a broken file.

## Schema

```toml
[vpn]
host         = "vpn.example.com"   # gateway hostname; openconnect connects here
user         = "alice"             # VPN username
password     = "..."               # VPN password (plaintext)
group        = "MFA-VPN"           # optional; auth group / connection profile
# mfa_response = "push"            # optional; only if your server prompts a 2nd factor separately

[wsl]
distro        = "Ubuntu"           # which registered WSL distro to use
sudo_password = "..."              # used to sudo openconnect/tinyproxy/iptables inside WSL
                                   # leave empty if you've set up a NOPASSWD sudoers rule

[proxy]
http_port = 8888                   # tinyproxy listens here on the WSL VM
pac_port  = 8889                   # vpnctl.py serves PAC + REST API + dashboard here
bind_addr = "0.0.0.0"              # bind both services to all interfaces of the WSL VM

[routing]
mode = "vpn-default"               # or "direct-default" — see lists.md
```

## `[vpn]` section

| Key | Required | Notes |
|---|---|---|
| `host` | yes | FQDN of your OpenConnect gateway. No `https://`, no port. |
| `user` | yes | Username. Some servers want bare `alice`, others `alice@example.com`. If login fails with "Login failed" before any 2FA fires, try the other form. |
| `password` | yes | VPN password, plaintext. |
| `group` | no | "Auth group" / "Connection profile" on the gateway. Servers with multiple authentication paths usually have this; openconnect logs `GROUP: [option1|option2]` when one's expected. Set this to whichever option matches what AnyConnect uses. |
| `mfa_response` | no | Daemon doesn't currently honor this key. Most Cisco + Duo setups push automatically after primary auth — leave it unset. If your server prompts a separate second-factor field, you'll need to approve interactively (out of scope today). |

## `[wsl]` section

| Key | Required | Notes |
|---|---|---|
| `distro` | yes | Name as it appears in `wsl -l -q`. Common values: `Ubuntu`, `Ubuntu-22.04`, `Ubuntu-24.04`. |
| `sudo_password` | yes (unless NOPASSWD) | The Linux user's sudo password. Used to elevate `openconnect`, `tinyproxy`, `iptables`. Can be empty if `/etc/sudoers.d/vpn-router` is set up — see [security.md](security.md#avoiding-sudo-password-in-configtoml). |

## `[proxy]` section

| Key | Default | Notes |
|---|---|---|
| `http_port` | `8888` | tinyproxy. Only used inside WSL; Windows reaches it via the WSL VM IP. |
| `pac_port` | `8889` | vpnctl.py — serves `/proxy.pac`, `/api/*`, and `/` (dashboard). |
| `bind_addr` | `0.0.0.0` | Bind address of both services *inside the WSL VM*. Leave this alone unless you understand WSL2's networking. |

## `[routing]` section

| Key | Allowed | Notes |
|---|---|---|
| `mode` | `vpn-default` \| `direct-default` | What unlisted hosts do. Hot-reloaded — change it and the PAC updates within ~1 second; no `vpn restart` needed. See [lists.md](lists.md#the-two-modes). |

## Mode quick-switch

You don't need to edit the file to flip mode:

```powershell
vpn mode               # print current
vpn mode direct-default
vpn mode vpn-default
```

Or click "Switch mode" in the tray. All paths rewrite `config.toml` `[routing] mode` and push a `/api/mode` POST so the daemon reloads without restart.

## File format

The parser is a minimal in-house TOML implementation that handles strings, integers, booleans, and `# comments`. It does NOT handle arrays, dotted keys, or multi-line strings. If you need those, file an issue — we'll switch to a real TOML library.

## After editing by hand

If you edit `config.toml` directly:

- **Mode change** → propagates automatically; nothing to do.
- **Anything else** (creds, distro, ports) → `vpn restart` (down + up) to pick up new daemon-startup settings.
