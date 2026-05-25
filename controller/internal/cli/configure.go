package cli

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/vpn/controller/internal/config"
	"github.com/vpn/controller/internal/wsl"
	"golang.org/x/term"
)

// Configure runs the interactive first-run / reconfigure wizard. It writes a
// fresh config.toml under wsl.DataDir(), using any existing config as defaults.
//
// Invoked by:
//   - MSI custom action on first install
//   - `vpn configure` from the CLI any time after
//
// Designed to be re-run safely: hitting Enter at each prompt keeps the current
// value. Passwords show "(unchanged)" when one already exists; entering a new
// value overwrites, leaving blank keeps the old one.
func Configure(args []string) error {
	dataDir, err := wsl.DataDir()
	if err != nil {
		return fmt.Errorf("resolving data dir: %w", err)
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	cfgPath := filepath.Join(dataDir, "config.toml")

	// Existing config (if any) supplies prompt defaults. Errors here are
	// non-fatal — a fresh install legitimately has no config yet.
	var existing *config.Config
	if c, err := config.Load(cfgPath); err == nil {
		existing = c
	}

	in := bufio.NewReader(os.Stdin)
	out := os.Stdout

	fmt.Fprintln(out, "wsl-vpn-router configuration wizard")
	fmt.Fprintln(out, "===================================")
	fmt.Fprintf(out, "Writing to: %s\n", cfgPath)
	fmt.Fprintln(out, "Press Enter at any prompt to keep the shown default / current value.")
	fmt.Fprintln(out)

	host := promptString(in, out, "VPN host (e.g. vpn.example.com)", defOf(existing, func(c *config.Config) string { return c.VPNHost }), true)
	user := promptString(in, out, "VPN username", defOf(existing, func(c *config.Config) string { return c.VPNUser }), true)
	pw := promptPassword(out, "VPN password", existing != nil && existing.VPNPassword != "")
	if pw == "" && existing != nil {
		pw = existing.VPNPassword
	}
	group := promptString(in, out, "VPN auth group (blank if not required)", defOf(existing, func(c *config.Config) string { return c.VPNGroup }), false)

	distro := promptString(in, out, "WSL distro", chooseDefaultDistro(existing), true)
	sudoPW := promptPassword(out, "WSL sudo password (blank = NOPASSWD configured)", existing != nil && existing.SudoPassword != "")
	if sudoPW == "" && existing != nil {
		sudoPW = existing.SudoPassword
	}
	mode := promptChoice(in, out, "Routing mode", []string{"vpn-default", "direct-default"},
		defOf(existing, func(c *config.Config) string { return c.Mode }))

	httpPort := defaultInt(existing, func(c *config.Config) int { return c.HTTPPort }, 8888)
	pacPort := defaultInt(existing, func(c *config.Config) int { return c.PACPort }, 8889)
	bindAddr := defOf(existing, func(c *config.Config) string { return c.BindAddr })
	if bindAddr == "" {
		bindAddr = "0.0.0.0"
	}

	contents := buildConfigTOML(host, user, pw, group, distro, sudoPW, httpPort, pacPort, bindAddr, mode)
	if err := atomicWrite(cfgPath, []byte(contents), 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", cfgPath, err)
	}
	fmt.Fprintln(out)
	fmt.Fprintf(out, "Wrote %s\n", cfgPath)
	fmt.Fprintln(out, "Next: `vpn up` to bring the tunnel up.")
	return nil
}

// ---- prompt helpers ----

// promptString shows label + default (if any), reads a line. Re-prompts on
// empty input when `required` is true and there's no default to fall back on.
func promptString(in *bufio.Reader, out io.Writer, label, def string, required bool) string {
	for {
		if def != "" {
			fmt.Fprintf(out, "%s [%s]: ", label, def)
		} else {
			fmt.Fprintf(out, "%s: ", label)
		}
		line, err := in.ReadString('\n')
		if err != nil && line == "" {
			// EOF (e.g. piped input ran out). Fall back to default to avoid an infinite loop.
			return def
		}
		line = strings.TrimSpace(line)
		if line == "" {
			if def != "" || !required {
				return def
			}
			fmt.Fprintln(out, "  (required)")
			continue
		}
		return line
	}
}

// promptChoice shows label + options, returns the user's pick. Re-prompts on
// invalid input. Default option is highlighted.
func promptChoice(in *bufio.Reader, out io.Writer, label string, opts []string, def string) string {
	if def == "" {
		def = opts[0]
	}
	for {
		fmt.Fprintf(out, "%s (%s) [%s]: ", label, strings.Join(opts, " / "), def)
		line, err := in.ReadString('\n')
		if err != nil && line == "" {
			return def
		}
		line = strings.TrimSpace(line)
		if line == "" {
			return def
		}
		for _, o := range opts {
			if strings.EqualFold(line, o) {
				return o
			}
		}
		fmt.Fprintf(out, "  (pick one of: %s)\n", strings.Join(opts, ", "))
	}
}

// promptPassword reads a password without echoing it. When hasExisting is
// true, a blank line means "keep the existing password" and the caller
// substitutes it back in. Returns "" if the user pressed Enter immediately.
func promptPassword(out io.Writer, label string, hasExisting bool) string {
	hint := ""
	if hasExisting {
		hint = " (unchanged)"
	}
	fmt.Fprintf(out, "%s%s: ", label, hint)
	// term.ReadPassword needs a terminal fd. On Windows, os.Stdin is one when
	// running interactively; when stdin is a pipe (e.g. test harness) we fall
	// back to plain ReadString so the wizard is still scriptable.
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		in := bufio.NewReader(os.Stdin)
		line, _ := in.ReadString('\n')
		fmt.Fprintln(out)
		return strings.TrimSpace(line)
	}
	pw, err := term.ReadPassword(fd)
	fmt.Fprintln(out)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(pw))
}

func defOf(c *config.Config, get func(*config.Config) string) string {
	if c == nil {
		return ""
	}
	return get(c)
}

func defaultInt(c *config.Config, get func(*config.Config) int, fallback int) int {
	if c == nil {
		return fallback
	}
	if v := get(c); v > 0 {
		return v
	}
	return fallback
}

// chooseDefaultDistro returns the existing config's distro if set; otherwise
// asks WSL for the registered distros and picks the first non-docker-desktop
// one (matching what `setup.ps1` used to do). Falls back to "Ubuntu" if WSL
// isn't installed yet (MSI is about to install it).
func chooseDefaultDistro(existing *config.Config) string {
	if existing != nil && existing.WSLDistro != "" {
		return existing.WSLDistro
	}
	cmd := exec.Command("wsl.exe", "-l", "-q")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.Output()
	if err != nil {
		return "Ubuntu"
	}
	// wsl.exe emits UTF-16 LE. Strip NULs to recover ASCII distro names.
	clean := string(bytes.ReplaceAll(out, []byte{0}, []byte{}))
	for _, line := range strings.Split(clean, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.EqualFold(line, "docker-desktop") {
			continue
		}
		return line
	}
	return "Ubuntu"
}

// buildConfigTOML renders the canonical config.toml schema. Keeping the
// keys and section order stable matches what humans expect when they open
// the file in an editor afterwards.
func buildConfigTOML(host, user, pw, group, distro, sudoPW string, httpPort, pacPort int, bindAddr, mode string) string {
	var b strings.Builder
	fmt.Fprintln(&b, "# wsl-vpn-router configuration. Generated by `vpn configure`.")
	fmt.Fprintln(&b, "# Plaintext secrets — this file lives under %LOCALAPPDATA% and is per-user.")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "[vpn]")
	fmt.Fprintf(&b, "host     = %q\n", host)
	fmt.Fprintf(&b, "user     = %q\n", user)
	fmt.Fprintf(&b, "password = %q\n", pw)
	if group != "" {
		fmt.Fprintf(&b, "group    = %q\n", group)
	} else {
		fmt.Fprintln(&b, `# group  = "your-auth-group"   # uncomment if your VPN demands one`)
	}
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "[wsl]")
	fmt.Fprintf(&b, "distro        = %q\n", distro)
	fmt.Fprintf(&b, "sudo_password = %q\n", sudoPW)
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "[proxy]")
	fmt.Fprintf(&b, "http_port = %d\n", httpPort)
	fmt.Fprintf(&b, "pac_port  = %d\n", pacPort)
	fmt.Fprintf(&b, "bind_addr = %q\n", bindAddr)
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "[routing]")
	fmt.Fprintf(&b, "mode = %q\n", mode)
	return b.String()
}

// atomicWrite writes to a sibling temp file and renames over the target so a
// crash mid-write can't leave a half-written config.toml that would brick
// `vpn up`. On Windows os.Rename across the same directory is atomic.
func atomicWrite(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".config.toml.tmp.*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }() // no-op if rename succeeded
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, perm); err != nil {
		// Best-effort: on Windows perm bits are largely advisory and may
		// not match POSIX semantics. Don't fail the write over it.
	}
	return os.Rename(tmpPath, path)
}
