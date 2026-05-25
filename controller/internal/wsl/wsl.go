// Package wsl owns interactions with the WSL distro: running short
// commands, launching the daemon as a hidden Windows-side process (keepalive
// trick to defeat vmIdleTimeout), syncing Windows hosts → WSL /etc/hosts.
package wsl

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/windows/registry"
)

const (
	// AppKey is the HKLM key the MSI writes on install. Its presence is the
	// "am I installed?" signal; its InstallDir value tells us where the
	// binaries + read-only assets (vpnctl.py, static/) live.
	AppKey        = `Software\wsl-vpn-router`
	AppKeyValue   = "InstallDir"
	AppDirName    = "wsl-vpn-router" // used under %LOCALAPPDATA%
)

// SysExe returns the absolute path to a Windows System32 executable.
//
// Go 1.19+ refuses to run a bare executable name (no path separator) when it
// would resolve via the current working directory, failing with "cannot run
// executable found relative to current directory". That bites us when vpn.exe
// runs with its CWD inside C:\Program Files\wsl-vpn-router. Resolving to the
// absolute %SystemRoot%\System32\<name> path sidesteps the check.
func SysExe(name string) string {
	if root := os.Getenv("SystemRoot"); root != "" {
		p := filepath.Join(root, "System32", name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return name
}

// Run executes `wsl.exe -d <distro> -e bash -c <script>` and returns stdout.
// stderr is captured and returned in the error message on non-zero exit.
func Run(distro, script string) (string, error) {
	cmd := exec.Command(SysExe("wsl.exe"), "-d", distro, "-e", "bash", "-c", script)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("wsl bash: %w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// RunBase64 base64-encodes the script before passing it to bash. wsl.exe's
// argv stays plain ASCII (so no shell-quoting surprises) and bash inside WSL
// decodes and runs the real script. Used for snippets with embedded quotes /
// heredocs / sudo-piped passwords that don't survive naive argument passing.
func RunBase64(distro, script string) (string, error) {
	enc := base64.StdEncoding.EncodeToString([]byte(script))
	return Run(distro, "echo "+enc+" | base64 -d | bash")
}

// IP returns the WSL distro's eth0 IPv4 (reachable from Windows).
func IP(distro string) (string, error) {
	out, err := Run(distro, `hostname -I | awk '{print $1}'`)
	if err != nil {
		return "", err
	}
	ip := strings.TrimSpace(out)
	if ip == "" {
		return "", fmt.Errorf("no IPv4 on eth0")
	}
	return ip, nil
}

// WSLPath converts a Windows path (e.g. C:\Users\...\vpn) to a WSL path
// (e.g. /mnt/c/Users/.../vpn) using `wslpath -a`.
func WSLPath(distro, winPath string) (string, error) {
	cmd := exec.Command(SysExe("wsl.exe"), "-d", distro, "-e", "wslpath", "-a", winPath)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// LaunchDaemon spawns `wsl.exe -d <distro> -e python3 <wslInstallDir>/vpnctl.py
// --data-dir <wslDataDir>` as a hidden Windows-side process, writes its PID to
// keepalivePath, and returns. The Windows-side wsl.exe must outlive the daemon —
// WSL2's idle timeout would shut the VM down within ~60s otherwise.
//
// Note: a bash wrapper here doesn't survive Start-Process-style detached
// launch, so vpnctl.py self-redirects stdout/stderr to pac.log instead.
//
// Both paths are quoted because the installed location is `C:\Program Files\…`
// which translates to `/mnt/c/Program Files/…` in WSL — the embedded space
// would otherwise be argv-split by Windows commandline parsing.
func LaunchDaemon(distro, wslInstallDir, wslDataDir, keepalivePath string) error {
	// wsl.exe ALWAYS spawns a conhost (console window) when launched via
	// CreateProcess (Go's os/exec), regardless of CREATE_NO_WINDOW / HideWindow.
	// PowerShell's Start-Process -WindowStyle Hidden works because it uses
	// ShellExecute under the hood. We do the same here via ShellExecuteExW
	// (SW_HIDE), which fully suppresses the conhost. Trade-off: ShellExecute
	// doesn't return a PID directly, so we get it from the SHELLEXECUTEINFO
	// hProcess and convert with GetProcessId.
	args := fmt.Sprintf(`-d %s -e python3 "%s/vpnctl.py" --data-dir "%s"`, distro, wslInstallDir, wslDataDir)
	pid, err := shellExecuteHiddenPID("wsl.exe", args, "")
	if err != nil {
		return err
	}
	if err := os.WriteFile(keepalivePath, []byte(strconv.Itoa(int(pid))), 0o644); err != nil {
		return err
	}
	return nil
}

// StopDaemon reads keepalivePath and terminates that PID if it's still ours
// (a wsl.exe). Killing the Windows-side wsl.exe propagates SIGTERM to the
// Linux daemon, which then runs its cleanup trap to tear down openconnect +
// tinyproxy via sudo.
func StopDaemon(keepalivePath string) error {
	data, err := os.ReadFile(keepalivePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		_ = os.Remove(keepalivePath)
		return nil
	}
	// Verify it's a wsl process before killing it (avoid hitting unrelated PIDs).
	if isWslProcess(pid) {
		proc, err := os.FindProcess(pid)
		if err == nil {
			_ = proc.Kill()
		}
	}
	_ = os.Remove(keepalivePath)
	return nil
}

// SyncHostsToWSL copies IP/host lines from C:\Windows\System32\drivers\etc\hosts
// into <distro>:/etc/hosts under a managed marker block. Without this,
// tinyproxy can't resolve internal hosts pinned in the Windows hosts file.
func SyncHostsToWSL(distro, sudoPassword string) (string, error) {
	const marker = "wsl-vpn-router managed"
	script := fmt.Sprintf(`echo %s | sudo -S bash -c '
sed -i "/# >>> %s >>>/,/# <<< %s <<</d" /etc/hosts
{
  echo
  echo "# >>> %s >>>"
  grep -E "^[[:space:]]*[0-9]+\." /mnt/c/Windows/System32/drivers/etc/hosts | grep -v "^[[:space:]]*#"
  echo "# <<< %s <<<"
} >> /etc/hosts
' 2>/dev/null
c=$(grep -c "^[[:space:]]*[0-9]" /etc/hosts 2>/dev/null || echo 0)
echo "[hosts] synced ($c host entries in WSL /etc/hosts)"`,
		shellQuote(sudoPassword), marker, marker, marker, marker)
	return RunBase64(distro, script)
}

// KillResidual is a safety net: pkill openconnect/tinyproxy and clean stale
// pid files. Used by `vpn down` after StopDaemon, in case the trap didn't
// finish before SIGKILL. Pid files live under <wslDataDir>/runtime/.
func KillResidual(distro, sudoPassword, wslDataDir string) error {
	rt := wslDataDir + "/runtime"
	script := fmt.Sprintf(`
[ -f %s/pac.pid ] && kill $(cat %s/pac.pid) 2>/dev/null
echo %s | sudo -S bash -c 'pkill tinyproxy ; pkill openconnect' 2>/dev/null
rm -f %s/oc.pid %s/proxy.pid %s/pac.pid /tmp/tinyproxy.pid 2>/dev/null
true
`, rt, rt, shellQuote(sudoPassword), rt, rt, rt)
	_, err := RunBase64(distro, script)
	return err
}

// ---- helpers ----

// shellQuote single-quotes a string for bash. We only need this for the sudo
// password, which is typically alphanumeric anyway.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// isWslProcess returns true if PID exists and its image name is "wsl.exe".
// Uses tasklist.exe — no native Win32 calls needed.
func isWslProcess(pid int) bool {
	out, err := exec.Command(SysExe("tasklist.exe"), "/FI", "PID eq "+strconv.Itoa(pid), "/FO", "CSV", "/NH").Output()
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(out)), `"wsl.exe"`)
}

// IsInstalled reports whether we're running as an installed product (vs a dev
// checkout). True when EITHER:
//   - the HKLM marker key the MSI writes is present, OR
//   - vpn.exe lives under a Program Files directory.
//
// The second check is a safety net: if the registry key is ever missing
// (failed write, manual file copy, AV stripping), an exe sitting in Program
// Files is still unmistakably "installed" — and we must route config.toml to
// %LOCALAPPDATA% rather than trying to write under read-only Program Files.
//
// In dev mode (running from a checkout under e.g. E:\...), both checks fail and
// the install + data layout collapses to a single directory, preserving the
// pre-1.0 single-directory project layout for hacking without an MSI build.
func IsInstalled() bool {
	if k, err := registry.OpenKey(registry.LOCAL_MACHINE, AppKey, registry.QUERY_VALUE|registry.WOW64_64KEY); err == nil {
		defer k.Close()
		if v, _, err := k.GetStringValue(AppKeyValue); err == nil && v != "" {
			return true
		}
	}
	return runningFromProgramFiles()
}

// runningFromProgramFiles reports whether the current executable is under one
// of the Program Files roots (native or x86). Case-insensitive prefix match.
func runningFromProgramFiles() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	exeLower := strings.ToLower(filepath.Clean(exe))
	for _, env := range []string{"ProgramW6432", "ProgramFiles", "ProgramFiles(x86)"} {
		if root := os.Getenv(env); root != "" {
			rootLower := strings.ToLower(filepath.Clean(root)) + string(filepath.Separator)
			if strings.HasPrefix(exeLower, rootLower) {
				return true
			}
		}
	}
	return false
}

// InstallDir returns the directory containing vpn.exe, vpnctl.py, static/, and
// other read-only assets shipped by the MSI.
//
//   - Installed:  HKLM\Software\wsl-vpn-router\InstallDir (e.g. "C:\Program Files\wsl-vpn-router")
//   - Dev:        the directory containing the running vpn.exe (a checkout)
func InstallDir() (string, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, AppKey, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err == nil {
		defer k.Close()
		if v, _, err := k.GetStringValue(AppKeyValue); err == nil && v != "" {
			return v, nil
		}
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Dir(exe), nil
}

// DataDir returns the writable per-user directory holding config.toml,
// vpn.list, direct.list, and runtime/ (logs, pid files, generated configs).
//
//   - Installed:  %LOCALAPPDATA%\wsl-vpn-router  (created if missing)
//   - Dev:        same as InstallDir() — keeps the pre-1.0 single-dir layout
//                 working from a checkout without any extra setup.
func DataDir() (string, error) {
	if !IsInstalled() {
		return InstallDir()
	}
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		return "", errors.New("LOCALAPPDATA not set")
	}
	dir := filepath.Join(base, AppDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}
