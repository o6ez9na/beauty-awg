package awg

import (
	"net/netip"
	"strings"
	"testing"
)

func mustPrefix(t *testing.T, s string) netip.Prefix {
	t.Helper()
	p, err := netip.ParsePrefix(s)
	if err != nil {
		t.Fatalf("bad prefix %q: %v", s, err)
	}
	return p
}

func testHub(t *testing.T) Hub {
	t.Helper()
	return Hub{
		Endpoint:   "203.0.113.10:51820",
		ListenPort: 51820,
		Address:    netip.MustParseAddr("10.8.0.1"),
		PoolCIDR:   mustPrefix(t, "10.8.0.0/24"),
		Keys:       Keypair{Private: "HUBPRIV", Public: "HUBPUB"},
	}
}

// A linked node's hub-peer AllowedIPs must include the pool AND the peer node's
// subnets, so it routes cross-site traffic into the tunnel and accepts the
// return path. No extra NAT lines are added.
func TestRenderNode_ReachSubnets(t *testing.T) {
	hub := testHub(t)
	n := Node{
		Name:     "siteA",
		Address:  netip.MustParseAddr("10.8.0.3"),
		Subnets:  []netip.Prefix{mustPrefix(t, "10.18.18.0/24")},
		LANIface: "eth0",
		Keys:     Keypair{Private: "APRIV", Public: "APUB"},
	}
	reach := []netip.Prefix{mustPrefix(t, "192.168.1.0/24")}

	got := RenderNode(hub, n, reach)

	if !strings.Contains(got, "AllowedIPs = 10.8.0.0/24, 192.168.1.0/24") {
		t.Errorf("hub-peer AllowedIPs missing linked subnet:\n%s", got)
	}
	// site-to-site is pure routing: the only masquerade is the pool->LAN pair
	// (one PostUp add + one PostDown delete). No LAN->tunnel SNAT is added.
	if strings.Count(got, "MASQUERADE") != 2 {
		t.Errorf("expected only the pool->LAN masquerade pair, got:\n%s", got)
	}
	if strings.Contains(got, "-o %i -j MASQUERADE") {
		t.Errorf("unexpected LAN->tunnel masquerade (should be NAT-free):\n%s", got)
	}
}

// With no links the config is unchanged: AllowedIPs is just the pool.
func TestRenderNode_NoLinks(t *testing.T) {
	hub := testHub(t)
	n := Node{
		Name:     "siteA",
		Address:  netip.MustParseAddr("10.8.0.3"),
		Subnets:  []netip.Prefix{mustPrefix(t, "10.18.18.0/24")},
		LANIface: "eth0",
		Keys:     Keypair{Public: "APUB", Private: "APRIV"},
	}
	got := RenderNode(hub, n, nil)
	if !strings.Contains(got, "AllowedIPs = 10.8.0.0/24\n") {
		t.Errorf("expected pool-only AllowedIPs:\n%s", got)
	}
}

// RenderNFT emits one forward-accept per src x dst subnet pair for each link.
func TestRenderNFT_Links(t *testing.T) {
	hub := testHub(t)
	links := []NodeLink{{
		SrcAddr:    netip.MustParseAddr("10.8.0.3"),
		SrcSubnets: []netip.Prefix{mustPrefix(t, "10.18.18.0/24")},
		DstSubnets: []netip.Prefix{mustPrefix(t, "192.168.1.0/24"), mustPrefix(t, "192.168.2.0/24")},
	}}

	got := RenderNFT(hub, nil, links)

	for _, want := range []string{
		"ip saddr 10.18.18.0/24 ip daddr 192.168.1.0/24 accept",
		"ip saddr 10.18.18.0/24 ip daddr 192.168.2.0/24 accept",
		// the source node itself (tunnel /32) is covered too
		"ip saddr 10.8.0.3/32 ip daddr 192.168.1.0/24 accept",
		"ip saddr 10.8.0.3/32 ip daddr 192.168.2.0/24 accept",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("nft missing %q:\n%s", want, got)
		}
	}
	// the return path relies on the shared ct rule, not an explicit reverse line.
	if strings.Contains(got, "ip saddr 192.168.1.0/24") {
		t.Errorf("unexpected reverse accept (should ride ct established):\n%s", got)
	}
}
