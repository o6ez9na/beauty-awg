package awg

import (
	"fmt"
	"log"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Applier writes and reloads the hub's live config. It runs ON the VPS hub.
type Applier struct {
	Iface   string // awg interface, e.g. "awg0"
	ConfDir string // e.g. "/etc/amnezia/amneziawg"
	NFTFile string // e.g. "/etc/awgpanel/acl.nft"
	DryRun  bool   // render only, never touch the system
}

// IfaceName reports the awg interface this Applier drives. It exists so callers
// can hold an Applier behind an interface (see service.Applier) and still name
// the device for read-only queries such as handshake listing.
func (a Applier) IfaceName() string { return a.Iface }

// Apply writes awg0.conf + acl.nft and hot-reloads both. `awg syncconf` updates
// peers in place WITHOUT tearing the interface down, so existing tunnels (and
// the node's CGNAT hole punch) survive a client add/remove.
func (a Applier) Apply(hubConf, nftRules string) error {
	confPath := filepath.Join(a.ConfDir, a.Iface+".conf")

	if a.DryRun {
		fmt.Printf("--- %s ---\n%s\n--- %s ---\n%s\n", confPath, hubConf, a.NFTFile, nftRules)
		return nil
	}

	// Ensure parent dirs exist. A bind-mounted /etc/amnezia can shadow the dir
	// the image created, so create it at runtime rather than at build time.
	if err := os.MkdirAll(a.ConfDir, 0o700); err != nil {
		return fmt.Errorf("mkdir conf dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(a.NFTFile), 0o700); err != nil {
		return fmt.Errorf("mkdir nft dir: %w", err)
	}

	if err := os.WriteFile(confPath, []byte(hubConf), 0o600); err != nil {
		return fmt.Errorf("write hub conf: %w", err)
	}
	if err := os.WriteFile(a.NFTFile, []byte(nftRules), 0o600); err != nil {
		return fmt.Errorf("write nft: %w", err)
	}

	// First run (interface absent): bring it up from the freshly written conf,
	// which also runs PostUp. Afterwards, hot-sync in place so live tunnels
	// (and the node's CGNAT hole punch) survive config changes.
	if !a.ifaceExists() {
		if out, err := runCmd("awg-quick", "up", a.Iface); err != nil {
			return fmt.Errorf("awg-quick up: %w: %s", err, out)
		}
	} else {
		// syncconf takes a STRIPPED config (no PostUp/DNS/etc).
		stripped, err := outCmd("awg-quick", "strip", a.Iface)
		if err != nil {
			return fmt.Errorf("awg-quick strip: %w", err)
		}
		tmp, err := os.CreateTemp("", "awgsync-*.conf")
		if err != nil {
			return err
		}
		defer os.Remove(tmp.Name())
		if _, err := tmp.Write(stripped); err != nil {
			return err
		}
		_ = tmp.Close()
		if out, err := runCmd("awg", "syncconf", a.Iface, tmp.Name()); err != nil {
			return fmt.Errorf("awg syncconf: %w: %s", err, out)
		}
	}

	if out, err := runCmd("nft", "-f", a.NFTFile); err != nil {
		return fmt.Errorf("nft reload: %w: %s", err, out)
	}
	return nil
}

// ifaceExists reports whether the awg interface is present in the netns.
func (a Applier) ifaceExists() bool {
	return tryCmd("ip", "link", "show", a.Iface) == nil
}

// EnsureRoutes installs a route to the awg interface for each node LAN subnet.
// `awg syncconf` updates peers but (unlike `awg-quick up`) does NOT touch the
// routing table, so subnets belonging to nodes added after the interface first
// came up would otherwise be routed out the default gateway. `ip route replace`
// is idempotent. Skip 0.0.0.0/0 (the hub-exit node) — that must not be pinned to
// awg0. Call after Apply.
func (a Applier) EnsureRoutes(subnets []netip.Prefix) error {
	if a.DryRun {
		for _, s := range subnets {
			fmt.Printf("ip route replace %s dev %s\n", s.String(), a.Iface)
		}
		return nil
	}
	for _, s := range subnets {
		if s.Bits() == 0 {
			continue // never pin a default route to the tunnel here
		}
		if out, err := runCmd("ip", "route", "replace", s.String(), "dev", a.Iface); err != nil {
			return fmt.Errorf("ip route replace %s: %w: %s", s, err, out)
		}
	}
	return nil
}

// ExitRoute maps an internet-exit client (tunnel IP) to the exit node (tunnel
// IP) its whole traffic should leave through. Different clients may point at
// different nodes — that is the whole point of the IPIP scheme.
type ExitRoute struct {
	Client netip.Addr
	Node   netip.Addr
}

// Exit policy-routing constants. Every exit node gets its own IPIP device and
// routing table, keyed off the node's tunnel address so a node maps to the same
// device/table across reconciles; every exit client gets a source rule at
// exitPref steering it into its node's table.
const (
	// In-VPN destinations are diverted back to the main table at a LOWER pref,
	// i.e. consulted before the per-client rule below.
	exitMeshPref  = "5090"
	exitPref      = "5100"
	exitTableBase = 1000
	exitDevPrefix = "awgex"
)

type exitDevice struct {
	Name   string
	Remote netip.Addr
	Table  string
}
type exitRule struct {
	Client netip.Addr
	Table  string
}

// exitIndex is a stable small integer for a node's tunnel address — its host
// part within a /16-or-smaller pool. Node tunnel IPs are unique in the pool, so
// this is collision-free for any sane deployment.
func exitIndex(node netip.Addr) int {
	b := node.As4()
	return int(b[2])<<8 | int(b[3])
}
func exitDevName(node netip.Addr) string  { return fmt.Sprintf("%s%d", exitDevPrefix, exitIndex(node)) }
func exitTableFor(node netip.Addr) string { return strconv.Itoa(exitTableBase + exitIndex(node)) }

// exitPlan turns desired routes into the IPIP devices to ensure (one per
// distinct exit node) and the per-client source rules. Pure and order-stable so
// it can be unit-tested and diffed.
func exitPlan(routes []ExitRoute) ([]exitDevice, []exitRule) {
	seen := map[netip.Addr]bool{}
	var devs []exitDevice
	var rules []exitRule
	for _, r := range routes {
		if !seen[r.Node] {
			seen[r.Node] = true
			devs = append(devs, exitDevice{Name: exitDevName(r.Node), Remote: r.Node, Table: exitTableFor(r.Node)})
		}
		rules = append(rules, exitRule{Client: r.Client, Table: exitTableFor(r.Node)})
	}
	sort.Slice(devs, func(i, j int) bool { return devs[i].Remote.Less(devs[j].Remote) })
	sort.Slice(rules, func(i, j int) bool {
		if rules[i].Client != rules[j].Client {
			return rules[i].Client.Less(rules[j].Client)
		}
		return rules[i].Table < rules[j].Table
	})
	return devs, rules
}

// rpFilterPath is the procfs knob for an interface's reverse-path filtering.
func rpFilterPath(dev string) string {
	return "/proc/sys/net/ipv4/conf/" + dev + "/rp_filter"
}

// EnsureExitRoutes reconciles the hub's internet-exit policy routing to exactly
// the given client->node routes. Per distinct exit node: a dedicated IPIP tunnel
// to the node's /32 with a default route in its own table. Per exit client: a
// source rule steering it into its node's table. IPIP devices we own
// (exitDevPrefix*) that are no longer wanted are torn down, and the source rules
// are flushed and rebuilt, so removing or repointing an exit cleans up. Multiple
// exit nodes coexist because each rides its node's unique /32, not a shared
// 0.0.0.0/0 that only one peer could hold.
//
// `mesh` is every destination that lives inside the VPN — each node's LAN plus
// the client pool. Each gets a rule diverting it back to the main table ahead of
// the per-client rules, because the source rule captures ALL of an exit client's
// traffic: with only a default route in its table, a client whose internet goes
// out one site would also have its access to every OTHER site (and to the rest
// of the pool) tunnelled there, where nothing knows those addresses. "Send my
// internet out this node" must not mean "send everything out this node" — the
// same split the nftables ACL already makes (see RenderNFT's meshSet).
//
// Diverting by destination rather than copying routes into each exit table keeps
// one source of truth: main already knows how to reach every mesh prefix (they
// are pinned to the awg interface by EnsureRoutes, and the pool is on-link),
// whereas per-table copies would have to be rebuilt and reaped per exit node.
// Call after Apply.
func (a Applier) EnsureExitRoutes(hubAddr netip.Addr, mesh []netip.Prefix, routes []ExitRoute) error {
	devs, rules := exitPlan(routes)

	if a.DryRun {
		for _, d := range devs {
			fmt.Printf("ip link add %s type ipip local %s remote %s\n", d.Name, hubAddr, d.Remote)
			fmt.Printf("ip addr replace %s/32 dev %s\n", hubAddr, d.Name)
			fmt.Printf("echo 2 > %s\n", rpFilterPath(d.Name))
			fmt.Printf("ip route replace default dev %s table %s\n", d.Name, d.Table)
		}
		fmt.Printf("flush ip rules pref %s and %s\n", exitMeshPref, exitPref)
		for _, m := range mesh {
			fmt.Printf("ip rule add to %s lookup main pref %s\n", m, exitMeshPref)
		}
		for _, r := range rules {
			fmt.Printf("ip rule add from %s/32 lookup %s pref %s\n", r.Client, r.Table, exitPref)
		}
		return nil
	}

	// Flush our rules first (ip rule has no "replace"); loop because del removes
	// one match at a time.
	for _, pref := range []string{exitMeshPref, exitPref} {
		for {
			if err := tryCmd("ip", "rule", "del", "pref", pref); err != nil {
				break
			}
		}
	}

	// Leftover from the single-exit design, which policy-routed exit clients into
	// awg0 via table 51. Its source rules used the same pref, so they are already
	// gone by here; only the route lingers, inert but confusing to anyone reading
	// `ip route show table all`. Removed by hand rather than by flushing table 51,
	// which is not ours to empty. Safe to delete once no upgraded hub predates it.
	_ = tryCmd("ip", "route", "del", "default", "dev", a.Iface, "table", "51")

	// Reconcile devices: tear down ones we own that are no longer wanted, create
	// the missing ones, and (re)point each table's default route (idempotent).
	want := map[string]bool{}
	for _, d := range devs {
		want[d.Name] = true
	}
	for _, name := range a.listExitDevices() {
		if !want[name] {
			_ = tryCmd("ip", "link", "del", name)
		}
	}
	for _, d := range devs {
		if !a.linkExists(d.Name) {
			if out, err := runCmd("ip", "link", "add", d.Name, "type", "ipip", "local", hubAddr.String(), "remote", d.Remote.String()); err != nil {
				return fmt.Errorf("ip link add %s: %w: %s", d.Name, err, out)
			}
			if out, err := runCmd("ip", "link", "set", d.Name, "up"); err != nil {
				return fmt.Errorf("ip link set %s up: %w: %s", d.Name, err, out)
			}
		}
		// The decap device carries return traffic sourced from arbitrary internet
		// hosts. Source validation happens to pass here even under strict
		// rp_filter, because it looks up the source with flowi4.saddr set to the
		// client — which matches that client's own `from <client> lookup <table>`
		// rule, whose default route points back out this very device. We do not
		// lean on that: these two lines make the device valid on its own terms.
		//
		// They are a pair. Loose rp_filter alone is not enough, because the
		// kernel's loose branch in __fib_validate_source() is unreachable on a
		// device with no IPv4 address — it falls through to last_resort, which
		// rejects under ANY non-zero rp_filter. So the device gets the hub's own
		// /32 (the address awg0 already holds; IPIP devices are NOARP, so nothing
		// contends for it) and rp_filter is set per device — 2 is the max, so it
		// wins the kernel's max(conf.all, conf.<dev>) whatever the host's global
		// setting is, without loosening the rest of the machine. Note the order:
		// removing an interface's last address also drops the routes through it,
		// so the address must land before the table's default route below.
		if out, err := runCmd("ip", "addr", "replace", hubAddr.String()+"/32", "dev", d.Name); err != nil {
			return fmt.Errorf("ip addr replace on %s: %w: %s", d.Name, err, out)
		}
		// Written straight to procfs rather than shelled out to sysctl(8), which
		// the backend container does not necessarily have — and best-effort,
		// because Docker mounts /proc/sys read-only even with NET_ADMIN.
		//
		// Failing here is not fatal. Source validation on this device passes even
		// under strict rp_filter: the kernel looks the source up with
		// flowi4.saddr set to the exit client, which matches that client's own
		// `from <client> lookup <table>` rule below, and that table's default
		// route points back out this very device. The knob is belt-and-braces for
		// the case where the rule is momentarily absent, so log and carry on
		// rather than leaving the hub with no exit routing at all.
		// The target is a pre-existing procfs knob, so the mode is ignored by the
		// kernel; 0o600 keeps gosec (G306) happy without changing behaviour.
		if err := os.WriteFile(rpFilterPath(d.Name), []byte("2\n"), 0o600); err != nil {
			log.Printf("awg: could not set rp_filter on %s (%v) — exit returns still validate via the client's source rule", d.Name, err)
		}
		if out, err := runCmd("ip", "route", "replace", "default", "dev", d.Name, "table", d.Table); err != nil {
			return fmt.Errorf("ip route replace default table %s: %w: %s", d.Table, err, out)
		}
	}

	// Divert in-VPN destinations back to the main table, which already routes
	// them over the awg interface, before the per-client rules send everything
	// else down the tunnel.
	for _, m := range mesh {
		if out, err := runCmd("ip", "rule", "add", "to", m.String(), "lookup", "main", "pref", exitMeshPref); err != nil {
			return fmt.Errorf("ip rule add to %s: %w: %s", m, err, out)
		}
	}

	// Add the source rules.
	for _, r := range rules {
		if out, err := runCmd("ip", "rule", "add", "from", r.Client.String()+"/32", "lookup", r.Table, "pref", exitPref); err != nil {
			return fmt.Errorf("ip rule add from %s: %w: %s", r.Client, err, out)
		}
	}
	return nil
}

// listExitDevices returns the IPIP devices this Applier owns (named
// exitDevPrefix*), so stale ones can be reaped when their node stops being an
// exit. Never returns the kernel's fallback tunl0.
func (a Applier) listExitDevices() []string {
	out, err := outCmd("ip", "-o", "link", "show")
	if err != nil {
		return nil
	}
	var names []string
	for _, line := range strings.Split(string(out), "\n") {
		// "<idx>: <name>@NONE: <...>" — the name is the second colon field, minus
		// any "@peer" suffix.
		parts := strings.SplitN(line, ":", 3)
		if len(parts) < 2 {
			continue
		}
		name := strings.TrimSpace(parts[1])
		if i := strings.IndexByte(name, '@'); i >= 0 {
			name = name[:i]
		}
		if strings.HasPrefix(name, exitDevPrefix) {
			names = append(names, name)
		}
	}
	return names
}

func (a Applier) linkExists(name string) bool {
	return tryCmd("ip", "link", "show", name) == nil
}

// This package orchestrates the host network stack: every subprocess is a fixed
// binary (ip / awg / awg-quick / nft) whose arguments come from validated
// internal state — interface names, netip addresses, and numeric routing-table
// ids — never from untrusted request input. The helpers below centralise those
// invocations so the accepted G204 risk is documented once rather than at every
// call site.

// runCmd runs a config command and returns its combined output.
func runCmd(name string, args ...string) ([]byte, error) {
	// #nosec G204 -- fixed binary, args from validated internal state (see above).
	return exec.Command(name, args...).CombinedOutput()
}

// outCmd runs a config command and returns its stdout.
func outCmd(name string, args ...string) ([]byte, error) {
	// #nosec G204 -- fixed binary, args from validated internal state (see above).
	return exec.Command(name, args...).Output()
}

// tryCmd runs a config command for its exit status only.
func tryCmd(name string, args ...string) error {
	// #nosec G204 -- fixed binary, args from validated internal state (see above).
	return exec.Command(name, args...).Run()
}
