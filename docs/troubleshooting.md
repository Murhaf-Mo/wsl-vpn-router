# Troubleshooting

Symptoms → causes → fixes. If your problem isn't here, check the logs:

```powershell
vpn logs oc -f         # openconnect (handshake / tunnel)
vpn logs pac -f        # vpnctl.py (daemon, REST API)
vpn logs proxy -f      # tinyproxy
```

All under `%LOCALAPPDATA%\wsl-vpn-router\runtime\`.

## Install fails

### "msiexec returned 1603" / "unknown error"

Generic MSI failure. Re-run with verbose logging to see what:

```powershell
$msi = "$env:TEMP\wsl-vpn-router-install\wsl-vpn-router-*.msi" | Resolve-Path
msiexec /i $msi /l*v "$env:TEMP\wvr-install.log"
notepad $env:TEMP\wvr-install.log
```

Most common: custom action `CaWslProvision` failed because the user account doesn't have WSL feature install rights. Run from an elevated PowerShell explicitly.

### "wsl --install failed" during wslprovision

Microsoft Store throttling. `wslprovision.ps1` uses `--web-download` to bypass the Store, but the Store is still consulted for kernel updates. Retry after a few minutes, or install Ubuntu manually:

```powershell
wsl --install -d Ubuntu --web-download
```

Then re-run the wizard:

```powershell
vpn configure
```

## `vpn up` succeeds but tunnel never comes up

### "Login failed" in oc.log

Your VPN rejected the username/password. Check, in order:

1. **Username format.** Some servers want bare `alice`, some want `alice@example.com`. Try both — `vpn configure` to update.
2. **Password actually correct.** Open the official Cisco AnyConnect client and confirm you can log in with the same value.
3. **Account locked from retries.** If you've banged on it 5+ times, wait 15-30 minutes for the lockout to clear.
4. **Auth group.** Some servers require a `[vpn] group = "…"` value matching a connection profile name. The openconnect log shows it when the server prompts: `GROUP: [profile-A|profile-B]`.

### Duo push never arrives

If the log shows `Login failed.` immediately after the POST, primary auth failed → Duo never fires. Fix primary auth first (above).

If the log shows the POST succeeding and then nothing for ~30 seconds: that IS the push waiting. Check your phone — approve. If the push never arrives:

- Confirm Duo is set up for push (not just passcode) on your account.
- Confirm your phone has internet.
- Try the official AnyConnect client — if push works there but not here, file an issue with the redacted `oc.log`.

### `getaddrinfo failed for host`

DNS failure inside WSL. Test:

```powershell
wsl -d Ubuntu -- nslookup vpn.example.com 8.8.8.8
```

If that returns NXDOMAIN, fix `[vpn] host` in config.toml. If it returns an IP, but `oc.log` still says getaddrinfo failed, your WSL distro's `/etc/resolv.conf` is broken — check with
`wsl -d Ubuntu -- cat /etc/resolv.conf`.

## Tunnel up, but pages won't load

### TLS handshake stalls forever for a `vpn.list` host

MSS / MTU mismatch. The daemon should install an iptables clamp; verify:

```powershell
wsl -d Ubuntu -- sudo iptables -t mangle -L OUTPUT -v -n | grep tun0
```

If empty, `vpn restart` to re-install. If still failing, your network has a weirder PMTU situation — open an issue.

### `vpn.list` host returns HTTP 000 from `curl -x`

`getent hosts <hostname>` inside WSL returns nothing. Your Windows hosts file maps the hostname, but WSL doesn't know:

```powershell
vpn sync-hosts
```

(`vpn up` already does this; sync-hosts is the manual button if you edited Windows hosts mid-session.)

### Chrome still hits your home network

WinINet PAC cache. `vpn add` / `vpn remove` flush it automatically, but mode changes and other edits sometimes don't. Order to try:

1. **Fully quit Chrome.** Task Manager → end every `chrome.exe`, including background processes.
2. Open Chrome → `chrome://net-internals/#proxy` → "Re-apply settings".
3. Open the target site in **incognito** or a different browser to confirm whether the issue is Chrome-specific.

### git / IDE still hits home network after `vpn up`

Env vars are read at process startup. Restart your shell, IDE, and any long-running services.

For JetBrains IDEs: **Settings → Appearance & Behavior → System Settings → HTTP Proxy** — set to "Auto-detect proxy settings" (which reads PAC) **or** "Manual proxy" pointing at the same URL as
`HTTP_PROXY`.

## After Windows reboot

The WSL VM gets a new internal IP. The PAC URL and env vars from the previous boot point at the old IP and are stale:

```powershell
vpn up        # re-runs everything, picks up new IP
```

The tray detects "daemon unreachable" automatically after a reboot and goes grey; click **Up**.

## Auto-up at Windows login

Not built in. If you want it:

1. **Task Scheduler** approach — create a "At log on" trigger that runs `C:\Program Files\wsl-vpn-router\vpn.exe up`. Caveat: Duo push fires at 9am whether or not you're looking.
2. **Tray + click-once** — the default. Tray auto-starts (after `vpn autostart enable`), shows grey dot, you click **Up** to bring the tunnel up.

## Tray clicks do nothing

Either `vpn.exe` has a bug or `cmd.exe` isn't on the PATH the tray inherited. Make sure the tray was launched via `vpn-tray.exe` from Program Files (or via the autostart Run key), not via some custom
launcher with a clobbered PATH.

To verify the tray's PATH:

```powershell
Get-Process vpn-tray | ForEach-Object { (Get-WmiObject Win32_Process -Filter "ProcessId=$($_.Id)").Path }
```

## Daemon won't start ("Could not determine WSL IP — daemon never came up")

The distro isn't running. Check:

```powershell
wsl -l -v
```

If yours shows **Stopped**, kick it:

```powershell
wsl -d Ubuntu -e true
vpn up
```

Persistent stuck VM:

```powershell
wsl --shutdown
vpn up
```

(Don't use `--shutdown` casually — it kills every running WSL distro on the machine.)

## tinyproxy: "Could not create the pool of children"

Hand-edited `runtime/tinyproxy.conf`. Restore by `vpn restart` (the daemon regenerates it from scratch on every start).

## "I changed config.toml and it's not picking up"

`config.toml` is re-read per PAC request for **mode only** (cheap, hot). Other keys (credentials, ports, distro) load at daemon startup. After editing creds:

```powershell
vpn restart
```
