// Package winproxy owns all the Windows-side knobs that the daemon can't
// touch from inside WSL: the AutoConfigURL registry value, the User-scope
// HTTP_PROXY/HTTPS_PROXY/NO_PROXY env vars, WinINet refresh, and the
// system-wide settings broadcast.
package winproxy

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	internetSettingsKey = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`
	environmentKey      = `Environment`

	// WinINet InternetSetOption codes.
	internetOptionSettingsChanged = 39
	internetOptionRefresh         = 37
)

// SetAutoConfigURL writes HKCU\...\Internet Settings\AutoConfigURL and
// notifies WinINet so browsers/apps pick it up immediately.
func SetAutoConfigURL(url string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, internetSettingsKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if err := k.SetStringValue("AutoConfigURL", url); err != nil {
		return err
	}
	return notifyWinINet()
}

// ClearAutoConfigURL removes the value and notifies WinINet.
func ClearAutoConfigURL() error {
	k, err := registry.OpenKey(registry.CURRENT_USER, internetSettingsKey, registry.SET_VALUE)
	if err != nil {
		if err == registry.ErrNotExist {
			return nil
		}
		return err
	}
	defer k.Close()
	_ = k.DeleteValue("AutoConfigURL")
	return notifyWinINet()
}

// GetAutoConfigURL returns the current value, or "" if not set.
func GetAutoConfigURL() string {
	k, err := registry.OpenKey(registry.CURRENT_USER, internetSettingsKey, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	v, _, err := k.GetStringValue("AutoConfigURL")
	if err != nil {
		return ""
	}
	return v
}

// SetProxyEnv sets HTTP_PROXY / HTTPS_PROXY / NO_PROXY at User scope and
// broadcasts WM_SETTINGCHANGE so new processes pick them up. Also writes
// git's http.proxy / https.proxy globals for git CLIs that don't honor env.
func SetProxyEnv(proxyURL, noProxy string) error {
	if err := setUserEnv("HTTP_PROXY", proxyURL); err != nil {
		return err
	}
	if err := setUserEnv("HTTPS_PROXY", proxyURL); err != nil {
		return err
	}
	if err := setUserEnv("NO_PROXY", noProxy); err != nil {
		return err
	}
	if err := broadcastEnvChange(); err != nil {
		return err
	}
	// Best-effort git config (silently ignore if git missing).
	_ = exec.Command("git", "config", "--global", "http.proxy", proxyURL).Run()
	_ = exec.Command("git", "config", "--global", "https.proxy", proxyURL).Run()
	return nil
}

// ClearProxyEnv removes the three env vars and the git http/https proxy.
func ClearProxyEnv() error {
	_ = deleteUserEnv("HTTP_PROXY")
	_ = deleteUserEnv("HTTPS_PROXY")
	_ = deleteUserEnv("NO_PROXY")
	_ = broadcastEnvChange()
	_ = exec.Command("git", "config", "--global", "--unset", "http.proxy").Run()
	_ = exec.Command("git", "config", "--global", "--unset", "https.proxy").Run()
	return nil
}

// ---- helpers ----

func setUserEnv(name, value string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, environmentKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetStringValue(name, value)
}

func deleteUserEnv(name string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, environmentKey, registry.SET_VALUE)
	if err != nil {
		if err == registry.ErrNotExist {
			return nil
		}
		return err
	}
	defer k.Close()
	_ = k.DeleteValue(name)
	return nil
}

// BuildNoProxy concatenates direct.list entries into a NO_PROXY string.
// Patterns starting with "*." get the leading "*" stripped (so "*.example.com"
// becomes ".example.com" which env-var-honoring tools match correctly).
// Lines starting with http(s):// are skipped — env NO_PROXY doesn't model
// path-based bypass.
func BuildNoProxy(directListEntries []string) string {
	var out []string
	for _, raw := range directListEntries {
		line := raw
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "http://") || strings.HasPrefix(line, "https://") {
			continue
		}
		if strings.HasPrefix(line, "*.") {
			line = line[1:]
		}
		out = append(out, line)
	}
	return strings.Join(out, ",")
}

// ---- WinINet + broadcast (syscall) ----

var (
	winInet                        = syscall.NewLazyDLL("wininet.dll")
	procInternetSetOptionW         = winInet.NewProc("InternetSetOptionW")
	user32                         = syscall.NewLazyDLL("user32.dll")
	procSendMessageTimeoutW        = user32.NewProc("SendMessageTimeoutW")
)

const (
	hwndBroadcast   = uintptr(0xffff)
	wmSettingChange = uintptr(0x001A)
	smtoAbortIfHung = uintptr(0x0002)
)

func notifyWinINet() error {
	// SETTINGS_CHANGED
	r1, _, e := procInternetSetOptionW.Call(0, internetOptionSettingsChanged, 0, 0)
	if r1 == 0 && e != nil && e.Error() != "The operation completed successfully." {
		// non-fatal
	}
	// REFRESH (re-fetch the PAC file)
	r1, _, e = procInternetSetOptionW.Call(0, internetOptionRefresh, 0, 0)
	if r1 == 0 && e != nil && e.Error() != "The operation completed successfully." {
		// non-fatal
	}
	return nil
}

// broadcastEnvChange tells other Windows processes that environment vars
// have changed (HWND_BROADCAST, WM_SETTINGCHANGE, lParam="Environment").
// Without this, only NEW processes pick up SetUserEnv changes; with it,
// at least Explorer-spawned children get the update.
func broadcastEnvChange() error {
	target, err := windows.UTF16PtrFromString("Environment")
	if err != nil {
		return err
	}
	var out uintptr
	procSendMessageTimeoutW.Call(
		hwndBroadcast,
		wmSettingChange,
		0,
		uintptr(unsafe.Pointer(target)),
		smtoAbortIfHung,
		5000,
		uintptr(unsafe.Pointer(&out)),
	)
	return nil
}

// PrintProxyEnv prints the User-scope values for diagnostics.
func PrintProxyEnv() string {
	k, err := registry.OpenKey(registry.CURRENT_USER, environmentKey, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	var b strings.Builder
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY"} {
		v, _, _ := k.GetStringValue(name)
		fmt.Fprintf(&b, "%s=%s\n", name, v)
	}
	return b.String()
}
