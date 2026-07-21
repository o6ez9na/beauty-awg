package store

import (
	"context"
	"net/netip"
	"os"
	"strings"
	"testing"

	"6ers3rk/internal/awg"

	"github.com/google/uuid"
)

// The multi-exit invariant lives in SQL, so it can only be tested against a real
// database. Point AWG_TEST_DSN at a throwaway postgres to run these; without it
// they skip, so `go test ./...` stays green on a machine with no database.
//
//	docker run -d --name awg-test-pg -e POSTGRES_USER=awg -e POSTGRES_PASSWORD=testpw \
//	  -e POSTGRES_DB=awgtest -p 55432:5432 postgres:17-alpine
//	AWG_TEST_DSN='postgres://awg:testpw@localhost:55432/awgtest?sslmode=disable' go test ./internal/store/
func testStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("AWG_TEST_DSN")
	if dsn == "" {
		t.Skip("AWG_TEST_DSN not set; skipping database-backed test")
	}
	ctx := context.Background()
	s, err := Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(s.Close)
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// The store and service packages test against the SAME database and Go runs
	// packages in parallel, so serialize on an advisory lock rather than making
	// the caller remember `-p 1`. Released before the pool closes (Cleanup is LIFO).
	conn, err := s.Pool.Acquire(ctx)
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
	// Each test starts from an empty mesh. hub is left alone (ensureHub is
	// idempotent and the row is shared), but everything keyed off it is dropped.
	for _, table := range []string{"grant_rules", "grants", "node_links", "clients", "node_subnets", "nodes"} {
		if _, err := s.Pool.Exec(ctx, "DELETE FROM "+table); err != nil {
			t.Fatalf("clean %s: %v", table, err)
		}
	}
	if _, err := s.EnsureHub(ctx, "vpn.example.com", 51820,
		netip.MustParseAddr("10.8.0.1"), netip.MustParsePrefix("10.8.0.0/24"), "1.1.1.1"); err != nil {
		t.Fatalf("ensure hub: %v", err)
	}
	return s
}

// node enrolls and approves a node, returning its id.
func mkNode(t *testing.T, s *Store, name, subnet string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	keys, err := awg.GenerateKeypair()
	if err != nil {
		t.Fatalf("keys: %v", err)
	}
	if _, _, err := s.EnrollNode(ctx, name, name+".local", "eth0", keys.Public,
		[]netip.Prefix{netip.MustParsePrefix(subnet)}); err != nil {
		t.Fatalf("enroll %s: %v", name, err)
	}
	var id uuid.UUID
	if err := s.Pool.QueryRow(ctx, `SELECT id FROM nodes WHERE public_key=$1`, keys.Public).Scan(&id); err != nil {
		t.Fatalf("lookup %s: %v", name, err)
	}
	if err := s.ApproveNode(ctx, id); err != nil {
		t.Fatalf("approve %s: %v", name, err)
	}
	return id
}

func mkClient(t *testing.T, s *Store, name string) uuid.UUID {
	t.Helper()
	keys, err := awg.GenerateKeypair()
	if err != nil {
		t.Fatalf("keys: %v", err)
	}
	id, _, err := s.CreateClient(context.Background(), name, "", keys, "")
	if err != nil {
		t.Fatalf("create client %s: %v", name, err)
	}
	return id
}

// The whole point of the IPIP rework: two devices exiting through two different
// nodes at the same time. The old invariant was global and rejected the second.
func TestSetGrantExit_DifferentDevicesDifferentNodes(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	n1 := mkNode(t, s, "kseen_lan", "192.168.0.0/24")
	n2 := mkNode(t, s, "st4v3r_lan", "192.168.1.0/24")
	cA, cB := mkClient(t, s, "laptop"), mkClient(t, s, "phone")

	for _, g := range []struct {
		c, n uuid.UUID
	}{{cA, n1}, {cB, n2}} {
		if err := s.SetGrant(ctx, g.c, g.n); err != nil {
			t.Fatalf("grant: %v", err)
		}
	}

	if err := s.SetGrantExit(ctx, cA, n1, true); err != nil {
		t.Fatalf("first exit (A via node1): %v", err)
	}
	if err := s.SetGrantExit(ctx, cB, n2, true); err != nil {
		t.Fatalf("second exit (B via node2) must be allowed, got: %v", err)
	}

	for _, want := range []struct {
		c, n uuid.UUID
		name string
	}{{cA, n1, "A via node1"}, {cB, n2, "B via node2"}} {
		exit, err := s.GrantExit(ctx, want.c, want.n)
		if err != nil {
			t.Fatalf("read %s: %v", want.name, err)
		}
		if !exit {
			t.Errorf("%s: exit not persisted", want.name)
		}
	}
}

// A single device still routes all its traffic to ONE place, so a second exit
// for the SAME device is refused — and the message names the node it already
// uses, which the web UI turns into a one-click fix (web/app/lib/errors.ts).
func TestSetGrantExit_SameDeviceTwoNodesRejected(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	n1 := mkNode(t, s, "kseen_lan", "192.168.0.0/24")
	n2 := mkNode(t, s, "st4v3r_lan", "192.168.1.0/24")
	c := mkClient(t, s, "laptop")

	for _, n := range []uuid.UUID{n1, n2} {
		if err := s.SetGrant(ctx, c, n); err != nil {
			t.Fatalf("grant: %v", err)
		}
	}
	if err := s.SetGrantExit(ctx, c, n1, true); err != nil {
		t.Fatalf("first exit: %v", err)
	}

	err := s.SetGrantExit(ctx, c, n2, true)
	if err == nil {
		t.Fatal("second exit for the same device must be rejected")
	}
	if !strings.Contains(err.Error(), `already sends all traffic through "kseen_lan"`) {
		t.Errorf("error must name the node in the shape errors.ts matches, got: %v", err)
	}

	// Turning the first one off frees the device to exit elsewhere.
	if err := s.SetGrantExit(ctx, c, n1, false); err != nil {
		t.Fatalf("disable first exit: %v", err)
	}
	if err := s.SetGrantExit(ctx, c, n2, true); err != nil {
		t.Fatalf("re-pointing the exit after disabling the old one: %v", err)
	}
}

// Re-enabling the exit a device already has must not trip the invariant on
// itself: the check excludes the node being toggled.
func TestSetGrantExit_IdempotentOnSameNode(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	n := mkNode(t, s, "kseen_lan", "192.168.0.0/24")
	c := mkClient(t, s, "laptop")
	if err := s.SetGrant(ctx, c, n); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if err := s.SetGrantExit(ctx, c, n, true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if err := s.SetGrantExit(ctx, c, n, true); err != nil {
		t.Fatalf("re-enable on the same node must be a no-op, got: %v", err)
	}
}
