// Package config reads and writes the project's tiny config.toml.
// It's a deliberately minimal TOML implementation that matches the schema
// the Python daemon already uses — no third-party TOML dep needed.
package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

type Config struct {
	VPNHost      string
	VPNUser      string
	VPNPassword  string
	VPNGroup     string
	WSLDistro    string
	SudoPassword string
	HTTPPort     int
	PACPort      int
	BindAddr     string
	Mode         string
	Path         string // where it was read from
}

var (
	reSection = regexp.MustCompile(`^\[([^\]]+)\]$`)
	reKV      = regexp.MustCompile(`^([^=]+)=\s*(.*)$`)
)

// Load parses config.toml at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	c := &Config{Path: path, Mode: "vpn-default", BindAddr: "0.0.0.0"}
	section := ""
	for _, raw := range strings.Split(string(data), "\n") {
		line := stripComment(raw)
		if line == "" {
			continue
		}
		if m := reSection.FindStringSubmatch(line); m != nil {
			section = strings.TrimSpace(m[1])
			continue
		}
		if m := reKV.FindStringSubmatch(line); m != nil {
			k := strings.TrimSpace(m[1])
			v := strings.Trim(strings.TrimSpace(m[2]), `"`)
			switch section + "." + k {
			case "vpn.host":
				c.VPNHost = v
			case "vpn.user":
				c.VPNUser = v
			case "vpn.password":
				c.VPNPassword = v
			case "vpn.group":
				c.VPNGroup = v
			case "wsl.distro":
				c.WSLDistro = v
			case "wsl.sudo_password":
				c.SudoPassword = v
			case "proxy.http_port":
				fmt.Sscanf(v, "%d", &c.HTTPPort)
			case "proxy.pac_port":
				fmt.Sscanf(v, "%d", &c.PACPort)
			case "proxy.bind_addr":
				c.BindAddr = v
			case "routing.mode":
				c.Mode = v
			}
		}
	}
	if c.HTTPPort == 0 {
		c.HTTPPort = 8888
	}
	if c.PACPort == 0 {
		c.PACPort = 8889
	}
	return c, nil
}

// SetMode rewrites routing.mode in place. Used by the CLI for offline edits
// (e.g. before the daemon is running). When the daemon is up, prefer POST
// /api/mode so PAC reloads automatically.
func SetMode(path, newMode string) error {
	if newMode != "vpn-default" && newMode != "direct-default" {
		return fmt.Errorf(`mode must be "vpn-default" or "direct-default"`)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	text := string(data)
	rMode := regexp.MustCompile(`(?m)^\s*mode\s*=.*$`)
	rSection := regexp.MustCompile(`(?m)^\s*\[routing\]\s*$`)
	hasModeInRouting := regexp.MustCompile(`(?ms)^\s*\[routing\][^\[]*?^\s*mode\s*=`).MatchString(text)
	switch {
	case hasModeInRouting:
		text = rMode.ReplaceAllString(text, fmt.Sprintf(`mode = "%s"`, newMode))
	case rSection.MatchString(text):
		text = rSection.ReplaceAllString(text, fmt.Sprintf("[routing]\nmode = \"%s\"", newMode))
	default:
		text = strings.TrimRight(text, "\n") + fmt.Sprintf("\n\n[routing]\nmode = \"%s\"\n", newMode)
	}
	return os.WriteFile(path, []byte(text), 0o644)
}

func stripComment(line string) string {
	if i := strings.Index(line, "#"); i >= 0 {
		line = line[:i]
	}
	return strings.TrimSpace(line)
}
