#!/usr/bin/env bash
#
# beautifulwg uninstaller. Mirror of install.sh.
#
#   curl -fsSL https://raw.githubusercontent.com/o6ez9na/beauty-awg/main/scripts/uninstall.sh | sudo bash
#
# Asks whether to remove the PANEL or a NODE.
# Non-interactive:  ... | sudo bash -s -- panel     (or: node)
#
# By default removes only beautifulwg's own bits and leaves shared things
# (Docker, the AmneziaWG kernel module) alone — the host may run other apps.
#   PURGE_DATA=1   also delete the DB volume + /opt/beautifulwg (panel) / state (node)
#   PURGE_AWG=1    also remove the AmneziaWG kernel module (+ tools on a node)
#   FORCE=1        skip the confirmation prompt
#
set -euo pipefail

INSTALL_DIR="${INSTALL_DIR:-/opt/beautifulwg}"
AWG_IFACE="${AWG_IFACE:-awg0}"
AWG_CONF_DIR="/etc/amnezia/amneziawg"

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
  echo "About to remove beautifulwg $(c '1;36' "$MODE")." >/dev/tty
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
  rm -f /usr/bin/amneziawg-go
  rm -rf /usr/share/doc/amneziawg-go
}

remove_sysctl() {
  rm -f /etc/sysctl.d/99-beautifulwg.conf
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
    docker image rm beautifulwg-backend beautifulwg-frontend 2>/dev/null || true
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
  systemctl disable --now awg-nodeagent 2>/dev/null || true
  rm -f /etc/systemd/system/awg-nodeagent.service /etc/awg-nodeagent.env
  rm -f /usr/local/bin/awg-nodeagent
  systemctl disable --now "awg-quick@$AWG_IFACE" 2>/dev/null || true
  systemctl daemon-reload 2>/dev/null || true

  teardown_awg_iface
  rm -f "$AWG_CONF_DIR/$AWG_IFACE.conf" "$AWG_CONF_DIR/$AWG_IFACE.conf.bak"
  remove_sysctl

  if [ -n "$PURGE_DATA" ]; then
    info "removing node state"
    rm -rf /var/lib/awg-nodeagent
  else
    info "keeping /var/lib/awg-nodeagent (set PURGE_DATA=1 to delete keypair+enrollment)"
  fi

  if [ -n "$PURGE_AWG" ]; then
    remove_awg_module
    rm -f /usr/bin/awg /usr/bin/awg-quick
    rm -rf /usr/share/doc/amneziawg-tools
  else
    info "keeping AmneziaWG module + tools (set PURGE_AWG=1 to remove)"
  fi
  ok "node removed."
}

case "$MODE" in
  panel) remove_panel ;;
  node)  remove_node ;;
esac

warn "Docker itself was left installed. Remove it manually if nothing else uses it."
