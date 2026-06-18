// Package tray renders the system-tray UI. It polls /api/status to keep
// the menu's status row and tooltip fresh, and delegates all actual work to
// the cli package so behavior matches `vpn <verb>` exactly.
package tray

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/getlantern/systray"
	"github.com/vpn/controller/internal/api"
	"github.com/vpn/controller/internal/cli"
	"github.com/vpn/controller/internal/release"
)

// updateCheckInterval is how often the tray polls GitHub for a newer release.
// 24h is gentle on the unauthenticated rate limit (60 req/h per IP) and
// matches a typical "I notice updates the next morning" cadence.
const updateCheckInterval = 24 * time.Hour

// updateCheckStartupDelay defers the very first check so login/boot isn't
// burdened with a network round-trip.
const updateCheckStartupDelay = 30 * time.Second

// updateCheckFile records the last successful check time so multiple tray
// restarts in a day don't hammer the API.
const updateCheckFile = ".update-check"

//go:embed icon-connected.ico
var iconConnected []byte

//go:embed icon-warn.ico
var iconWarn []byte

//go:embed icon-off.ico
var iconOff []byte

var (
	env        *cli.Env
	lastIP     atomic.Value // string
	currentIcn atomic.Value // string — which icon is loaded
	lastMode   atomic.Value // string — last seen mode
)

// componentMenu groups a component's submenu parent and its action children so
// renderStatus can relabel/enable them from /api/status without threading eight
// menu items through every call.
type componentMenu struct {
	parent              *systray.MenuItem
	start, pause, restart *systray.MenuItem
	label               string // "Tunnel", "Proxy"
}

// compMenus holds the two per-component submenus, populated in onReady.
var compMenus struct {
	tunnel componentMenu // openconnect
	proxy  componentMenu // tinyproxy
}

// Run blocks until the user picks Quit.
func Run(e *cli.Env) {
	env = e
	systray.Run(onReady, func() {})
}

func onReady() {
	setIcon("off")
	systray.SetTitle("")
	systray.SetTooltip("vpn — initializing…")

	// Compact menu: status, mode toggle, up/down/restart, dashboard, quit.
	statusRow := systray.AddMenuItem("connecting…", "")
	statusRow.Disable()

	// Update item lives at the top of the menu but is hidden until the
	// daily-check goroutine sees a newer release.
	updateItem := systray.AddMenuItem("↑ Update available", "Download and install the latest release")
	updateItem.Hide()

	systray.AddSeparator()
	modeToggle := systray.AddMenuItem("Switch mode", "Flip between vpn-default and direct-default")

	systray.AddSeparator()
	upItem := systray.AddMenuItem("Up", "Bring tunnel + proxy up")
	downItem := systray.AddMenuItem("Down", "Tear down")
	restartItem := systray.AddMenuItem("Restart", "Down then up")

	// Per-component control. Each component can be started, paused (stopped and
	// pinned down so the supervisor leaves it), or restarted independently.
	systray.AddSeparator()
	compMenus.tunnel = newComponentMenu("Tunnel", "openconnect")
	compMenus.proxy = newComponentMenu("Proxy", "tinyproxy")

	systray.AddSeparator()
	dashItem := systray.AddMenuItem("Dashboard", "Open the web dashboard")
	quitItem := systray.AddMenuItem("Quit", "Exit the tray (does NOT bring VPN down)")

	systray.AddSeparator()
	verItem := systray.AddMenuItem("version "+release.LocalVersion(), "wsl-vpn-router version")
	verItem.Disable()

	// Status poller — updates icon, status row, mode-toggle label.
	go func() {
		for {
			s, err := pollStatus()
			renderStatus(statusRow, modeToggle, s, err)
			time.Sleep(3 * time.Second)
		}
	}()

	// Update checker — polls GitHub releases at most once per 24h. Stored
	// timestamp in DataDir/.update-check survives tray restarts so opening
	// and closing the tray doesn't bypass the rate limit.
	go updateChecker(updateItem)

	// Action handlers.
	go func() {
		for {
			select {
			case <-upItem.ClickedCh:
				go runVerb("up", func() error { return cli.Up(env) })
			case <-downItem.ClickedCh:
				go runVerb("down", func() error { return cli.Down(env) })
			case <-restartItem.ClickedCh:
				go runVerb("restart", func() error { return cli.Restart(env) })
			case <-compMenus.tunnel.start.ClickedCh:
				go componentVerb("tunnel", "openconnect", "start")
			case <-compMenus.tunnel.pause.ClickedCh:
				go componentVerb("tunnel", "openconnect", "pause")
			case <-compMenus.tunnel.restart.ClickedCh:
				go componentVerb("tunnel", "openconnect", "restart")
			case <-compMenus.proxy.start.ClickedCh:
				go componentVerb("proxy", "tinyproxy", "start")
			case <-compMenus.proxy.pause.ClickedCh:
				go componentVerb("proxy", "tinyproxy", "pause")
			case <-compMenus.proxy.restart.ClickedCh:
				go componentVerb("proxy", "tinyproxy", "restart")
			case <-modeToggle.ClickedCh:
				go runVerb("mode", switchMode)
			case <-dashItem.ClickedCh:
				go runVerb("dashboard", func() error { return cli.Dashboard(env) })
			case <-updateItem.ClickedCh:
				// cli.Upgrade spawns msiexec and returns immediately; the
				// tray keeps running until MSI replaces our exe (when it
				// kills us via Restart Manager).
				go runVerb("upgrade", func() error { return cli.Upgrade(nil) })
			case <-quitItem.ClickedCh:
				systray.Quit()
				return
			}
		}
	}()
}

// newComponentMenu builds a "<label> (<comp>)" submenu with Start/Pause/Restart
// children. The children's clicks are handled in onReady's action loop.
func newComponentMenu(label, comp string) componentMenu {
	parent := systray.AddMenuItem(label, comp+" lifecycle")
	return componentMenu{
		parent:  parent,
		start:   parent.AddSubMenuItem("Start", "Start / resume "+comp),
		pause:   parent.AddSubMenuItem("Pause", "Stop "+comp+" and keep it down"),
		restart: parent.AddSubMenuItem("Restart", "Restart "+comp),
		label:   label,
	}
}

// componentVerb fires a component action and surfaces errors via tooltip.
func componentVerb(label, comp, verb string) {
	runVerb(label+" "+verb, func() error {
		_, err := cli.ComponentAction(env, comp, verb)
		return err
	})
}

// updateComponentMenus relabels both submenus from /api/status. reachable=false
// (daemon down) disables every action.
func updateComponentMenus(s *api.Status, reachable bool) {
	var ocPid, tpPid int
	if s != nil {
		ocPid, tpPid = s.OcPid, s.TinyproxyPid
	}
	updateComponentMenu(compMenus.tunnel, componentFor(s, "openconnect", ocPid), reachable)
	updateComponentMenu(compMenus.proxy, componentFor(s, "tinyproxy", tpPid), reachable)
}

// componentFor returns the per-component view, falling back to the flat pid
// fields when talking to an older daemon that doesn't send `components`.
func componentFor(s *api.Status, name string, flatPid int) api.Component {
	if s != nil && s.Components != nil {
		if c, ok := s.Components[name]; ok {
			return c
		}
	}
	return api.Component{Pid: flatPid, Desired: "up", Supervised: false}
}

func updateComponentMenu(m componentMenu, c api.Component, reachable bool) {
	if !reachable {
		m.parent.SetTitle(m.label)
		m.start.Disable()
		m.pause.Disable()
		m.restart.Disable()
		return
	}
	m.start.Enable()
	m.restart.Enable()
	switch {
	case c.Paused():
		m.parent.SetTitle(m.label + " — paused")
		m.pause.Disable() // already down; offer Start to resume
	case c.Pid == 0:
		m.parent.SetTitle(m.label + " — recovering")
		m.pause.Enable()
	default:
		m.parent.SetTitle(m.label + " — running")
		m.pause.Enable()
	}
}

// switchMode flips between the two modes based on whatever mode the poller
// last saw. Falls back to vpn-default if we have no last-seen state.
func switchMode() error {
	cur, _ := lastMode.Load().(string)
	next := "vpn-default"
	if cur == "vpn-default" {
		next = "direct-default"
	}
	return cli.Mode(env, []string{next})
}

// ---- status rendering ----

func pollStatus() (*api.Status, error) {
	ip, _ := lastIP.Load().(string)
	if ip == "" {
		ip = "127.0.0.1"
	}
	return api.New(ip, env.Cfg.PACPort).Quick().Status()
}

func renderStatus(statusRow, modeToggle *systray.MenuItem, s *api.Status, err error) {
	if err != nil || s == nil {
		setIcon("off")
		statusRow.SetTitle("○ disconnected")
		systray.SetTooltip("vpn — daemon unreachable")
		modeToggle.SetTitle("Switch mode")
		modeToggle.Disable()
		updateComponentMenus(s, false)
		return
	}
	lastIP.Store(s.WSLIp)
	lastMode.Store(s.Mode)
	modeToggle.Enable()
	updateComponentMenus(s, true)

	other := "direct-default"
	if s.Mode == "direct-default" {
		other = "vpn-default"
	}
	modeToggle.SetTitle("Switch to " + other)

	oc := componentFor(s, "openconnect", s.OcPid)
	tp := componentFor(s, "tinyproxy", s.TinyproxyPid)

	switch {
	case oc.Paused() || tp.Paused():
		// A loud, distinct state: the operator deliberately stopped something,
		// so traffic through it will fail until resumed.
		setIcon("warn")
		statusRow.SetTitle("⏸ " + pausedLabel(oc, tp) + " paused · " + s.Mode)
		systray.SetTooltip("vpn — " + pausedLabel(oc, tp) + " paused; traffic through it will fail")
	case s.Tun0IP != "" && tp.Pid > 0:
		setIcon("connected")
		statusRow.SetTitle(fmt.Sprintf("● %s · %s", s.Tun0IP, s.Mode))
		systray.SetTooltip(fmt.Sprintf("vpn — connected (%s)\nproxy %s:%d", s.Mode, s.WSLIp, s.ProxyPort))
	case tp.Pid > 0 && oc.Pid == 0:
		setIcon("warn")
		statusRow.SetTitle("● tunnel down · " + s.Mode)
		systray.SetTooltip("vpn — proxy up, tunnel recovering")
	default:
		setIcon("warn")
		statusRow.SetTitle("○ partial")
		systray.SetTooltip("vpn — partially up")
	}
}

// pausedLabel names which component(s) are paused for the status line/tooltip.
func pausedLabel(oc, tp api.Component) string {
	switch {
	case oc.Paused() && tp.Paused():
		return "tunnel+proxy"
	case oc.Paused():
		return "tunnel"
	default:
		return "proxy"
	}
}

// setIcon swaps the tray icon, deduped to avoid shell flicker.
func setIcon(state string) {
	if cur, _ := currentIcn.Load().(string); cur == state {
		return
	}
	currentIcn.Store(state)
	switch state {
	case "connected":
		systray.SetIcon(iconConnected)
	case "warn":
		systray.SetIcon(iconWarn)
	default:
		systray.SetIcon(iconOff)
	}
}

// runVerb executes a CLI verb in a goroutine and surfaces errors via tooltip.
func runVerb(label string, fn func() error) {
	if err := fn(); err != nil {
		systray.SetTooltip("vpn — " + label + ": " + err.Error())
	}
}

// updateChecker polls GitHub for newer releases on a 24h cadence and reveals
// the hidden update menu item with "↑ Update to <v>" when one is available.
// Persists the last-check timestamp under DataDir/.update-check so multiple
// tray sessions in a day don't exhaust the unauthenticated API rate limit.
func updateChecker(item *systray.MenuItem) {
	// Honour previous-session check time so we don't probe immediately if
	// another tray instance already checked within the window.
	if last := readLastCheck(); time.Since(last) < updateCheckInterval {
		wait := updateCheckInterval - time.Since(last)
		time.Sleep(wait)
	} else {
		time.Sleep(updateCheckStartupDelay)
	}
	for {
		checkOnce(item)
		writeLastCheck(time.Now())
		time.Sleep(updateCheckInterval)
	}
}

func checkOnce(item *systray.MenuItem) {
	remote, err := release.LatestVersion(context.Background())
	if err != nil {
		// Network blip / rate limit / GitHub down — fail soft, retry next cycle.
		return
	}
	local := release.LocalVersion()
	if !release.IsNewer(local, remote) {
		return
	}
	item.SetTitle(fmt.Sprintf("↑ Update to %s", remote))
	item.SetTooltip(fmt.Sprintf("Download and install wsl-vpn-router %s (current: %s)", remote, local))
	item.Show()
}

func updateCheckPath() string {
	if env == nil {
		return ""
	}
	return filepath.Join(env.DataDir, updateCheckFile)
}

func readLastCheck() time.Time {
	p := updateCheckPath()
	if p == "" {
		return time.Time{}
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return time.Time{}
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(n, 0)
}

func writeLastCheck(t time.Time) {
	p := updateCheckPath()
	if p == "" {
		return
	}
	_ = os.WriteFile(p, []byte(strconv.FormatInt(t.Unix(), 10)), 0o644)
}
