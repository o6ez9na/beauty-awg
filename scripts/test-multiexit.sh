#!/usr/bin/env bash
# Integration test for IPIP multi-exit (see internal/awg/{render,apply}.go).
#
# Models the WireGuard mesh with veth + /32 routes inside network namespaces:
# the design only depends on L3 reachability plus WG's per-peer source check,
# which is emulated here with explicit source-drop rules on the hub.
#
# Runs unprivileged: re-execs itself into a user+mount+net namespace, so nothing
# on the host (including a real awg0) is touched, and everything it builds dies
# with the process — there is nothing to clean up. Just: scripts/test-multiexit.sh
# Needs: unshare with user namespaces, iproute2, iptables, the ipip module.
#
# The rule blocks below are hand-mirrored from the Go that emits them. Their
# unit tests (internal/awg/exit_test.go) pin the exact command strings, so a
# change there that is not reflected here shows up as a unit-test failure.
#
# Topology
#   mx-cA 10.8.0.5 ─┐                                   ┌─ mx-n1 10.8.0.2 / WAN 203.0.113.1 ─┐
#                   ├─ mx-hub 10.8.0.1 (WAN 203.0.113.3)┤                                    ├─ mx-inet 203.0.113.254
#   mx-cB 10.8.0.6 ─┘                                   └─ mx-n2 10.8.0.3 / WAN 203.0.113.2 ─┘

set -euo pipefail

if [ "$(id -u)" != 0 ]; then
	exec unshare -Urmn --propagation private -- "$(readlink -f "$0")" "$@"
fi

mount -t tmpfs none /run
mkdir -p /run/netns

PASS=0
FAIL=0
ok()   { PASS=$((PASS+1)); printf '  \033[32mPASS\033[0m %s\n' "$1"; }
bad()  { FAIL=$((FAIL+1)); printf '  \033[31mFAIL\033[0m %s\n' "$1"; }
step() { printf '\n\033[1m== %s\033[0m\n' "$1"; }

n() { ip netns exec "$1" "${@:2}"; }

# ---------------------------------------------------------------- topology ---
step "building namespaces"

for ns in mx-hub mx-n1 mx-n2 mx-cA mx-cB mx-cC mx-inet mx-lan; do
	ip netns add "$ns"
	n "$ns" ip link set lo up
done

link() { # link <nsA> <ifA> <nsB> <ifB>
	ip -n "$1" link add "$2" type veth peer name "$4" netns "$3"
	ip -n "$1" link set "$2" up
	ip -n "$3" link set "$4" up
}

# tunnel links (stand in for awg0 peerings)
link mx-hub h-n1  mx-n1   n1-h
link mx-hub h-n2  mx-n2   n2-h
link mx-hub h-ca  mx-cA   ca-h
link mx-hub h-cb  mx-cB   cb-h
link mx-hub h-cc  mx-cC   cc-h
link mx-n1  n1-lan mx-lan   lan-n1
# "internet" links
link mx-hub h-wan mx-inet i-hub
link mx-n1  n1-wan mx-inet i-n1
link mx-n2  n2-wan mx-inet i-n2

# Tunnel links stand in for awg0, so give them WireGuard's MTU: the IPIP devices
# built on top then land at 1400, and the double encapsulation is under real
# pressure rather than veth's roomy 1500.
for l in "mx-hub h-n1" "mx-n1 n1-h" "mx-hub h-n2" "mx-n2 n2-h" \
         "mx-hub h-ca" "mx-cA ca-h" "mx-hub h-cb" "mx-cB cb-h" \
         "mx-hub h-cc" "mx-cC cc-h"; do
	set -- $l; ip -n "$1" link set "$2" mtu 1420
done

# internet segment: one bridge, server lives on it
ip -n mx-inet link add br0 type bridge
ip -n mx-inet link set br0 up
for i in i-hub i-n1 i-n2; do ip -n mx-inet link set "$i" master br0; done
ip -n mx-inet addr add 203.0.113.254/24 dev br0

# hub: /32 on lo, per-peer /32 routes, plus its own WAN default route
ip -n mx-hub addr add 10.8.0.1/32 dev lo
ip -n mx-hub addr add 203.0.113.3/24 dev h-wan
ip -n mx-hub route add 10.8.0.2/32 dev h-n1 src 10.8.0.1
ip -n mx-hub route add 10.8.0.3/32 dev h-n2 src 10.8.0.1
ip -n mx-hub route add 10.8.0.5/32 dev h-ca src 10.8.0.1
ip -n mx-hub route add 10.8.0.6/32 dev h-cb src 10.8.0.1
ip -n mx-hub route add 10.8.0.7/32 dev h-cc src 10.8.0.1
ip -n mx-hub route add default via 203.0.113.254 dev h-wan
n mx-hub sysctl -qw net.ipv4.ip_forward=1
# a realistic distro default: strict rp_filter globally. The per-device
# rp_filter=2 that EnsureExitRoutes sets must win over this.
n mx-hub sysctl -qw net.ipv4.conf.all.rp_filter=1
n mx-hub sysctl -qw net.ipv4.conf.default.rp_filter=1

# WireGuard's cryptokey-routing source check, emulated: a peer may only send
# packets sourced from its own AllowedIPs.
# Its own chain, entered first from both INPUT and FORWARD: a peer's allowed
# sources RETURN to carry on through the rest of the ruleset, anything else is
# dropped. (A plain ACCEPT here would short-circuit the policy rules below it.)
n mx-hub iptables -N SPOOF
n mx-hub iptables -A INPUT   -j SPOOF
n mx-hub iptables -A FORWARD -j SPOOF
antispoof() { # antispoof <hub-iface> <allowed-src>...
	local ifc=$1; shift
	for src in "$@"; do
		n mx-hub iptables -A SPOOF -i "$ifc" -s "$src" -j RETURN
	done
	n mx-hub iptables -A SPOOF -i "$ifc" -j DROP
}
# node peers are allowed their /32 AND their LAN subnet, exactly like AllowedIPs
antispoof h-n1 10.8.0.2 192.168.0.0/24
antispoof h-n2 10.8.0.3 192.168.1.0/24
antispoof h-ca 10.8.0.5
antispoof h-cb 10.8.0.6
antispoof h-cc 10.8.0.7

# nodes
setup_node() { # setup_node <ns> <tun-if> <tun-ip> <wan-if> <wan-ip>
	local ns=$1 tun=$2 ip4=$3 wan=$4 wanip=$5
	ip -n "$ns" addr add "$ip4/32" dev "$tun"
	ip -n "$ns" addr add "$wanip/24" dev "$wan"
	# the route awg0 would install for the client pool
	ip -n "$ns" route add 10.8.0.0/24 dev "$tun"
	ip -n "$ns" route add default via 203.0.113.254 dev "$wan"
	n "$ns" sysctl -qw net.ipv4.ip_forward=1
	n "$ns" sysctl -qw net.ipv4.conf.all.rp_filter=1
	n "$ns" sysctl -qw net.ipv4.conf.default.rp_filter=1
}
setup_node mx-n1 n1-h 10.8.0.2 n1-wan 203.0.113.1
# node1's LAN, and the hub route that its AllowedIPs would install
ip -n mx-n1 addr add 192.168.0.1/24 dev n1-lan
ip -n mx-lan addr add 192.168.0.10/24 dev lan-n1
ip -n mx-lan route add default via 192.168.0.1 dev lan-n1
ip -n mx-hub route add 192.168.0.0/24 dev h-n1 src 10.8.0.1
setup_node mx-n2 n2-h 10.8.0.3 n2-wan 203.0.113.2

# The hub routes a node's whole LAN prefix at the tunnel; over WireGuard that
# needs no address resolution at all. On veth it does, and the node owns none of
# the LAN hosts' addresses, so let it answer for the prefixes it routes.
n mx-n1 sysctl -qw net.ipv4.conf.n1-h.proxy_arp=1
n mx-n2 sysctl -qw net.ipv4.conf.n2-h.proxy_arp=1

# WireGuard has no L2 at all: a node hands any packet routed at the pool straight
# to the hub. veth does have L2, and the hub owns none of the client addresses,
# so ARP for them would fail and mask the L3 behaviour under test. Static neigh
# entries pointing at the hub stand in for WireGuard's L2-less delivery.
for spec in "mx-n1 n1-h h-n1" "mx-n2 n2-h h-n2"; do
	set -- $spec
	hubmac=$(ip -n mx-hub -o link show "$3" | sed -n 's/.*link\/ether \([0-9a-f:]*\).*/\1/p')
	for c in 10.8.0.5 10.8.0.6; do
		ip -n "$1" neigh replace "$c" lladdr "$hubmac" dev "$2" nud permanent
	done
done

# clients
ip -n mx-cA addr add 10.8.0.5/32 dev ca-h
ip -n mx-cA route add default via 10.8.0.1 dev ca-h onlink
ip -n mx-cB addr add 10.8.0.6/32 dev cb-h
ip -n mx-cB route add default via 10.8.0.1 dev cb-h onlink
ip -n mx-cC addr add 10.8.0.7/32 dev cc-h
ip -n mx-cC route add default via 10.8.0.1 dev cc-h onlink

# Hub-exit (a client full-tunnelling out the HUB's own WAN) is a separate,
# older feature that RenderHub changes could regress, so model it too: the
# nftables the hub emits for it are `ip saddr <pool> oifname <WAN> masquerade`
# plus an MSS clamp on everything that is not WAN-bound.
n mx-hub iptables -t nat -A POSTROUTING -s 10.8.0.0/24 -o h-wan -j MASQUERADE
n mx-hub iptables -t mangle -A FORWARD ! -o h-wan -p tcp --tcp-flags SYN,RST SYN \
	-j TCPMSS --set-mss 1280
# The hub's WAN is NOT open to the whole pool: nftables emits the accept per
# hub-exit grant (`ip saddr <client> oifname <WAN> accept`), so only cC may use
# it. Without this, a client whose node-exit was removed would silently fall
# back to the hub's own internet instead of losing it.
n mx-hub iptables -A FORWARD -o h-wan ! -s 10.8.0.7 -j DROP

# --------------------------------------------------- RenderNode PostUp block ---
# Mirrors internal/awg/render.go RenderNode, %i -> the node's tunnel interface.
node_postup() { # node_postup <ns> <tun-if> <node-ip>
	local ns=$1 i=$2 addr=$3
	# pre-existing lines this feature relies on
	n "$ns" iptables -t nat -A POSTROUTING -s 10.8.0.0/24 ! -o "$i" -j MASQUERADE
	n "$ns" iptables -I FORWARD -i "$i" ! -o "$i" -j ACCEPT
	n "$ns" iptables -I FORWARD ! -i "$i" -o "$i" -j ACCEPT
	# the IPIP exit return path
	n "$ns" ip link add ipip-hub type ipip local "$addr" remote 10.8.0.1 || true
	n "$ns" ip addr replace "$addr/32" dev ipip-hub
	n "$ns" ip link set ipip-hub up
	n "$ns" sysctl -qw "net.ipv4.conf.ipip-hub.rp_filter=2"
	n "$ns" iptables -I FORWARD -i ipip-hub -j ACCEPT
	n "$ns" iptables -I FORWARD -o ipip-hub -j ACCEPT
	n "$ns" iptables -t mangle -A PREROUTING -i ipip-hub -j CONNMARK --set-mark 0x33
	n "$ns" iptables -t mangle -A PREROUTING ! -i ipip-hub -j CONNMARK --restore-mark
	n "$ns" iptables -t mangle -A FORWARD -o ipip-hub -p tcp --tcp-flags SYN,RST SYN -j TCPMSS --clamp-mss-to-pmtu
	n "$ns" ip rule add fwmark 0x33 lookup 133 pref 1330 || true
	n "$ns" ip route replace default dev ipip-hub table 133
}
node_postup mx-n1 n1-h 10.8.0.2
node_postup mx-n2 n2-h 10.8.0.3

# ------------------------------------------------ hub EnsureExitRoutes ---------
# Mirrors internal/awg/apply.go EnsureExitRoutes (verified by its DryRun test).
hub_exit_flush() {
	while n mx-hub ip rule del pref 5100 2>/dev/null; do :; done
}
hub_exit_dev() { # hub_exit_dev <dev> <node-ip> <table>
	n mx-hub ip link add "$1" type ipip local 10.8.0.1 remote "$2"
	n mx-hub ip addr replace 10.8.0.1/32 dev "$1"
	n mx-hub ip link set "$1" up
	n mx-hub sysctl -qw "net.ipv4.conf.$1.rp_filter=2"
	n mx-hub ip route replace default dev "$1" table "$3"
}
hub_exit_rule() { # hub_exit_rule <client> <table>
	n mx-hub ip rule add from "$1/32" lookup "$2" pref 5100
}

step "applying exit routes: cA -> node1, cB -> node2"
hub_exit_dev awgex2 10.8.0.2 1002
hub_exit_dev awgex3 10.8.0.3 1003
hub_exit_flush
hub_exit_rule 10.8.0.5 1002
hub_exit_rule 10.8.0.6 1003

# --------------------------------------------------------------- assertions ---
# The server counts echo-requests per observed source address.
n mx-inet iptables -A INPUT -s 203.0.113.1 -p icmp -j ACCEPT
n mx-inet iptables -A INPUT -s 203.0.113.2 -p icmp -j ACCEPT
n mx-inet iptables -A INPUT -s 203.0.113.3 -p icmp -j ACCEPT
n mx-inet iptables -A INPUT -s 10.8.0.0/24  -p icmp -j ACCEPT

seen() { # seen <source> -> packet count observed by the server
	n mx-inet iptables -L INPUT -nvx | awk -v s="$1" '$8==s {print $1; exit}'
}
ping_out() { # ping_out <client-ns> ; success == reply came back
	n "$1" ping -c 3 -i 0.3 -W 2 203.0.113.254 >/dev/null 2>&1
}

step "1. both clients exit simultaneously, each via its own node"

a0=$(seen 203.0.113.1); b0=$(seen 203.0.113.2)
if ping_out mx-cA; then ok "cA reaches the internet (replies returned)"
else bad "cA reaches the internet (replies returned)"; fi
if ping_out mx-cB; then ok "cB reaches the internet (replies returned)"
else bad "cB reaches the internet (replies returned)"; fi
a1=$(seen 203.0.113.1); b1=$(seen 203.0.113.2)

if [ "$a1" -gt "$a0" ]; then ok "server saw cA as 203.0.113.1 (node1 WAN)  [$a0 -> $a1]"
else bad "server saw cA as 203.0.113.1 (node1 WAN)  [$a0 -> $a1]"; fi
if [ "$b1" -gt "$b0" ]; then ok "server saw cB as 203.0.113.2 (node2 WAN)  [$b0 -> $b1]"
else bad "server saw cB as 203.0.113.2 (node2 WAN)  [$b0 -> $b1]"; fi

# Hub-exit (out the hub's own WAN) is the older, separate path. It must keep
# working alongside the per-node exits — RenderHub is what changed under it.
c0=$(seen 203.0.113.3)
if ping_out mx-cC; then ok "cC still full-tunnels out the hub's own WAN"
else bad "cC still full-tunnels out the hub's own WAN"; fi
c1=$(seen 203.0.113.3)
if [ "$c1" -gt "$c0" ]; then ok "server saw cC as 203.0.113.3 (hub WAN)  [$c0 -> $c1]"
else bad "server saw cC as 203.0.113.3 (hub WAN)  [$c0 -> $c1]"; fi
if [ "$a1" -gt "$a0" ] && [ "$b1" -gt "$b0" ] && [ "$c1" -gt "$c0" ]; then
	ok "three clients, three different exit IPs, all at once"
else bad "three clients, three different exit IPs, all at once"; fi

leak=$(seen 10.8.0.0/24)
if [ "${leak:-0}" -eq 0 ]; then ok "no un-NAT'd pool source ever reached the server"
else bad "no un-NAT'd pool source ever reached the server (saw $leak)"; fi

step "2. return traffic really rides the IPIP tunnel"

# Disable the node's policy return (the fwmark rule) and replies follow the main
# table straight back over the tunnel veth, un-encapsulated, carrying an internet
# source. That is exactly what WireGuard's per-peer source check rejects.
dropped() { n mx-hub iptables -L SPOOF -nvx | awk '$3=="DROP" && $6=="h-n1" {print $1; exit}'; }
before=$(dropped)
n mx-n1 ip rule del fwmark 0x33 lookup 133 pref 1330
if ping_out mx-cA; then bad "without the IPIP return path cA loses connectivity"
else ok "without the IPIP return path cA loses connectivity"; fi

# Attribute the loss precisely. The hub's own rp_filter would also reject that
# packet, which would mask the point, so relax it for this one probe: what must
# reject it is the WireGuard source check (here: the emulating DROP rule).
n mx-hub sysctl -qw net.ipv4.conf.all.rp_filter=0
n mx-hub sysctl -qw net.ipv4.conf.h-n1.rp_filter=0
if ping_out mx-cA; then bad "the WireGuard source check rejects the un-tunnelled reply"
else ok "the WireGuard source check rejects the un-tunnelled reply"; fi
after=$(dropped)
if [ "$after" -gt "$before" ]; then ok "  ...and it is that check that dropped it  [$before -> $after]"
else bad "  ...and it is that check that dropped it  [$before -> $after]"; fi
n mx-hub sysctl -qw net.ipv4.conf.all.rp_filter=1
n mx-hub sysctl -qw net.ipv4.conf.h-n1.rp_filter=1

n mx-n1 ip rule add fwmark 0x33 lookup 133 pref 1330
if ping_out mx-cA; then ok "restoring the fwmark rule restores connectivity"
else bad "restoring the fwmark rule restores connectivity"; fi

# And with the tunnel return in place, nothing a node sends is ever dropped by
# that check: the outer source is its own /32. Zero the counters first — the
# probe above deliberately generated drops.
n mx-hub iptables -Z SPOOF
ping_out mx-cA >/dev/null 2>&1
sp=$(dropped)
if [ "$sp" -eq 0 ]; then ok "tunnelled returns pass the hub source check untouched"
else bad "tunnelled returns pass the hub source check untouched (dropped $sp)"; fi

step "3. anti-loop: the CONNMARK restore is scoped ! -i ipip-hub"

# With an unscoped restore, the client->internet packet would be re-routed into
# ipip-hub and never egress WAN. Show the node's WAN actually carried traffic.
fwd=$(n mx-n1 iptables -t nat -L POSTROUTING -nvx | awk '$3=="MASQUERADE" {print $1; exit}')
if [ "${fwd:-0}" -gt 0 ]; then ok "node1 masqueraded exit traffic out its WAN ($fwd pkts, WAN != tunnel iface)"
else bad "node1 masqueraded exit traffic out its WAN"; fi

step "4. the decap device needs BOTH an address and loose rp_filter"

for v in "mx-hub net.ipv4.conf.awgex2.rp_filter 2" "mx-hub net.ipv4.conf.awgex3.rp_filter 2" \
         "mx-n1 net.ipv4.conf.ipip-hub.rp_filter 2" "mx-n2 net.ipv4.conf.ipip-hub.rp_filter 2"; do
	set -- $v
	got=$(n "$1" sysctl -n "$2")
	if [ "$got" = "$3" ]; then ok "$1 $2 = $got"; else bad "$1 $2 = $got (want $3)"; fi
done

# Both knobs are load-bearing on the node, and they only work as a pair. The
# kernel's loose branch in __fib_validate_source() is unreachable on a device
# with no address, so an unaddressed tunnel is rejected under ANY non-zero
# rp_filter -- which is why setting rp_filter=2 alone is not a fix.
# (Removing an interface's last address also drops the routes through it, hence
# the route restore.)
n mx-n1 ip addr del 10.8.0.2/32 dev ipip-hub
n mx-n1 ip route replace default dev ipip-hub table 133
if ping_out mx-cA; then bad "an unaddressed tunnel breaks the return path even at rp_filter=2"
else ok "an unaddressed tunnel breaks the return path even at rp_filter=2"; fi
n mx-n1 ip addr replace 10.8.0.2/32 dev ipip-hub
n mx-n1 ip route replace default dev ipip-hub table 133
if ping_out mx-cA; then ok "re-addressing the tunnel restores it"
else bad "re-addressing the tunnel restores it"; fi

n mx-n1 sysctl -qw net.ipv4.conf.ipip-hub.rp_filter=1
if ping_out mx-cA; then bad "strict rp_filter on the node's tunnel breaks the return path"
else ok "strict rp_filter on the node's tunnel breaks the return path"; fi
n mx-n1 sysctl -qw net.ipv4.conf.ipip-hub.rp_filter=2
if ping_out mx-cA; then ok "restoring rp_filter=2 brings cA back"
else bad "restoring rp_filter=2 brings cA back"; fi

step "5. isolation: removing cB's exit leaves cA working"

hub_exit_flush
n mx-hub ip link del awgex3
hub_exit_rule 10.8.0.5 1002

if ping_out mx-cA; then ok "cA still exits via node1 after node2's exit is torn down"
else bad "cA still exits via node1 after node2's exit is torn down"; fi
if ping_out mx-cB; then bad "cB no longer reaches the internet"
else ok "cB no longer reaches the internet"; fi

devs=$(n mx-hub ip -o link show | awk -F': ' '/awgex/ {print $2}' | tr '\n' ' ')
if [ "$(echo "$devs" | tr -d ' ')" = "awgex2@NONE" ]; then ok "only awgex2 survives the reconcile ($devs)"
else bad "only awgex2 survives the reconcile (got: $devs)"; fi

step "6. re-adding cB's exit is idempotent"

hub_exit_dev awgex3 10.8.0.3 1003
hub_exit_flush
hub_exit_rule 10.8.0.5 1002
hub_exit_rule 10.8.0.6 1003
if ping_out mx-cB; then ok "cB exits via node2 again after re-add"
else bad "cB exits via node2 again after re-add"; fi
rules=$(n mx-hub ip rule show pref 5100 | wc -l)
if [ "$rules" -eq 2 ]; then ok "exactly 2 source rules at pref 5100 (no duplicates)"
else bad "exactly 2 source rules at pref 5100 (got $rules)"; fi

step "7. MTU: the double encapsulation carries real traffic"

# Path MTU is 1400 here (1420 tunnel - 20 IPIP), so 1372+28 sits exactly on it.
if n mx-cA ping -c 2 -i 0.3 -W 2 -M do -s 1372 203.0.113.254 >/dev/null 2>&1; then
	ok "a full-MTU DF packet traverses the double encapsulation"
else bad "a full-MTU DF packet traverses the double encapsulation"; fi
# Oversized with DF must fail loudly (PMTUD), not black-hole silently.
if n mx-cA ping -c 1 -W 2 -M do -s 1500 203.0.113.254 >/dev/null 2>&1; then
	bad "oversized DF packet is rejected rather than silently passed"
else ok "oversized DF packet is rejected (PMTUD signals, no black hole)"; fi
if n mx-cA ping -c 2 -i 0.3 -W 2 -s 2000 203.0.113.254 >/dev/null 2>&1; then
	ok "2028-byte packet gets through fragmented (no black hole)"
else bad "2028-byte packet gets through fragmented (no black hole)"; fi

# A real bulk TCP transfer is what actually catches an MSS/PMTUD black hole:
# the handshake would succeed and the first full-size segment would vanish.
: >/run/rx.count
n mx-inet sh -c 'ncat -l 8080 --recv-only | wc -c >/run/rx.count' &
srv=$!
sleep 0.5
if n mx-cA sh -c 'head -c 1000000 /dev/zero | ncat -w 5 --send-only 203.0.113.254 8080' 2>/dev/null; then
	sleep 0.5; rx=$(cat /run/rx.count 2>/dev/null || echo 0)
	if [ "${rx:-0}" -eq 1000000 ]; then ok "1 MB TCP transfer completes through node1 ($rx bytes)"
	else bad "1 MB TCP transfer completes through node1 (got ${rx:-0} bytes)"; fi
else
	bad "1 MB TCP transfer completes through node1 (connection failed)"
fi
kill $srv 2>/dev/null || true; wait $srv 2>/dev/null || true

# And prove the hub's clamp is the thing acting on those SYNs.
clamped=$(n mx-hub iptables -t mangle -L FORWARD -nvx | awk '$3=="TCPMSS" {print $1; exit}')
if [ "${clamped:-0}" -gt 0 ]; then ok "the hub's MSS clamp fired on exit-bound SYNs ($clamped)"
else bad "the hub's MSS clamp fired on exit-bound SYNs (${clamped:-0})"; fi

step "8. PostDown tears the node's IPIP block down cleanly"

node_postdown() { # node_postdown <ns>
	n "$1" ip route flush table 133 || true
	n "$1" ip rule del fwmark 0x33 lookup 133 pref 1330 || true
	n "$1" iptables -t mangle -D FORWARD -o ipip-hub -p tcp --tcp-flags SYN,RST SYN -j TCPMSS --clamp-mss-to-pmtu || true
	n "$1" iptables -t mangle -D PREROUTING ! -i ipip-hub -j CONNMARK --restore-mark || true
	n "$1" iptables -t mangle -D PREROUTING -i ipip-hub -j CONNMARK --set-mark 0x33 || true
	n "$1" iptables -D FORWARD -i ipip-hub -j ACCEPT || true
	n "$1" iptables -D FORWARD -o ipip-hub -j ACCEPT || true
	n "$1" ip link del ipip-hub || true
}
if node_postdown mx-n2 2>&1; then ok "every PostDown line succeeds"
else bad "every PostDown line succeeds"; fi
leftover=$(n mx-n2 iptables -t mangle -S | grep -c ipip-hub || true)
if [ "$leftover" -eq 0 ]; then ok "no mangle rules left behind"
else bad "no mangle rules left behind ($leftover)"; fi
leftover=$(n mx-n2 ip rule show | grep -c 0x33 || true)
if [ "$leftover" -eq 0 ]; then ok "no fwmark rule left behind"
else bad "no fwmark rule left behind"; fi

step "9. re-up after the teardown works (idempotent bring-up)"

if node_postup_out=$(node_postup mx-n2 n2-h 10.8.0.3 2>&1); then ok "PostUp re-runs cleanly"
else bad "PostUp re-runs cleanly: $node_postup_out"; fi
if ping_out mx-cB; then ok "cB exits via node2 after a full down/up cycle"
else bad "cB exits via node2 after a full down/up cycle"; fi

step "10. bring-up survives an interrupted teardown"

# awg-quick runs PostUp lines with errexit and aborts the interface on the first
# failure. Simulate a `down` that never ran (or died halfway): every line has to
# tolerate finding its own leftovers, or the node never comes back up.
postup_strict() { # postup_strict <ns> <tun-if> <node-ip>; mirrors PostUp verbatim
	local ns=$1 i=$2 addr=$3 line
	set -e
	n "$ns" ip link add ipip-hub type ipip local "$addr" remote 10.8.0.1 || true
	n "$ns" ip addr replace "$addr/32" dev ipip-hub
	n "$ns" ip link set ipip-hub up
	n "$ns" sysctl -qw "net.ipv4.conf.ipip-hub.rp_filter=2"
	n "$ns" ip rule add fwmark 0x33 lookup 133 pref 1330 || true
	n "$ns" ip route replace default dev ipip-hub table 133
	set +e
}

# node2 is fully configured right now; re-run PostUp straight over it.
if err=$(postup_strict mx-n2 n2-h 10.8.0.3 2>&1); then
	ok "PostUp re-runs over a live IPIP block without aborting"
else
	bad "PostUp re-runs over a live IPIP block without aborting: $err"
fi
rules=$(n mx-n2 ip rule show | grep -c 0x33)
if [ "$rules" -eq 1 ]; then ok "no duplicate fwmark rule after the re-run"
else bad "no duplicate fwmark rule after the re-run (got $rules)"; fi
if ping_out mx-cB; then ok "cB still exits via node2 after the re-run"
else bad "cB still exits via node2 after the re-run"; fi

# A half-finished teardown must not stop the rest from being cleaned up either.
n mx-n2 ip rule del fwmark 0x33 lookup 133 pref 1330
if err=$(node_postdown mx-n2 2>&1); then ok "PostDown completes despite an already-removed rule"
else bad "PostDown completes despite an already-removed rule: $err"; fi
if n mx-n2 ip link show ipip-hub >/dev/null 2>&1; then
	bad "the tunnel is gone after the partial teardown"
else ok "the tunnel is gone after the partial teardown"; fi

step "11. upgrading a node that is still on the pre-IPIP config"

# How a real upgrade lands: the panel starts emitting the new config, the agent
# writes it to disk and only THEN runs `awg-quick down` — so the NEW PostDown
# executes against state the OLD PostUp created. Everything it tries to remove
# is therefore missing. What must survive it is access to the node's LAN.
if n mx-cA ping -c 2 -i 0.3 -W 2 192.168.0.10 >/dev/null 2>&1; then
	ok "baseline: cA reaches the LAN behind node1"
else bad "baseline: cA reaches the LAN behind node1"; fi

# Roll node1 back to the pre-IPIP world: no tunnel, and the older masquerade
# form that this branch replaced (-o LAN instead of ! -o %i).
node_postdown mx-n1
n mx-n1 iptables -t nat -D POSTROUTING -s 10.8.0.0/24 ! -o n1-h -j MASQUERADE
n mx-n1 iptables -D FORWARD -i n1-h ! -o n1-h -j ACCEPT
n mx-n1 iptables -D FORWARD ! -i n1-h -o n1-h -j ACCEPT
n mx-n1 iptables -t nat -A POSTROUTING -s 10.8.0.0/24 -o n1-lan -j MASQUERADE
n mx-n1 iptables -A FORWARD -i n1-h -j ACCEPT
n mx-n1 iptables -A FORWARD -o n1-h -j ACCEPT
# and the hub back to the single-exit mechanism it used to drive
hub_exit_flush
n mx-hub ip link del awgex2
n mx-hub ip route replace default dev h-n1 table 51
n mx-hub ip rule add from 10.8.0.5/32 lookup 51 pref 5100

if n mx-cA ping -c 2 -i 0.3 -W 2 192.168.0.10 >/dev/null 2>&1; then
	ok "old-style node still serves its LAN (the state we upgrade FROM)"
else bad "old-style node still serves its LAN (the state we upgrade FROM)"; fi

# Now the upgrade itself. awg-quick stops at the first failing PostDown line, so
# run the WHOLE PostDown that way — including the masquerade/forward lines that
# precede the IPIP block and, unlike it, carry no `|| true`. Against an older
# node those lines describe rules that were never created, so the teardown is
# expected to abort partway; what matters is that the node still comes back.
# NB: the subshell must not be the left side of `||` — bash suppresses errexit
# inside a command whose status is being tested, which would silently make this
# check pass no matter what. Read $? on the next line instead.
set +e
(
	set -e
	n mx-n1 iptables -t nat -D POSTROUTING -s 10.8.0.0/24 ! -o n1-h -j MASQUERADE
	n mx-n1 iptables -D FORWARD -i n1-h ! -o n1-h -j ACCEPT
	n mx-n1 iptables -D FORWARD ! -i n1-h -o n1-h -j ACCEPT
	node_postdown mx-n1
) >/dev/null 2>&1
down_rc=$?
set -e
if [ "$down_rc" -ne 0 ]; then
	ok "PostDown aborts on the pre-IPIP node, as expected (exit $down_rc)"
else bad "PostDown was expected to abort against the old rule set"; fi
node_postup mx-n1 n1-h 10.8.0.2
# the hub reconciles to the new mechanism
n mx-hub ip route del default dev h-n1 table 51 2>/dev/null || true
hub_exit_dev awgex2 10.8.0.2 1002
hub_exit_flush
hub_exit_rule 10.8.0.5 1002
hub_exit_rule 10.8.0.6 1003

if n mx-cA ping -c 2 -i 0.3 -W 2 192.168.0.10 >/dev/null 2>&1; then
	ok "LAN access survives the upgrade despite the aborted teardown"
else bad "LAN access survives the upgrade despite the aborted teardown"; fi
if ping_out mx-cA; then ok "and the node-exit now works through the new IPIP path"
else bad "and the node-exit now works through the new IPIP path"; fi
if [ -z "$(n mx-hub ip route show table 51 2>/dev/null)" ]; then
	ok "the old table 51 default route is gone"
else bad "the old table 51 default route is gone"; fi

# ------------------------------------------------------------------ summary ---
printf '\n\033[1m%d passed, %d failed\033[0m\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ]
