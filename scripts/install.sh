#!/usr/bin/env bash
#
# beautifulwg installer.
#
#   curl -fsSL https://raw.githubusercontent.com/YOURUSER/beautifulwg/main/scripts/install.sh | sudo bash
#
# Asks whether to install the PANEL (VPS hub) or a NODE (home server behind CGNAT).
# Non-interactive:  ... | sudo bash -s -- panel      (or: node)
#                   or set INSTALL_MODE=panel|node
#
set -euo pipefail

REPO_URL="${REPO_URL:-https://github.com/o6ez9na/beautifulwg.git}"
INSTALL_DIR="${INSTALL_DIR:-/opt/beautifulwg}"
AWG_IFACE="${AWG_IFACE:-awg0}"
AWG_CONF_DIR="/etc/amnezia/amneziawg"

# --- pretty output ---------------------------------------------------------
c() { printf '\033[%sm%s\033[0m' "$1" "$2"; }
info() { echo "$(c '1;34' '::') $*"; }
ok()   { echo "$(c '1;32' 'ok') $*"; }
warn() { echo "$(c '1;33' 'warn') $*" >&2; }
die()  { echo "$(c '1;31' 'error') $*" >&2; exit 1; }

[ "$(id -u)" -eq 0 ] || die "run as root (sudo)."

# tty for prompts even when the script itself is piped over stdin.
if [ -r /dev/tty ]; then TTY=/dev/tty; else TTY=/dev/stdin; fi
ask() { # ask <prompt> <var> [default]
  local prompt="$1" __var="$2" def="${3:-}" reply
  if [ -n "$def" ]; then prompt="$prompt [$def]"; fi
  printf '%s: ' "$prompt" >/dev/tty
  read -r reply <"$TTY" || true
  printf -v "$__var" '%s' "${reply:-$def}"
}
ask_secret() {
  local prompt="$1" __var="$2" reply
  printf '%s: ' "$prompt" >/dev/tty
  read -rs reply <"$TTY" || true
  echo >/dev/tty
  printf -v "$__var" '%s' "$reply"
}

# --- distro detection ------------------------------------------------------
if command -v apt-get >/dev/null 2>&1; then PKG=apt
elif command -v dnf >/dev/null 2>&1; then PKG=dnf
else die "unsupported distro (need apt or dnf)."; fi

pkg_install() {
  case "$PKG" in
    apt) DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends "$@" ;;
    dnf) dnf install -y "$@" ;;
  esac
}

# --- mode selection --------------------------------------------------------
MODE="${1:-${INSTALL_MODE:-}}"
if [ -z "$MODE" ]; then
  echo "What do you want to install?" >/dev/tty
  echo "  1) panel  — the web panel (run on the VPS with a public IP)" >/dev/tty
  echo "  2) node   — a home server behind CGNAT that exposes a LAN" >/dev/tty
  ask "Choose 1 or 2" choice
  case "$choice" in
    1|panel) MODE=panel ;;
    2|node)  MODE=node ;;
    *) die "invalid choice: $choice" ;;
  esac
fi
[ "$MODE" = panel ] || [ "$MODE" = node ] || die "MODE must be panel or node (got: $MODE)"
info "installing mode: $(c '1;36' "$MODE")"

# --- AmneziaWG (kernel module + userspace tools) ---------------------------
install_amneziawg() {
  if command -v awg >/dev/null 2>&1 && awg-quick --help >/dev/null 2>&1; then
    ok "amneziawg tools already present"; return
  fi
  info "installing AmneziaWG (kernel module + tools)"
  case "$PKG" in
    apt)
      pkg_install software-properties-common ca-certificates
      add-apt-repository -y ppa:amnezia/ppa
      apt-get update
      pkg_install amneziawg amneziawg-tools
      ;;
    dnf)
      pkg_install dnf-plugins-core
      dnf copr enable -y amneziavpn/amneziawg || warn "copr enable failed; install amneziawg manually"
      pkg_install amneziawg-dkms amneziawg-tools || warn "package install failed; see amnezia-vpn/amneziawg-linux-kernel-module"
      ;;
  esac
  modprobe amneziawg 2>/dev/null || warn "could not load amneziawg module now (reboot may be required)"
}

enable_forwarding() {
  info "enabling net.ipv4.ip_forward"
  echo 'net.ipv4.ip_forward=1' >/etc/sysctl.d/99-beautifulwg.conf
  sysctl -q -w net.ipv4.ip_forward=1
}

# --- panel install ---------------------------------------------------------
install_docker() {
  if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
    ok "docker + compose already present"; return
  fi
  info "installing Docker"
  curl -fsSL https://get.docker.com | sh
  systemctl enable --now docker
}

install_panel() {
  install_amneziawg
  enable_forwarding
  install_docker
  pkg_install git

  if [ -d "$INSTALL_DIR/.git" ]; then
    info "updating repo in $INSTALL_DIR"; git -C "$INSTALL_DIR" pull --ff-only
  else
    info "cloning $REPO_URL -> $INSTALL_DIR"; git clone "$REPO_URL" "$INSTALL_DIR"
  fi
  cd "$INSTALL_DIR"

  if [ ! -f .env ]; then
    info "configuring .env"
    local endpoint admin_pw dns secret
    local defip; defip="$(curl -fsSL https://api.ipify.org 2>/dev/null || echo '')"
    ask "Public endpoint the clients dial (IP:port)" endpoint "${defip:+$defip:51820}"
    [ -n "$endpoint" ] || die "endpoint required"
    ask "Optional global DNS pushed to clients (blank = none)" dns ""
    ask_secret "Admin password for the panel" admin_pw
    [ -n "$admin_pw" ] || die "admin password required"
    secret="$(openssl rand -hex 32)"
    cat > .env <<EOF
DB_PASSWORD=$(openssl rand -hex 16)
SESSION_SECRET=$secret
HUB_ENDPOINT=$endpoint
HUB_POOL_CIDR=10.8.0.0/24
HUB_ADDRESS=10.8.0.1
HUB_DNS=$dns
ADMIN_USER=admin
ADMIN_PASSWORD=$admin_pw
INSECURE_COOKIES=1
EOF
    chmod 600 .env
    ok "wrote $INSTALL_DIR/.env"
  else
    warn ".env already exists; leaving it untouched"
  fi

  info "building + starting containers"
  docker compose up -d --build
  ok "panel up. UI: http://$(hostname -I | awk '{print $1}'):3000  (login: admin)"
  warn "put a TLS reverse proxy in front, then set INSECURE_COOKIES= empty in .env"
}

# --- node install ----------------------------------------------------------
install_node() {
  install_amneziawg
  enable_forwarding
  mkdir -p "$AWG_CONF_DIR"

  local conf="$AWG_CONF_DIR/$AWG_IFACE.conf"
  if [ -f "$conf" ]; then
    warn "$conf already exists; leaving it untouched"
  else
    echo "Paste the node config downloaded from the panel (Nodes -> Config)." >/dev/tty
    echo "Finish with Ctrl-D on an empty line:" >/dev/tty
    umask 077
    cat >"$conf" <"$TTY"
    [ -s "$conf" ] || die "empty config; aborting"
    ok "wrote $conf"
  fi

  info "enabling awg-quick@$AWG_IFACE service"
  systemctl enable "awg-quick@$AWG_IFACE"
  systemctl restart "awg-quick@$AWG_IFACE"
  ok "node up. tunnel to hub should establish within ~25s"
  awg show "$AWG_IFACE" 2>/dev/null || true
}

case "$MODE" in
  panel) install_panel ;;
  node)  install_node ;;
esac
