@echo off
rem wslprovision.cmd — installed alongside vpn.exe; called by the MSI as a
rem deferred elevated custom action.
rem
rem Responsibilities (each best-effort and idempotent):
rem   1. Enable Microsoft-Windows-Subsystem-Linux + VirtualMachinePlatform
rem      features if either is disabled. Requires admin (we are).
rem   2. Ensure a usable WSL distro is registered. We don't pick or override
rem      the user's existing distro — if one non-docker-desktop distro is
rem      already present, we use that. Otherwise we install Ubuntu.
rem   3. Run setup-wsl.sh inside the chosen distro to apt-install
rem      openconnect / tinyproxy / python3 / iptables.
rem
rem Exit code is informational; the MSI swallows it (CA Return="ignore") so
rem a transient WSL hiccup doesn't roll back a successful binary install.

setlocal EnableExtensions EnableDelayedExpansion

set "INSTALL_DIR=%~dp0"
if "%INSTALL_DIR:~-1%"=="\" set "INSTALL_DIR=%INSTALL_DIR:~0,-1%"

echo [wslprovision] InstallDir: %INSTALL_DIR%

rem ---- 1. Ensure WSL features are enabled ----
rem dism's /Get-FeatureInfo is verbose. We grep its State line.
for /f "tokens=2 delims=:" %%S in ('dism /online /Get-FeatureInfo /FeatureName:Microsoft-Windows-Subsystem-Linux 2^>nul ^| findstr /R /C:"^State :"') do (
    set "WSL_STATE=%%S"
)
set "WSL_STATE=%WSL_STATE: =%"
if /I not "%WSL_STATE%"=="Enabled" (
    echo [wslprovision] Enabling Microsoft-Windows-Subsystem-Linux
    dism /online /Enable-Feature /FeatureName:Microsoft-Windows-Subsystem-Linux /All /NoRestart >nul 2>&1
)

for /f "tokens=2 delims=:" %%S in ('dism /online /Get-FeatureInfo /FeatureName:VirtualMachinePlatform 2^>nul ^| findstr /R /C:"^State :"') do (
    set "VMP_STATE=%%S"
)
set "VMP_STATE=%VMP_STATE: =%"
if /I not "%VMP_STATE%"=="Enabled" (
    echo [wslprovision] Enabling VirtualMachinePlatform
    dism /online /Enable-Feature /FeatureName:VirtualMachinePlatform /All /NoRestart >nul 2>&1
)

rem ---- 2. Pick / install a distro ----
rem Powershell tucked into a one-liner to handle wsl.exe's UTF-16 output.
for /f "usebackq delims=" %%D in (`powershell -NoProfile -Command "(wsl.exe -l -q 2>$null) -replace '\0','' -split \"`r`n\" ^| Where-Object { $_ -and $_ -notmatch '^docker-desktop$' } ^| Select-Object -First 1"`) do (
    set "DISTRO=%%D"
)

if "%DISTRO%"=="" (
    echo [wslprovision] No suitable distro found; installing Ubuntu
    rem --web-download bypasses Microsoft Store throttling (the failure mode
    rem we saw most often during manual installs).
    wsl.exe --update 2>&1
    wsl.exe --install -d Ubuntu --no-launch --web-download 2>&1
    if errorlevel 1 (
        echo [wslprovision] wsl --install failed
        echo [wslprovision] Re-run `setup-wsl.sh` manually after creating a Linux user.
        exit /b 0
    )
    set "DISTRO=Ubuntu"
    rem A fresh distro has no default user yet; user must launch `wsl -d Ubuntu`
    rem once after install to create one. The config wizard will guide them.
    echo [wslprovision] Ubuntu installed. User must run `wsl -d Ubuntu` once
    echo                to create their Linux user/password before `vpn up`.
    exit /b 0
)

echo [wslprovision] Using distro: %DISTRO%

rem ---- 3. Run setup-wsl.sh inside it (idempotent apt) ----
rem We can't supply the sudo password here — it's interactive. setup-wsl.sh
rem will sudo-prompt the user. If they've configured NOPASSWD it's seamless.
rem The script is in the WSL view of INSTALL_DIR.
for /f "usebackq delims=" %%P in (`wsl.exe -d %DISTRO% -e wslpath -a "%INSTALL_DIR%"`) do set "WSL_INSTALL_DIR=%%P"

echo [wslprovision] Running setup-wsl.sh in %DISTRO%
wsl.exe -d %DISTRO% -e bash "%WSL_INSTALL_DIR%/setup-wsl.sh"

echo [wslprovision] Done.
exit /b 0
