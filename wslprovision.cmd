@echo off
rem wslprovision.cmd - installed alongside vpn.exe; invoked by install.ps1
rem (the irm one-liner) AFTER msiexec finishes the file-copy phase. Runs in
rem the user's already-elevated PowerShell console so every line of DISM,
rem wsl --update, wsl --install, and apt output is visible live.
rem
rem Can also be run manually any time:
rem     "C:\Program Files\wsl-vpn-router\wslprovision.cmd"
rem (requires an elevated shell)
rem
rem Responsibilities (each best-effort and idempotent):
rem   1. Enable Microsoft-Windows-Subsystem-Linux + VirtualMachinePlatform
rem      features if either is disabled. Requires admin (we are).
rem   2. Ensure a usable WSL distro is registered. We don't pick or override
rem      the user's existing distro - if one non-docker-desktop distro is
rem      already present, we use that. Otherwise we install Ubuntu.
rem   3. Run setup-wsl.sh inside the chosen distro to apt-install
rem      openconnect / tinyproxy / python3 / iptables.
rem
rem Exit code: 0 on success, non-zero on hard failure. install.ps1 surfaces
rem the code but does not abort the rest of the install - wsl setup can be
rem retried later by re-running this script.

setlocal EnableExtensions EnableDelayedExpansion

set "INSTALL_DIR=%~dp0"
if "%INSTALL_DIR:~-1%"=="\" set "INSTALL_DIR=%INSTALL_DIR:~0,-1%"

echo.
echo === wslprovision (WSL setup for wsl-vpn-router) ===
echo.
echo Install dir: %INSTALL_DIR%
echo Started:     %DATE% %TIME%
echo.

rem ---- 1. Ensure WSL features are enabled ----
echo [1/3] Checking Windows features (WSL + VirtualMachinePlatform)...
for /f "tokens=2 delims=:" %%S in ('dism /online /Get-FeatureInfo /FeatureName:Microsoft-Windows-Subsystem-Linux 2^>nul ^| findstr /R /C:"^State :"') do (
    set "WSL_STATE=%%S"
)
set "WSL_STATE=%WSL_STATE: =%"
if /I not "%WSL_STATE%"=="Enabled" (
    echo       Enabling Microsoft-Windows-Subsystem-Linux (this is a one-time step)...
    dism /online /Enable-Feature /FeatureName:Microsoft-Windows-Subsystem-Linux /All /NoRestart
    echo.
) else (
    echo       Microsoft-Windows-Subsystem-Linux: already enabled.
)

for /f "tokens=2 delims=:" %%S in ('dism /online /Get-FeatureInfo /FeatureName:VirtualMachinePlatform 2^>nul ^| findstr /R /C:"^State :"') do (
    set "VMP_STATE=%%S"
)
set "VMP_STATE=%VMP_STATE: =%"
if /I not "%VMP_STATE%"=="Enabled" (
    echo       Enabling VirtualMachinePlatform (this is a one-time step)...
    dism /online /Enable-Feature /FeatureName:VirtualMachinePlatform /All /NoRestart
    echo.
) else (
    echo       VirtualMachinePlatform: already enabled.
)

rem ---- 2. Pick / install a distro ----
echo.
echo [2/3] Checking for usable WSL distro...
for /f "usebackq delims=" %%D in (`powershell -NoProfile -Command "(wsl.exe -l -q 2>$null) -replace '\0','' -split \"`r`n\" ^| Where-Object { $_ -and $_ -notmatch '^docker-desktop$' } ^| Select-Object -First 1"`) do (
    set "DISTRO=%%D"
)

if "%DISTRO%"=="" (
    echo       No suitable distro registered. Installing Ubuntu.
    echo       (downloads ~150 MB; takes several minutes the first time)
    echo.
    echo       Running: wsl --update
    wsl.exe --update
    echo.
    echo       Running: wsl --install -d Ubuntu --no-launch --web-download
    wsl.exe --install -d Ubuntu --no-launch --web-download
    if errorlevel 1 (
        echo.
        echo XX  wsl --install failed.
        echo     Re-run this script after manually installing a WSL distro.
        exit /b 1
    )
    set "DISTRO=Ubuntu"
    echo.
    echo       Ubuntu installed. Next manual step:
    echo           wsl -d Ubuntu
    echo       to create your Linux user + password before `vpn up`.
    echo       Re-run this script after that, then run `vpn configure`.
    exit /b 0
)

echo       Using distro: %DISTRO%

rem ---- 3. Run setup-wsl.sh inside it (idempotent apt) ----
echo.
echo [3/3] Installing apt packages in %DISTRO% (openconnect, tinyproxy, python3, iptables)
echo       sudo password will be prompted by the script unless NOPASSWD is set.
echo       apt output streams below:
echo.
for /f "usebackq delims=" %%P in (`wsl.exe -d %DISTRO% -e wslpath -a "%INSTALL_DIR%"`) do set "WSL_INSTALL_DIR=%%P"

wsl.exe -d %DISTRO% -e bash "%WSL_INSTALL_DIR%/setup-wsl.sh"
if errorlevel 1 (
    echo.
    echo XX  setup-wsl.sh failed inside %DISTRO%.
    echo     Re-run this script, or run setup-wsl.sh manually:
    echo         wsl -d %DISTRO% -e bash %WSL_INSTALL_DIR%/setup-wsl.sh
    exit /b 1
)

echo.
echo === wslprovision done ===
exit /b 0
