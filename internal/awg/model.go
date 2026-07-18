package awg

import "net/netip"

// Hub is the VPS relay with the public IP. There is exactly one per deployment.
type Hub struct {
	Endpoint   string     // public "IP:port" clients & nodes dial
	ListenPort int        // awg ListenPort
	Address    netip.Addr // hub tunnel IP, e.g. 10.8.0.1
	PoolCIDR   netip.Prefix
	Keys       Keypair
	Params     ObfuscationParams
	DNS        string // optional DNS pushed to clients (e.g. 10.8.0.1)
	WANIface   string // hub's internet-facing iface, for exit masquerade
	Resolver   bool   // hub runs the split-horizon DNS resolver on its tunnel IP
	ResolverPort int  // resolver's local port; client :53 is nft-redirected here
}

// Node is a home server behind CGNAT. It dials OUT to the hub and owns one or
// more LAN subnets. Its config is static once installed.
type Node struct {
	Name      string
	Address   netip.Addr     // node tunnel IP, e.g. 10.8.0.2
	Subnets   []netip.Prefix // LAN(s) it exposes, e.g. 192.168.1.0/24
	Keys      Keypair
	LANIface  string // interface facing the LAN, for masquerade (e.g. "eth0")
	Preshared string // optional PSK shared with hub
	IsHub     bool     // virtual node representing the hub itself (internet exit)
	DNS       string   // DNS server behind this node
	Domains   []string // local domains this node's DNS is authoritative for
}

// Client is a VPN user (laptop/phone). Gets a /32 tunnel IP.
type Client struct {
	Name      string
	Address   netip.Addr // /32 tunnel IP, e.g. 10.8.0.10
	Keys      Keypair
	Preshared string
	DNS       string // per-client override; empty = fall back to Hub.DNS
}

// Grant links a client to a node it may reach.
type Grant struct {
	ClientAddr netip.Addr
	NodeAddr   netip.Addr
	Subnets    []netip.Prefix // the node's full subnets
	Rules      []GrantRule    // access level; empty = full access to Subnets
	IsExit     bool           // grant to the hub node => internet full-tunnel via hub
	NodeExit   bool           // route this client's whole internet out THIS node's WAN
	NodeDNS    string         // node's DNS server; if set, DNAT this client's :53 here
	Domains    []string       // node's local domains (client-side split-DNS search domains)
}

// NodeLink is a directed site-to-site route: hosts on a source node's LAN — and
// the source node itself (its tunnel IP) — may initiate to a destination node's
// subnets. Rendered as hub nft accepts (one per src x dst subnet pair, plus the
// source node's /32); the return path is covered by conntrack. No NAT — real
// source IPs are preserved, so src and dst subnets must not overlap.
type NodeLink struct {
	SrcAddr    netip.Addr     // source node's tunnel IP, so the node itself is covered
	SrcSubnets []netip.Prefix // source node's LAN(s)
	DstSubnets []netip.Prefix // destination node's LAN(s)
}

// GrantRule restricts access to a destination + optional proto/port range.
type GrantRule struct {
	Dest     netip.Prefix // subnet or host (/32)
	Proto    string       // "any" | "tcp" | "udp"
	PortFrom int          // 0 = all ports
	PortTo   int          // 0 = same as PortFrom (single port) or all if PortFrom 0
}

// dests returns the destination prefixes this grant permits: rule dests if any,
// else the node's full subnets. Used for both nft ACL and client AllowedIPs.
func (g Grant) dests() []netip.Prefix {
	if len(g.Rules) == 0 {
		return g.Subnets
	}
	seen := map[string]bool{}
	var out []netip.Prefix
	for _, r := range g.Rules {
		if s := r.Dest.String(); !seen[s] {
			seen[s] = true
			out = append(out, r.Dest)
		}
	}
	return out
}
