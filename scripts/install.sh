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
REPO_SLUG="${REPO_SLUG:-o6ez9na/beauty-awg}"
INSTALL_DIR="${INSTALL_DIR:-/opt/beautifulwg}"
PANEL_IMAGE_API="${PANEL_IMAGE_API:-ghcr.io/${REPO_SLUG}/panel-api}"
PANEL_IMAGE_WEB="${PANEL_IMAGE_WEB:-ghcr.io/${REPO_SLUG}/panel-web}"
AWG_IFACE="${AWG_IFACE:-awg0}"
AWG_CONF_DIR="/etc/amnezia/amneziawg"
# Set to 1 when we fall back to the userspace backend (no kernel module).
AWG_USERSPACE=""

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
# Where the module can't be built (no matching headers, WSL2, containers), we
# fall back to the userspace backend amneziawg-go — awg-quick uses it
# automatically when `ip link add ... type amneziawg` fails and it is in PATH.

# 0 if kernel headers for the running kernel are present or installable.
kernel_headers_available() {
  [ -d "/lib/modules/$(uname -r)/build" ] && return 0
  case "$PKG" in
    apt) apt-cache show "linux-headers-$(uname -r)" >/dev/null 2>&1 ;;
    dnf) dnf list "kernel-devel-$(uname -r)" >/dev/null 2>&1 ;;
  esac
}

# Build the userspace AmneziaWG backend (amneziawg-go) into /usr/bin.
AWG_GO_REF="${AWG_GO_REF:-master}"
# Download a prebuilt amneziawg-go from our GitHub Releases into /usr/bin.
# Honors NODEAGENT_VERSION (release tag) or resolves the latest release.
download_awg_go_binary() {
  local arch; arch="$(nodeagent_arch)" || { warn "no prebuilt amneziawg-go for arch $(uname -m)"; return 1; }
  local url
  if [ -n "${NODEAGENT_VERSION:-}" ]; then
    url="https://github.com/${REPO_SLUG}/releases/download/${NODEAGENT_VERSION}/amneziawg-go-${NODEAGENT_VERSION}-linux-${arch}"
  else
    info "resolving latest amneziawg-go release for linux/${arch}"
    url="$(curl -fsSL "https://api.github.com/repos/${REPO_SLUG}/releases/latest" 2>/dev/null \
      | grep -oE '"browser_download_url"[[:space:]]*:[[:space:]]*"[^"]*"' \
      | cut -d'"' -f4 \
      | grep -E "amneziawg-go-.*-linux-${arch}$" \
      | head -1)"
    [ -n "$url" ] || { warn "no matching amneziawg-go asset for linux/${arch}"; return 1; }
  fi
  info "downloading $url"
  curl -fsSL "$url" -o /usr/bin/amneziawg-go || { warn "download failed"; return 1; }
  chmod +x /usr/bin/amneziawg-go
  # MIT requires the license notice to accompany the binary (same release path).
  mkdir -p /usr/share/doc/amneziawg-go
  curl -fsSL "${url%/*}/amneziawg-go-LICENSE" -o /usr/share/doc/amneziawg-go/LICENSE 2>/dev/null \
    || warn "could not fetch amneziawg-go LICENSE (MIT); see ${REPO_SLUG} release"
  ok "installed prebuilt amneziawg-go"
}

# Build amneziawg-go from source into /usr/bin.
build_awg_go_from_source() {
  info "building amneziawg-go (userspace backend)"
  install_go
  case "$PKG" in apt) pkg_install git ;; dnf) pkg_install git ;; esac
  local tmp; tmp="$(mktemp -d)"
  git clone --depth 1 --branch "$AWG_GO_REF" \
    https://github.com/amnezia-vpn/amneziawg-go.git "$tmp/awg-go"
  ( cd "$tmp/awg-go" && /usr/local/go/bin/go build -o /usr/bin/amneziawg-go . ) \
    || ( cd "$tmp/awg-go" && go build -o /usr/bin/amneziawg-go . ) \
    || die "amneziawg-go build failed"
  # Keep the MIT license notice alongside the binary.
  mkdir -p /usr/share/doc/amneziawg-go
  cp "$tmp/awg-go/LICENSE" /usr/share/doc/amneziawg-go/LICENSE 2>/dev/null || true
  rm -rf "$tmp"
  ok "amneziawg-go installed (/usr/bin/amneziawg-go)"
}

# Provide /usr/bin/amneziawg-go: prefer a prebuilt release, fall back to source.
# NODE_INSTALL_METHOD=source forces a local build.
install_awg_go() {
  if command -v amneziawg-go >/dev/null 2>&1; then
    ok "amneziawg-go (userspace) already present"; AWG_USERSPACE=1; return
  fi
  if [ "${NODE_INSTALL_METHOD:-}" != source ] && download_awg_go_binary; then
    AWG_USERSPACE=1; return
  fi
  build_awg_go_from_source
  AWG_USERSPACE=1
}

install_awg_module() {
  if modinfo amneziawg >/dev/null 2>&1 || [ -d /sys/module/amneziawg ]; then
    ok "amneziawg kernel module already present"; return
  fi

  # Ask which backend to use. Default follows header availability: kernel module
  # when headers are present, userspace otherwise. AWG_BACKEND=module|userspace
  # skips the prompt.
  local backend="${AWG_BACKEND:-}"
  if [ -z "$backend" ]; then
    local def=1 hint=""
    if ! kernel_headers_available; then def=2; hint="  <- no kernel headers for $(uname -r)"; fi
    echo "Which AmneziaWG backend?" >/dev/tty
    echo "  1) module     — kernel module, best performance." >/dev/tty
    echo "                  needs: dkms, a C toolchain (build-essential/gcc+make)," >/dev/tty
    echo "                  and matching kernel headers (linux-headers-$(uname -r))." >/dev/tty
    echo "  2) userspace  — amneziawg-go, no kernel headers needed (WSL2, etc).${hint}" >/dev/tty
    echo "                  downloads a prebuilt binary; needs Go only if that fails." >/dev/tty
    ask "Choose 1 or 2" abchoice "$def"
    case "$abchoice" in
      1|module) backend=module ;;
      2|userspace) backend=userspace ;;
      *) die "invalid choice: $abchoice" ;;
    esac
  fi

  if [ "$backend" = userspace ]; then
    install_awg_go
    return
  fi

  if ! kernel_headers_available; then
    warn "no kernel headers for $(uname -r); the module build will likely fail (falls back to userspace)"
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
  elif make -C "$src" && make -C "$src" install && depmod -a; then
    warn "DKMS failed; installed module directly (won't survive kernel upgrades)"
  else
    warn "kernel module build failed; falling back to userspace amneziawg-go"
    rm -rf "$tmp"
    install_awg_go
    return
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

# Compose override that points backend/frontend at the prebuilt GHCR images so
# `docker compose up` uses the pulled images instead of building locally.
write_ghcr_override() {
  local tag="$1"
  cat > docker-compose.ghcr.yml <<EOF
services:
  backend:
    image: ${PANEL_IMAGE_API}:${tag}
    pull_policy: always
  frontend:
    image: ${PANEL_IMAGE_WEB}:${tag}
    pull_policy: always
EOF
}

# Bring the stack up either from prebuilt GHCR images or a local source build.
# PANEL_INSTALL_METHOD=images|source skips the prompt; PANEL_VERSION pins a tag
# (e.g. v1.1.1); if unset, :latest is used. Assumes CWD is the repo checkout.
provision_panel() {
  local method="${PANEL_INSTALL_METHOD:-}"
  if [ -z "$method" ]; then
    echo "How do you want to install the panel?" >/dev/tty
    echo "  1) images  — pull prebuilt containers from GHCR (fast, no build)" >/dev/tty
    echo "  2) source  — build the containers locally from source" >/dev/tty
    ask "Choose 1 or 2" pchoice 1
    case "$pchoice" in
      1|images) method=images ;;
      2|source) method=source ;;
      *) die "invalid choice: $pchoice" ;;
    esac
  fi

  if [ "$method" = images ]; then
    local tag="${PANEL_VERSION:-latest}"; tag="${tag#v}"
    info "pulling prebuilt panel images ($tag) from GHCR"
    write_ghcr_override "$tag"
    if docker compose -f docker-compose.yml -f docker-compose.ghcr.yml pull; then
      docker compose -f docker-compose.yml -f docker-compose.ghcr.yml up -d --no-build
      return
    fi
    warn "pulling prebuilt images failed (private package? run 'docker login ghcr.io'); building from source"
    rm -f docker-compose.ghcr.yml
  fi
  info "building + starting containers"
  docker compose up -d --build
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
    local wan; wan="$(ip route get 1.1.1.1 2>/dev/null | grep -oP 'dev \K\S+' | head -1)"
    wan="${wan:-eth0}"
    info "detected WAN interface: $wan"
    cat > .env <<EOF
DB_PASSWORD=$(openssl rand -hex 16)
SESSION_SECRET=$secret
HUB_ENDPOINT=$endpoint
HUB_POOL_CIDR=10.8.0.0/24
HUB_ADDRESS=10.8.0.1
HUB_WAN_IFACE=$wan
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

  provision_panel
  ok "panel up. UI: http://$(hostname -I | awk '{print $1}'):3000  (login: admin)"
  warn "put a TLS reverse proxy in front, then set INSECURE_COOKIES= empty in .env"
}

# --- node install ----------------------------------------------------------
# The node self-enrolls: it announces itself to the panel and waits for the admin
# to approve, then pulls + applies its config automatically (config push over
# CGNAT via polling). No config is pasted by hand.
install_node() {
  install_awg_module
  install_awg_tools
  enable_forwarding
  mkdir -p "$AWG_CONF_DIR"

  local webpw
  ask_secret "Set a password for the node web UI (user: admin)" webpw
  [ -n "$webpw" ] || die "web UI password required"

  install_nodeagent "$webpw"

  local ip; ip="$(hostname -I | awk '{print $1}')"
  ok "node agent installed."
  ok "open the node web UI: http://${ip}:8088  (user: admin)"
  info "there, enter the panel's IP and click Connect, then approve the node in the panel."
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

# Map `uname -m` to the Go arch used in release asset names. Echoes nothing on
# an unsupported arch (caller decides whether to fall back to source).
nodeagent_arch() {
  case "$(uname -m)" in
    x86_64) echo amd64 ;;
    aarch64|arm64) echo arm64 ;;
    *) return 1 ;;
  esac
}

# Download a prebuilt nodeagent from GitHub Releases into /usr/local/bin.
# Honors NODEAGENT_VERSION (a release tag like v1.1.1); otherwise resolves the
# latest release via the GitHub API. Returns non-zero so the caller can fall
# back to a source build.
download_nodeagent_binary() {
  local arch; arch="$(nodeagent_arch)" || { warn "no prebuilt binary for arch $(uname -m)"; return 1; }
  local url
  if [ -n "${NODEAGENT_VERSION:-}" ]; then
    url="https://github.com/${REPO_SLUG}/releases/download/${NODEAGENT_VERSION}/nodeagent-${NODEAGENT_VERSION}-linux-${arch}"
  else
    info "resolving latest nodeagent release for linux/${arch}"
    url="$(curl -fsSL "https://api.github.com/repos/${REPO_SLUG}/releases/latest" 2>/dev/null \
      | grep -oE '"browser_download_url"[[:space:]]*:[[:space:]]*"[^"]*"' \
      | cut -d'"' -f4 \
      | grep -E "nodeagent-.*-linux-${arch}$" \
      | head -1)"
    [ -n "$url" ] || { warn "no matching release asset for linux/${arch}"; return 1; }
  fi
  info "downloading $url"
  curl -fsSL "$url" -o /usr/local/bin/awg-nodeagent || { warn "download failed"; return 1; }
  chmod +x /usr/local/bin/awg-nodeagent
  ok "installed prebuilt node agent"
}

# Install Go, fetch sources, and compile the nodeagent into /usr/local/bin.
build_nodeagent_from_source() {
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
}

# Provision /usr/local/bin/awg-nodeagent either from a GitHub release binary or
# by compiling from source. NODE_INSTALL_METHOD=binary|source skips the prompt.
provision_nodeagent() {
  local method="${NODE_INSTALL_METHOD:-}"
  if [ -z "$method" ]; then
    echo "How do you want to install the node agent?" >/dev/tty
    echo "  1) binary  — download a prebuilt release from GitHub (fast, no build tools)" >/dev/tty
    echo "  2) source  — install Go + toolchain and compile from source" >/dev/tty
    ask "Choose 1 or 2" nchoice 1
    case "$nchoice" in
      1|binary) method=binary ;;
      2|source) method=source ;;
      *) die "invalid choice: $nchoice" ;;
    esac
  fi

  if [ "$method" = binary ]; then
    download_nodeagent_binary && return
    warn "prebuilt install failed; falling back to building from source"
  fi
  build_nodeagent_from_source
}

# install_nodeagent <web_password>
# Installs the agent as a systemd service. The node's panel is set later via the
# web UI (enter panel IP -> Connect); LAN subnet + iface are auto-detected.
install_nodeagent() {
  local webpw="$1"

  provision_nodeagent

  umask 077
  cat >/etc/awg-nodeagent.env <<EOF
STATE_FILE=/var/lib/awg-nodeagent/state.json
AWG_IFACE=$AWG_IFACE
AWG_CONF=$AWG_CONF_DIR/$AWG_IFACE.conf
NODE_PASSWORD=$webpw
NODE_LISTEN=:8088
EOF
  # No kernel module: force awg-quick down the userspace amneziawg-go path.
  if [ -n "$AWG_USERSPACE" ]; then
    echo "WG_QUICK_USERSPACE_IMPLEMENTATION=amneziawg-go" >>/etc/awg-nodeagent.env
  fi
  cat >/etc/systemd/system/awg-nodeagent.service <<'EOF'
[Unit]
Description=beautifulwg node agent (enroll + config push + web editor)
After=network-online.target
Wants=network-online.target

[Service]
EnvironmentFile=/etc/awg-nodeagent.env
ExecStart=/usr/local/bin/awg-nodeagent
Restart=on-failure
RestartSec=5
User=root

[Install]
WantedBy=multi-user.target
EOF
  systemctl daemon-reload
  systemctl enable --now awg-nodeagent
  ok "node agent running (systemd: awg-nodeagent)"
  [ -n "$webpw" ] && ok "node web editor: http://<node-lan-ip>:8088 (user: admin)"
  [ -n "$webpw" ] && warn "web editor runs awg-quick as root — keep it LAN-only"
}

case "$MODE" in
  panel) install_panel ;;
  node)  install_node ;;
esac