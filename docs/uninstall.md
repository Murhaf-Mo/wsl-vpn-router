# Uninstall

## Standard

**Settings → Apps → Installed apps** → search "wsl-vpn-router" → Uninstall.

Or from PowerShell:

```powershell
$msi = Get-ChildItem 'C:\Program Files\wsl-vpn-router\wsl-vpn-router-*.msi' | Select-Object -First 1
msiexec /x $msi.FullName /qb
```

This:

- Stops `vpn-tray.exe` and `vpn.exe` (Restart Manager)
- Runs `uninstall.ps1` which:
  - Calls `vpn down` to clear `HKCU\…\AutoConfigURL`, proxy env vars, git config
  - Kills `openconnect` / `tinyproxy` / `vpnctl.py` inside the WSL distro
  - Removes `HKCU\…\Run\VpnTray` (autostart entry)
  - Removes `HKLM\Software\wsl-vpn-router`
- Deletes `C:\Program Files\wsl-vpn-router\`

**Preserved by default**: `%LOCALAPPDATA%\wsl-vpn-router\` — your config.toml, lists, and logs. So a future reinstall picks up where you left off.

## Full purge (config + lists + logs)

```powershell
& "C:\Program Files\wsl-vpn-router\uninstall.ps1" -PurgeData
msiexec /x ((Get-ChildItem 'C:\Program Files\wsl-vpn-router\wsl-vpn-router-*.msi')[0].FullName) /qb
```

Or, after the MSI uninstall:

```powershell
Remove-Item -Recurse -Force "$env:LOCALAPPDATA\wsl-vpn-router"
```

## Removing the WSL distro

The uninstaller **does not** remove the Ubuntu (or whichever) WSL distro it installed. That distro may be used by other tools on your machine, and a single tool shouldn't unilaterally drop it.

If you want to remove it:

```powershell
wsl --unregister Ubuntu     # or whichever distro you used
```

That's destructive — the distro's filesystem is gone with everything inside.

## Removing WSL entirely

Out of scope for this uninstaller. If you don't want WSL at all anymore:

```powershell
wsl --uninstall                                           # removes the WSL component
dism /online /Disable-Feature /FeatureName:Microsoft-Windows-Subsystem-Linux /NoRestart
dism /online /Disable-Feature /FeatureName:VirtualMachinePlatform /NoRestart
```

Both DISM steps need elevation, and require a reboot.

## Verifying everything is gone

```powershell
Test-Path 'C:\Program Files\wsl-vpn-router'                        # False
Test-Path 'HKLM:\Software\wsl-vpn-router'                          # False
Get-ItemProperty 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Run' -Name 'VpnTray' -EA SilentlyContinue   # nothing
Get-ItemProperty 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Internet Settings' -Name 'AutoConfigURL' -EA SilentlyContinue    # nothing
[Environment]::GetEnvironmentVariable('HTTP_PROXY','User')         # null
git config --global --get http.proxy                               # nothing
Get-Process vpn,vpn-tray -EA SilentlyContinue                       # nothing
```

If any of these still show something, run `uninstall.ps1` again (it's idempotent).

## "I installed manually from a checkout, not via MSI"

You're in dev mode (no HKLM key, everything in your checkout dir). Uninstall is:

1. Kill processes: `taskkill /F /IM vpn-tray.exe`, `taskkill /F /IM vpn.exe`
2. `.\vpn.exe down` from the checkout (clears AutoConfigURL, env vars, etc.)
3. Delete the checkout directory
4. (Optional) Remove HKCU autostart: `Remove-ItemProperty 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Run' VpnTray`
