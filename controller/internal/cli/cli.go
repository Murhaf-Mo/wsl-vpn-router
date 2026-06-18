// Package cli implements all `vpn <verb>` actions. Verbs that need the
// daemon talk to it via the api package; verbs that touch Windows state
// (registry, env vars, hosts file, daemon process lifecycle) use winproxy
// and wsl packages. The tray UI calls into the same functions so behavior
// stays identical between CLI and GUI.
package cli

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/vpn/controller/internal/api"
	"github.com/vpn/controller/internal/config"
	"github.com/vpn/controller/internal/release"
	"github.com/vpn/controller/internal/winproxy"
	"github.com/vpn/controller/internal/wsl"
)

// Env bundles everything verbs need to operate.
//
// We carry two roots because the MSI ships read-only assets to Program Files
// while writable per-user state (config.toml, *.list, runtime/) lives under
// %LOCALAPPDATA%. In dev mode (no MSI installed) InstallDir == DataDir, so
// the controller still runs from a single checkout directory.
type Env struct {
	InstallDir    string // ships with binaries: vpnctl.py, static/, vpn.list/direct.list defaults
	DataDir       string // user-writable: config.toml, vpn.list, direct.list, runtime/
	WSLInstallDir string // InstallDir translated for the WSL distro
	WSLDataDir    string // DataDir translated for the WSL distro
	Cfg           *config.Config
	KeepalivePID  string // <DataDir>\runtime\wsl-keepalive.pid
}

// seedFromInstallDir copies seed.list from src to dst if dst doesn't exist.
// First-run bootstrap so a freshly-installed user gets the maintainer's default
// vpn.list / direct.list without having to populate them by hand.
func seedFromInstallDir(src, dst string) {
	if _, err := os.Stat(dst); err == nil {
		return // already present, don't clobber
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return // no template to seed from (or unreadable) — silently skip
	}
	_ = os.WriteFile(dst, data, 0o644)
}

func LoadEnv() (*Env, error) {
	installDir, err := wsl.InstallDir()
	if err != nil {
		return nil, err
	}
	dataDir, err := wsl.DataDir()
	if err != nil {
		return nil, err
	}
	// First-run seeding (no-op when DataDir == InstallDir).
	if dataDir != installDir {
		seedFromInstallDir(filepath.Join(installDir, "vpn.list"), filepath.Join(dataDir, "vpn.list"))
		seedFromInstallDir(filepath.Join(installDir, "direct.list"), filepath.Join(dataDir, "direct.list"))
	}
	cfgPath := filepath.Join(dataDir, "config.toml")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no config.toml at %s — run `vpn configure` to create one", cfgPath)
		}
		return nil, fmt.Errorf("loading %s: %w", cfgPath, err)
	}
	if cfg.WSLDistro == "" {
		return nil, errors.New("config.toml: [wsl] distro is required")
	}
	wslInstallDir, err := wsl.WSLPath(cfg.WSLDistro, installDir)
	if err != nil {
		return nil, fmt.Errorf("translating install dir %s to WSL path: %w", installDir, err)
	}
	wslDataDir := wslInstallDir
	if dataDir != installDir {
		wslDataDir, err = wsl.WSLPath(cfg.WSLDistro, dataDir)
		if err != nil {
			return nil, fmt.Errorf("translating data dir %s to WSL path: %w", dataDir, err)
		}
	}
	// runtime/ holds logs, pid files, and the generated tinyproxy.conf. We
	// create it here (rather than letting the daemon do it on first launch)
	// so that `vpn down`'s residual cleanup can reference paths under it
	// even when the daemon has never run.
	runtimeDir := filepath.Join(dataDir, "runtime")
	_ = os.MkdirAll(runtimeDir, 0o755)
	return &Env{
		InstallDir:    installDir,
		DataDir:       dataDir,
		WSLInstallDir: wslInstallDir,
		WSLDataDir:    wslDataDir,
		Cfg:           cfg,
		KeepalivePID:  filepath.Join(runtimeDir, "wsl-keepalive.pid"),
	}, nil
}

// ---- up ----

func Up(e *Env) error {
	fmt.Println("[up] syncing Windows hosts -> WSL /etc/hosts...")
	if out, err := wsl.SyncHostsToWSL(e.Cfg.WSLDistro, e.Cfg.SudoPassword); err != nil {
		fmt.Printf("[up] hosts sync warning: %v\n", err)
	} else {
		fmt.Print(strings.TrimRight(out, "\n") + "\n")
	}

	fmt.Printf("[up] launching vpnctl.py in %s...\n", e.Cfg.WSLDistro)
	// Replace any previous keepalive.
	_ = wsl.StopDaemon(e.KeepalivePID)
	if err := wsl.LaunchDaemon(e.Cfg.WSLDistro, e.WSLInstallDir, e.WSLDataDir, release.LocalVersion(), e.KeepalivePID); err != nil {
		return fmt.Errorf("launching daemon: %w", err)
	}

	// Wait for /api/status to respond (daemon fully up).
	wslIP, err := waitForDaemon(e)
	if err != nil {
		return err
	}
	fmt.Printf("[up] WSL IP: %s\n", wslIP)

	pacURL := fmt.Sprintf("http://%s:%d/proxy.pac", wslIP, e.Cfg.PACPort)
	if err := winproxy.SetAutoConfigURL(pacURL); err != nil {
		return fmt.Errorf("setting AutoConfigURL: %w", err)
	}
	fmt.Printf("[up] AutoConfigURL = %s\n", pacURL)

	// Env vars: only meaningful in vpn-default mode (env-honoring tools always
	// hit the proxy with NO_PROXY exceptions). In direct-default, clear them.
	mode := e.Cfg.Mode
	fmt.Printf("[up] mode          = %s\n", mode)
	if mode == "vpn-default" {
		proxyURL := fmt.Sprintf("http://%s:%d", wslIP, e.Cfg.HTTPPort)
		noProxy, _ := buildNoProxy(e)
		if err := winproxy.SetProxyEnv(proxyURL, noProxy); err != nil {
			return fmt.Errorf("setting proxy env: %w", err)
		}
		fmt.Printf("[up] HTTP(S)_PROXY = %s\n", proxyURL)
		fmt.Printf("[up] NO_PROXY      = %s\n", noProxy)
	} else {
		_ = winproxy.ClearProxyEnv()
		fmt.Println("[up] HTTP(S)_PROXY = (cleared - direct-default mode)")
	}
	fmt.Println("[up] (open IDEs/terminals must be restarted to inherit new env vars)")
	fmt.Println("[up] done.")
	return Status(e)
}

func buildNoProxy(e *Env) (string, error) {
	// Prefer the daemon's view (mtime-fresh); fall back to reading the file
	// directly if the daemon isn't up yet.
	client := newClient(e)
	if entries, err := client.Quick().ListEntries("direct"); err == nil {
		return winproxy.BuildNoProxy(entries), nil
	}
	data, err := os.ReadFile(filepath.Join(e.DataDir, "direct.list"))
	if err != nil {
		return "", err
	}
	return winproxy.BuildNoProxy(strings.Split(string(data), "\n")), nil
}

func waitForDaemon(e *Env) (string, error) {
	// Daemon startup includes a synchronous openconnect handshake (~15-45s
	// depending on the VPN's auth path). Give it up to 60s, and print a
	// heartbeat every 5s so the user knows we're still waiting.
	client := newClient(e).Quick()
	deadline := time.Now().Add(60 * time.Second)
	lastBeat := time.Now()
	for time.Now().Before(deadline) {
		s, err := client.Status()
		if err == nil && s.WSLIp != "" {
			return s.WSLIp, nil
		}
		if time.Since(lastBeat) >= 5*time.Second {
			fmt.Println("[up] waiting for daemon (openconnect handshake)...")
			lastBeat = time.Now()
		}
		time.Sleep(700 * time.Millisecond)
	}
	ip, err := wsl.IP(e.Cfg.WSLDistro)
	if err != nil || ip == "" {
		return "", fmt.Errorf("daemon did not respond and could not get WSL IP: %v", err)
	}
	return ip, fmt.Errorf("daemon did not respond on http://%s:%d/api/status after 60s — check pac.log", ip, e.Cfg.PACPort)
}

// ---- down ----

func Down(e *Env) error {
	fmt.Println("[down] stopping daemon keepalive...")
	if err := wsl.StopDaemon(e.KeepalivePID); err != nil {
		fmt.Printf("[down] stop warning: %v\n", err)
	}

	fmt.Println("[down] killing residual processes...")
	if err := wsl.KillResidual(e.Cfg.WSLDistro, e.Cfg.SudoPassword, e.WSLDataDir); err != nil {
		fmt.Printf("[down] residual cleanup warning: %v\n", err)
	}

	fmt.Println("[down] clearing AutoConfigURL...")
	if err := winproxy.ClearAutoConfigURL(); err != nil {
		fmt.Printf("[down] clear AutoConfigURL warning: %v\n", err)
	}

	fmt.Println("[down] clearing proxy env vars + git proxy config...")
	_ = winproxy.ClearProxyEnv()
	fmt.Println("[down] done.")
	return nil
}

// ---- status ----

func Status(e *Env) error {
	auto := winproxy.GetAutoConfigURL()
	fmt.Println("=== VPN Status ===")
	if auto == "" {
		fmt.Println("Windows AutoConfigURL: (unset)")
	} else {
		fmt.Println("Windows AutoConfigURL:", auto)
	}
	fmt.Println()
	fmt.Println("--- WSL daemon status (/api/status) ---")
	s, err := newClient(e).Quick().Status()
	if err != nil {
		fmt.Println("(could not reach daemon:", err.Error()+")")
		return nil
	}
	printStatus(s)
	return nil
}

func printStatus(s *api.Status) {
	ok := func(b bool) string {
		if b { return "●" }
		return "○"
	}
	fmt.Printf("  %s tunnel    %s\n", ok(s.Tun0IP != ""), valOrDash(s.Tun0IP))
	if s.OcPid > 0 {
		fmt.Printf("                openconnect pid %d\n", s.OcPid)
	}
	fmt.Printf("  %s proxy     %s:%d\n", ok(s.TinyproxyPid != 0), s.WSLIp, s.ProxyPort)
	if s.TinyproxyPid > 0 {
		fmt.Printf("                tinyproxy pid %d\n", s.TinyproxyPid)
	}
	fmt.Printf("  %s daemon    pid %d\n", ok(s.PacPid != 0), s.PacPid)
	fmt.Printf("    mode      %s\n", s.Mode)
	fmt.Printf("    vpn.list  %d domains / %d cidrs / %d urls\n", s.VPNList.Domains, s.VPNList.Cidrs, s.VPNList.URLs)
	fmt.Printf("    direct    %d domains / %d cidrs / %d urls\n", s.DirectList.Domains, s.DirectList.Cidrs, s.DirectList.URLs)
}

func valOrDash(s string) string {
	if s == "" { return "—" }
	return s
}

// ---- mode ----

func Mode(e *Env, args []string) error {
	if len(args) == 0 {
		client := newClient(e).Quick()
		if m, err := client.GetMode(); err == nil {
			fmt.Println("current mode:", m)
		} else {
			fmt.Println("current mode (from config.toml):", e.Cfg.Mode)
		}
		fmt.Println("  vpn-default    : everything via VPN; vpn.list -> VPN (force), direct.list -> DIRECT")
		fmt.Println("  direct-default : everything DIRECT; vpn.list -> VPN")
		fmt.Println("switch with:  vpn mode <vpn-default|direct-default>")
		return nil
	}
	newMode := strings.ToLower(args[0])
	if newMode != "vpn-default" && newMode != "direct-default" {
		return fmt.Errorf(`mode must be "vpn-default" or "direct-default"`)
	}
	// Prefer the daemon (so PAC + counters reload immediately).
	client := newClient(e)
	if err := client.SetMode(newMode); err == nil {
		fmt.Printf("[mode] -> %s\n", newMode)
	} else {
		if err2 := config.SetMode(filepath.Join(e.DataDir, "config.toml"), newMode); err2 != nil {
			return err2
		}
		fmt.Printf("[mode] -> %s (offline; restart daemon to apply)\n", newMode)
	}
	// Re-apply env vars to match the new mode (only if daemon's reachable).
	if s, err := client.Quick().Status(); err == nil && s.WSLIp != "" {
		if newMode == "vpn-default" {
			proxyURL := fmt.Sprintf("http://%s:%d", s.WSLIp, s.ProxyPort)
			noProxy, _ := buildNoProxy(e)
			_ = winproxy.SetProxyEnv(proxyURL, noProxy)
			fmt.Printf("[mode] env updated: HTTP(S)_PROXY=%s\n", proxyURL)
		} else {
			_ = winproxy.ClearProxyEnv()
			fmt.Println("[mode] env cleared (direct-default)")
		}
		fmt.Println("[mode] open IDEs/terminals must be restarted to pick up new env vars")
	}
	return nil
}

// ---- add / remove ----

func Add(e *Env, args []string) error    { return editList(e, args, true) }
func Remove(e *Env, args []string) error { return editList(e, args, false) }

func editList(e *Env, args []string, add bool) error {
	which, patterns := parseListArgs(args)
	if len(patterns) == 0 {
		verb := "add"
		if !add { verb = "remove" }
		return fmt.Errorf("usage: vpn %s [-vpn|-direct] <pattern>...  (default: vpn)", verb)
	}
	client := newClient(e)
	var (
		r   *api.ListEditResponse
		err error
	)
	if add {
		r, err = client.ListAdd(which, patterns)
	} else {
		r, err = client.ListRemove(which, patterns)
	}
	if err != nil {
		return fmt.Errorf("daemon unreachable: %w", err)
	}
	for _, p := range r.Added {
		fmt.Printf("added to %s.list   : %s\n", which, p)
	}
	for _, p := range r.Skipped {
		fmt.Printf("already in %s.list : %s\n", which, p)
	}
	for _, p := range r.Removed {
		fmt.Printf("removed from %s.list : %s\n", which, p)
	}
	return nil
}

func parseListArgs(args []string) (which string, patterns []string) {
	which = "vpn"
	for _, a := range args {
		la := strings.ToLower(a)
		switch la {
		case "-direct", "--direct", "direct":
			which = "direct"
		case "-vpn", "--vpn", "vpn":
			which = "vpn"
		default:
			patterns = append(patterns, a)
		}
	}
	return
}

// ---- list ----

func List(e *Env) error {
	fmt.Println("=== vpn.list (always through VPN) ===")
	if err := catFile(filepath.Join(e.DataDir, "vpn.list")); err != nil {
		return err
	}
	fmt.Println()
	fmt.Println("=== direct.list (always through home) ===")
	return catFile(filepath.Join(e.DataDir, "direct.list"))
}

func catFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fmt.Println(sc.Text())
	}
	return sc.Err()
}

// ---- logs ----

func Logs(e *Env, args []string) error {
	name := "pac"
	follow := false
	tail := 50
	for _, a := range args {
		switch {
		case a == "-f" || a == "--follow":
			follow = true
		case a == "oc" || a == "openconnect":
			name = "oc"
		case a == "pac" || a == "vpnctl":
			name = "pac"
		case a == "proxy" || a == "tinyproxy":
			name = "proxy"
		}
	}
	client := newClient(e)
	if !follow {
		out, err := client.LogTail(name, tail)
		if err != nil {
			return err
		}
		fmt.Print(out)
		return nil
	}
	// Poll-based follow.
	seen := ""
	for {
		out, err := client.LogTail(name, 200)
		if err != nil {
			fmt.Println("(log read error:", err.Error()+")")
			time.Sleep(2 * time.Second)
			continue
		}
		// Print only the suffix that's new.
		if strings.HasPrefix(out, seen) {
			fmt.Print(out[len(seen):])
		} else {
			fmt.Print(out)
		}
		seen = out
		time.Sleep(1200 * time.Millisecond)
	}
}

// ---- reload / restart / sync-hosts ----

func Reload(e *Env) error {
	client := newClient(e)
	if err := client.Reload(); err != nil {
		return err
	}
	fmt.Println("[reload] lists re-read")
	// Push proxy env again to pick up any NO_PROXY changes.
	if s, err := client.Quick().Status(); err == nil && s.WSLIp != "" && s.Mode == "vpn-default" {
		proxyURL := fmt.Sprintf("http://%s:%d", s.WSLIp, s.ProxyPort)
		noProxy, _ := buildNoProxy(e)
		_ = winproxy.SetProxyEnv(proxyURL, noProxy)
		fmt.Println("[reload] env refreshed")
	}
	return nil
}

func Restart(e *Env) error {
	if err := Down(e); err != nil {
		fmt.Printf("(down had errors: %v)\n", err)
	}
	time.Sleep(1500 * time.Millisecond)
	return Up(e)
}

// ComponentAction is the shared entry point (CLI verb + tray menu) for driving
// one supervised component through one verb. Pause/resume are aliases of
// stop/start on the daemon side.
func ComponentAction(e *Env, name, verb string) (*api.ComponentResult, error) {
	r, err := newClient(e).ComponentAction(name, verb)
	if err != nil {
		return nil, fmt.Errorf("daemon unreachable or rejected: %w", err)
	}
	return r, nil
}

// Component implements: vpn component <openconnect|tinyproxy> <verb>.
func Component(e *Env, args []string) error {
	const usage = "usage: vpn component <openconnect|tinyproxy> <start|stop|pause|resume|restart>"
	if len(args) < 2 {
		return fmt.Errorf(usage)
	}
	name, verb := strings.ToLower(args[0]), strings.ToLower(args[1])
	r, err := ComponentAction(e, name, verb)
	if err != nil {
		return err
	}
	state := "stopped"
	if r.Pid != 0 {
		state = fmt.Sprintf("running (pid %d)", r.Pid)
	}
	fmt.Printf("[component] %s %s -> desired=%s, %s\n", name, verb, r.Desired, state)
	return nil
}

func SyncHosts(e *Env) error {
	out, err := wsl.SyncHostsToWSL(e.Cfg.WSLDistro, e.Cfg.SudoPassword)
	if err != nil {
		return err
	}
	fmt.Print(out)
	return nil
}

// DashboardURL returns the URL the dashboard is reachable at — daemon's WSL IP
// if we can discover it, else localhost (which WSL2's port forwarding also
// covers as long as a wsl.exe process is alive).
func DashboardURL(e *Env) string {
	ip, err := wsl.IP(e.Cfg.WSLDistro)
	if err != nil || ip == "" {
		ip = "127.0.0.1"
	}
	return fmt.Sprintf("http://%s:%d/", ip, e.Cfg.PACPort)
}

// Dashboard opens the web dashboard, starting the daemon first if it isn't
// already running. When it has to start the daemon it uses "dashboard-only"
// mode: the PAC/API/UI server comes up but openconnect + tinyproxy are left
// stopped and the Windows proxy settings are NOT touched — so you can view and
// configure things (or start components individually) without a `vpn up`.
func Dashboard(e *Env) error {
	if IsDaemonReachable(e) {
		return OpenDashboard(e, "")
	}
	fmt.Println("[dashboard] daemon not running — starting in dashboard-only mode (no VPN)...")
	_ = wsl.StopDaemon(e.KeepalivePID)
	if err := wsl.LaunchDaemon(e.Cfg.WSLDistro, e.WSLInstallDir, e.WSLDataDir,
		release.LocalVersion(), e.KeepalivePID, "--no-connect"); err != nil {
		return fmt.Errorf("launching dashboard daemon: %w", err)
	}
	if err := waitForDashboard(e); err != nil {
		return err
	}
	fmt.Println("[dashboard] up — components are stopped; run `vpn up` (or use the dashboard) to connect.")
	return OpenDashboard(e, "")
}

// waitForDashboard waits for the daemon's HTTP server to answer. Unlike
// waitForDaemon it doesn't expect an openconnect handshake, so the server is
// ready in ~1-2s.
func waitForDashboard(e *Env) error {
	client := newClient(e).Quick()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if s, err := client.Status(); err == nil && s.PacPid != 0 {
			return nil
		}
		time.Sleep(400 * time.Millisecond)
	}
	return fmt.Errorf("dashboard daemon did not respond after 20s — check pac.log")
}

// OpenDashboard launches the user's default browser pointed at the dashboard.
// Optional hash (e.g. "#log=oc") is appended verbatim.
//
// Use %ComSpec% (always an absolute path to cmd.exe on Windows) rather than
// the bare "cmd" name — that fails with `executable file not found in %PATH%`
// when the parent process inherits a Unix-style PATH (e.g. tray launched from
// git-bash, where C:\Windows\System32 shows up as /c/Windows/system32).
func OpenDashboard(e *Env, hash string) error {
	url := DashboardURL(e) + hash
	cmd := os.Getenv("ComSpec")
	if cmd == "" {
		cmd = `C:\Windows\System32\cmd.exe`
	}
	return exec.Command(cmd, "/c", "start", "", url).Start()
}

// Autostart manages the HKCU Run entry that launches vpn-tray.exe on login.
//   vpn autostart           → print status
//   vpn autostart enable    → add Run entry
//   vpn autostart disable   → remove it
func Autostart(e *Env, args []string) error {
	exe := filepath.Join(e.InstallDir, "vpn-tray.exe")
	action := "status"
	if len(args) > 0 {
		action = strings.ToLower(args[0])
	}
	switch action {
	case "enable", "on":
		if _, err := os.Stat(exe); err != nil {
			return fmt.Errorf("can't find %s: %w", exe, err)
		}
		if err := winproxy.EnableAutostart(exe); err != nil {
			return err
		}
		fmt.Printf("[autostart] enabled — %s will launch at every login\n", exe)
		// Also launch it now if not already running, so the user doesn't have
		// to sign out and back in to see it.
		if !isProcessRunning("vpn-tray.exe") {
			c := exec.Command(exe)
			if err := c.Start(); err != nil {
				fmt.Printf("[autostart] note: launch now failed: %v\n", err)
			} else {
				_ = c.Process.Release()
				fmt.Println("[autostart] launched it now (PID released)")
			}
		} else {
			fmt.Println("[autostart] vpn-tray.exe is already running")
		}
		return nil
	case "disable", "off":
		if err := winproxy.DisableAutostart(); err != nil {
			return err
		}
		fmt.Println("[autostart] disabled (vpn-tray.exe will not launch at login)")
		return nil
	case "status", "":
		path, on := winproxy.AutostartStatus()
		if on {
			fmt.Printf("[autostart] ENABLED  -> %s\n", path)
		} else {
			fmt.Println("[autostart] disabled")
		}
		return nil
	default:
		return fmt.Errorf("usage: vpn autostart [enable|disable|status]")
	}
}

func isProcessRunning(name string) bool {
	out, err := exec.Command(wsl.SysExe("tasklist.exe"), "/FI", "IMAGENAME eq "+name, "/FO", "CSV", "/NH").Output()
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(out)), strings.ToLower(`"`+name+`"`))
}

// ---- usage ----

func Usage() {
	fmt.Println(`vpn — WSL-backed PAC + OpenConnect routing for Windows

Usage:
  vpn up                        bring tunnel + proxy + PAC up
  vpn down                      tear down + clear Windows proxy
  vpn restart                   down then up
  vpn status                    show live state
  vpn mode [<m>]                read or set routing mode
                                  m = vpn-default | direct-default
  vpn add [-vpn|-direct] <p>... add patterns to a list (default: vpn)
  vpn remove [-vpn|-direct] <p>...
  vpn list                      cat both list files
  vpn logs [oc|pac|proxy] [-f]  tail a log (default: pac, -f follows)
  vpn reload                    re-read lists + refresh env vars
  vpn component <name> <verb>   control one component without a full restart
                                  name = openconnect | tinyproxy
                                  verb = start | stop | pause | resume | restart
  vpn sync-hosts                copy Windows hosts -> WSL /etc/hosts
  vpn tray                      run the system-tray UI
  vpn dashboard                 open the web dashboard in your browser
  vpn autostart [enable|disable]  manage Windows login autostart for the tray
  vpn configure                 interactive wizard (writes config.toml)
  vpn upgrade                   download latest release and run msiexec
  vpn version                   print version
`)
}

// ---- shared helper ----

func newClient(e *Env) *api.Client {
	// Resolve the WSL IP each call: it can rotate between reboots.
	ip, err := wsl.IP(e.Cfg.WSLDistro)
	if err != nil || ip == "" {
		ip = "127.0.0.1"
	}
	return api.New(ip, e.Cfg.PACPort)
}

// IsDaemonReachable returns true if /api/status responds quickly.
func IsDaemonReachable(e *Env) bool {
	// Bypass the http client to keep this cheap.
	ip, err := wsl.IP(e.Cfg.WSLDistro)
	if err != nil || ip == "" {
		return false
	}
	d := net.Dialer{Timeout: 600 * time.Millisecond}
	c, err := d.Dial("tcp", fmt.Sprintf("%s:%d", ip, e.Cfg.PACPort))
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}
