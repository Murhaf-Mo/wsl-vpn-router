# Cross-compiles the Go controller in WSL and drops vpn.exe next to vpnctl.py.
# Requires: Go installed in the WSL distro named in config.toml ([wsl] distro).
#
# Usage:  .\build.ps1            (release, stripped)
#         .\build.ps1 -Debug     (keep symbols, no -s -w)

param([switch]$Debug)

$ErrorActionPreference = 'Stop'
$root = $PSScriptRoot
$cfg  = Get-Content "$root\config.toml" -Raw
if ($cfg -match '(?m)^\s*distro\s*=\s*"([^"]+)"') { $distro = $matches[1] }
else { throw 'Could not find [wsl] distro in config.toml' }

$wslRoot = (& wsl.exe -d $distro -e wslpath -a $root).Trim()
if ($LASTEXITCODE -ne 0 -or -not $wslRoot) { throw "Could not resolve $root in $distro" }

$ld = if ($Debug) { '' } else { "-ldflags='-s -w'" }

# -buildvcs=false: WSL git can't read /mnt/e Windows-side repo metadata cleanly
# ("error obtaining VCS status: exit status 128"). We don't need the VCS stamp.
$vcsOff = '-buildvcs=false'

Write-Host "[build] cross-compiling vpn.exe (CLI) in $distro..."
$buildCli = "cd $wslRoot/controller && GOOS=windows GOARCH=amd64 go build $vcsOff $ld -o $wslRoot/vpn.exe ./cmd/vpn"
& wsl.exe -d $distro -e bash -c $buildCli
if ($LASTEXITCODE -ne 0) { throw "vpn.exe build failed (exit $LASTEXITCODE)" }

Write-Host "[build] cross-compiling vpn-tray.exe (windowsgui)..."
# -H windowsgui suppresses the console window on launch (so double-click is clean).
$ldTray = if ($Debug) { "-ldflags='-H windowsgui'" } else { "-ldflags='-s -w -H windowsgui'" }
$buildTray = "cd $wslRoot/controller && GOOS=windows GOARCH=amd64 go build $vcsOff $ldTray -o $wslRoot/vpn-tray.exe ./cmd/vpn-tray"
& wsl.exe -d $distro -e bash -c $buildTray
if ($LASTEXITCODE -ne 0) { throw "vpn-tray.exe build failed (exit $LASTEXITCODE)" }

Get-Item "$root\vpn.exe", "$root\vpn-tray.exe" | ForEach-Object {
    "{0,-15} {1,10:N0} bytes  {2}" -f $_.Name, $_.Length, $_.LastWriteTime | Write-Host
}
Write-Host "[build] done."
