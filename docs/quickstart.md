# Quickstart

You've run the installer ([install.md](install.md)) and the wizard wrote your `config.toml`. This is what the first 60 seconds look like.

## 1. Open a new PowerShell

A *new* one, after install — the existing shell doesn't know about `C:\Program Files\wsl-vpn-router\` on `PATH` yet.

```powershell
vpn --version       # "vpn 1.0.0"
```

## 2. Bring the tunnel up

```powershell
vpn up
```

What you'll see:

```
[up] syncing Windows hosts -> WSL /etc/hosts...
[hosts] synced (37 host entries in WSL /etc/hosts)
[up] launching vpnctl.py in Ubuntu...
[up] WSL IP: 192.168.x.x
[up] AutoConfigURL = http://192.168.x.x:8889/proxy.pac
[up] mode          = vpn-default
[up] HTTP(S)_PROXY = http://192.168.x.x:8888
[up] NO_PROXY      = localhost,127.0.0.0/8,…
[up] done.
=== VPN Status ===
  ● tunnel    172.31.x.x/32
  ● proxy     192.168.x.x:8888
  ● daemon    pid 1234
```

If your VPN sends a **Duo push**, approve it on your phone now. If it asks for a passcode or a specific second-factor word in a separate prompt, you'd put that in `[vpn] mfa_response` in `config.toml` — see [configuration.md](configuration.md#vpn-section).

## 3. Verify routing

Open Chrome / Edge and visit one of:

- `https://ifconfig.me` — should now show your **VPN egress** IP, not your home IP (in `vpn-default` mode).
- A corporate URL that previously failed without VPN — should now load.

In `direct-default` mode, `ifconfig.me` shows your home IP and *only* hosts listed in `vpn.list` go through the VPN.

## 4. Make the tray visible

The MSI installs `vpn-tray.exe` but doesn't auto-pin it to the tray. To have it run at every login:

```powershell
vpn autostart enable
```

That writes an `HKCU\…\Run` entry (per-user, no admin) and launches the tray immediately. See [tray.md](tray.md) for the menu.

## 5. Tear down when done

```powershell
vpn down
```

Clears Windows' `AutoConfigURL`, clears proxy env vars + git config, kills `openconnect` + `tinyproxy` inside WSL.

## What to read next

- [usage.md](usage.md) — every CLI verb.
- [lists.md](lists.md) — how to add/remove hosts from VPN or DIRECT routing.
- [troubleshooting.md](troubleshooting.md) — if any of the above didn't work.
