# Updates

Three ways to update — pick whichever matches what's broken / where you are.

## 1. Tray notification (default)

If `vpn-tray.exe` is running, it checks GitHub once every 24 hours. When it finds a newer release, the top of the tray menu sprouts:

```
↑ Update to 1.2.0
```

Click it. The flow:

1. `vpn upgrade` runs (under the hood).
2. Downloads `wsl-vpn-router-1.2.0.msi` + `.sha256` to `%TEMP%`.
3. Verifies SHA-256.
4. Launches `msiexec /i wsl-vpn-router-1.2.0.msi /qb /norestart`.
5. **UAC prompts**, you approve.
6. MSI's MajorUpgrade rule replaces binaries in `C:\Program Files\wsl-vpn-router\`.
7. Tray and `vpn.exe` restart from the new binaries.

Your `%LOCALAPPDATA%\wsl-vpn-router\` (config.toml, lists, runtime logs) is **untouched**.

The daily check fails soft — network blip, GitHub rate limit, GitHub down: silent, retries next cycle.

## 2. CLI

```powershell
vpn upgrade
```

Same flow, manual trigger. Useful when you want to update right now without waiting for the tray's daily ping.

```
current: 1.1.0
latest:  1.2.0
downloading https://github.com/Murhaf-Mo/wsl-vpn-router/releases/download/v1.2.0/wsl-vpn-router-1.2.0.msi ...
verifying SHA-256 ...
launching installer (UAC will prompt) ...
upgrade launched; this process exits now.
```

If you're already on the latest:

```
current: 1.2.0
latest:  1.2.0
already up to date.
```

## 3. Re-run the installer

```powershell
irm https://raw.githubusercontent.com/Murhaf-Mo/wsl-vpn-router/main/install.ps1 | iex
```

The same one-liner that did the initial install does an upgrade. The MSI knows how to identify itself (stable `UpgradeCode`), and `MajorUpgrade` triggers the same in-place file replace.

This path is what you use if `vpn.exe` is too broken to run `vpn upgrade` itself.

## What gets preserved across upgrades

- `%LOCALAPPDATA%\wsl-vpn-router\config.toml` — your credentials, distro, mode
- `%LOCALAPPDATA%\wsl-vpn-router\vpn.list` — your edited list
- `%LOCALAPPDATA%\wsl-vpn-router\direct.list` — your edited list
- `%LOCALAPPDATA%\wsl-vpn-router\runtime\` — logs, pids, generated tinyproxy.conf
- HKCU autostart entry (so the tray keeps launching at login)

## What gets replaced

- All files in `C:\Program Files\wsl-vpn-router\` — binaries, vpnctl.py, static/, setup scripts, the shipped default lists
- `HKLM\Software\wsl-vpn-router\Version` — set to the new version

## Versioning

We follow semver:

- **Major** (`1.x.y` → `2.0.0`): breaking changes — config schema changes that need migration, CLI verb removals, etc. Read the release notes before upgrading.
- **Minor** (`1.1.x` → `1.2.0`): new features, new verbs, new config keys (with defaults). Safe to upgrade blindly.
- **Patch** (`1.1.0` → `1.1.1`): bug fixes only. Always safe.

`vpn --version` prints the embedded tag. The tray's status tooltip also includes it.

## Skipping a release

If you don't want a particular release, just don't click the tray notification. Next cycle it'll still see "1.2.0 newer than 1.1.0" and re-show the prompt. There's no per-release "skip" — the tool's
state is just "what version am I" vs "what's latest". If you want to pin to a specific older version, install that version's MSI directly from
the [GitHub releases page](https://github.com/Murhaf-Mo/wsl-vpn-router/releases) and don't run `vpn upgrade`.

## Rolling back

Uninstall the current version (Add/Remove Programs), then install the older MSI manually from the releases page. Your config + lists survive.

## How the release pipeline works

`.github/workflows/release.yml` triggers on `v*.*.*` tag push:

1. **build-binaries** (ubuntu-latest): cross-compiles `vpn.exe` and `vpn-tray.exe` with `GOOS=windows`, stamps the tag into the embedded version via `-ldflags "-X …localVersion=<tag>"`.
2. **package-msi** (windows-latest): installs WiX v6 (`dotnet tool install --global wix`), stages all asset files, runs `wix build` to produce `wsl-vpn-router-<ver>.msi`.
3. Computes SHA-256, generates the `.sha256` sidecar.
4. Publishes a GitHub Release with the MSI, the SHA, and `install.ps1` as assets.
