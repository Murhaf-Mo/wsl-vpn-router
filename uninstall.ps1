# wsl-vpn-router uninstaller.
#
# Invoked by the MSI's "deferred uninstall" custom action (so it runs elevated
# with the install context still available). Can also be run manually:
#   & "C:\Program Files\wsl-vpn-router\uninstall.ps1" [-PurgeData]
#
# Tear-down order:
#   1. Stop running processes (vpn-tray.exe, vpn.exe)
#   2. `vpn down` to clear Windows AutoConfigURL + proxy env vars + git config
#      (we do this BEFORE killing processes so the daemon can clean up).
#   3. Kill openconnect / tinyproxy / vpnctl inside WSL.
#   4. Remove HKCU Run autostart entry.
#   5. Remove HKLM\Software\wsl-vpn-router.
#   6. Optionally (-PurgeData) delete %LOCALAPPDATA%\wsl-vpn-router. Default
#      is to preserve so the user's vpn.list / direct.list / config.toml
#      survive a reinstall.
#
# Idempotent: each step is best-effort. Missing items are not errors.

[CmdletBinding()]
param(
    [switch]$PurgeData
)

$ErrorActionPreference = 'Continue'  # best-effort; never bail half-way

function Info($m) { Write-Host "    $m" }
function Step($m) { Write-Host ""; Write-Host "==> $m" -ForegroundColor Cyan }
function Warn($m) { Write-Host "!!  $m" -ForegroundColor Yellow }

$InstallDir = (Get-Item -Path $PSScriptRoot -ErrorAction SilentlyContinue).FullName
if (-not $InstallDir) { $InstallDir = "$env:ProgramFiles\wsl-vpn-router" }
$DataDir    = Join-Path $env:LOCALAPPDATA 'wsl-vpn-router'

# ---------- 1. Tear down Windows-side proxy state via vpn.exe ----------
$vpnExe = Join-Path $InstallDir 'vpn.exe'
if (Test-Path $vpnExe) {
    Step "Running vpn down (clears AutoConfigURL + env vars + WSL daemon)"
    try {
        & $vpnExe down 2>&1 | ForEach-Object { Info $_ }
    } catch {
        Warn "vpn down failed: $($_.Exception.Message)  (continuing)"
    }
}

# ---------- 2. Kill any lingering processes ----------
Step "Stopping vpn-tray.exe / vpn.exe"
foreach ($name in @('vpn-tray.exe', 'vpn.exe')) {
    Get-Process -Name ($name -replace '\.exe$', '') -ErrorAction SilentlyContinue |
        ForEach-Object {
            Info "kill $($_.ProcessName) pid $($_.Id)"
            Stop-Process -Id $_.Id -Force -ErrorAction SilentlyContinue
        }
}

# ---------- 3. Stop residual WSL processes ----------
# vpn down above should have done this; this is a safety net.
Step "Cleaning residual processes in WSL distros"
$distros = & wsl.exe -l -q 2>$null
if ($distros) {
    foreach ($d in ($distros -replace "`0", "" -split "`n" | ForEach-Object { $_.Trim() } | Where-Object { $_ -and $_ -ne 'docker-desktop' })) {
        & wsl.exe -d $d -e bash -c "pkill -f vpnctl.py; pkill openconnect; pkill tinyproxy; true" 2>$null | Out-Null
    }
}

# ---------- 4. HKCU autostart ----------
Step "Removing HKCU autostart entry"
$runKey = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Run'
if (Test-Path $runKey) {
    Remove-ItemProperty -Path $runKey -Name 'VpnTray' -ErrorAction SilentlyContinue
}

# ---------- 5. HKLM install marker ----------
Step "Removing HKLM\Software\wsl-vpn-router"
$appKey = 'HKLM:\Software\wsl-vpn-router'
if (Test-Path $appKey) {
    Remove-Item -Path $appKey -Recurse -Force -ErrorAction SilentlyContinue
}

# ---------- 6. Data dir (opt-in) ----------
if ($PurgeData) {
    Step "Purging $DataDir (config.toml + lists + runtime/ logs)"
    if (Test-Path $DataDir) {
        Remove-Item -Path $DataDir -Recurse -Force -ErrorAction SilentlyContinue
    }
} else {
    Info "Preserving $DataDir (use -PurgeData to also remove your config + lists)"
}

Step "Uninstall steps complete. MSI will remove $InstallDir."
