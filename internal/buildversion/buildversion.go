// Package buildversion resolves a build identifier for display in the UI.
package buildversion

import "runtime/debug"

// Resolve returns declared unchanged if it already names an explicit release
// (anything other than "" or "dev" — i.e. it was set via
// -ldflags "-X main.version=..." for a tagged build). Otherwise it falls back
// to the short VCS revision Go's toolchain stamps into binaries built from a
// git checkout (with a "-dirty" suffix for uncommitted changes), so a build
// made straight from source still shows something traceable instead of a bare
// "dev". Returns "dev" if neither is available (e.g. `go build` outside a git
// checkout, or with -buildvcs=false).
func Resolve(declared string) string {
	if declared != "" && declared != "dev" {
		return declared
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	var rev string
	dirty := false
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev == "" {
		return "dev"
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	if dirty {
		rev += "-dirty"
	}
	return rev
}
