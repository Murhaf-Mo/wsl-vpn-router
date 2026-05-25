package winproxy

import (
	"fmt"

	"golang.org/x/sys/windows/registry"
)

const (
	runKeyPath     = `Software\Microsoft\Windows\CurrentVersion\Run`
	autostartValue = "VpnTray"
)

// EnableAutostart adds an HKCU Run entry pointing at exePath so vpn-tray.exe
// launches at every user login. We use the per-user Run key (HKCU) so no
// elevation is required.
func EnableAutostart(exePath string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	// Quote the path so a future spaces-in-path doesn't break the parse.
	return k.SetStringValue(autostartValue, fmt.Sprintf("\"%s\"", exePath))
}

// DisableAutostart removes the Run entry.
func DisableAutostart() error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		if err == registry.ErrNotExist {
			return nil
		}
		return err
	}
	defer k.Close()
	_ = k.DeleteValue(autostartValue)
	return nil
}

// AutostartStatus returns ("", false) if not set, else (exePath, true).
func AutostartStatus() (string, bool) {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return "", false
	}
	defer k.Close()
	v, _, err := k.GetStringValue(autostartValue)
	if err != nil {
		return "", false
	}
	return v, true
}
