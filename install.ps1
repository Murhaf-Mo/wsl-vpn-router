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
#   5. Runs msiexec /i <msi> /qb /norestart - copies binaries to
#      C:\Program Files\wsl-vpn-router\, writes HKLM key. ~10s, visible
#      MSI progress bar.
#   6. Runs wslprovision.ps1 in this same console: you SEE the DISM output,
#      wsl --install download progress, and apt install output as they happen.
#   7. Runs vpn configure (interactive wizard) so first-run config writes
#      to the launching user's %LOCALAPPDATA%\wsl-vpn-router\.
#
# Why this layout: putting the WSL provisioning inside the MSI made it run
# silently via WixQuietExec, leaving users staring at a frozen "Please wait
# while Windows configures wsl-vpn-router" dialog for the 5+ minutes wsl
# install takes. Moving it out into the wrapper means everything happens in
# a console where progress is visible.
#
# Style: pure ASCII (no smart-quotes, no em-dashes, no curly quotes). Windows
# PowerShell 5.1 reads .ps1 files using the system ANSI codepage by default;
# UTF-8 multibyte chars get mojibake'd into invalid tokens. Stay safe by
# sticking to ASCII in code paths reached during installation.

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
# When invoked via irm | iex, there is no script file on disk, so we can't
# just re-run ourselves with Start-Process -Verb RunAs. Instead we save the
# script body to %TEMP%\wsl-vpn-router-install.ps1 and relaunch from there.
if (-not (Test-Admin)) {
    Step "Self-elevating (UAC will prompt once)"
    $localScript = Join-Path $env:TEMP "wsl-vpn-router-install.ps1"
    # Write THIS script's own in-memory text to the temp file rather than
    # re-downloading from raw.githubusercontent.com. The raw CDN caches
    # main/install.ps1 for ~5 min, so a re-download could hand the elevated
    # instance a STALE copy (the bug that kept resurrecting the old
    # `& powershell.exe` line). Using the body we already have guarantees the
    # elevated run is identical to what was just fetched.
    $selfText = $MyInvocation.MyCommand.ScriptBlock.ToString()
    if (-not $selfText -and $PSCommandPath) {
        $selfText = Get-Content -Raw -LiteralPath $PSCommandPath
    }
    if (-not $selfText) {
        Fail "Could not capture installer body for elevation."
    }
    Set-Content -LiteralPath $localScript -Value $selfText -Encoding Ascii
    # Relaunch the SAME PowerShell executable that's running now. We used to
    # force the 64-bit host via a Sysnative path, but Sysnative doesn't resolve
    # reliably through Start-Process -Verb RunAs (ShellExecute can't find it),
    # which failed with "The system cannot find the path specified". It's also
    # unnecessary: Resolve-InstallDir reads the 64-bit registry view explicitly
    # and vpn.exe is a 64-bit binary, so the installer's own bitness is moot.
    $psExe = [System.Diagnostics.Process]::GetCurrentProcess().MainModule.FileName
    Start-Process -FilePath $psExe `
        -ArgumentList '-NoExit', '-ExecutionPolicy', 'Bypass', '-File', $localScript `
        -Verb RunAs
    Write-Host ""
    Write-Host "    Continued in an elevated window - you can close this one." -ForegroundColor Cyan
    exit 0
}

# ---------- 1. Header ----------
Step "wsl-vpn-router installer"
Info "repo: https://github.com/$Repo"
Info "running elevated as: $env:USERNAME"

# ---------- 2 + 3. Download latest MSI (no GitHub API) ----------
# We deliberately AVOID the api.github.com REST endpoint: it's rate-limited to
# 60 requests/hour per IP unauthenticated, and shared/corporate/VPN egress IPs
# routinely have that exhausted -> "(403) Forbidden". Instead we use GitHub's
# stable web redirect:
#     https://github.com/<repo>/releases/latest/download/<asset>
# which 302-redirects to the newest release's asset with no API call. This
# requires the release to publish a STABLE, unversioned asset name; the
# release pipeline uploads wsl-vpn-router.msi (+ .sha256) alongside the
# version-stamped copies for exactly this purpose.
$RelBase = "https://github.com/$Repo/releases/latest/download"
$tmpDir = Join-Path $env:TEMP "wsl-vpn-router-install"
New-Item -ItemType Directory -Force -Path $tmpDir | Out-Null
$msiPath = Join-Path $tmpDir "wsl-vpn-router.msi"
$shaPath = Join-Path $tmpDir "wsl-vpn-router.msi.sha256"

Step "Downloading latest MSI from GitHub Releases"
Info "$RelBase/wsl-vpn-router.msi"
try {
    Invoke-WebRequest -Uri "$RelBase/wsl-vpn-router.msi" -OutFile $msiPath -UseBasicParsing
    Invoke-WebRequest -Uri "$RelBase/wsl-vpn-router.msi.sha256" -OutFile $shaPath -UseBasicParsing
} catch {
    Fail "Could not download release asset: $($_.Exception.Message)"
}
$mb = [math]::Round((Get-Item $msiPath).Length / 1048576, 1)
Info ("got wsl-vpn-router.msi ({0} MB)" -f $mb)

# ---------- 4. Verify SHA-256 ----------
Step "Verifying SHA-256"
$expected = (Get-Content $shaPath -Raw).Trim() -split '\s+' | Select-Object -First 1
$actual   = (Get-FileHash -Algorithm SHA256 $msiPath).Hash
if ($expected -ne $actual) {
    Fail "SHA-256 mismatch: expected $expected, got $actual"
}
Info "match: $actual"

# ---------- 5. msiexec (file copy + registry: fast, visible MSI dialog) ----------
Step "Installing files (file copy + registry, ~10s)"
$logPath = Join-Path $env:TEMP "wsl-vpn-router-install.log"
$msi = Start-Process -FilePath 'msiexec.exe' `
    -ArgumentList '/i', "`"$msiPath`"", '/qb', '/norestart', '/l*v', "`"$logPath`"" `
    -Wait -PassThru
if ($msi.ExitCode -ne 0) {
    Fail "msiexec returned $($msi.ExitCode). Verbose log: $logPath"
}
Info "OK"

# Resolve install dir robustly. The MSI writes HKLM\Software\wsl-vpn-router\
# InstallDir in the 64-bit registry view, but a 32-bit PowerShell would read
# the WOW6432Node view and miss it. So: (1) read the 64-bit view explicitly
# via the .NET registry API, (2) fall back to the canonical Program Files path
# (ProgramW6432 always resolves to the real 64-bit Program Files, even from a
# 32-bit process). We never hard-fail here.
function Resolve-InstallDir {
    try {
        $base = [Microsoft.Win32.RegistryKey]::OpenBaseKey('LocalMachine', 'Registry64')
        $k = $base.OpenSubKey('Software\wsl-vpn-router')
        if ($k) {
            $v = $k.GetValue('InstallDir')
            $k.Close()
            if ($v) { return $v.TrimEnd('\') }
        }
    } catch { }
    $pf = $env:ProgramW6432
    if (-not $pf) { $pf = $env:ProgramFiles }
    return (Join-Path $pf 'wsl-vpn-router')
}
$installDir = Resolve-InstallDir
if (-not (Test-Path (Join-Path $installDir 'vpn.exe'))) {
    Fail "Could not locate vpn.exe under '$installDir' after MSI install. Verbose log: $logPath"
}
Info "installed to: $installDir"

# ---------- 6. Provision WSL (visible output) ----------
Step "Provisioning WSL (this can take several minutes on a fresh machine)"
Info "Downloads ~150 MB if Ubuntu isn't already installed. Apt installs ~50 MB more."
Info "All output streams live below; no silent waiting:"
Write-Host ""
# Resolve the running PowerShell's own exe by absolute path. A bare
# `powershell.exe` failed with CommandNotFoundException on machines whose
# elevated session PATH doesn't include System32. (Get-Process -Id $PID).Path
# is always the full path to the host we're already running under.
$psHost = (Get-Process -Id $PID).Path
if (-not $psHost) { $psHost = Join-Path $PSHOME 'powershell.exe' }
& $psHost -NoProfile -ExecutionPolicy Bypass -File "$installDir\wslprovision.ps1"
$rc = $LASTEXITCODE
if ($rc -ne 0) {
    Warn "wslprovision.ps1 exited with $rc - you can re-run it later from an elevated shell:"
    Info "    & `"$installDir\wslprovision.ps1`""
}

# ---------- 7. First-run configuration wizard ----------
Step "Configuring (interactive)"
Info "Writes %LOCALAPPDATA%\wsl-vpn-router\config.toml - your VPN credentials."
Write-Host ""
& "$installDir\vpn.exe" configure
$rc = $LASTEXITCODE
if ($rc -ne 0) {
    Warn "vpn configure exited with $rc - re-run any time with: vpn configure"
}

# ---------- 8. Autostart prompt ----------
Step "Tray autostart at Windows login?"
Info "Enabling autostart writes an HKCU Run entry that launches vpn-tray.exe at"
Info "every login. Per-user, no admin needed (despite this elevated session, the"
Info "Run key written via vpn.exe goes under the LOGGED-ON user's HKCU)."
$ans = Read-Host "Enable autostart? [Y/n]"
if ([string]::IsNullOrWhiteSpace($ans) -or $ans -match '^[Yy]') {
    & "$installDir\vpn.exe" autostart enable
} else {
    Info "Skipped. Run 'vpn autostart enable' any time to turn it on."
}

# ---------- 9. Done ----------
Step "Done."
Info "Try: vpn up        (brings the tunnel up)"
Info "     vpn status    (health check)"
Info "     Start Menu -> wsl-vpn-router  (launches the tray)"
Info ""
Info "Re-running this installer upgrades in place. See 'vpn upgrade' for the in-app path."
Write-Host ""
Write-Host "Press any key to close this window..." -ForegroundColor DarkGray
$null = [System.Console]::ReadKey($true)
