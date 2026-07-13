package awg

import (
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
)

// Applier writes and reloads the hub's live config. It runs ON the VPS hub.
type Applier struct {
	Iface   string // awg interface, e.g. "awg0"
	ConfDir string // e.g. "/etc/amnezia/amneziawg"
	NFTFile string // e.g. "/etc/awgpanel/acl.nft"
	DryRun  bool   // render only, never touch the system
}

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
		if out, err := exec.Command("awg-quick", "up", a.Iface).CombinedOutput(); err != nil {
			return fmt.Errorf("awg-quick up: %w: %s", err, out)
		}
	} else {
		// syncconf takes a STRIPPED config (no PostUp/DNS/etc).
		stripped, err := exec.Command("awg-quick", "strip", a.Iface).Output()
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
		tmp.Close()
		if out, err := exec.Command("awg", "syncconf", a.Iface, tmp.Name()).CombinedOutput(); err != nil {
			return fmt.Errorf("awg syncconf: %w: %s", err, out)
		}
	}

	if out, err := exec.Command("nft", "-f", a.NFTFile).CombinedOutput(); err != nil {
		return fmt.Errorf("nft reload: %w: %s", err, out)
	}
	return nil
}

// ifaceExists reports whether the awg interface is present in the netns.
func (a Applier) ifaceExists() bool {
	return exec.Command("ip", "link", "show", a.Iface).Run() == nil
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
		if out, err := exec.Command("ip", "route", "replace", s.String(), "dev", a.Iface).CombinedOutput(); err != nil {
			return fmt.Errorf("ip route replace %s: %w: %s", s, err, out)
		}
	}
	return nil
}
