# wslprovision.ps1 - WSL setup for wsl-vpn-router.
#
# Invoked by install.ps1 (the irm one-liner) AFTER msiexec finishes the
# file-copy phase. Runs in the already-elevated PowerShell console so every
# line of DISM / wsl / apt output is visible live.
#
# Can also be run manually (from an elevated PowerShell):
#     & "C:\Program Files\wsl-vpn-router\wslprovision.ps1"
#
# Replaces the earlier wslprovision.cmd, whose batch escaping (for /f, ^|,
# caret-quoted findstr regexes) was fragile and broke with "... was unexpected
# at this time" depending on how it was launched. PowerShell parsing is sane.
#
# Steps (each idempotent, best-effort):
#   1. Enable WSL + VirtualMachinePlatform Windows features if disabled.
#   2. Ensure a usable WSL distro is registered (install Ubuntu if none).
#   3. Run setup-wsl.sh inside the distro to apt-install the runtime deps.
#
# Exit code: 0 on success / nothing-to-do, non-zero on hard failure.
# Pure ASCII on purpose (see install.ps1 note about PS 5.1 + ANSI codepage).

#requires -Version 5.1
$ErrorActionPreference = 'Stop'

$InstallDir = $PSScriptRoot
function Info($m) { Write-Host "    $m" }
function Step($m) { Write-Host ""; Write-Host "==> $m" -ForegroundColor Cyan }
function Warn($m) { Write-Host "!!  $m" -ForegroundColor Yellow }

# Resolve System32 tools by absolute path (robust regardless of CWD / PATH).
$wsl  = Join-Path $env:WINDIR 'System32\wsl.exe'
$dism = Join-Path $env:WINDIR 'System32\dism.exe'
if (-not (Test-Path $wsl))  { $wsl  = 'wsl.exe' }
if (-not (Test-Path $dism)) { $dism = 'dism.exe' }

Write-Host ""
Write-Host "=== wslprovision (WSL setup for wsl-vpn-router) ===" -ForegroundColor Cyan
Info "install dir: $InstallDir"
Info "started:     $(Get-Date -Format 'yyyy-MM-dd HH:mm:ss')"

# ---------- 1. Windows features ----------
Step "[1/3] Checking Windows features (WSL + VirtualMachinePlatform)"
function Get-FeatureState([string]$name) {
    try {
        $out = & $dism /online /Get-FeatureInfo "/FeatureName:$name" 2>$null
        $line = $out | Where-Object { $_ -match '^State\s*:' } | Select-Object -First 1
        if ($line -and $line -match ':\s*(\w+)') { return $Matches[1] }
    } catch { }
    return 'Unknown'
}

foreach ($feat in @('Microsoft-Windows-Subsystem-Linux', 'VirtualMachinePlatform')) {
    $state = Get-FeatureState $feat
    if ($state -eq 'Enabled') {
        Info "$feat : already enabled"
    } else {
        Info "$feat : enabling (one-time)..."
        & $dism /online /Enable-Feature "/FeatureName:$feat" /All /NoRestart | Out-Host
    }
}

# ---------- 2. Pick / install a distro ----------
Step "[2/3] Checking for a usable WSL distro"
function Get-UsableDistro {
    try {
        # wsl -l -q emits UTF-16; PowerShell decodes it. Strip stray NULs / CRs.
        $raw = & $wsl -l -q 2>$null
        $names = ($raw -join "`n") -replace "`0", '' -split "`r?`n" |
                 ForEach-Object { $_.Trim() } |
                 Where-Object { $_ -and $_ -ne 'docker-desktop' }
        if ($names) { return $names[0] }
    } catch { }
    return $null
}

$distro = Get-UsableDistro
if (-not $distro) {
    Info "No suitable distro registered. Installing Ubuntu."
    Info "(downloads ~150 MB; takes several minutes the first time)"
    Write-Host ""
    Info "Running: wsl --update"
    & $wsl --update | Out-Host
    Write-Host ""
    Info "Running: wsl --install -d Ubuntu --no-launch --web-download"
    & $wsl --install -d Ubuntu --no-launch --web-download | Out-Host
    if ($LASTEXITCODE -ne 0) {
        Warn "wsl --install failed (rc=$LASTEXITCODE)."
        Warn "Re-run this script after manually installing a WSL distro."
        exit 1
    }
    Write-Host ""
    Info "Ubuntu installed. Next manual step:"
    Info "    wsl -d Ubuntu"
    Info "to create your Linux user + password, then re-run this script and 'vpn configure'."
    exit 0
}
Info "Using distro: $distro"

# ---------- 3. apt deps via setup-wsl.sh ----------
Step "[3/3] Installing apt packages in $distro (openconnect, tinyproxy, python3, iptables)"
Info "sudo password is prompted by the script unless NOPASSWD is configured."
Info "apt output streams below:"
Write-Host ""
$wslInstallDir = (& $wsl -d $distro -e wslpath -a "$InstallDir").Trim()
& $wsl -d $distro -e bash "$wslInstallDir/setup-wsl.sh"
if ($LASTEXITCODE -ne 0) {
    Warn "setup-wsl.sh failed inside $distro (rc=$LASTEXITCODE)."
    Warn "Re-run this script, or run manually:"
    Warn "    wsl -d $distro -e bash $wslInstallDir/setup-wsl.sh"
    exit 1
}

Write-Host ""
Write-Host "=== wslprovision done ===" -ForegroundColor Green
exit 0
