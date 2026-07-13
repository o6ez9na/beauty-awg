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
func RenderNFT(hub Hub, grants []Grant) string {
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
	b.WriteString("  }\n")
	b.WriteString("}\n")

	// NAT: masquerade the pool out the WAN iface so internet-exit clients get a
	// return path. Harmless when unused (filter forward drops non-granted flows;
	// node-bound traffic egresses awg0, not the WAN iface).
	if hub.WANIface != "" {
		fmt.Fprintf(&b, "\nadd table ip awgnat\nflush table ip awgnat\n")
		fmt.Fprintf(&b, "table ip awgnat {\n")
		fmt.Fprintf(&b, "  chain postrouting {\n")
		fmt.Fprintf(&b, "    type nat hook postrouting priority srcnat; policy accept;\n")
		fmt.Fprintf(&b, "    ip saddr %s oifname %q masquerade\n", hub.PoolCIDR.String(), hub.WANIface)
		fmt.Fprintf(&b, "  }\n}\n")
	}

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
