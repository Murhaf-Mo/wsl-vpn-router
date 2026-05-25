// vpn-tray — windowsgui twin of vpn.exe that only runs the tray UI.
// No console window appears on launch.
package main

import (
	"os"
	"path/filepath"

	"github.com/vpn/controller/internal/cli"
	"github.com/vpn/controller/internal/tray"
)

func main() {
	env, err := cli.LoadEnv()
	if err != nil {
		// No console available; write a tiny crash log next to the exe.
		exe, _ := os.Executable()
		_ = os.WriteFile(filepath.Join(filepath.Dir(exe), "vpn-tray.err"),
			[]byte(err.Error()+"\n"), 0o644)
		os.Exit(1)
	}
	tray.Run(env)
}
