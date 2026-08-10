#!/usr/bin/env bash
#
# 6ers3rk uninstaller. Mirror of install.sh.
#
#   curl -fsSL https://raw.githubusercontent.com/o6ez9na/beauty-awg/main/scripts/uninstall.sh | sudo bash
#
# Asks whether to remove the PANEL or a NODE.
# Non-interactive:  ... | sudo bash -s -- panel     (or: node)
#
# By default removes only 6ers3rk's own bits and leaves shared things
# (Docker, the AmneziaWG kernel module) alone — the host may run other apps.
#   PURGE_DATA=1   also delete the DB volume + /opt/6ers3rk (panel) / state (node)
#   PURGE_AWG=1    also remove the AmneziaWG kernel module (+ tools on a node)
#   FORCE=1        skip the confirmation prompt
#
set -euo pipefail

INSTALL_DIR="${INSTALL_DIR:-/opt/6ers3rk}"
AWG_IFACE="${AWG_IFACE:-awg0}"

# Layout mirror of install.sh: Entware routers (Keenetic) keep everything under
# /opt and have no systemd — their service is an /opt/etc/init.d/S* script.
if command -v opkg >/dev/null 2>&1; then
  ENTWARE=1
  BIN_DIR=/opt/bin; TOOL_BIN_DIR=/opt/bin; DOC_DIR=/opt/share/doc
  ENV_FILE=/opt/etc/awg-nodeagent.env
  STATE_DIR=/opt/var/lib/awg-nodeagent
  AWG_CONF_DIR="/opt/etc/amnezia/amneziawg"
  INIT_SCRIPT=/opt/etc/init.d/S99awg-nodeagent
else
  ENTWARE=""
  BIN_DIR=/usr/local/bin; TOOL_BIN_DIR=/usr/bin; DOC_DIR=/usr/share/doc
  ENV_FILE=/etc/awg-nodeagent.env
  STATE_DIR=/var/lib/awg-nodeagent
  AWG_CONF_DIR="/etc/amnezia/amneziawg"
  INIT_SCRIPT=""
fi

c() { printf '\033[%sm%s\033[0m' "$1" "$2"; }
info() { echo "$(c '1;34' '::') $*"; }
ok()   { echo "$(c '1;32' 'ok') $*"; }
warn() { echo "$(c '1;33' 'warn') $*" >&2; }
die()  { echo "$(c '1;31' 'error') $*" >&2; exit 1; }

[ "$(id -u)" -eq 0 ] || die "run as root (sudo)."

if [ -r /dev/tty ]; then TTY=/dev/tty; else TTY=/dev/stdin; fi
ask() {
  local prompt="$1" __var="$2" def="${3:-}" reply
  if [ -n "$def" ]; then prompt="$prompt [$def]"; fi
  printf '%s: ' "$prompt" >/dev/tty
  read -r reply <"$TTY" || true
  printf -v "$__var" '%s' "${reply:-$def}"
}

PURGE_DATA="${PURGE_DATA:-}"
PURGE_AWG="${PURGE_AWG:-}"
FORCE="${FORCE:-}"

# --- mode selection --------------------------------------------------------
MODE="${1:-${UNINSTALL_MODE:-}}"
if [ -z "$MODE" ]; then
  echo "What do you want to remove?" >/dev/tty
  echo "  1) panel" >/dev/tty
  echo "  2) node" >/dev/tty
  ask "Choose 1 or 2" choice
  case "$choice" in
    1|panel) MODE=panel ;;
    2|node)  MODE=node ;;
    *) die "invalid choice: $choice" ;;
  esac
fi
[ "$MODE" = panel ] || [ "$MODE" = node ] || die "MODE must be panel or node (got: $MODE)"

if [ -z "$FORCE" ]; then
  echo "About to remove 6ers3rk $(c '1;36' "$MODE")." >/dev/tty
  [ -n "$PURGE_DATA" ] && echo "  + PURGE_DATA: DB volume / repo / node state will be DELETED" >/dev/tty
  [ -n "$PURGE_AWG" ]  && echo "  + PURGE_AWG: AmneziaWG kernel module will be removed" >/dev/tty
  ask "Type 'yes' to continue" confirm
  [ "$confirm" = yes ] || die "aborted"
fi

# --- shared teardown -------------------------------------------------------
teardown_awg_iface() {
  info "tearing down $AWG_IFACE + nftables"
  awg-quick down "$AWG_IFACE" 2>/dev/null || ip link del "$AWG_IFACE" 2>/dev/null || true
  nft delete table inet awgacl 2>/dev/null || true
  nft delete table ip awgnat 2>/dev/null || true
  rm -f /etc/awgpanel/acl.nft
}

remove_awg_module() {
  [ -n "$PURGE_AWG" ] || { info "keeping AmneziaWG kernel module (set PURGE_AWG=1 to remove)"; return; }
  info "removing AmneziaWG kernel module"
  modprobe -r amneziawg 2>/dev/null || true
  local ver; ver="$(dkms status 2>/dev/null | sed -n 's/^amneziawg\/\([^,]*\).*/\1/p' | head -1)"
  [ -n "$ver" ] && dkms remove "amneziawg/$ver" --all 2>/dev/null || true
  rm -rf /usr/src/amneziawg-* 2>/dev/null || true
  # userspace backend (installed as a fallback when the module can't be built)
  rm -f "$TOOL_BIN_DIR/amneziawg-go"
  rm -rf "$DOC_DIR/amneziawg-go"
}

remove_sysctl() {
  rm -f /etc/sysctl.d/99-6ers3rk.conf
}

# Stop and forget the node agent service, whichever init system runs it.
remove_node_service() {
  if [ -n "$ENTWARE" ]; then
    [ -x "$INIT_SCRIPT" ] && "$INIT_SCRIPT" stop >/dev/null 2>&1
    rm -f "$INIT_SCRIPT" /opt/var/run/awg-nodeagent.pid /opt/var/run/awg-nodeagent.child
    return 0
  fi
  systemctl disable --now awg-nodeagent 2>/dev/null || true
  rm -f /etc/systemd/system/awg-nodeagent.service
  systemctl disable --now "awg-quick@$AWG_IFACE" 2>/dev/null || true
  systemctl daemon-reload 2>/dev/null || true
}

# --- panel removal ---------------------------------------------------------
remove_panel() {
  if [ -d "$INSTALL_DIR" ]; then
    info "stopping containers"
    if [ -n "$PURGE_DATA" ]; then
      ( cd "$INSTALL_DIR" && docker compose down -v --remove-orphans 2>/dev/null ) || true
    else
      ( cd "$INSTALL_DIR" && docker compose down --remove-orphans 2>/dev/null ) || true
    fi
    docker image rm 6ers3rk-backend 6ers3rk-frontend 2>/dev/null || true
  else
    warn "$INSTALL_DIR not found; skipping compose"
  fi

  teardown_awg_iface
  remove_sysctl
  remove_awg_module

  if [ -n "$PURGE_DATA" ]; then
    info "removing $INSTALL_DIR"
    rm -rf "$INSTALL_DIR"
  else
    info "keeping $INSTALL_DIR (set PURGE_DATA=1 to delete repo + DB volume)"
  fi
  ok "panel removed."
}

# --- node removal ----------------------------------------------------------
remove_node() {
  info "stopping node agent"
  remove_node_service
  rm -f "$ENV_FILE" "$BIN_DIR/awg-nodeagent"

  teardown_awg_iface
  rm -f "$AWG_CONF_DIR/$AWG_IFACE.conf" "$AWG_CONF_DIR/$AWG_IFACE.conf.bak"
  remove_sysctl

  if [ -n "$PURGE_DATA" ]; then
    info "removing node state"
    rm -rf "$STATE_DIR"
  else
    info "keeping $STATE_DIR (set PURGE_DATA=1 to delete keypair+enrollment)"
  fi

  if [ -n "$PURGE_AWG" ]; then
    remove_awg_module
    rm -f "$TOOL_BIN_DIR/awg" "$TOOL_BIN_DIR/awg-quick"
    rm -rf "$DOC_DIR/amneziawg-tools"
  else
    info "keeping AmneziaWG module + tools (set PURGE_AWG=1 to remove)"
  fi
  ok "node removed."
}

case "$MODE" in
  panel) remove_panel ;;
  node)  remove_node ;;
esac

[ -n "$ENTWARE" ] || warn "Docker itself was left installed. Remove it manually if nothing else uses it."
