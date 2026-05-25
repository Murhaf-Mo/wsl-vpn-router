# Security

Quick threat model and an honest list of what's protected, what isn't, and where the rough edges are.

## Where secrets live

| Secret | Location | Form | Who can read |
|---|---|---|---|
| VPN password | `%LOCALAPPDATA%\wsl-vpn-router\config.toml` | plaintext | the Windows user that owns the file (NTFS ACLs); also: any process running as that user; also: any malware that gets local code execution as that user |
| WSL sudo password | same file | plaintext | same |
| GitHub repo URL, version, list contents | `HKLM\Software\wsl-vpn-router` | plaintext | any local user (HKLM is machine-wide read) |
| OpenConnect session cookie | `runtime/oc.log` (transient) and openconnect's in-memory state | plaintext in memory | the WSL `openconnect` process, killable by root inside WSL |

## What's NOT done (yet)

- **DPAPI / Windows Credential Manager**. The VPN password could be DPAPI-wrapped so it'd only be readable by the user that wrote it (and not, say, by a copied-off `config.toml` opened on another machine). Not implemented yet. Pull request welcome.
- **Code signing**. The MSI isn't signed. Windows SmartScreen will warn "Unrecognized publisher" on first install — click "More info → Run anyway". An EV cert costs ~$300/yr and we haven't justified it for a personal-use tool.
- **TLS pinning** for OpenConnect. We trust the system CA bundle. If the corporate VPN uses a private CA, install that CA inside the WSL distro the normal way (`/usr/local/share/ca-certificates/` + `update-ca-certificates`).

## Network exposure

The daemon binds `tinyproxy` (`:8888`) and `vpnctl.py` (`:8889`) to `0.0.0.0` of the WSL VM. WSL2 NATs that VM behind the Windows host, so by default:

- **Reachable from**: Windows host (the whole point — Chrome talks to the PAC URL here).
- **Not reachable from**: other machines on your LAN, the internet.

If you put your Windows machine on a public network and someone else has shell access, they could browse to `http://<your-wsl-ip>:8889/` and:

- Read your routing lists (which leak corporate hostnames if you put them there)
- POST to `/api/lists/*` to add/remove entries
- POST to `/api/mode` to flip routing modes
- Use `:8888` as an open HTTP proxy → their traffic exits your VPN tunnel

To lock down: change `[proxy] bind_addr = "127.0.0.1"` in config.toml + `vpn restart`. You'd lose the dashboard from the Windows side (you'd need to SSH-tunnel in), but the attack surface shrinks to "local code execution on the Windows host" which is already game over.

## File permissions

- `config.toml` is written with `0o600` perms by `vpn configure`. On NTFS, perm bits are advisory — what actually matters is the ACL, which inherits from `%LOCALAPPDATA%` (user-only).
- `runtime/*.log` are world-readable by default. They contain openconnect's HTTPS handshake logs (header values, redirect chains) but never your password.
- `runtime/oc.log` redacts `Password:` lines in openconnect's verbose output.

## What openconnect sends over the wire

The Cisco AnyConnect protocol that openconnect speaks is fully encrypted (TLS 1.3 / DTLS 1.2 depending on phase). What the server logs:

- Your username + password (POST body of the auth form)
- Your client IP (the source TCP connection)
- Time of session start / end / bytes transferred (the gateway's session log)

Standard for any AnyConnect-compatible client. There's no way to hide your username from the VPN server itself.

## Avoiding sudo password in config.toml

The daemon `sudo`s `openconnect`, `tinyproxy`, `iptables`, and `kill` inside WSL. If you set up a `NOPASSWD` sudoers rule for those binaries:

```bash
sudo tee /etc/sudoers.d/wsl-vpn-router > /dev/null <<EOF
$USER ALL=(root) NOPASSWD: /usr/sbin/openconnect, /usr/sbin/tinyproxy, /sbin/iptables, /bin/kill, /usr/bin/pkill
EOF
sudo chmod 440 /etc/sudoers.d/wsl-vpn-router
sudo visudo -c        # syntax-check
```

Then run `vpn configure` and leave `sudo_password` blank. The trade-off: anyone who can run code as your Linux user inside WSL can now run those binaries as root, no password. For a single-user WSL distro on your personal machine, that's a non-trade-off.

## Repo / release pipeline

- All releases come from `github.com/Murhaf-Mo/wsl-vpn-router` only. The installer hardcodes the repo path. If you fork, change `Repo` in `install.ps1` and `controller/internal/release/release.go`.
- MSIs are built in GitHub Actions on `windows-latest` from the public source. You can audit the workflow at `.github/workflows/release.yml`.
- The `.sha256` sidecar is published alongside the MSI in the same Release. `install.ps1` and `vpn upgrade` both verify SHA-256 before invoking msiexec. A man-in-the-middle attacker would have to control GitHub's release storage (which serves both files from the same TLS-protected origin) to swap a backdoored MSI.

## Reporting a vulnerability

Open a private security advisory on GitHub or DM the maintainer. Please don't file a public issue for anything that lets an attacker reach a user's VPN.
