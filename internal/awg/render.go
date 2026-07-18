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

	// Nodes acting as an internet exit: their hub peer must accept the whole
	// 0.0.0.0/0 crypto route so client internet can be sent into their tunnel.
	exitNode := map[string]bool{}
	for _, g := range grants {
		if g.NodeExit {
			exitNode[g.NodeAddr.String()] = true
		}
	}

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
		if exitNode[n.Address.String()] {
			allowed = append(allowed, "0.0.0.0/0")
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
	// Forward + masquerade pool -> LAN. awg-quick runs PostUp/PostDown; %i
	// expands to the awg interface name (e.g. awg0). The explicit FORWARD
	// accepts are required because Docker (typically present on a LAN node,
	// since the panel runs in containers) flips the FORWARD policy to DROP,
	// which otherwise silently blocks tunnel->LAN forwarding.
	fmt.Fprintf(&b, "PostUp = sysctl -w net.ipv4.ip_forward=1\n")
	fmt.Fprintf(&b, "PostUp = iptables -t nat -A POSTROUTING -s %s -o %s -j MASQUERADE\n", hub.PoolCIDR.String(), n.LANIface)
	for _, s := range reachSubnets {
		fmt.Fprintf(&b, "PostUp = iptables -t nat -A POSTROUTING -s %s -o %s -j MASQUERADE\n", s.String(), n.LANIface)
	}
	fmt.Fprintf(&b, "PostUp = iptables -I FORWARD -i %%i -o %s -j ACCEPT\n", n.LANIface)
	fmt.Fprintf(&b, "PostUp = iptables -I FORWARD -i %s -o %%i -j ACCEPT\n", n.LANIface)
	fmt.Fprintf(&b, "PostDown = iptables -t nat -D POSTROUTING -s %s -o %s -j MASQUERADE\n", hub.PoolCIDR.String(), n.LANIface)
	for _, s := range reachSubnets {
		fmt.Fprintf(&b, "PostDown = iptables -t nat -D POSTROUTING -s %s -o %s -j MASQUERADE\n", s.String(), n.LANIface)
	}
	fmt.Fprintf(&b, "PostDown = iptables -D FORWARD -i %%i -o %s -j ACCEPT\n", n.LANIface)
	fmt.Fprintf(&b, "PostDown = iptables -D FORWARD -i %s -o %%i -j ACCEPT\n", n.LANIface)

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
