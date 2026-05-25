# CLI usage

All verbs run as `vpn <verb> [args]` from any shell. None require admin.

## Lifecycle

```
vpn up                          start everything, wire Windows
vpn down                        stop everything, restore Windows
vpn restart                     down then up
vpn status                      one-shot health dump
```

## Routing

```
vpn mode                        print current routing mode
vpn mode vpn-default            flip default to "everything via VPN"
vpn mode direct-default         flip default to "everything direct, only vpn.list goes VPN"
```

## Lists

```
vpn add github.com              add to vpn.list (default list)
vpn add -vpn github.com         same, explicit
vpn add -direct *.spotify.com   add to direct.list
vpn remove github.com           remove from default list (vpn.list)
vpn remove -direct *.spotify.com
vpn list                        cat both lists
```

`vpn add` / `vpn remove` modify `%LOCALAPPDATA%\wsl-vpn-router\vpn.list` (or `direct.list`) and flush the wininet PAC cache so browsers re-fetch within ~1 second. See [lists.md](lists.md).

## Logs

```
vpn logs                        tail pac.log (default)
vpn logs oc                     tail openconnect log
vpn logs proxy                  tail tinyproxy log
vpn logs oc -f                  follow (Ctrl-C to exit)
```

Logs live under `%LOCALAPPDATA%\wsl-vpn-router\runtime\`.

## Reload

```
vpn reload                      force daemon re-read of lists + refresh env vars
                                + flush wininet PAC cache
vpn sync-hosts                  copy Windows hosts -> WSL /etc/hosts
                                (only needed if you edit Windows hosts mid-session)
```

## Tray & dashboard

```
vpn tray                        run the tray UI (also auto-runs as windowsgui app)
vpn dashboard                   open the web dashboard in your default browser
```

## Autostart

```
vpn autostart status            on / off
vpn autostart enable            HKCU Run entry → tray runs at every login
vpn autostart disable           remove HKCU Run entry
```

Per-user. No admin needed.

## Config & updates

```
vpn configure                   interactive wizard (rewrites config.toml)
vpn upgrade                     check GitHub for new release, download + run MSI
vpn version                     print embedded version
vpn help                        this list
```

## Tab-completion (optional)

The CLI does not ship a completion script. The verb set is small enough that history search is faster.
