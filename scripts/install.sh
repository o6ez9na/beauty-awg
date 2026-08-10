#!/usr/bin/env bash
#
# 6ers3rk installer.
#
#   curl -fsSL https://raw.githubusercontent.com/YOURUSER/6ers3rk/main/scripts/install.sh | sudo bash
#
# Asks whether to install the PANEL (VPS hub) or a NODE (home server behind CGNAT).
# Non-interactive:  ... | sudo bash -s -- panel      (or: node)
#                   or set INSTALL_MODE=panel|node
#
# If the chosen component is ALREADY installed, the script UPDATES it in place
# (pull latest + refresh containers, or refresh the node agent binary + restart)
# instead of reinstalling — keeping .env / the web-UI password / enrollment.
# Set FORCE_REINSTALL=1 to run the full install path anyway.
#
set -euo pipefail

REPO_URL="${REPO_URL:-https://github.com/o6ez9na/beauty-awg.git}"
REPO_SLUG="${REPO_SLUG:-o6ez9na/beauty-awg}"
INSTALL_DIR="${INSTALL_DIR:-/opt/6ers3rk}"
# Pre-rename install locations, migrated to $INSTALL_DIR on update (see
# migrate_legacy_panel). Space-separated so extra paths can be appended via env.
LEGACY_INSTALL_DIRS="${LEGACY_INSTALL_DIRS:-/opt/beautifulwg /opt/beauty-awg}"
PANEL_IMAGE_API="${PANEL_IMAGE_API:-ghcr.io/${REPO_SLUG}/panel-api}"
PANEL_IMAGE_WEB="${PANEL_IMAGE_WEB:-ghcr.io/${REPO_SLUG}/panel-web}"
AWG_IFACE="${AWG_IFACE:-awg0}"
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
elif command -v opkg >/dev/null 2>&1; then PKG=opkg
else die "unsupported distro (need apt, dnf or opkg)."; fi

OPKG_UPDATED=""
pkg_install() {
  case "$PKG" in
    apt) DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends "$@" ;;
    dnf) dnf install -y "$@" ;;
    opkg)
      [ -n "$OPKG_UPDATED" ] || { opkg update >/dev/null && OPKG_UPDATED=1; }
      # Entware package sets differ per target; a missing optional package must
      # not abort the install, so each one is tried on its own.
      local p
      for p in "$@"; do opkg install "$p" >/dev/null 2>&1 || warn "opkg: $p unavailable"; done
      ;;
  esac
}

# --- install layout --------------------------------------------------------
# Entware (Keenetic and other routers) keeps everything under /opt, usually on
# external storage: the firmware's rootfs is read-only and /etc is a tmpfs
# rebuilt on every boot, so nothing written outside /opt survives a reboot.
# There is no systemd either — boot-time services are /opt/etc/init.d/S* scripts
# run by rc.unslung.
if [ "$PKG" = opkg ]; then
  ENTWARE=1
  BIN_DIR=/opt/bin              # node agent
  TOOL_BIN_DIR=/opt/bin         # awg, awg-quick, amneziawg-go
  DOC_DIR=/opt/share/doc
  ENV_FILE=/opt/etc/awg-nodeagent.env
  STATE_FILE=/opt/var/lib/awg-nodeagent/state.json
  AWG_CONF_DIR="/opt/etc/amnezia/amneziawg"
  INIT_SCRIPT=/opt/etc/init.d/S99awg-nodeagent
else
  ENTWARE=""
  BIN_DIR=/usr/local/bin
  TOOL_BIN_DIR=/usr/bin
  DOC_DIR=/usr/share/doc
  ENV_FILE=/etc/awg-nodeagent.env
  STATE_FILE=/var/lib/awg-nodeagent/state.json
  AWG_CONF_DIR="/etc/amnezia/amneziawg"
  INIT_SCRIPT=""
fi

# --- service control (systemd or Entware init script) ----------------------
svc_install() { # svc_install — write the unit/init script for the node agent
  if [ -n "$ENTWARE" ]; then write_init_script; else write_systemd_unit; fi
}
svc_enable_start() {
  if [ -n "$ENTWARE" ]; then
    "$INIT_SCRIPT" start
  else
    systemctl daemon-reload
    systemctl enable --now awg-nodeagent
  fi
}
svc_restart() {
  if [ -n "$ENTWARE" ]; then
    "$INIT_SCRIPT" restart
  else
    systemctl daemon-reload
    systemctl restart awg-nodeagent
  fi
}

# First non-loopback IPv4 of this host. busybox has no `hostname -I`, so fall
# back to the address the default route picks.
host_ip() {
  hostname -I 2>/dev/null | awk '{print $1}' | grep -q . && { hostname -I | awk '{print $1}'; return; }
  ip -4 route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src") print $(i+1)}' | head -1
}

# Single-quote a value for a shell-sourced env file (and systemd's
# EnvironmentFile, which uses the same rules).
shell_quote() { printf "'%s'" "$(printf '%s' "$1" | sed "s/'/'\\\\''/g")"; }

# Best guess for the LAN the node should expose: the first RFC1918 IPv4 on an
# interface that is NOT the default-route (WAN) one. On a router the default
# route leaves via the WAN, so auto-detect-by-default-route picks the WAN subnet
# — wrong. This offers the LAN bridge (e.g. br0 192.168.1.1/24) as the prompt
# default instead. Prints "<iface> <cidr>" or nothing.
lan_guess() {
  local wan
  wan="$(ip -o route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="dev") print $(i+1)}' | head -1)"
  ip -o -4 addr show 2>/dev/null | awk -v wan="$wan" '
    $2!=wan && $2!="lo" {
      split($4,a,"/"); ip=a[1]
      if (ip ~ /^192\.168\./ || ip ~ /^10\./ || ip ~ /^172\.(1[6-9]|2[0-9]|3[01])\./) { print $2, $4; exit }
    }'
}

# --- already-installed detection -------------------------------------------
# A panel is "installed" once its repo is cloned AND configured (.env written).
panel_installed() { [ -d "$INSTALL_DIR/.git" ] && [ -f "$INSTALL_DIR/.env" ]; }
# A node is "installed" once the agent's service definition exists — a systemd
# unit, or an Entware init script on routers.
node_installed()  {
  [ -f /etc/systemd/system/awg-nodeagent.service ] && return 0
  [ -n "$INIT_SCRIPT" ] && [ -f "$INIT_SCRIPT" ]
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
info "mode: $(c '1;36' "$MODE")"
if [ -n "$ENTWARE" ]; then
  info "Entware detected — installing under /opt (no systemd)"
  [ "$MODE" = node ] || die "the panel needs Docker + Postgres; a router can only run a node."
fi

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
    # A router's firmware ships no headers, and an empty case would return 0.
    opkg) return 1 ;;
  esac
}

# Build the userspace AmneziaWG backend (amneziawg-go) into $TOOL_BIN_DIR.
AWG_GO_REF="${AWG_GO_REF:-master}"
# Download a prebuilt amneziawg-go from our GitHub Releases into $TOOL_BIN_DIR.
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
  # Download to a temp file + rename over the target rather than writing it in
  # place: on an update, amneziawg-go may be running as a child of awg-quick,
  # and the kernel refuses to open a currently-executing file for writing
  # (ETXTBSY). rename() has no such restriction.
  local tmp; tmp="$(mktemp "$TOOL_BIN_DIR/.amneziawg-go.XXXXXX")"
  if ! curl -fsSL "$url" -o "$tmp"; then
    warn "download failed"; rm -f "$tmp"; return 1
  fi
  chmod +x "$tmp"
  mv -f "$tmp" "$TOOL_BIN_DIR/amneziawg-go"
  # MIT requires the license notice to accompany the binary (same release path).
  mkdir -p "$DOC_DIR/amneziawg-go"
  curl -fsSL "${url%/*}/amneziawg-go-LICENSE" -o "$DOC_DIR/amneziawg-go/LICENSE" 2>/dev/null \
    || warn "could not fetch amneziawg-go LICENSE (MIT); see ${REPO_SLUG} release"
  ok "installed prebuilt amneziawg-go"
}

# Build amneziawg-go from source into $TOOL_BIN_DIR.
build_awg_go_from_source() {
  info "building amneziawg-go (userspace backend)"
  install_go
  case "$PKG" in apt) pkg_install git ;; dnf) pkg_install git ;; esac
  local tmp; tmp="$(mktemp -d)"
  git clone --depth 1 --branch "$AWG_GO_REF" \
    https://github.com/amnezia-vpn/amneziawg-go.git "$tmp/awg-go"
  ( cd "$tmp/awg-go" && /usr/local/go/bin/go build -o "$TOOL_BIN_DIR/amneziawg-go" . ) \
    || ( cd "$tmp/awg-go" && go build -o "$TOOL_BIN_DIR/amneziawg-go" . ) \
    || die "amneziawg-go build failed"
  # Keep the MIT license notice alongside the binary.
  mkdir -p "$DOC_DIR/amneziawg-go"
  cp "$tmp/awg-go/LICENSE" "$DOC_DIR/amneziawg-go/LICENSE" 2>/dev/null || true
  rm -rf "$tmp"
  ok "amneziawg-go installed ($TOOL_BIN_DIR/amneziawg-go)"
}

# Provide $TOOL_BIN_DIR/amneziawg-go: prefer a prebuilt release, fall back to source.
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
  # Routers ship a closed, headerless kernel: DKMS is not an option there, so
  # the userspace backend is the only backend.
  if [ -n "$ENTWARE" ]; then
    info "no kernel headers on this router — using the userspace backend"
    install_awg_go
    return
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

# Download prebuilt awg/awg-quick (a tarball) from our GitHub Releases. The
# tarball ships COPYING (GPL-2) + a source pointer, laid out under $DOC_DIR.
download_awg_tools_binary() {
  local arch; arch="$(nodeagent_arch)" || { warn "no prebuilt awg-tools for arch $(uname -m)"; return 1; }
  local url
  if [ -n "${NODEAGENT_VERSION:-}" ]; then
    url="https://github.com/${REPO_SLUG}/releases/download/${NODEAGENT_VERSION}/amneziawg-tools-${NODEAGENT_VERSION}-linux-${arch}.tar.gz"
  else
    info "resolving latest amneziawg-tools release for linux/${arch}"
    url="$(curl -fsSL "https://api.github.com/repos/${REPO_SLUG}/releases/latest" 2>/dev/null \
      | grep -oE '"browser_download_url"[[:space:]]*:[[:space:]]*"[^"]*"' \
      | cut -d'"' -f4 \
      | grep -E "amneziawg-tools-.*-linux-${arch}\.tar\.gz$" \
      | head -1)"
    [ -n "$url" ] || { warn "no matching amneziawg-tools asset for linux/${arch}"; return 1; }
  fi
  # awg-quick is a bash script and drives `ip`. awg (C) links libmnl — except
  # in the MIPS builds, which are static, so Entware needs no libmnl package.
  case "$PKG" in
    apt) pkg_install bash iproute2 libmnl0 ;;
    dnf) pkg_install bash iproute libmnl ;;
    opkg) pkg_install bash ip-full iptables ;;
  esac
  info "downloading $url"
  local tmp; tmp="$(mktemp -d)"
  if ! curl -fsSL "$url" -o "$tmp/awg-tools.tar.gz" || ! tar -xzf "$tmp/awg-tools.tar.gz" -C "$tmp"; then
    warn "download/extract failed"; rm -rf "$tmp"; return 1
  fi
  # cp+chmod rather than install(1): busybox does not always carry that applet.
  cp "$tmp/awg" "$TOOL_BIN_DIR/awg" && chmod 755 "$TOOL_BIN_DIR/awg"
  cp "$tmp/awg-quick" "$TOOL_BIN_DIR/awg-quick" && chmod 755 "$TOOL_BIN_DIR/awg-quick"
  # Entware keeps bash at /opt/bin/bash, but the tarball's awg-quick hard-codes
  # `#!/bin/bash`. With no /bin/bash the kernel can't load the interpreter and
  # every awg-quick call dies with "not found", so awg0 never comes up (and
  # `awg show` then reports "Protocol not supported"). Repoint the shebang at the
  # bash we just installed.
  if [ -n "$ENTWARE" ]; then
    local bash_path; bash_path="$(command -v bash || echo /opt/bin/bash)"
    sed -i "1s|^#!.*|#!${bash_path}|" "$TOOL_BIN_DIR/awg-quick"
  fi
  mkdir -p "$DOC_DIR/amneziawg-tools"
  cp "$tmp/COPYING" "$DOC_DIR/amneziawg-tools/COPYING" 2>/dev/null || true
  cp "$tmp/README.source" "$DOC_DIR/amneziawg-tools/README.source" 2>/dev/null || true
  rm -rf "$tmp"
  ok "installed prebuilt amneziawg-tools (awg, awg-quick)"
}

# Build awg/awg-quick from source (GPL-2). Keeps COPYING alongside the binaries.
build_awg_tools_from_source() {
  [ -z "$ENTWARE" ] || die "no C toolchain on this router: awg/awg-quick must come from a release build (see NODEAGENT_VERSION)."
  info "building AmneziaWG tools (awg, awg-quick)"
  case "$PKG" in
    apt) pkg_install git build-essential libmnl-dev bash iproute2 ;;
    dnf) pkg_install git make gcc libmnl-devel bash iproute ;;
  esac
  local tmp; tmp="$(mktemp -d)"
  git clone --depth 1 https://github.com/amnezia-vpn/amneziawg-tools.git "$tmp/tools"
  make -C "$tmp/tools/src"
  make -C "$tmp/tools/src" install
  mkdir -p "$DOC_DIR/amneziawg-tools"
  cp "$tmp/tools/COPYING" "$DOC_DIR/amneziawg-tools/COPYING" 2>/dev/null || true
  rm -rf "$tmp"
}

# Provide awg + awg-quick: prefer a prebuilt release, fall back to source.
# NODE_INSTALL_METHOD=source forces a local build.
install_awg_tools() {
  if command -v awg-quick >/dev/null 2>&1; then
    ok "amneziawg tools already present"; return
  fi
  if [ "${NODE_INSTALL_METHOD:-}" != source ] && download_awg_tools_binary; then
    return
  fi
  build_awg_tools_from_source
}

enable_forwarding() {
  info "enabling net.ipv4.ip_forward"
  # On Entware /etc is a tmpfs the firmware rebuilds at boot, so a sysctl.d drop-in
  # would be lost. Set it live; the init script re-applies it on every start.
  if [ -n "$ENTWARE" ]; then
    sysctl -q -w net.ipv4.ip_forward=1 || warn "could not set ip_forward (the router may manage it itself)"
    return
  fi
  echo 'net.ipv4.ip_forward=1' >/etc/sysctl.d/99-6ers3rk.conf
  # Also persist in /etc/sysctl.conf: uncomment an existing entry if present,
  # otherwise append one.
  if [ -f /etc/sysctl.conf ]; then
    if grep -qE '^[[:space:]]*#?[[:space:]]*net\.ipv4\.ip_forward[[:space:]]*=' /etc/sysctl.conf; then
      sed -i -E 's|^[[:space:]]*#?[[:space:]]*net\.ipv4\.ip_forward[[:space:]]*=.*|net.ipv4.ip_forward=1|' /etc/sysctl.conf
    else
      echo 'net.ipv4.ip_forward=1' >>/etc/sysctl.conf
    fi
  else
    echo 'net.ipv4.ip_forward=1' >/etc/sysctl.conf
  fi
  sysctl -q -w net.ipv4.ip_forward=1
}

# Internet-exit routing rides a per-exit-node IPIP tunnel (see internal/awg).
# The hub creates those tunnels from the backend container, which has NET_ADMIN
# but cannot load kernel modules, so `ipip` has to be present on the HOST. The
# node side needs the same module plus the iptables mangle extensions its
# awg-quick PostUp uses; a missing one there aborts interface bring-up.
ensure_ipip() {
  modprobe ipip 2>/dev/null || true
  # /etc/modules-load.d is systemd's; on Entware the module either is in the
  # firmware or cannot be had at all, so there is nothing to persist.
  [ -n "$ENTWARE" ] || echo 'ipip' >/etc/modules-load.d/6ers3rk-ipip.conf
  if ip link show tunl0 >/dev/null 2>&1 || lsmod 2>/dev/null | grep -q '^ipip'; then
    ok "ipip module available"
  else
    warn "the ipip kernel module is unavailable: 'browse the internet through <site>' will not work."
    is_lxc && warn "inside LXC the module must be loaded ON THE HOST."
  fi
  # Probe by actually inserting the rule the node config relies on, then remove
  # it again: -S alone would not tell us whether xt_CONNMARK is loadable.
  if iptables -t mangle -A PREROUTING -j CONNMARK --restore-mark 2>/dev/null; then
    iptables -t mangle -D PREROUTING -j CONNMARK --restore-mark 2>/dev/null || true
    ok "iptables mangle + CONNMARK available"
  else
    warn "iptables mangle/CONNMARK is unavailable: internet-exit return traffic will be dropped."
  fi
}

# True when we're running inside an LXC/container (not a VM or bare metal).
is_lxc() {
  systemd-detect-virt -c 2>/dev/null | grep -qiE 'lxc|container' && return 0
  grep -qa 'container=lxc' /proc/1/environ 2>/dev/null && return 0
  [ -f /run/systemd/container ] && grep -qi lxc /run/systemd/container 2>/dev/null && return 0
  return 1
}

# Ensure /dev/net/tun exists on this host. The backend container declares
# `devices: /dev/net/tun` in docker-compose (and awg-quick/amneziawg-go need TUN),
# so `docker compose up` hard-fails if the node is missing. On bare metal/VMs a
# modprobe is enough; inside LXC the device must be passed IN FROM THE HOST.
ensure_tun() {
  modprobe tun 2>/dev/null || true
  if [ ! -c /dev/net/tun ]; then
    info "creating /dev/net/tun"
    mkdir -p /dev/net
    mknod /dev/net/tun c 10 200 2>/dev/null || true
    chmod 600 /dev/net/tun 2>/dev/null || true
  fi
  if [ -c /dev/net/tun ]; then
    ok "/dev/net/tun present"
    return
  fi
  warn "/dev/net/tun is missing and could not be created."
  if is_lxc; then
    warn "this looks like an LXC container: TUN must be passed IN FROM THE HOST."
    warn "run on the LXC host, then re-run this installer:"
    warn "  curl -fsSL https://raw.githubusercontent.com/${REPO_SLUG}/main/scripts/enable-tun-lxc.sh | sudo bash -s -- <CTID>"
  fi
  warn "the backend container needs /dev/net/tun; 'docker compose up' will fail without it."
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
  ensure_tun
  ensure_ipip
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

# Update an existing panel in place: pull the latest source and refresh the
# containers, reusing the install-time method and leaving .env untouched. Skips
# the one-time steps (AmneziaWG, forwarding, TUN, .env) that install_panel runs.
update_panel() {
  info "existing panel found in $INSTALL_DIR — $(c '1;36' updating) (not reinstalling)"
  install_docker
  pkg_install git
  info "pulling latest source"
  git -C "$INSTALL_DIR" pull --ff-only || warn "git pull failed; refreshing current checkout"
  cd "$INSTALL_DIR"
  # Default to prebuilt GHCR images on update — fast, no local build. Whether a
  # GHCR override file exists from a previous install says nothing about
  # whether prebuilt images exist for the CURRENT release, so don't gate on it:
  # provision_panel already tries a pull first and silently falls back to a
  # source build if that fails (private package, no matching tag, offline...).
  : "${PANEL_INSTALL_METHOD:=images}"
  info "refreshing containers (method: $PANEL_INSTALL_METHOD)"
  provision_panel
  ok "panel updated. UI: http://$(hostname -I | awk '{print $1}'):3000  (login: admin)"
}

# Migrate a pre-rename panel checkout (e.g. /opt/beautifulwg) to $INSTALL_DIR
# WITHOUT data loss. Docker Compose derives the project name — and therefore the
# DB volume name (e.g. beautifulwg_pgdata) — from the directory name, so we pin
# COMPOSE_PROJECT_NAME to the old name to reuse the same volume. Handles both:
#   - $INSTALL_DIR doesn't exist yet: move the whole legacy checkout there.
#   - $INSTALL_DIR already has a checkout but no .env (e.g. cloned separately):
#     bring over just the config instead of clobbering that checkout.
# No-op when the panel is already fully set up at $INSTALL_DIR or no legacy
# checkout is found. Run before the panel_installed check so the result is then
# updated normally.
migrate_legacy_panel() {
  panel_installed && return 0   # already fully set up at the new location
  local old="" d
  for d in $LEGACY_INSTALL_DIRS; do
    [ "$d" = "$INSTALL_DIR" ] && continue
    if [ -d "$d/.git" ] && [ -f "$d/.env" ]; then old="$d"; break; fi
  done
  [ -n "$old" ] || return 0

  local proj; proj="$(basename "$old")"
  info "legacy panel found at $old — $(c '1;36' migrating) to $INSTALL_DIR"
  # Stop the old stack first so its containers/network are cleanly recreated
  # under the new path. Named volumes are kept (no -v), and awg0 lives in the
  # host netns, so the tunnels stay up across the restart.
  if command -v docker >/dev/null 2>&1; then
    ( cd "$old" && docker compose down --remove-orphans ) || warn "couldn't stop old stack; continuing"
  fi

  if [ -d "$INSTALL_DIR/.git" ]; then
    info "reusing existing checkout at $INSTALL_DIR; copying config from $old"
    cp "$old/.env" "$INSTALL_DIR/.env"
    [ -f "$old/docker-compose.ghcr.yml" ] && cp "$old/docker-compose.ghcr.yml" "$INSTALL_DIR/"
    rm -rf "$old"
  else
    mkdir -p "$(dirname "$INSTALL_DIR")"
    mv "$old" "$INSTALL_DIR"
  fi

  # Pin the compose project name so the SAME DB volume (${proj}_pgdata) is reused.
  if ! grep -q '^COMPOSE_PROJECT_NAME=' "$INSTALL_DIR/.env" 2>/dev/null; then
    printf 'COMPOSE_PROJECT_NAME=%s\n' "$proj" >> "$INSTALL_DIR/.env"
  fi
  ok "migrated $old -> $INSTALL_DIR (project '$proj' pinned; DB volume ${proj}_pgdata reused)"
}

# Packages a router needs before anything else: awg-quick is a bash script,
# it drives the full `ip` (busybox's applet has no `route get`), and the agent
# fetches its config over TLS.
entware_prereqs() {
  [ -n "$ENTWARE" ] || return 0
  info "installing Entware prerequisites"
  pkg_install bash ip-full iptables curl ca-bundle ca-certificates
  command -v bash >/dev/null 2>&1 || die "bash is required (opkg install bash) — awg-quick is a bash script."
  command -v ip >/dev/null 2>&1 || warn "no 'ip' command: awg-quick cannot configure the interface."
}

# --- node install ----------------------------------------------------------
# The node self-enrolls: it announces itself to the panel and waits for the admin
# to approve, then pulls + applies its config automatically (config push over
# CGNAT via polling). No config is pasted by hand.
install_node() {
  entware_prereqs
  install_awg_module
  install_awg_tools
  enable_forwarding
  ensure_tun
  ensure_ipip
  mkdir -p "$AWG_CONF_DIR"

  local webpw
  ask_secret "Set a password for the node web UI (user: admin)" webpw
  [ -n "$webpw" ] || die "web UI password required"

  # LAN the node exposes. Blank = auto-detect at enroll (fine on a plain server;
  # WRONG on a router, where auto-detect follows the default route out the WAN).
  # Prefilled with a best guess (first RFC1918 iface that isn't the WAN).
  local guess def_if def_sn
  guess="$(lan_guess)"; def_if="${guess%% *}"; def_sn="${guess#* }"
  [ "$def_sn" = "$guess" ] && def_sn=""
  info "leave blank to auto-detect the LAN at enroll (not reliable on a router)"
  ask "LAN interface the node exposes"      NODE_LAN_IFACE "$def_if"
  ask "LAN subnet (CIDR) to route to clients" NODE_SUBNET   "$def_sn"

  install_nodeagent "$webpw"
  write_ndm_netfilter_hook

  local ip; ip="$(host_ip)"
  ok "node agent installed."
  ok "open the node web UI: http://${ip}:8088  (user: admin)"
  info "there, enter the panel's IP and click Connect, then approve the node in the panel."
}

# Update an existing node agent in place: fetch the newest agent binary and
# restart the service. Leaves /etc/awg-nodeagent.env (web password), the systemd
# unit, and enrollment/state intact; skips AmneziaWG + forwarding + TUN setup.
update_node() {
  info "existing node agent found — $(c '1;36' updating) (not reinstalling)"
  # Reuse install-time method; default to a prebuilt binary (source fallback built in).
  : "${NODE_INSTALL_METHOD:=binary}"
  # Nodes pull their awg config from the panel, so an upgraded panel can hand a
  # node a config using features the host lacks. Re-check them here too, or the
  # node quietly keeps falling back to its previous config.
  ensure_ipip
  provision_nodeagent
  svc_restart
  ok "node agent updated + restarted"
  ok "web editor unchanged: http://$(host_ip):8088 (user + password preserved)"
}

# Optional local web UI on the node to view/edit awg config from a LAN browser.
GO_VER="${GO_VER:-1.26.4}"
install_go() {
  if command -v go >/dev/null 2>&1 && go version | grep -qE 'go1\.(2[6-9]|[3-9][0-9])'; then
    ok "go $(go version | awk '{print $3}') present"; return
  fi
  local arch; case "$(uname -m)" in
    x86_64) arch=amd64 ;; aarch64|arm64) arch=arm64 ;;
    # Upstream ships no MIPS toolchain, and a router would be a miserable place
    # to compile anyway: those hosts must use the prebuilt release binaries.
    mips*) die "no Go toolchain for $(uname -m); install from a release binary instead (NODE_INSTALL_METHOD=binary)" ;;
    *) die "unsupported arch for Go: $(uname -m)" ;;
  esac
  info "installing Go $GO_VER (distro package too old for this module)"
  curl -fsSL "https://go.dev/dl/go${GO_VER}.linux-${arch}.tar.gz" | tar -C /usr/local -xz
  export PATH="/usr/local/go/bin:$PATH"
}

# Linux reports plain "mips" in `uname -m` for both endiannesses, so the Go
# arch (mips vs mipsle) has to come from somewhere else: byte 5 of any local
# ELF binary is EI_DATA — 1 = little-endian, 2 = big-endian.
#
# The byte is read with dd (skip 5, read 1) and compared against literal \1/\2
# built by printf. No `od`/`hexdump`: Entware's BusyBox is often built without
# the od applet, so the earlier od-based probe returned empty and mips detection
# failed. dd and printf are always present. Command substitution preserves the
# non-newline control byte, so the case match is exact.
# Set NODEAGENT_ARCH (mips|mipsle|...) to bypass the probe entirely.
mips_go_arch() {
  local probe b5 le be
  le="$(printf '\1')"; be="$(printf '\2')"
  for probe in /bin/sh /bin/busybox "$0"; do
    [ -r "$probe" ] || continue
    b5="$(dd if="$probe" bs=1 skip=5 count=1 2>/dev/null)"
    case "$b5" in
      "$le") echo mipsle; return 0 ;;
      "$be") echo mips; return 0 ;;
    esac
  done
  return 1
}

# Map `uname -m` to the Go arch used in release asset names. Echoes nothing on
# an unsupported arch (caller decides whether to fall back to source).
nodeagent_arch() {
  if [ -n "${NODEAGENT_ARCH:-}" ]; then echo "$NODEAGENT_ARCH"; return 0; fi
  case "$(uname -m)" in
    x86_64) echo amd64 ;;
    aarch64|arm64) echo arm64 ;;
    # Keenetic/Entware routers: mipsel-3.4 and mips-3.4 targets.
    mips|mips64) mips_go_arch ;;
    mipsel|mipsle|mips64el) echo mipsle ;;
    *) return 1 ;;
  esac
}

# Download a prebuilt nodeagent from GitHub Releases into $BIN_DIR.
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
  # Download to a temp file in the SAME dir (same filesystem) and rename over
  # the target, rather than writing the target in place: on update the agent's
  # own systemd service is running that exact binary, and the kernel refuses to
  # open a currently-executing file for writing (ETXTBSY) — curl would fail
  # with "client returned ERROR on write". rename() has no such restriction.
  local tmp; tmp="$(mktemp "$BIN_DIR/.awg-nodeagent.XXXXXX")"
  if ! curl -fsSL "$url" -o "$tmp"; then
    warn "download failed"; rm -f "$tmp"; return 1
  fi
  chmod +x "$tmp"
  mv -f "$tmp" "$BIN_DIR/awg-nodeagent"
  ok "installed prebuilt node agent"
}

# Install Go, fetch sources, and compile the nodeagent into $BIN_DIR.
build_nodeagent_from_source() {
  install_go
  case "$PKG" in apt) pkg_install git ;; dnf) pkg_install git ;; esac
  if [ -d "$INSTALL_DIR/.git" ]; then
    git -C "$INSTALL_DIR" pull --ff-only || warn "git pull failed; building existing checkout"
  else
    git clone "$REPO_URL" "$INSTALL_DIR"
  fi
  info "building node agent"
  ( cd "$INSTALL_DIR" && /usr/local/go/bin/go build -o "$BIN_DIR/awg-nodeagent" ./cmd/nodeagent ) \
    || ( cd "$INSTALL_DIR" && go build -o "$BIN_DIR/awg-nodeagent" ./cmd/nodeagent ) \
    || die "node agent build failed"
}

# Provision $BIN_DIR/awg-nodeagent either from a GitHub release binary or
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

write_systemd_unit() {
  cat >/etc/systemd/system/awg-nodeagent.service <<EOF
[Unit]
Description=6ers3rk node agent (enroll + config push + web editor)
After=network-online.target
Wants=network-online.target

[Service]
EnvironmentFile=$ENV_FILE
ExecStart=$BIN_DIR/awg-nodeagent
Restart=on-failure
RestartSec=5
User=root

[Install]
WantedBy=multi-user.target
EOF
}

# Entware's boot runner (/opt/etc/init.d/rc.unslung) invokes every S* script
# with "start", so the S99 prefix is what enables the agent at boot. The script
# is plain POSIX sh — a router has no bash unless one was installed — and keeps
# the agent under a small supervisor loop, standing in for systemd's
# Restart=on-failure.
write_init_script() {
  mkdir -p "$(dirname "$INIT_SCRIPT")"
  cat >"$INIT_SCRIPT" <<EOF
#!/bin/sh
# 6ers3rk node agent (enroll + config push + web editor).
ENV_FILE=$ENV_FILE
BIN=$BIN_DIR/awg-nodeagent
STATEDIR=$(dirname "$STATE_FILE")
EOF
  cat >>"$INIT_SCRIPT" <<'EOF'
RUNDIR=/opt/var/run
PIDFILE=$RUNDIR/awg-nodeagent.pid
CHILDPID=$RUNDIR/awg-nodeagent.child
LOG=/opt/var/log/awg-nodeagent.log
PATH=/opt/bin:/opt/sbin:/usr/sbin:/usr/bin:/sbin:/bin
export PATH

running() { [ -f "$PIDFILE" ] && kill -0 "$(cat "$PIDFILE")" 2>/dev/null; }

start() {
  running && { echo "awg-nodeagent already running"; return 0; }
  mkdir -p "$RUNDIR" /opt/var/log "$STATEDIR" 2>/dev/null
  # The router reboots into a fresh /proc state; forwarding is what makes this
  # box a router for the tunnel, so re-assert it on every start.
  sysctl -w net.ipv4.ip_forward=1 >/dev/null 2>&1
  set -a
  . "$ENV_FILE"
  set +a
  # Supervisor loop: restart the agent if it dies, mirroring systemd's
  # Restart=on-failure/RestartSec=5. The agent's own pid is recorded on every
  # iteration — killing the supervisor alone would leave the agent orphaned but
  # running, still holding :8088 and the tunnel.
  ( while :; do
      "$BIN" >>"$LOG" 2>&1 &
      echo $! >"$CHILDPID"
      wait $!
      sleep 5
    done ) &
  echo $! >"$PIDFILE"
  echo "awg-nodeagent started"
}

stop() {
  # Supervisor first, or it respawns the agent the moment we kill it.
  if running; then kill "$(cat "$PIDFILE")" 2>/dev/null; fi
  rm -f "$PIDFILE"
  if [ -f "$CHILDPID" ]; then
    kill "$(cat "$CHILDPID")" 2>/dev/null
    sleep 1
    kill -9 "$(cat "$CHILDPID")" 2>/dev/null
    rm -f "$CHILDPID"
  fi
  # Belt and braces for an agent started outside this script.
  pids="$(pidof awg-nodeagent 2>/dev/null)"
  [ -n "$pids" ] && kill $pids 2>/dev/null
  echo "awg-nodeagent stopped"
  return 0
}

case "$1" in
  start) start ;;
  stop) stop ;;
  restart) stop; start ;;
  check|status) running && echo "running" || { echo "stopped"; exit 1; } ;;
  *) echo "usage: $0 {start|stop|restart|check}"; exit 1 ;;
esac
EOF
  chmod 755 "$INIT_SCRIPT"
}

# Refuse to start the agent until every dependency it will call at first
# config-apply is actually present and runnable. The agent applies its config
# the moment the panel approves it; if awg-quick (or its bash interpreter, or
# ip) is missing at that point, it retries in a tight loop and looks broken.
# Failing loudly here — before the service is enabled — turns a confusing
# runtime retry-storm into one clear install-time error.
node_preflight() {
  local missing="" q interp
  command -v awg       >/dev/null 2>&1 || missing="$missing awg"
  command -v awg-quick >/dev/null 2>&1 || missing="$missing awg-quick"
  command -v ip        >/dev/null 2>&1 || missing="$missing ip"
  # Userspace backend must exist when there's no kernel module (always on Entware).
  if [ -n "$AWG_USERSPACE" ] || [ -n "$ENTWARE" ]; then
    command -v amneziawg-go >/dev/null 2>&1 || missing="$missing amneziawg-go"
  fi
  [ -z "$missing" ] || die "node dependencies missing:$missing — install aborted before starting the agent."

  # awg-quick is a bash script; verify its shebang interpreter exists AND is
  # executable. A missing interpreter makes the kernel report "awg-quick: not
  # found" (pointing at the script, not bash), which is exactly what stalls a
  # Keenetic node. The Entware shebang fix runs in download_awg_tools_binary;
  # this is the backstop that proves it took.
  q="$(command -v awg-quick)"
  interp="$(sed -n '1s/^#![[:space:]]*\([^[:space:]]*\).*/\1/p' "$q")"
  if [ -n "$interp" ] && [ ! -x "$interp" ]; then
    die "awg-quick needs '$interp' but it is missing/not executable (Entware ships bash at /opt/bin/bash). Install it or fix the shebang."
  fi
}

# ndm (Keenetic's supervisor) rebuilds netfilter on WAN/link changes and flushes
# rules it does not own — taking the tunnel's FORWARD/nat/mangle rules with it,
# so the node stops forwarding silently minutes later or after a WAN flap. ndm
# runs every executable in /opt/etc/ndm/netfilter.d/ on each rebuild; this hook
# re-applies the awg0 config's PostUp lines there. Entware-only.
write_ndm_netfilter_hook() {
  [ -n "$ENTWARE" ] || return 0
  local dir=/opt/etc/ndm/netfilter.d hook
  hook="$dir/50-awg-nodeagent.sh"
  mkdir -p "$dir"
  cat >"$hook" <<EOF
#!/bin/sh
# Re-apply the awg0 tunnel's PostUp firewall rules after ndm flushes netfilter.
# Installed by the 6ers3rk node installer.
CONF=$AWG_CONF_DIR/$AWG_IFACE.conf
IFACE=$AWG_IFACE
EOF
  cat >>"$hook" <<'EOF'
# ndm calls hooks for several table types; only act on the iptables rebuild.
[ "$type" = "iptables" ] || exit 0
[ -f "$CONF" ] || exit 0
# Nothing to restore if the interface itself is down (agent not up yet).
ip link show "$IFACE" >/dev/null 2>&1 || exit 0
export PATH=/opt/bin:/opt/sbin:/usr/sbin:/usr/bin:/sbin:/bin
# Re-run each PostUp line. These append (`-A`); ndm has just flushed, so this
# restores rather than duplicates. Failures are ignored — a partial restore
# still beats none.
sed -n 's/^PostUp *= *//p' "$CONF" | while IFS= read -r cmd; do
  [ -n "$cmd" ] && eval "$cmd" >/dev/null 2>&1
done
exit 0
EOF
  chmod 755 "$hook"
  ok "ndm netfilter hook installed ($hook) — rules survive WAN flaps"
}

# install_nodeagent <web_password>
# Installs the agent as a service (systemd, or an Entware init script on
# routers). The node's panel is set later via the web UI (enter panel IP ->
# Connect); LAN subnet + iface are auto-detected.
install_nodeagent() {
  local webpw="$1"

  provision_nodeagent
  node_preflight

  umask 077
  mkdir -p "$(dirname "$ENV_FILE")" "$(dirname "$STATE_FILE")"
  # The password is single-quoted: the Entware init script sources this file
  # with `.`, so an unquoted space or $ in it would be word-split or expanded.
  # systemd's EnvironmentFile understands the same quoting.
  cat >"$ENV_FILE" <<EOF
STATE_FILE=$STATE_FILE
AWG_IFACE=$AWG_IFACE
AWG_CONF=$AWG_CONF_DIR/$AWG_IFACE.conf
NODE_PASSWORD=$(shell_quote "$webpw")
NODE_LISTEN=:8088
EOF
  # Manual LAN override (essential on a router — see lan_guess). Empty = the
  # agent auto-detects at enroll.
  [ -n "${NODE_SUBNET:-}" ]    && echo "NODE_SUBNET=$(shell_quote "$NODE_SUBNET")" >>"$ENV_FILE"
  [ -n "${NODE_LAN_IFACE:-}" ] && echo "NODE_LAN_IFACE=$(shell_quote "$NODE_LAN_IFACE")" >>"$ENV_FILE"
  # awg-quick shells out to ip/iptables/sysctl/awg. On Keenetic those live in
  # /opt/sbin (ip-full — busybox's `ip` can't do `ip rule fwmark`), which is NOT
  # in ndm's default PATH, so awg-quick's rules silently fail. Pin a full PATH so
  # the agent (and the awg-quick it forks) always finds them. The init script
  # exports the same; this covers systemd and any manual sourcing of the env.
  echo 'PATH=/opt/bin:/opt/sbin:/usr/sbin:/usr/bin:/sbin:/bin' >>"$ENV_FILE"
  # No kernel module on a router — awg-quick must use the userspace backend, or
  # it tries `ip link add type amneziawg` and fails. Always set it on Entware
  # (there is never a kernel module there); elsewhere only when userspace was the
  # chosen/forced backend.
  if [ -n "$AWG_USERSPACE" ] || [ -n "$ENTWARE" ]; then
    echo "WG_QUICK_USERSPACE_IMPLEMENTATION=amneziawg-go" >>"$ENV_FILE"
  fi
  svc_install
  svc_enable_start
  if [ -n "$ENTWARE" ]; then
    ok "node agent running (init script: $INIT_SCRIPT, log: /opt/var/log/awg-nodeagent.log)"
  else
    ok "node agent running (systemd: awg-nodeagent)"
  fi
  [ -n "$webpw" ] && ok "node web editor: http://<node-lan-ip>:8088 (user: admin)"
  [ -n "$webpw" ] && warn "web editor runs awg-quick as root — keep it LAN-only"
}

case "$MODE" in
  panel)
    [ -n "${FORCE_REINSTALL:-}" ] || migrate_legacy_panel
    if [ -z "${FORCE_REINSTALL:-}" ] && panel_installed; then update_panel; else install_panel; fi ;;
  node)
    if [ -z "${FORCE_REINSTALL:-}" ] && node_installed; then update_node; else install_node; fi ;;
esac