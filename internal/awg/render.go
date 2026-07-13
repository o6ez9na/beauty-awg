package awg

import (
	"fmt"
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

	fmt.Fprintf(&b, "[Interface]\n")
	// Mask = pool bits so the hub gets an on-link route to the whole pool.
	fmt.Fprintf(&b, "Address = %s/%d\n", hub.Address.String(), hub.PoolCIDR.Bits())
	fmt.Fprintf(&b, "ListenPort = %d\n", hub.ListenPort)
	fmt.Fprintf(&b, "PrivateKey = %s\n", hub.Keys.Private)
	b.WriteString(paramsBlock(hub.Params))

	for _, n := range nodes {
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

// RenderNode builds the static awg config installed once on a home server.
// It NATs (masquerades) the client pool into the LAN so LAN hosts need no route
// back. AllowedIPs for the hub peer = whole pool, so all client traffic returns.
func RenderNode(hub Hub, n Node) string {
	var b strings.Builder

	fmt.Fprintf(&b, "[Interface]\n")
	fmt.Fprintf(&b, "Address = %s/32\n", n.Address.String())
	fmt.Fprintf(&b, "PrivateKey = %s\n", n.Keys.Private)
	b.WriteString(paramsBlock(hub.Params))
	// Forward + masquerade pool -> LAN. awg-quick runs PostUp/PostDown.
	fmt.Fprintf(&b, "PostUp = sysctl -w net.ipv4.ip_forward=1\n")
	fmt.Fprintf(&b, "PostUp = iptables -t nat -A POSTROUTING -s %s -o %s -j MASQUERADE\n", hub.PoolCIDR.String(), n.LANIface)
	fmt.Fprintf(&b, "PostDown = iptables -t nat -D POSTROUTING -s %s -o %s -j MASQUERADE\n", hub.PoolCIDR.String(), n.LANIface)

	fmt.Fprintf(&b, "\n# hub\n[Peer]\n")
	fmt.Fprintf(&b, "PublicKey = %s\n", hub.Keys.Public)
	if n.Preshared != "" {
		fmt.Fprintf(&b, "PresharedKey = %s\n", n.Preshared)
	}
	fmt.Fprintf(&b, "Endpoint = %s\n", hub.Endpoint)
	fmt.Fprintf(&b, "AllowedIPs = %s\n", hub.PoolCIDR.String())
	fmt.Fprintf(&b, "PersistentKeepalive = 25\n") // punches CGNAT, keeps tunnel up

	return b.String()
}

// RenderClient builds the config a user imports. AllowedIPs = ONLY the subnets
// of nodes this client is granted (split-tunnel). Traffic to other subnets never
// leaves the device. This is the client-side half of access control.
func RenderClient(hub Hub, c Client, granted []Grant) string {
	var b strings.Builder

	fmt.Fprintf(&b, "[Interface]\n")
	fmt.Fprintf(&b, "Address = %s/32\n", c.Address.String())
	fmt.Fprintf(&b, "PrivateKey = %s\n", c.Keys.Private)
	// Per-client DNS wins; else global Hub.DNS. Empty = no DNS line.
	dns := c.DNS
	if dns == "" {
		dns = hub.DNS
	}
	if dns != "" {
		fmt.Fprintf(&b, "DNS = %s\n", dns)
	}
	b.WriteString(paramsBlock(hub.Params))

	// Collect granted subnets. Always include hub tunnel IP for DNS/reachability.
	allowed := []string{hub.Address.String() + "/32"}
	for _, g := range granted {
		for _, s := range g.Subnets {
			allowed = append(allowed, s.String())
		}
	}

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
