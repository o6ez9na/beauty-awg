package awg

import (
	"fmt"
	"net/netip"
	"strings"
)

// paramsBlock renders the shared obfuscation lines. Must be byte-identical
// across hub/node/client.
func paramsBlock(p ObfuscationParams) string {
	return fmt.Sprintf(
		"Jc = %d\nJmin = %d\nJmax = %d\nS1 = %d\nS2 = %d\nH1 = %d\nH2 = %d\nH3 = %d\nH4 = %d\n",
		p.Jc, p.Jmin, p.Jmax, p.S1, p.S2, p.H1, p.H2, p.H3, p.H4,
	)
}

// RenderHub builds awg0.conf for the VPS. Peers = all nodes + all enabled
// clients. Access control is NOT here — it is enforced by nftables (see nft.go).
// AllowedIPs here is pure cryptokey routing: each node owns its subnets.
func RenderHub(hub Hub, nodes []Node, clients []Client, grants []Grant) string {
	var b strings.Builder

	// A node's hub-peer AllowedIPs is its /32 plus its LAN subnets — never
	// 0.0.0.0/0, even for an internet-exit node. Cryptokey routing is dst-based
	// and global per interface, so at most ONE peer could own the default route;
	// giving it to an exit node would cap the mesh at a single exit and silently
	// steal every client's internet the moment a second exit was configured.
	// Instead, exit clients are policy-routed into a per-node IPIP tunnel on the
	// hub (see Applier.EnsureExitRoutes), which reaches the node via its /32 — so
	// any number of exit nodes coexist.
	fmt.Fprintf(&b, "[Interface]\n")
	// Mask = pool bits so the hub gets an on-link route to the whole pool.
	fmt.Fprintf(&b, "Address = %s/%d\n", hub.Address.String(), hub.PoolCIDR.Bits())
	fmt.Fprintf(&b, "ListenPort = %d\n", hub.ListenPort)
	fmt.Fprintf(&b, "PrivateKey = %s\n", hub.Keys.Private)
	b.WriteString(paramsBlock(hub.Params))

	for _, n := range nodes {
		if n.IsHub {
			continue // the hub is the interface itself, not a peer
		}
		allowed := []string{n.Address.String() + "/32"}
		for _, s := range n.Subnets {
			allowed = append(allowed, s.String())
		}
		fmt.Fprintf(&b, "\n# node: %s\n[Peer]\n", n.Name)
		fmt.Fprintf(&b, "PublicKey = %s\n", n.Keys.Public)
		if n.Preshared != "" {
			fmt.Fprintf(&b, "PresharedKey = %s\n", n.Preshared)
		}
		fmt.Fprintf(&b, "AllowedIPs = %s\n", strings.Join(allowed, ", "))
	}

	for _, c := range clients {
		fmt.Fprintf(&b, "\n# client: %s\n[Peer]\n", c.Name)
		fmt.Fprintf(&b, "PublicKey = %s\n", c.Keys.Public)
		if c.Preshared != "" {
			fmt.Fprintf(&b, "PresharedKey = %s\n", c.Preshared)
		}
		fmt.Fprintf(&b, "AllowedIPs = %s/32\n", c.Address.String())
	}

	return b.String()
}

// NodePrivatePlaceholder is emitted in place of PrivateKey when the panel does
// not hold the node's private key (reverse-enrolled nodes keep it locally). The
// node agent substitutes its own private key before writing the config.
const NodePrivatePlaceholder = "__REPLACE_WITH_NODE_PRIVATE_KEY__"

// Node-side internet-exit return path (see the IPIP block in RenderNode). One
// dedicated IPIP device, policy-routing table, conntrack mark, and rule
// priority — constant per node, since a node has exactly one hub.
const (
	nodeExitDev   = "ipip-hub"
	nodeExitTable = "133"
	nodeExitMark  = "0x33"
	nodeExitPref  = "1330"
)

// RenderNode builds the static awg config installed once on a home server.
// It NATs (masquerades) the client pool into the LAN so LAN hosts need no route
// back. AllowedIPs for the hub peer = whole pool, so all client traffic returns.
//
// reachSubnets are the subnets of nodes this one is linked to (site-to-site).
// They are added to the hub peer's AllowedIPs so this node routes cross-site
// traffic into the tunnel AND WireGuard accepts the peer's cross-site source on
// the return path. Cross-site traffic is ALSO masqueraded into the LAN (like the
// client pool), because devices on the LAN commonly run their own host firewalls
// that only accept traffic from their own subnet (e.g. Windows Defender's default
// ICMP/SMB rules scoped to "local subnet") — a foreign source is silently dropped
// even though routing and the hub ACL both permit it. Masquerading makes
// cross-site traffic indistinguishable from local traffic, at the cost of the
// remote LAN no longer seeing individual source hosts (all traffic from the
// linked site arrives as this node's own LAN address, same trade-off clients
// already accept).
func RenderNode(hub Hub, n Node, reachSubnets []netip.Prefix) string {
	var b strings.Builder

	priv := n.Keys.Private
	if priv == "" {
		priv = NodePrivatePlaceholder
	}
	fmt.Fprintf(&b, "[Interface]\n")
	fmt.Fprintf(&b, "Address = %s/32\n", n.Address.String())
	fmt.Fprintf(&b, "PrivateKey = %s\n", priv)
	b.WriteString(paramsBlock(hub.Params))
	// Forward + masquerade the client pool. awg-quick runs PostUp/PostDown; %i
	// expands to the awg interface name (e.g. awg0). The explicit FORWARD accepts
	// are required because Docker (typically present on a LAN node, since the panel
	// runs in containers) flips the FORWARD policy to DROP, which otherwise
	// silently blocks tunnel forwarding.
	//
	// The pool masquerade and the FORWARD accepts are scoped by "not the tunnel"
	// (`! -o %i`) rather than by the LAN interface. Pool-sourced packets only ever
	// egress a physical interface — the LAN for on-net services, or the default
	// route (WAN) when this node is an internet-exit — never back out the tunnel,
	// so excluding the tunnel is enough and, unlike `-o <LAN>`, it also covers
	// exit traffic on a node whose internet-facing interface differs from its LAN
	// one. On the common single-NIC home server the two are identical, so this is
	// a superset that changes nothing there. The hub's nftables ACL is still the
	// authority on what may be forwarded at all.
	fmt.Fprintf(&b, "PostUp = sysctl -w net.ipv4.ip_forward=1\n")
	fmt.Fprintf(&b, "PostUp = iptables -t nat -A POSTROUTING -s %s ! -o %%i -j MASQUERADE\n", hub.PoolCIDR.String())
	// Cross-site (linked-node) traffic is masqueraded INTO the LAN specifically, so
	// remote hosts see this node's LAN address (host firewalls scoped to "local
	// subnet" then accept it). That intent is LAN-only, so it keeps -o <LAN>.
	for _, s := range reachSubnets {
		fmt.Fprintf(&b, "PostUp = iptables -t nat -A POSTROUTING -s %s -o %s -j MASQUERADE\n", s.String(), n.LANIface)
	}
	fmt.Fprintf(&b, "PostUp = iptables -I FORWARD -i %%i ! -o %%i -j ACCEPT\n")
	fmt.Fprintf(&b, "PostUp = iptables -I FORWARD ! -i %%i -o %%i -j ACCEPT\n")
	fmt.Fprintf(&b, "PostDown = iptables -t nat -D POSTROUTING -s %s ! -o %%i -j MASQUERADE\n", hub.PoolCIDR.String())
	for _, s := range reachSubnets {
		fmt.Fprintf(&b, "PostDown = iptables -t nat -D POSTROUTING -s %s -o %s -j MASQUERADE\n", s.String(), n.LANIface)
	}
	fmt.Fprintf(&b, "PostDown = iptables -D FORWARD -i %%i ! -o %%i -j ACCEPT\n")
	fmt.Fprintf(&b, "PostDown = iptables -D FORWARD ! -i %%i -o %%i -j ACCEPT\n")

	// Internet-exit return path. Idle unless the hub selects this node as an exit
	// for some client: it then sends that client's traffic here inside an IPIP
	// tunnel (the hub reaches us by our /32, so any number of exit nodes coexist —
	// see Applier.EnsureExitRoutes). We decapsulate, the inner client-pool source
	// is NAT'd out our WAN by the `! -o %i` masquerade above, and replies are sent
	// BACK through the same tunnel.
	//
	// Why the reply MUST retrace the tunnel: the hub's WireGuard drops any packet
	// from us whose source isn't in our AllowedIPs (/32 + LANs). A direct reply
	// has an internet source, so it would be dropped; wrapped in IPIP its outer
	// source is our /32, which passes. Steering is by conntrack mark — the first
	// packet of a flow arriving on the tunnel marks the connection; the mark is
	// restored onto reply packets (everything NOT arriving on the tunnel, so the
	// forward packet is never re-routed back into it and looped); marked replies
	// are policy-routed out the tunnel instead of following the main route to the
	// pool (which goes over awg0 and would be dropped).
	//
	// The tunnel needs BOTH an address and loose reverse-path filtering, and the
	// two are not independent. The decapsulated inner source (a pool address) is
	// "best reached" via awg0, not the tunnel, so strict rp_filter treats it as a
	// spoof. Loose would accept it — but the kernel only reaches its loose branch
	// if the receiving device has at least one IPv4 address; on an addressless
	// device __fib_validate_source() falls through to last_resort, which rejects
	// under ANY non-zero rp_filter. So we give the tunnel our own /32 (harmless:
	// it is the same address awg0 already carries, and IPIP devices are NOARP)
	// and set rp_filter=2 on the device itself rather than globally — 2 is the
	// max, so it wins the kernel's max(conf.all, conf.<dev>) whatever the host's
	// global setting is, without loosening the rest of the machine.
	fmt.Fprintf(&b, "PostUp = ip link add %s type ipip local %s remote %s || true\n", nodeExitDev, n.Address.String(), hub.Address.String())
	fmt.Fprintf(&b, "PostUp = ip addr replace %s/32 dev %s\n", n.Address.String(), nodeExitDev)
	fmt.Fprintf(&b, "PostUp = ip link set %s up\n", nodeExitDev)
	fmt.Fprintf(&b, "PostUp = sysctl -w net.ipv4.conf.%s.rp_filter=2\n", nodeExitDev)
	fmt.Fprintf(&b, "PostUp = iptables -I FORWARD -i %s -j ACCEPT\n", nodeExitDev)
	fmt.Fprintf(&b, "PostUp = iptables -I FORWARD -o %s -j ACCEPT\n", nodeExitDev)
	fmt.Fprintf(&b, "PostUp = iptables -t mangle -A PREROUTING -i %s -j CONNMARK --set-mark %s\n", nodeExitDev, nodeExitMark)
	fmt.Fprintf(&b, "PostUp = iptables -t mangle -A PREROUTING ! -i %s -j CONNMARK --restore-mark\n", nodeExitDev)
	fmt.Fprintf(&b, "PostUp = iptables -t mangle -A FORWARD -o %s -p tcp --tcp-flags SYN,RST SYN -j TCPMSS --clamp-mss-to-pmtu\n", nodeExitDev)
	// awg-quick aborts the whole bring-up on the first PostUp line that fails, so
	// every line here has to survive finding its own leftovers: a `down` that was
	// interrupted (or a reboot mid-teardown) leaves the rule and the route behind,
	// and a plain `ip rule add` / `ip route add` then answers "File exists" and
	// takes the interface down with it. `replace` is idempotent; the rule add is
	// tolerated because a duplicate means the rule we want is already there.
	fmt.Fprintf(&b, "PostUp = ip rule add fwmark %s lookup %s pref %s || true\n", nodeExitMark, nodeExitTable, nodeExitPref)
	fmt.Fprintf(&b, "PostUp = ip route replace default dev %s table %s\n", nodeExitDev, nodeExitTable)
	// Teardown is best-effort for the same reason, in reverse: a half-removed
	// block must not stop the rest from being cleaned up, or the next `up` starts
	// from an even messier state. `ip link del` last takes the address with it.
	fmt.Fprintf(&b, "PostDown = ip route flush table %s || true\n", nodeExitTable)
	fmt.Fprintf(&b, "PostDown = ip rule del fwmark %s lookup %s pref %s || true\n", nodeExitMark, nodeExitTable, nodeExitPref)
	fmt.Fprintf(&b, "PostDown = iptables -t mangle -D FORWARD -o %s -p tcp --tcp-flags SYN,RST SYN -j TCPMSS --clamp-mss-to-pmtu || true\n", nodeExitDev)
	fmt.Fprintf(&b, "PostDown = iptables -t mangle -D PREROUTING ! -i %s -j CONNMARK --restore-mark || true\n", nodeExitDev)
	fmt.Fprintf(&b, "PostDown = iptables -t mangle -D PREROUTING -i %s -j CONNMARK --set-mark %s || true\n", nodeExitDev, nodeExitMark)
	fmt.Fprintf(&b, "PostDown = iptables -D FORWARD -i %s -j ACCEPT || true\n", nodeExitDev)
	fmt.Fprintf(&b, "PostDown = iptables -D FORWARD -o %s -j ACCEPT || true\n", nodeExitDev)
	fmt.Fprintf(&b, "PostDown = ip link del %s || true\n", nodeExitDev)

	fmt.Fprintf(&b, "\n# hub\n[Peer]\n")
	fmt.Fprintf(&b, "PublicKey = %s\n", hub.Keys.Public)
	if n.Preshared != "" {
		fmt.Fprintf(&b, "PresharedKey = %s\n", n.Preshared)
	}
	fmt.Fprintf(&b, "Endpoint = %s\n", hub.Endpoint)
	// Pool + any linked nodes' subnets. The pool covers all client/hub return
	// traffic; the linked subnets make this node route cross-site traffic into
	// the tunnel and accept the peer node's source on the way back.
	allowed := []string{hub.PoolCIDR.String()}
	seen := map[string]bool{hub.PoolCIDR.String(): true}
	for _, s := range reachSubnets {
		if k := s.String(); !seen[k] {
			seen[k] = true
			allowed = append(allowed, k)
		}
	}
	fmt.Fprintf(&b, "AllowedIPs = %s\n", strings.Join(allowed, ", "))
	fmt.Fprintf(&b, "PersistentKeepalive = 25\n") // punches CGNAT, keeps tunnel up

	return b.String()
}

// RenderClient builds the config a user imports. AllowedIPs = ONLY the subnets
// of nodes this client is granted (split-tunnel). Traffic to other subnets never
// leaves the device. This is the client-side half of access control.
func RenderClient(hub Hub, c Client, granted []Grant) string {
	var b strings.Builder

	// Collect granted dests (rule dests if any, else full subnets). Always
	// include the hub tunnel IP for DNS/reachability.
	allowed := []string{hub.Address.String() + "/32"}
	for _, g := range granted {
		if g.NodeExit {
			// full internet out this node => the whole default route to the tunnel.
			allowed = append(allowed, "0.0.0.0/0")
		}
		for _, d := range g.dests() {
			allowed = append(allowed, d.String())
		}
	}
	// If a full tunnel (0.0.0.0/0) is present, collapse to just that: the other
	// entries are subsets of it, and a mixed list alongside 0.0.0.0/0 breaks
	// route/metric handling in some clients (notably wireguard-windows), which
	// can send TCP out the wrong interface while ICMP still works.
	fullTunnel := false
	for _, a := range allowed {
		if a == "0.0.0.0/0" {
			allowed = []string{"0.0.0.0/0"}
			fullTunnel = true
			break
		}
	}

	fmt.Fprintf(&b, "[Interface]\n")
	fmt.Fprintf(&b, "Address = %s/32\n", c.Address.String())
	fmt.Fprintf(&b, "PrivateKey = %s\n", c.Keys.Private)
	// DNS. With the resolver on: full-tunnel clients just point at the hub IP (all
	// DNS already flows through the tunnel to the resolver). Split-tunnel clients
	// also list the granted nodes' domains as search domains so only those route
	// to the tunnel resolver. Some Windows clients choke on a mixed IP+domain DNS
	// line, so we avoid it when full-tunnel makes it unnecessary.
	var dns string
	if hub.Resolver {
		if fullTunnel {
			dns = hub.Address.String()
		} else {
			parts := []string{hub.Address.String()}
			seen := map[string]bool{}
			for _, g := range granted {
				for _, d := range g.Domains {
					if d != "" && !seen[d] {
						seen[d] = true
						parts = append(parts, d)
					}
				}
			}
			dns = strings.Join(parts, ", ")
		}
	} else {
		dns = c.DNS
		if dns == "" {
			for _, g := range granted {
				if g.NodeDNS != "" {
					dns = g.NodeDNS
					break
				}
			}
		}
		if dns == "" {
			dns = hub.DNS
		}
	}
	if dns != "" {
		fmt.Fprintf(&b, "DNS = %s\n", dns)
	}
	b.WriteString(paramsBlock(hub.Params))

	fmt.Fprintf(&b, "\n# hub\n[Peer]\n")
	fmt.Fprintf(&b, "PublicKey = %s\n", hub.Keys.Public)
	if c.Preshared != "" {
		fmt.Fprintf(&b, "PresharedKey = %s\n", c.Preshared)
	}
	fmt.Fprintf(&b, "Endpoint = %s\n", hub.Endpoint)
	fmt.Fprintf(&b, "AllowedIPs = %s\n", strings.Join(allowed, ", "))
	fmt.Fprintf(&b, "PersistentKeepalive = 25\n")

	return b.String()
}
