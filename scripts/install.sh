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

REPO_URL="${REPO_URL:-https://github.com/o6ez9na/beauty-awg.git}"
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

# --- AmneziaWG -------------------------------------------------------------
# Built from source via DKMS so it works on Debian AND Ubuntu (the Ubuntu-only
# PPA is deliberately avoided). The panel needs only the kernel module on the
# host (awg/awg-quick run inside the backend container); a node needs both.

install_awg_module() {
  if modinfo amneziawg >/dev/null 2>&1; then
    ok "amneziawg kernel module already present"; return
  fi
  info "building AmneziaWG kernel module (DKMS)"
  case "$PKG" in
    apt) pkg_install dkms git build-essential "linux-headers-$(uname -r)" ;;
    dnf) pkg_install dkms git make gcc "kernel-devel-$(uname -r)" || pkg_install dkms git make gcc kernel-devel ;;
  esac
  local tmp; tmp="$(mktemp -d)"
  git clone --depth 1 https://github.com/amnezia-vpn/amneziawg-linux-kernel-module.git "$tmp/mod"
  local src="$tmp/mod/src"
  # dkms-install only copies sources to /usr/src/amneziawg-<ver>; register + build after.
  local ver; ver="$(sed -n 's/.*PACKAGE_VERSION="\([^"]*\)".*/\1/p' "$src/dkms.conf")"
  ver="${ver:-1.0.0}"
  make -C "$src" dkms-install
  dkms add -m amneziawg -v "$ver" 2>/dev/null || true
  if dkms build -m amneziawg -v "$ver" && dkms install -m amneziawg -v "$ver"; then
    ok "amneziawg module installed via DKMS ($ver)"
  else
    warn "DKMS failed; falling back to direct module install (won't survive kernel upgrades)"
    make -C "$src" && make -C "$src" install && depmod -a \
      || die "kernel module build failed (need matching linux-headers for $(uname -r))"
  fi
  rm -rf "$tmp"
  modprobe amneziawg || warn "module installed but modprobe failed; a reboot may be required"
}

install_awg_tools() {
  if command -v awg-quick >/dev/null 2>&1; then
    ok "amneziawg tools already present"; return
  fi
  info "building AmneziaWG tools (awg, awg-quick)"
  case "$PKG" in
    apt) pkg_install git build-essential libmnl-dev bash iproute2 ;;
    dnf) pkg_install git make gcc libmnl-devel bash iproute ;;
  esac
  local tmp; tmp="$(mktemp -d)"
  git clone --depth 1 https://github.com/amnezia-vpn/amneziawg-tools.git "$tmp/tools"
  make -C "$tmp/tools/src"
  make -C "$tmp/tools/src" install
  rm -rf "$tmp"
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
  install_awg_module
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
  install_awg_module
  install_awg_tools
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

  install_nodeagent
}

# Optional local web UI on the node to view/edit awg config from a LAN browser.
GO_VER="${GO_VER:-1.26.4}"
install_go() {
  if command -v go >/dev/null 2>&1 && go version | grep -qE 'go1\.(2[6-9]|[3-9][0-9])'; then
    ok "go $(go version | awk '{print $3}') present"; return
  fi
  local arch; case "$(uname -m)" in
    x86_64) arch=amd64 ;; aarch64|arm64) arch=arm64 ;;
    *) die "unsupported arch for Go: $(uname -m)" ;;
  esac
  info "installing Go $GO_VER (distro package too old for this module)"
  curl -fsSL "https://go.dev/dl/go${GO_VER}.linux-${arch}.tar.gz" | tar -C /usr/local -xz
  export PATH="/usr/local/go/bin:$PATH"
}

install_nodeagent() {
  local want; ask "Install the node web UI to edit the config in a browser? (y/n)" want y
  case "$want" in y|Y|yes|"") ;; *) return ;; esac

  install_go
  case "$PKG" in apt) pkg_install git ;; dnf) pkg_install git ;; esac
  if [ -d "$INSTALL_DIR/.git" ]; then
    git -C "$INSTALL_DIR" pull --ff-only || warn "git pull failed; building existing checkout"
  else
    git clone "$REPO_URL" "$INSTALL_DIR"
  fi
  info "building node agent"
  ( cd "$INSTALL_DIR" && /usr/local/go/bin/go build -o /usr/local/bin/awg-nodeagent ./cmd/nodeagent ) \
    || ( cd "$INSTALL_DIR" && go build -o /usr/local/bin/awg-nodeagent ./cmd/nodeagent ) \
    || die "node agent build failed"

  local pw addr
  ask_secret "Set a password for the node web UI (user: admin)" pw
  [ -n "$pw" ] || die "password required"
  ask "Web UI listen address" addr ":8088"

  umask 077
  cat >/etc/awg-nodeagent.env <<EOF
NODE_PASSWORD=$pw
NODE_LISTEN=$addr
AWG_IFACE=$AWG_IFACE
AWG_CONF=$AWG_CONF_DIR/$AWG_IFACE.conf
EOF
  cat >/etc/systemd/system/awg-nodeagent.service <<'EOF'
[Unit]
Description=beautifulwg node web UI
After=network-online.target

[Service]
EnvironmentFile=/etc/awg-nodeagent.env
ExecStart=/usr/local/bin/awg-nodeagent
Restart=on-failure
User=root

[Install]
WantedBy=multi-user.target
EOF
  systemctl daemon-reload
  systemctl enable --now awg-nodeagent
  ok "node web UI: http://<node-lan-ip>${addr}  (user: admin)"
  warn "it edits the awg config + runs awg-quick as root — keep it on the LAN only"
}

case "$MODE" in
  panel) install_panel ;;
  node)  install_node ;;
esac
