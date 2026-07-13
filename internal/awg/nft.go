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
	// Only forward traffic that both enters and leaves the tunnel pool logic;
	// established handles return path, so we only need per-grant new flows.
	for _, g := range grants {
		for _, s := range g.Subnets {
			fmt.Fprintf(&b, "    ip saddr %s ip daddr %s accept\n", g.ClientAddr.String(), s.String())
		}
	}
	b.WriteString("  }\n")
	b.WriteString("}\n")

	return b.String()
}
