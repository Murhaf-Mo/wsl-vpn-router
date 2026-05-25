# Routing lists

Two plain-text files decide whether a request goes via the VPN or DIRECT:

```
%LOCALAPPDATA%\wsl-vpn-router\vpn.list      # hosts ALWAYS through VPN
%LOCALAPPDATA%\wsl-vpn-router\direct.list   # hosts ALWAYS through home network
```

Edits take effect within ~1 second (the daemon polls file mtime). `vpn add` and `vpn remove` modify these files **and** flush the wininet PAC cache so browsers re-fetch immediately.

## The two modes

A third setting decides what happens for hosts in **neither** list:

```toml
# config.toml
[routing]
mode = "vpn-default"     # default: VPN; direct.list entries → DIRECT
# or
mode = "direct-default"  # default: DIRECT; vpn.list entries → VPN
```

| Scenario | Use mode | Why |
|---|---|---|
| Working day, mostly corporate apps | `vpn-default` | Less to maintain. Anything corporate just works. Pin personal/blocked sites into `direct.list`. |
| Personal day, occasional corporate ping | `direct-default` | Home network speed for everything else. Corporate-only stuff still works because it's pinned in `vpn.list`. |

The lists don't change when you flip the mode — that's the whole point.

## Precedence

For each request URL, the PAC checks in this order (first match wins):

1. Match against `vpn.list` → `PROXY <wsl_ip>:8888`
2. Match against `direct.list` → `DIRECT`
3. Default per `[routing] mode`:
   - `vpn-default` → `PROXY <wsl_ip>:8888`
   - `direct-default` → `DIRECT`

`vpn.list` is checked **before** `direct.list` so a host pinned to VPN never accidentally leaks via a broader CIDR rule in `direct.list`.

## Entry shapes

The same syntax works in both files.

| Form | Example | Matches |
|---|---|---|
| Plain host | `service.example.com` | exact host **and** all subdomains (PAC `dnsDomainIs`) |
| Wildcard host | `*.example.com` | shell-style glob (PAC `shExpMatch` on host) |
| IPv4 | `8.8.8.8` | resolved IP equals (PAC `isInNet`) |
| CIDR | `10.77.0.0/16` | resolved IP falls in range |
| URL pattern | `https://api.example.com/v1/*` | full URL match (PAC `shExpMatch` on url) |

Lines starting with `#` are comments. Blank lines ignored.

### Plain host vs wildcard

`example.com` matches `example.com` and `mail.example.com`. To match **only** `example.com` and nothing else, you can't — PAC's `dnsDomainIs` always includes subdomains. To match `mail.example.com` and `chat.example.com` but NOT `example.com`, use `*.example.com`.

### IP vs CIDR

PAC's `isInNet` resolves the URL's host to an IP first. Useful for catching corporate apps that haven't been added to your list explicitly but happen to live in a corporate subnet:

```
# direct.list
10.0.0.0/8       # entire RFC1918 to home
```

```
# vpn.list (wins because it's checked first)
10.77.0.0/16     # corporate /16 carved out for VPN
```

## CLI

```powershell
vpn add github.com                  # → vpn.list (default list)
vpn add -vpn   github.com           # explicit
vpn add -direct *.spotify.com       # → direct.list
vpn add -direct https://api.spotify.com/v1/*

vpn remove github.com               # remove from vpn.list
vpn remove -direct *.spotify.com

vpn list                            # cat both files with section headers
```

`vpn add` is idempotent — adding the same line twice doesn't duplicate.

## Editing by hand

Just edit the file. Within ~1 second the daemon notices via mtime and reloads. If a browser tab already had an open connection with the *old* decision, hit Ctrl-Shift-R or restart the browser tab.

## Examples

### Corporate-day, with personal exceptions

```toml
[routing]
mode = "vpn-default"
```
```
# direct.list — things you DON'T want through VPN
*.youtube.com
youtube.com
*.googlevideo.com
github.com
*.github.com
```
```
# vpn.list — left empty or with a few "force VPN even if direct.list matches" entries
```

### Personal-day, with corporate exceptions

```toml
[routing]
mode = "direct-default"
```
```
# vpn.list — corporate-only hosts
*.corp.example.com
sourcecontrol.example.com
10.77.0.0/16
```
```
# direct.list — usually empty in this mode
```

## Hosts-file sync

`tinyproxy` runs **inside WSL**, which means it does DNS lookups via WSL's resolver, not via Windows' resolver. Internal hosts pinned to private IPs in your Windows `C:\Windows\System32\drivers\etc\hosts` file are invisible to WSL by default.

`vpn up` solves this by mirroring all numeric IP lines from Windows hosts into WSL `/etc/hosts` under a marker block:

```
# >>> wsl-vpn-router managed >>>
10.77.77.59  internal.example.com
# <<< wsl-vpn-router managed <<<
```

Re-runnable any time:

```powershell
vpn sync-hosts
```

(`vpn up` calls this automatically.)
