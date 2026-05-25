# wsl-vpn-router installer.
#
# One-liner usage (from any PowerShell, elevated or not):
#   irm https://raw.githubusercontent.com/Murhaf-Mo/wsl-vpn-router/main/install.ps1 | iex
#
# What it does:
#   1. Asks GitHub for the latest release tag.
#   2. Downloads wsl-vpn-router-<ver>.msi + .msi.sha256 sidecar to %TEMP%.
#   3. Verifies SHA-256.
#   4. Runs `msiexec /i <msi>` — Windows prompts for UAC, MSI installs to
#      C:\Program Files\wsl-vpn-router, runs WSL provisioning + the
#      `vpn configure` wizard, registers tray autostart.
#
# Re-running on an installed system is supported: MSI's MajorUpgrade phase
# does in-place upgrade, preserving %LOCALAPPDATA%\wsl-vpn-router\config.toml.

#requires -Version 5.1
$ErrorActionPreference = 'Stop'

$Repo    = 'Murhaf-Mo/wsl-vpn-router'
$ApiBase = "https://api.github.com/repos/$Repo"

function Info($m) { Write-Host "    $m" }
function Step($m) { Write-Host ""; Write-Host "==> $m" -ForegroundColor Cyan }
function Fail($m) { Write-Host "XX  $m" -ForegroundColor Red; exit 1 }

Step "wsl-vpn-router installer"
Info "repo: https://github.com/$Repo"

# ---------- 1. Locate latest release ----------
Step "Querying GitHub for the latest release"
try {
    # Accept header pins the API version; UA is required by GitHub.
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
Info "latest release: $tag"

$msiAsset = $rel.assets | Where-Object { $_.name -like 'wsl-vpn-router-*.msi' -and $_.name -notlike '*.sha256' } | Select-Object -First 1
$shaAsset = $rel.assets | Where-Object { $_.name -like 'wsl-vpn-router-*.msi.sha256' } | Select-Object -First 1
if (-not $msiAsset) { Fail "No MSI asset found on release $tag" }
if (-not $shaAsset) { Fail "No .sha256 sidecar found on release $tag" }

# ---------- 2. Download to %TEMP% ----------
$tmpDir = Join-Path $env:TEMP "wsl-vpn-router-install"
New-Item -ItemType Directory -Force -Path $tmpDir | Out-Null
$msiPath = Join-Path $tmpDir $msiAsset.name
$shaPath = Join-Path $tmpDir $shaAsset.name

Step "Downloading $($msiAsset.name) ($([math]::Round($msiAsset.size / 1MB, 1)) MB)"
Invoke-WebRequest -Uri $msiAsset.browser_download_url -OutFile $msiPath -UseBasicParsing

Step "Downloading $($shaAsset.name)"
Invoke-WebRequest -Uri $shaAsset.browser_download_url -OutFile $shaPath -UseBasicParsing

# ---------- 3. Verify SHA-256 ----------
Step "Verifying SHA-256"
$expected = (Get-Content $shaPath -Raw).Trim() -split '\s+' | Select-Object -First 1
$actual   = (Get-FileHash -Algorithm SHA256 $msiPath).Hash
if ($expected -ne $actual) {
    Fail "SHA-256 mismatch: expected $expected, got $actual"
}
Info "match: $actual"

# ---------- 4. Run msiexec ----------
Step "Installing (UAC will prompt)"
# /qb = basic UI with progress bar; /norestart = never silently reboot.
# Start-Process -Verb RunAs triggers UAC. We Wait so the caller sees the rc.
$msi = Start-Process -FilePath 'msiexec.exe' `
    -ArgumentList '/i', "`"$msiPath`"", '/qb', '/norestart' `
    -Verb RunAs -Wait -PassThru
if ($msi.ExitCode -ne 0) {
    Fail "msiexec returned $($msi.ExitCode). See %WINDIR%\Logs\wsl-vpn-router.log if /l*v was passed."
}
Step "Done."
Info "Try: vpn status   (open a NEW shell so PATH picks up Program Files\wsl-vpn-router)"
