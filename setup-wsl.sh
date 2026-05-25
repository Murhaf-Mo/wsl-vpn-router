#!/usr/bin/env bash
# setup-wsl.sh - inside-WSL bootstrap for wsl-vpn-router. Idempotent.
# Usage:
#   bash setup-wsl.sh              # interactive: sudo will prompt
#   bash setup-wsl.sh <pw-file>    # non-interactive: read sudo pw from file
# Called by ../setup.ps1 with a temp pw-file so sudo doesn't need a TTY.

set -euo pipefail

step() { printf '\n==> %s\n' "$*"; }
info() { printf '    %s\n' "$*"; }
warn() { printf '!!  %s\n' "$*" >&2; }
fail() { printf 'XX  %s\n' "$*" >&2; exit 1; }

[[ $EUID -eq 0 ]] && fail "Run as your regular WSL user, not root."
command -v apt-get >/dev/null || fail "This script targets Debian/Ubuntu (apt-get)."

WSL_SUDO=""
if [[ $# -ge 1 && -f "$1" ]]; then
    WSL_SUDO=$(cat "$1")
fi

_sudo() {
    if [[ -n "$WSL_SUDO" ]]; then
        echo "$WSL_SUDO" | sudo -S -p "" "$@"
    else
        sudo "$@"
    fi
}

step "apt update"
_sudo apt-get update -qq

step "Installing packages: openconnect tinyproxy python3 iptables"
_sudo apt-get install -y openconnect tinyproxy python3 iptables

step "Disabling stock tinyproxy service (conflicts with our managed instance on :8888)"
_sudo systemctl disable --now tinyproxy 2>/dev/null || true

# vpnctl.py invokes:  sudo bash -c '<cmd>'  for openconnect/tinyproxy/iptables/kill.
# A NOPASSWD rule restricted to those binaries doesn't help, because sudo sees
# /bin/bash, not the inner binary. The honest options are:
#   (a) leave sudo_password set in config.toml  (default)
#   (b) grant NOPASSWD to /bin/bash for this user - effectively passwordless
#       root - by re-running with VPN_NOPASSWD=1
if [[ "${VPN_NOPASSWD:-0}" = "1" ]]; then
    step "Installing /etc/sudoers.d/vpn-router (NOPASSWD - passwordless root for this user)"
    SUDOERS=/etc/sudoers.d/vpn-router
    RULE="$USER ALL=(root) NOPASSWD: /bin/bash, /usr/bin/bash, /bin/kill, /usr/bin/kill, /usr/bin/pkill"
    echo "$RULE" | _sudo tee "$SUDOERS" >/dev/null
    _sudo chmod 440 "$SUDOERS"
    _sudo visudo -c -f "$SUDOERS" >/dev/null || { _sudo rm -f "$SUDOERS"; fail "sudoers rule failed validation"; }
    info "Installed. You can set sudo_password = \"\" in config.toml."
else
    info "Skipping NOPASSWD sudoers rule (set VPN_NOPASSWD=1 to enable)."
fi

step "Sanity check"
for bin in openconnect tinyproxy python3 iptables; do
    if command -v "$bin" >/dev/null; then
        info "$bin: $(command -v "$bin")"
    else
        warn "$bin missing"
    fi
done

step "WSL setup complete."
