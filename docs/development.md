# Development

How to run, build, and contribute changes.

## Layout

```
.
├── vpn.exe                 # built artifact (gitignored)
├── vpn-tray.exe            # built artifact (gitignored)
├── vpnctl.py               # WSL daemon (Python, stdlib only)
├── setup-wsl.sh            # apt install bootstrap (LF line endings - see .gitattributes)
├── wslprovision.ps1        # MSI custom-action entry point for WSL bootstrap
├── install.ps1             # the irm one-liner target
├── uninstall.ps1           # MSI custom-action removal helper
├── build.ps1               # local dev build (wraps go build inside WSL)
├── vpn.list, direct.list   # default routing lists shipped with the release
├── config.toml.example     # config schema example
├── static/                 # dashboard SPA (index.html, app.js, style.css)
├── installer/
│   └── wsl-vpn-router.wxs  # WiX v6 MSI source
├── .github/workflows/
│   └── release.yml         # tag-triggered build + MSI package + GitHub Release
├── controller/             # the Go source
│   ├── go.mod, go.sum
│   ├── cmd/
│   │   ├── vpn/            # CLI binary entrypoint
│   │   └── vpn-tray/       # tray binary entrypoint
│   └── internal/
│       ├── api/            # REST client to vpnctl.py
│       ├── cli/            # all `vpn <verb>` implementations
│       ├── config/         # minimal TOML reader/writer
│       ├── release/        # GitHub releases query, semver compare, MSI URL builder
│       ├── tray/           # systray UI + daily update check
│       ├── winproxy/       # HKCU AutoConfigURL + env vars + autostart Run key
│       └── wsl/            # wsl.exe interop, paths, daemon launch
└── docs/                   # this folder
```

## Dev mode (no MSI required)

Running `vpn.exe` from a checkout directory automatically falls back to single-directory layout: `wsl.IsInstalled()` returns false (no HKLM key), and
`DataDir() == InstallDir() == filepath.Dir(os.Executable())`. So `config.toml`, `vpn.list`, `direct.list`, and `runtime/` live next to your checkout.

This is what we use during development. No need to install the MSI to test changes.

```powershell
# from the project root
cd controller
go build -o ..\vpn.exe -ldflags "-s -w" .\cmd\vpn
go build -o ..\vpn-tray.exe -ldflags "-s -w -H windowsgui" .\cmd\vpn-tray
cd ..
.\vpn.exe configure       # writes ./config.toml (dev mode collapses paths)
.\vpn.exe up
```

Or use the wrapper:

```powershell
.\build.ps1               # release-y build, both binaries
.\build.ps1 -Debug        # keep symbols, no -s -w
```

`build.ps1` cross-compiles via `GOOS=windows go build` inside whichever WSL distro `[wsl] distro` in your `config.toml` points at — so you need Go installed inside that distro (
`sudo apt install golang-go`).

## Tests

Cross-platform unit tests live in `controller/internal/release/`. Run on Linux (the test runner used by CI):

```powershell
wsl -d Ubuntu -- bash -c 'cd /mnt/<your-checkout>/controller && go test ./internal/release/...'
```

`go test ./...` won't work as-is because `internal/wsl`, `internal/winproxy`, `internal/tray` import `golang.org/x/sys/windows/...` (Windows-only). Cross-compile a binary with `GOOS=windows` to
type-check the whole tree:

```powershell
wsl -d Ubuntu -- bash -c 'cd /mnt/<your-checkout>/controller && GOOS=windows go build -buildvcs=false ./...'
```

## Building an MSI locally

You need .NET 6+ and the WiX v6 dotnet tool:

```powershell
dotnet tool install --global wix --version 6.0.0
wix --version
```

Pin to an exact version (not `--version 6.*`) so the matching `WixToolset.Util.wixext/6.0.0` extension below is guaranteed compatible — a bare `WixToolset.Util.wixext` install pulls the v7 extension
and `wix build` rejects it with "Could not find expected package root folder wixext6".

Stage the files exactly as `wsl-vpn-router.wxs` expects, then build:

```powershell
$ver = '0.0.1-dev'
$stage = "$PWD\installer\staging"
New-Item -ItemType Directory -Force -Path "$stage\static" | Out-Null
Copy-Item .\vpn.exe, .\vpn-tray.exe, .\vpnctl.py, .\setup-wsl.sh,
          .\vpn.list, .\direct.list, .\config.toml.example,
          .\uninstall.ps1, .\wslprovision.ps1  $stage
Copy-Item .\static\*  "$stage\static\"

wix extension add -g WixToolset.Util.wixext/6.0.0
wix build installer\wsl-vpn-router.wxs `
    -arch x64 `
    -ext WixToolset.Util.wixext `
    -d "Version=$ver" `
    -d "StagingDir=$stage" `
    -o "wsl-vpn-router-$ver.msi"
```

Then install it locally (UAC-prompts):

```powershell
msiexec /i "wsl-vpn-router-$ver.msi" /qb /l*v $env:TEMP\wvr-install.log
```

`/l*v` writes a verbose log to `%TEMP%\wvr-install.log` — invaluable for debugging custom actions.

## Cutting a release

1. Land your changes on `main`. Make sure tests pass.
2. Tag: `git tag v1.2.3 && git push origin v1.2.3`
3. `.github/workflows/release.yml` fires:
    - cross-builds binaries with the version baked in
    - stages files, runs `wix build`
    - generates SHA-256 sidecar
    - creates the GitHub Release with MSI + sidecar + install.ps1 as assets
4. Within 24h, every running tray pings the GitHub API, sees the new tag, and prompts users to update.

Don't tag `v1.2.3` before the same code is on `main` — the release pipeline checks out the tag, but `install.ps1` is pulled from `main`'s raw URL (so the irm one-liner stays stable across releases).

## Contributing

- One feature per PR. Keep them small enough to review in an evening.
- Match the existing style (Go: gofmt; Python: 4-space indent, stdlib only).
- Don't add dependencies casually. `vpnctl.py` is intentionally stdlib-only; the Go side has 1.5 external deps (systray + x/sys + x/term) and we'd like to keep it that way.
- Document config schema changes in `docs/configuration.md` and bump the version on release.

## Things we'd happily take PRs for

- DPAPI / Credential Manager integration for the VPN password (see [security.md](security.md)).
- Auto-up-at-login as a first-class option (not just "create a Task Scheduler entry yourself").
- An MSI custom action that prompts the user "create your WSL user now" mid-install instead of dropping them at a wsl.exe shell.
- A winget manifest, once we have a stable 1.x release.

## Things we'd probably reject

- Adding a TAP / WireGuard / kernel-mode component to capture non-PAC apps. See [architecture.md](architecture.md#why-pac--env-vars-instead-of-a-tap-driver).
- Telemetry of any kind. Not even crash reports.
- A Windows Service — daily ops don't need one and it complicates the per-user model.
- IPv6 support. PAC's IPv6 story is incomplete and our user base doesn't need it.
