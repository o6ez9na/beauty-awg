package awg

import (
	"net/netip"
	"os"
	"strings"
	"testing"
)

// captureStdout runs fn with os.Stdout redirected and returns what it printed.
// Used to assert the Applier's DryRun command output.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	fn()
	w.Close()
	os.Stdout = orig
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, rerr := r.Read(buf)
		if n > 0 {
			sb.Write(buf[:n])
		}
		if rerr != nil {
			break
		}
	}
	return sb.String()
}

// An internet-exit node's hub peer must NOT carry 0.0.0.0/0. Multi-exit routes
// clients into per-node IPIP tunnels instead; a default route in a peer's
// AllowedIPs would cap the mesh at one exit and hijack every client's internet.
func TestRenderHub_ExitNodeHasNoDefaultRoute(t *testing.T) {
	hub := testHub(t)
	node := Node{
		Name:    "st4v3r_lan",
		Address: netip.MustParseAddr("10.8.0.3"),
		Subnets: []netip.Prefix{mustPrefix(t, "192.168.1.0/24")},
		Keys:    Keypair{Public: "NPUB"},
	}
	client := Client{Name: "kseen", Address: netip.MustParseAddr("10.8.0.5"), Keys: Keypair{Public: "CPUB"}}
	grants := []Grant{{ClientAddr: client.Address, NodeAddr: node.Address, Subnets: node.Subnets, NodeExit: true}}

	got := RenderHub(hub, []Node{node}, []Client{client}, grants)
	if strings.Contains(got, "0.0.0.0/0") {
		t.Errorf("exit node peer must not carry 0.0.0.0/0 (multi-exit uses IPIP):\n%s", got)
	}
	if !strings.Contains(got, "AllowedIPs = 10.8.0.3/32, 192.168.1.0/24") {
		t.Errorf("node peer should own only its /32 + LAN subnets:\n%s", got)
	}
}

// The node's static config carries an idle IPIP return path. Pin the exact lines
// down — they're the half of the exit that WireGuard's source check makes load-
// bearing, and a typo silently black-holes exit traffic.
func TestRenderNode_IPIPExitReturnPath(t *testing.T) {
	hub := testHub(t) // hub tunnel IP 10.8.0.1
	n := Node{
		Name:     "st4v3r_lan",
		Address:  netip.MustParseAddr("10.8.0.3"),
		Subnets:  []netip.Prefix{mustPrefix(t, "192.168.1.0/24")},
		LANIface: "eth0",
		Keys:     Keypair{Public: "NPUB", Private: "NPRIV"},
	}
	got := RenderNode(hub, n, nil)

	for _, want := range []string{
		"PostUp = ip link add ipip-hub type ipip local 10.8.0.3 remote 10.8.0.1 || true",
		// The address is load-bearing, not cosmetic: the kernel's loose rp_filter
		// branch is unreachable on an addressless device, so without it the
		// decapsulated pool source is rejected whatever rp_filter says.
		"PostUp = ip addr replace 10.8.0.3/32 dev ipip-hub",
		"PostUp = ip link set ipip-hub up",
		"PostUp = sysctl -w net.ipv4.conf.ipip-hub.rp_filter=2",
		"PostUp = iptables -I FORWARD -i ipip-hub -j ACCEPT",
		"PostUp = iptables -I FORWARD -o ipip-hub -j ACCEPT",
		"PostUp = iptables -t mangle -A PREROUTING -i ipip-hub -j CONNMARK --set-mark 0x33",
		"PostUp = iptables -t mangle -A PREROUTING ! -i ipip-hub -j CONNMARK --restore-mark",
		// Every line must tolerate its own leftovers: awg-quick aborts bring-up on
		// the first PostUp failure, and an interrupted down leaves these behind.
		"PostUp = ip rule add fwmark 0x33 lookup 133 pref 1330 || true",
		"PostUp = ip route replace default dev ipip-hub table 133",
		"PostDown = ip route flush table 133 || true",
		"PostDown = ip rule del fwmark 0x33 lookup 133 pref 1330 || true",
		"PostDown = ip link del ipip-hub || true",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("node config missing %q:\n%s", want, got)
		}
	}
	// The mark restore MUST be scoped away from the tunnel. An unscoped restore
	// would re-mark the outbound (client->internet) packet, which would then be
	// policy-routed straight back into ipip-hub and loop instead of egressing WAN.
	if strings.Contains(got, "-A PREROUTING -j CONNMARK --restore-mark") {
		t.Errorf("restore-mark must be scoped `! -i ipip-hub` to avoid a forwarding loop:\n%s", got)
	}
}

// exitPlan: one IPIP device per distinct exit node (deduped, sorted), one source
// rule per client, each client mapped to its node's table.
func TestExitPlan(t *testing.T) {
	n1 := netip.MustParseAddr("10.8.0.2")
	n2 := netip.MustParseAddr("10.8.0.3")
	cA := netip.MustParseAddr("10.8.0.5")
	cB := netip.MustParseAddr("10.8.0.6")
	cC := netip.MustParseAddr("10.8.0.7")
	// cA and cC share node1; cB uses node2. Order shuffled to prove sorting.
	devs, rules := exitPlan([]ExitRoute{{cB, n2}, {cC, n1}, {cA, n1}})

	if len(devs) != 2 {
		t.Fatalf("want 2 devices (one per node), got %d: %+v", len(devs), devs)
	}
	if devs[0].Remote != n1 || devs[0].Name != "awgex2" || devs[0].Table != "1002" {
		t.Errorf("dev[0] wrong (want node1 awgex2/1002): %+v", devs[0])
	}
	if devs[1].Remote != n2 || devs[1].Name != "awgex3" || devs[1].Table != "1003" {
		t.Errorf("dev[1] wrong (want node2 awgex3/1003): %+v", devs[1])
	}
	if len(rules) != 3 {
		t.Fatalf("want 3 client rules, got %d: %+v", len(rules), rules)
	}
	tableFor := map[netip.Addr]string{}
	for _, r := range rules {
		tableFor[r.Client] = r.Table
	}
	if tableFor[cA] != "1002" || tableFor[cC] != "1002" || tableFor[cB] != "1003" {
		t.Errorf("client->table mapping wrong: %+v", tableFor)
	}
}

// Device names and table numbers are a stable function of the node's tunnel IP,
// so a node keeps the same device/table across reconciles.
func TestExitDevNaming(t *testing.T) {
	cases := map[string]struct{ name, table string }{
		"10.8.0.2": {"awgex2", "1002"},
		"10.8.0.3": {"awgex3", "1003"},
		"10.8.1.5": {"awgex261", "1261"}, // (1<<8)|5 = 261
	}
	for addr, want := range cases {
		a := netip.MustParseAddr(addr)
		if got := exitDevName(a); got != want.name {
			t.Errorf("exitDevName(%s)=%s want %s", addr, got, want.name)
		}
		if got := exitTableFor(a); got != want.table {
			t.Errorf("exitTableFor(%s)=%s want %s", addr, got, want.table)
		}
	}
}

// The dry-run prints exactly the ip commands the integration test applies by
// hand, so this locks the two in step: per-node IPIP device + table default
// route, then per-client source rule.
func TestEnsureExitRoutes_DryRun(t *testing.T) {
	a := Applier{Iface: "awg0", DryRun: true}
	hub := netip.MustParseAddr("10.8.0.1")
	routes := []ExitRoute{
		{netip.MustParseAddr("10.8.0.5"), netip.MustParseAddr("10.8.0.2")},
		{netip.MustParseAddr("10.8.0.6"), netip.MustParseAddr("10.8.0.3")},
	}
	out := captureStdout(t, func() {
		if err := a.EnsureExitRoutes(hub, routes); err != nil {
			t.Fatalf("EnsureExitRoutes: %v", err)
		}
	})
	for _, want := range []string{
		"ip link add awgex2 type ipip local 10.8.0.1 remote 10.8.0.2",
		// Addressing the decap device is what makes loose rp_filter reachable at
		// all; see EnsureExitRoutes. Dropping this line silently breaks returns.
		"ip addr replace 10.8.0.1/32 dev awgex2",
		"sysctl -w net.ipv4.conf.awgex2.rp_filter=2",
		"ip route replace default dev awgex2 table 1002",
		"ip link add awgex3 type ipip local 10.8.0.1 remote 10.8.0.3",
		"ip addr replace 10.8.0.1/32 dev awgex3",
		"sysctl -w net.ipv4.conf.awgex3.rp_filter=2",
		"ip route replace default dev awgex3 table 1003",
		"flush ip rules pref 5100",
		"ip rule add from 10.8.0.5/32 lookup 1002 pref 5100",
		"ip rule add from 10.8.0.6/32 lookup 1003 pref 5100",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("dry-run missing %q:\n%s", want, out)
		}
	}
}

// No exits => nothing to print, no error.
func TestEnsureExitRoutes_Empty(t *testing.T) {
	a := Applier{Iface: "awg0", DryRun: true}
	out := captureStdout(t, func() {
		if err := a.EnsureExitRoutes(netip.MustParseAddr("10.8.0.1"), nil); err != nil {
			t.Fatalf("EnsureExitRoutes(nil): %v", err)
		}
	})
	if strings.Contains(out, "ip link add") {
		t.Errorf("no routes should add no devices:\n%s", out)
	}
}
