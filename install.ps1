# wsl-vpn-router installer.
#
# One-liner usage (from any PowerShell, elevated or not):
#   irm https://raw.githubusercontent.com/Murhaf-Mo/wsl-vpn-router/main/install.ps1 | iex
#
# Flow:
#   1. Self-elevate (one UAC prompt) so msiexec + wsl-install + wizard all
#      run in the same elevated console with visible output.
#   2. Asks GitHub for the latest release tag.
#   3. Downloads wsl-vpn-router-<ver>.msi + .msi.sha256 sidecar to %TEMP%.
#   4. Verifies SHA-256.
#   5. Runs `msiexec /i <msi> /qb /norestart` — copies binaries to
#      C:\Program Files\wsl-vpn-router\, writes HKLM key. ~10s, visible
#      MSI progress bar.
#   6. Runs wslprovision.cmd in this same console: you SEE the DISM output,
#      wsl --install download progress, and apt install output as they happen.
#   7. Runs `vpn configure` (interactive wizard) so first-run config writes
#      to the launching user's %LOCALAPPDATA%\wsl-vpn-router\.
#
# Why this layout: putting the WSL provisioning inside the MSI made it run
# silently via WixQuietExec, leaving users staring at a frozen "Please wait
# while Windows configures wsl-vpn-router" dialog for the 5+ minutes wsl
# install takes. Moving it out into the wrapper means everything happens in
# a console where progress is visible.

#requires -Version 5.1
$ErrorActionPreference = 'Stop'

$Repo    = 'Murhaf-Mo/wsl-vpn-router'
$ApiBase = "https://api.github.com/repos/$Repo"
$RawBase = "https://raw.githubusercontent.com/$Repo/main"

function Info($m) { Write-Host "    $m" }
function Step($m) { Write-Host ""; Write-Host "==> $m" -ForegroundColor Cyan }
function Warn($m) { Write-Host "!!  $m" -ForegroundColor Yellow }
function Fail($m) { Write-Host "XX  $m" -ForegroundColor Red; exit 1 }

function Test-Admin {
    $id = [Security.Principal.WindowsIdentity]::GetCurrent()
    (New-Object Security.Principal.WindowsPrincipal($id)).IsInRole(
        [Security.Principal.WindowsBuiltInRole]::Administrator)
}

# ---------- 0. Self-elevate ----------
# When invoked via `irm | iex`, there is no script file on disk, so we can't
# just re-run ourselves with Start-Process -Verb RunAs. Instead we save the
# script body to %TEMP%\wsl-vpn-router-install.ps1 and relaunch from there.
if (-not (Test-Admin)) {
    Step "Self-elevating (UAC will prompt once)"
    $localScript = Join-Path $env:TEMP "wsl-vpn-router-install.ps1"
    try {
        Invoke-WebRequest -Uri "$RawBase/install.ps1" -OutFile $localScript -UseBasicParsing
    } catch {
        Fail "Could not download install.ps1 to relaunch elevated: $($_.Exception.Message)"
    }
    Start-Process -FilePath 'powershell.exe' `
        -ArgumentList '-NoExit', '-ExecutionPolicy', 'Bypass', '-File', $localScript `
        -Verb RunAs
    Write-Host ""
    Write-Host "    Continued in an elevated window — you can close this one." -ForegroundColor Cyan
    exit 0
}

# ---------- 1. Header ----------
Step "wsl-vpn-router installer"
Info "repo: https://github.com/$Repo"
Info "running elevated as: $env:USERNAME"

# ---------- 2. Locate latest release ----------
Step "Querying GitHub for the latest release"
try {
    $rel = Invoke-RestMethod -Uri "$ApiBase/releases/latest" -Headers @{
        'Accept'               = 'application/vnd.github+json'
        'X-GitHub-Api-Version' = '2022-11-28'
        'User-Agent'           = 'wsl-vpn-router-installer'
    }
} catch {
    Fail "Could not reach GitHub: $($_.Exception.Message)"
}

$tag = $rel.tag_name
if (-not $tag) { Fail "No tag_name on latest release" }
$ver = $tag.TrimStart('v')
Info "latest release: $tag"

$msiAsset = $rel.assets | Where-Object { $_.name -like 'wsl-vpn-router-*.msi' -and $_.name -notlike '*.sha256' } | Select-Object -First 1
$shaAsset = $rel.assets | Where-Object { $_.name -like 'wsl-vpn-router-*.msi.sha256' } | Select-Object -First 1
if (-not $msiAsset) { Fail "No MSI asset found on release $tag" }
if (-not $shaAsset) { Fail "No .sha256 sidecar found on release $tag" }

# ---------- 3. Download ----------
$tmpDir = Join-Path $env:TEMP "wsl-vpn-router-install"
New-Item -ItemType Directory -Force -Path $tmpDir | Out-Null
$msiPath = Join-Path $tmpDir $msiAsset.name
$shaPath = Join-Path $tmpDir $shaAsset.name

Step "Downloading $($msiAsset.name) ($([math]::Round($msiAsset.size / 1MB, 1)) MB)"
Invoke-WebRequest -Uri $msiAsset.browser_download_url -OutFile $msiPath -UseBasicParsing

Step "Downloading $($shaAsset.name)"
Invoke-WebRequest -Uri $shaAsset.browser_download_url -OutFile $shaPath -UseBasicParsing

# ---------- 4. Verify SHA-256 ----------
Step "Verifying SHA-256"
$expected = (Get-Content $shaPath -Raw).Trim() -split '\s+' | Select-Object -First 1
$actual   = (Get-FileHash -Algorithm SHA256 $msiPath).Hash
if ($expected -ne $actual) {
    Fail "SHA-256 mismatch: expected $expected, got $actual"
}
Info "match: $actual"

# ---------- 5. msiexec (file copy + registry — fast, visible MSI dialog) ----------
Step "Installing files (file copy + registry — ~10s)"
$logPath = Join-Path $env:TEMP "wsl-vpn-router-install.log"
$msi = Start-Process -FilePath 'msiexec.exe' `
    -ArgumentList '/i', "`"$msiPath`"", '/qb', '/norestart', '/l*v', "`"$logPath`"" `
    -Wait -PassThru
if ($msi.ExitCode -ne 0) {
    Fail "msiexec returned $($msi.ExitCode). Verbose log: $logPath"
}
Info "OK"

# Resolve install dir from the registry key the MSI just wrote.
$installDir = (Get-ItemProperty -Path 'HKLM:\Software\wsl-vpn-router' -Name 'InstallDir' -ErrorAction Stop).InstallDir
Info "installed to: $installDir"

# ---------- 6. Provision WSL (visible output) ----------
Step "Provisioning WSL (this can take several minutes on a fresh machine)"
Info "Downloads ~150 MB if Ubuntu isn't already installed. Apt installs ~50 MB more."
Info "All output streams live below; no silent waiting:"
Write-Host ""
& "$installDir\wslprovision.cmd"
$rc = $LASTEXITCODE
if ($rc -ne 0) {
    Warn "wslprovision.cmd exited with $rc — you can re-run it later from:"
    Info "    $installDir\wslprovision.cmd"
}

# ---------- 7. First-run configuration wizard ----------
Step "Configuring (interactive)"
Info "Writes %LOCALAPPDATA%\wsl-vpn-router\config.toml — your VPN credentials."
Write-Host ""
& "$installDir\vpn.exe" configure
$rc = $LASTEXITCODE
if ($rc -ne 0) {
    Warn "vpn configure exited with $rc — re-run any time with: vpn configure"
}

# ---------- 8. Done ----------
Step "Done."
Info "Try: vpn up        (brings the tunnel up)"
Info "     vpn status    (health check)"
Info "     vpn autostart enable   (launch tray at every login)"
Info ""
Info "Re-running this installer upgrades in place. See `vpn upgrade` for the in-app path."
