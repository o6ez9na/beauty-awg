package buildversion

import "testing"

func TestResolve_ExplicitReleaseUnchanged(t *testing.T) {
	if got := Resolve("v1.2.3"); got != "v1.2.3" {
		t.Errorf("Resolve(%q) = %q, want unchanged", "v1.2.3", got)
	}
}

func TestResolve_FallsBackToVCSOrDev(t *testing.T) {
	// Under `go test`, ReadBuildInfo's Settings won't include vcs.revision
	// (test binaries aren't built with VCS stamping the same way `go build`
	// stamps a git checkout), so this exercises the "dev" fallback path.
	for _, in := range []string{"", "dev"} {
		got := Resolve(in)
		if got == "" {
			t.Errorf("Resolve(%q) returned empty string", in)
		}
	}
}
