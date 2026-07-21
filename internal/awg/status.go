package awg

import (
	"os/exec"
	"strconv"
	"strings"
)

// LatestHandshakes returns pubkey -> last-handshake unix seconds for every peer
// on the interface, read from `awg show <iface> dump`. A value of 0 means the
// peer has never completed a handshake.
func LatestHandshakes(iface string) map[string]int64 {
	out := map[string]int64{}
	// #nosec G204 -- fixed binary; iface is a validated config value, not user input.
	b, err := exec.Command("awg", "show", iface, "dump").Output()
	if err != nil {
		return out
	}
	lines := strings.Split(string(b), "\n")
	// First line is the interface itself; peers follow.
	for i, line := range lines {
		if i == 0 || strings.TrimSpace(line) == "" {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 5 {
			continue
		}
		hs, _ := strconv.ParseInt(f[4], 10, 64)
		out[f[0]] = hs
	}
	return out
}
