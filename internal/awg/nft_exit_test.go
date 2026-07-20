package awg

import (
	"net/netip"
	"strings"
	"testing"
)

// A node-exit grant used to emit a bare "ip saddr <client> accept". That is a
// blanket forward permit: it reached every other site's LAN, including nodes the
// client had no grant for, and made the client's own per-node rules dead
// letters. These tests pin the scoped form down.
func exitFixture() (Hub, []Node, []Grant) {
	hub := Hub{
		Address:  netip.MustParseAddr("10.8.0.1"),
		PoolCIDR: netip.MustParsePrefix("10.8.0.0/24"),
		WANIface: "eth0",
	}
	nodes := []Node{
		{Name: "kseen_lan", Address: netip.MustParseAddr("10.8.0.2"), Subnets: []netip.Prefix{
			netip.MustParsePrefix("10.18.18.0/24"),
			netip.MustParsePrefix("10.99.99.0/24"),
		}},
		{Name: "st4v3r_lan", Address: netip.MustParseAddr("10.8.0.3"), Subnets: []netip.Prefix{
			netip.MustParsePrefix("192.168.1.0/24"),
		}},
		// No grant to this one at all — the leak used to expose it anyway.
		{Name: "third_lan", Address: netip.MustParseAddr("10.8.0.4"), Subnets: []netip.Prefix{
			netip.MustParsePrefix("172.20.0.0/24"),
		}},
	}
	client := netip.MustParseAddr("10.8.0.5")
	grants := []Grant{
		// Limited to ONE of kseen_lan's two subnets.
		{
			ClientAddr: client,
			NodeAddr:   nodes[0].Address,
			Subnets:    nodes[0].Subnets,
			Rules:      []GrantRule{{Dest: netip.MustParsePrefix("10.18.18.0/24"), Proto: "any"}},
		},
		// Full tunnel out st4v3r_lan.
		{
			ClientAddr: client,
			NodeAddr:   nodes[1].Address,
			Subnets:    nodes[1].Subnets,
			NodeExit:   true,
		},
	}
	return hub, nodes, grants
}

func TestRenderNFT_NodeExitIsNotABlanketAccept(t *testing.T) {
	hub, nodes, grants := exitFixture()
	got := RenderNFT(hub, nodes, grants, nil)

	if strings.Contains(got, "    ip saddr 10.8.0.5 accept\n") {
		t.Fatalf("node-exit still emits a blanket accept:\n%s", got)
	}

	// The internet accept must exclude every mesh LAN and the client pool.
	for _, must := range []string{"10.18.18.0/24", "10.99.99.0/24", "192.168.1.0/24", "172.20.0.0/24", "10.8.0.0/24"} {
		line := exitLine(got)
		if !strings.Contains(line, must) {
			t.Errorf("exit accept does not exclude %s:\n%s", must, line)
		}
	}
}

// The narrower rule on the other node must still be the only thing that opens
// that node — the exit grant must not widen it.
func TestRenderNFT_NodeExitDoesNotVoidOtherRules(t *testing.T) {
	hub, nodes, grants := exitFixture()
	got := RenderNFT(hub, nodes, grants, nil)

	if !strings.Contains(got, "ip saddr 10.8.0.5 ip daddr 10.18.18.0/24 accept") {
		t.Errorf("granted subnet lost its accept:\n%s", got)
	}
	// 10.99.99.0/24 belongs to the same node but was excluded by the rule, and
	// 172.20.0.0/24 belongs to a node with no grant. Neither may be accepted.
	for _, forbidden := range []string{
		"ip saddr 10.8.0.5 ip daddr 10.99.99.0/24 accept",
		"ip saddr 10.8.0.5 ip daddr 172.20.0.0/24 accept",
	} {
		if strings.Contains(got, forbidden) {
			t.Errorf("unexpected accept %q:\n%s", forbidden, got)
		}
	}
}

// Routing all traffic through a node must still reach that node's own LAN,
// which is the whole point of picking it as the exit.
func TestRenderNFT_NodeExitKeepsItsOwnLAN(t *testing.T) {
	hub, nodes, grants := exitFixture()
	got := RenderNFT(hub, nodes, grants, nil)

	if !strings.Contains(got, "ip saddr 10.8.0.5 ip daddr 192.168.1.0/24 accept") {
		t.Errorf("exit node's own LAN is no longer reachable:\n%s", got)
	}
}

// exitLine returns the "ip daddr != {...}" accept, for readable failures.
func exitLine(ruleset string) string {
	for _, l := range strings.Split(ruleset, "\n") {
		if strings.Contains(l, "ip daddr != {") {
			return l
		}
	}
	return "(no exclusion accept emitted)"
}
