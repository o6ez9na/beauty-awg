package awg

import (
	"fmt"
	"strings"
)

// RenderNFT builds an atomic nftables ruleset enforcing per-client → per-node
// access. This is the authoritative ACL: even if a client's AllowedIPs is wide,
// the hub drops any forward that has no matching grant.
//
// Apply with: nft -f <file>  (the `flush table` line makes it idempotent).
//
// links add site-to-site (node->node) accepts: one line per src x dst subnet
// pair. The return path rides the ct established rule, so a one-way link is one
// direction here; a bidirectional link is two NodeLinks.
func RenderNFT(hub Hub, grants []Grant, links []NodeLink) string {
	var b strings.Builder

	// add is a no-op if the table exists; flush then clears it so the reload is
	// atomic and idempotent.
	b.WriteString("add table inet awgacl\n")
	b.WriteString("flush table inet awgacl\n")
	b.WriteString("table inet awgacl {\n")
	b.WriteString("  chain forward {\n")
	b.WriteString("    type filter hook forward priority filter; policy drop;\n")
	b.WriteString("    ct state established,related accept\n")
	// established handles the return path, so we only emit per-grant new flows.
	for _, g := range grants {
		if g.IsExit {
			// internet full-tunnel: allow only packets egressing the WAN iface,
			// so this does NOT open the other nodes' LANs (those egress via awg0).
			fmt.Fprintf(&b, "    ip saddr %s oifname %q accept\n", g.ClientAddr.String(), hub.WANIface)
			continue
		}
		if g.NodeExit {
			// full internet via this node: the client's default route is policy-
			// routed into awg0, so accept all its forwarded traffic (which egresses
			// awg0 toward the exit node; the node then masquerades to its home WAN).
			fmt.Fprintf(&b, "    ip saddr %s accept\n", g.ClientAddr.String())
			continue
		}
		if len(g.Rules) == 0 {
			// no access level => full access to every node subnet
			for _, s := range g.Subnets {
				fmt.Fprintf(&b, "    ip saddr %s ip daddr %s accept\n", g.ClientAddr.String(), s.String())
			}
			continue
		}
		for _, r := range g.Rules {
			for _, line := range nftRuleLines(g.ClientAddr.String(), r) {
				fmt.Fprintf(&b, "    %s\n", line)
			}
		}
	}
	// Site-to-site: allow each linked node's LAN to reach the peer node's LAN.
	for _, l := range links {
		for _, src := range l.SrcSubnets {
			for _, dst := range l.DstSubnets {
				fmt.Fprintf(&b, "    ip saddr %s ip daddr %s accept\n", src.String(), dst.String())
			}
		}
	}
	b.WriteString("  }\n")
	b.WriteString("}\n")

	// NAT table: masquerade for internet-exit + DNS DNAT for node DNS servers.
	b.WriteString("\nadd table ip awgnat\nflush table ip awgnat\n")
	b.WriteString("table ip awgnat {\n")

	// DNS force-redirect: any granted client whose node has a DNS server gets its
	// port-53 traffic DNAT'd to that server. Skipped when the hub resolver is on
	// (clients then send DNS to the hub and the resolver does split-horizon — a
	// prerouting DNAT would otherwise hijack those queries before they arrive).
	b.WriteString("  chain prerouting {\n")
	b.WriteString("    type nat hook prerouting priority dstnat; policy accept;\n")
	if hub.Resolver {
		// Redirect client DNS (to the hub tunnel IP:53) to the resolver's local
		// port. This bypasses anything else already bound to :53 on the host.
		if hub.ResolverPort > 0 {
			fmt.Fprintf(&b, "    ip daddr %s udp dport 53 redirect to :%d\n", hub.Address.String(), hub.ResolverPort)
			fmt.Fprintf(&b, "    ip daddr %s tcp dport 53 redirect to :%d\n", hub.Address.String(), hub.ResolverPort)
		}
	} else {
		for _, g := range grants {
			if g.NodeDNS == "" {
				continue
			}
			fmt.Fprintf(&b, "    ip saddr %s udp dport 53 dnat to %s\n", g.ClientAddr.String(), g.NodeDNS)
			fmt.Fprintf(&b, "    ip saddr %s tcp dport 53 dnat to %s\n", g.ClientAddr.String(), g.NodeDNS)
		}
	}
	b.WriteString("  }\n")

	// Masquerade the pool out the WAN iface so internet-exit clients get a return
	// path. Harmless when unused (filter forward drops non-granted flows).
	if hub.WANIface != "" {
		b.WriteString("  chain postrouting {\n")
		b.WriteString("    type nat hook postrouting priority srcnat; policy accept;\n")
		fmt.Fprintf(&b, "    ip saddr %s oifname %q masquerade\n", hub.PoolCIDR.String(), hub.WANIface)
		b.WriteString("  }\n")
	}

	b.WriteString("}\n")
	return b.String()
}

// nftRuleLines renders one grant rule into one or more nft accept lines.
//   - proto "any" + a port  -> emit both tcp and udp
//   - proto "any" + no port -> plain daddr accept
//   - specific proto + port -> "<proto> dport ..."
//   - specific proto, no port -> "ip protocol <proto>"
func nftRuleLines(client string, r GrantRule) []string {
	base := fmt.Sprintf("ip saddr %s ip daddr %s", client, r.Dest.String())
	hasPort := r.PortFrom > 0

	port := ""
	if hasPort {
		if r.PortTo > r.PortFrom {
			port = fmt.Sprintf("dport %d-%d", r.PortFrom, r.PortTo)
		} else {
			port = fmt.Sprintf("dport %d", r.PortFrom)
		}
	}

	protos := []string{r.Proto}
	if r.Proto == "" || r.Proto == "any" {
		if hasPort {
			protos = []string{"tcp", "udp"}
		} else {
			return []string{base + " accept"} // any proto, any port
		}
	}

	var lines []string
	for _, p := range protos {
		if hasPort {
			lines = append(lines, fmt.Sprintf("%s %s %s accept", base, p, port))
		} else {
			lines = append(lines, fmt.Sprintf("%s ip protocol %s accept", base, p))
		}
	}
	return lines
}
