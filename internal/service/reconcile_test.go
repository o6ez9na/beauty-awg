package service

import (
	"context"
	"net/netip"
	"os"
	"sort"
	"strings"
	"testing"

	"6ers3rk/internal/awg"
	"6ers3rk/internal/store"

	"github.com/google/uuid"
)

// fakeApplier records what Reconcile asked the system to do instead of touching
// it, so the DB -> render -> apply wiring can be asserted end to end.
type fakeApplier struct {
	hubConf    string
	nftRules   string
	routes     []netip.Prefix
	exitHub    netip.Addr
	exitMesh   []netip.Prefix
	exitRoutes []awg.ExitRoute
	calls      int
}

func (f *fakeApplier) Apply(hubConf, nftRules string) error {
	f.hubConf, f.nftRules = hubConf, nftRules
	f.calls++
	return nil
}
func (f *fakeApplier) EnsureRoutes(subnets []netip.Prefix) error { f.routes = subnets; return nil }
func (f *fakeApplier) EnsureExitRoutes(hubAddr netip.Addr, mesh []netip.Prefix, routes []awg.ExitRoute) error {
	f.exitHub, f.exitMesh, f.exitRoutes = hubAddr, mesh, routes
	return nil
}
func (f *fakeApplier) IfaceName() string { return "awg0" }

// Reconcile reads real DB state, so this needs a throwaway postgres — see
// internal/store/grant_exit_test.go for how to start one and AWG_TEST_DSN.
func testService(t *testing.T) (*Service, *fakeApplier) {
	t.Helper()
	dsn := os.Getenv("AWG_TEST_DSN")
	if dsn == "" {
		t.Skip("AWG_TEST_DSN not set; skipping database-backed test")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(st.Close)
	// The store and service packages test against the SAME database and Go runs
	// packages in parallel, so serialize on an advisory lock rather than making
	// the caller remember `-p 1`. Taken BEFORE Migrate: concurrent first runs
	// would otherwise race to create the same types. Released before the pool
	// closes (Cleanup is LIFO).
	conn, err := st.Pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(823041)`); err != nil {
		t.Fatalf("advisory lock: %v", err)
	}
	t.Cleanup(func() {
		conn.Exec(context.Background(), `SELECT pg_advisory_unlock(823041)`)
		conn.Release()
	})
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for _, table := range []string{"grant_rules", "grants", "node_links", "clients", "node_subnets", "nodes"} {
		if _, err := st.Pool.Exec(ctx, "DELETE FROM "+table); err != nil {
			t.Fatalf("clean %s: %v", table, err)
		}
	}
	if _, err := st.EnsureHub(ctx, "vpn.example.com", 51820,
		netip.MustParseAddr("10.8.0.1"), netip.MustParsePrefix("10.8.0.0/24"), "1.1.1.1"); err != nil {
		t.Fatalf("ensure hub: %v", err)
	}
	f := &fakeApplier{}
	return &Service{St: st, Applier: f}, f
}

func mkNode(t *testing.T, st *store.Store, name, subnet string) (uuid.UUID, netip.Addr) {
	t.Helper()
	ctx := context.Background()
	keys, err := awg.GenerateKeypair()
	if err != nil {
		t.Fatalf("keys: %v", err)
	}
	if _, _, err := st.EnrollNode(ctx, name, name+".local", "eth0", keys.Public,
		[]netip.Prefix{netip.MustParsePrefix(subnet)}); err != nil {
		t.Fatalf("enroll %s: %v", name, err)
	}
	var id uuid.UUID
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM nodes WHERE public_key=$1`, keys.Public).Scan(&id); err != nil {
		t.Fatalf("lookup %s: %v", name, err)
	}
	if err := st.ApproveNode(ctx, id); err != nil {
		t.Fatalf("approve %s: %v", name, err)
	}
	// inet renders with a prefix length; the mesh addresses are single hosts.
	var addr string
	if err := st.Pool.QueryRow(ctx, `SELECT host(address) FROM nodes WHERE id=$1`, id).Scan(&addr); err != nil {
		t.Fatalf("addr %s: %v", name, err)
	}
	return id, netip.MustParseAddr(addr)
}

func mkClient(t *testing.T, st *store.Store, name string) (uuid.UUID, netip.Addr) {
	t.Helper()
	keys, err := awg.GenerateKeypair()
	if err != nil {
		t.Fatalf("keys: %v", err)
	}
	id, addr, err := st.CreateClient(context.Background(), name, "", keys, "")
	if err != nil {
		t.Fatalf("create client %s: %v", name, err)
	}
	return id, addr
}

// Two devices exiting through two different nodes must reach the applier as two
// independent client->node routes, and the hub config must NOT hand either node
// the default route (that is what capped the old design at one exit).
func TestReconcile_TwoExitNodesBecomeTwoExitRoutes(t *testing.T) {
	svc, f := testService(t)
	ctx := context.Background()

	n1, n1Addr := mkNode(t, svc.St, "kseen_lan", "192.168.0.0/24")
	n2, n2Addr := mkNode(t, svc.St, "st4v3r_lan", "192.168.1.0/24")
	cA, cAAddr := mkClient(t, svc.St, "laptop")
	cB, cBAddr := mkClient(t, svc.St, "phone")

	for _, g := range []struct{ c, n uuid.UUID }{{cA, n1}, {cB, n2}} {
		if err := svc.St.SetGrant(ctx, g.c, g.n); err != nil {
			t.Fatalf("grant: %v", err)
		}
	}
	if err := svc.St.SetGrantExit(ctx, cA, n1, true); err != nil {
		t.Fatalf("exit A: %v", err)
	}
	if err := svc.St.SetGrantExit(ctx, cB, n2, true); err != nil {
		t.Fatalf("exit B: %v", err)
	}

	if err := svc.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	if f.exitHub != netip.MustParseAddr("10.8.0.1") {
		t.Errorf("exit routes anchored at %v, want the hub address 10.8.0.1", f.exitHub)
	}
	got := append([]awg.ExitRoute(nil), f.exitRoutes...)
	sort.Slice(got, func(i, j int) bool { return got[i].Client.Less(got[j].Client) })
	want := []awg.ExitRoute{{Client: cAAddr, Node: n1Addr}, {Client: cBAddr, Node: n2Addr}}
	sort.Slice(want, func(i, j int) bool { return want[i].Client.Less(want[j].Client) })
	if len(got) != len(want) {
		t.Fatalf("got %d exit routes, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("exit route %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	// The mesh carve-out must reach the applier, or exit clients lose every other
	// site and the pool: the source rule captures all their traffic.
	meshSeen := map[string]bool{}
	for _, m := range f.exitMesh {
		meshSeen[m.String()] = true
	}
	for _, want := range []string{"192.168.0.0/24", "192.168.1.0/24", "10.8.0.0/24"} {
		if !meshSeen[want] {
			t.Errorf("exit mesh carve-out missing %s: %v", want, f.exitMesh)
		}
	}

	// Neither node peer may carry 0.0.0.0/0: on one interface only one peer could
	// hold it, so a second exit node would silently steal everyone's internet.
	if strings.Contains(f.hubConf, "0.0.0.0/0") {
		t.Errorf("hub config must not give any peer the default route:\n%s", f.hubConf)
	}
	for _, addr := range []netip.Addr{n1Addr, n2Addr} {
		if !strings.Contains(f.hubConf, addr.String()+"/32") {
			t.Errorf("hub config missing %s/32 peer:\n%s", addr, f.hubConf)
		}
	}
}

// A client granted access to a node WITHOUT the exit flag must not produce an
// exit route — otherwise a plain split-tunnel grant would hijack the device's
// whole internet.
func TestReconcile_NonExitGrantsProduceNoExitRoutes(t *testing.T) {
	svc, f := testService(t)
	ctx := context.Background()

	n, _ := mkNode(t, svc.St, "kseen_lan", "192.168.0.0/24")
	c, _ := mkClient(t, svc.St, "laptop")
	if err := svc.St.SetGrant(ctx, c, n); err != nil {
		t.Fatalf("grant: %v", err)
	}

	if err := svc.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(f.exitRoutes) != 0 {
		t.Errorf("plain grant produced exit routes: %+v", f.exitRoutes)
	}
	// The node's LAN is still pinned to the interface.
	if len(f.routes) != 1 || f.routes[0].String() != "192.168.0.0/24" {
		t.Errorf("routes = %v, want [192.168.0.0/24]", f.routes)
	}
}

// Turning the exit back off must withdraw the route, not merely stop adding it:
// EnsureExitRoutes reconciles to exactly what it is given.
func TestReconcile_DisablingExitWithdrawsTheRoute(t *testing.T) {
	svc, f := testService(t)
	ctx := context.Background()

	n, _ := mkNode(t, svc.St, "kseen_lan", "192.168.0.0/24")
	c, _ := mkClient(t, svc.St, "laptop")
	if err := svc.St.SetGrant(ctx, c, n); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if err := svc.St.SetGrantExit(ctx, c, n, true); err != nil {
		t.Fatalf("enable exit: %v", err)
	}
	if err := svc.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(f.exitRoutes) != 1 {
		t.Fatalf("expected 1 exit route, got %+v", f.exitRoutes)
	}

	if err := svc.St.SetGrantExit(ctx, c, n, false); err != nil {
		t.Fatalf("disable exit: %v", err)
	}
	if err := svc.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(f.exitRoutes) != 0 {
		t.Errorf("exit route survived being turned off: %+v", f.exitRoutes)
	}
	if f.calls != 2 {
		t.Errorf("hub config applied %d times, want one per reconcile (2)", f.calls)
	}
}
