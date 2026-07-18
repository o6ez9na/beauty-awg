package release

import (
	"testing"
	"time"
)

func TestUpdate_NoBaseline(t *testing.T) {
	c := NewChecker()
	if _, _, ok := c.Update("", "amd64"); ok {
		t.Error("empty curVersion must never report an update")
	}
	if _, _, ok := c.Update("dev", "amd64"); ok {
		t.Error("dev build must never report an update")
	}
	if _, _, ok := c.Update("v1.0.0", ""); ok {
		t.Error("empty arch must never report an update")
	}
}

func TestUpdate_SameVersion(t *testing.T) {
	c := NewChecker()
	c.tag = "v1.2.3"
	c.assets = map[string]bool{"nodeagent-v1.2.3-linux-amd64": true}
	c.fetched = time.Now()
	if _, _, ok := c.Update("v1.2.3", "amd64"); ok {
		t.Error("same version as latest must not report an update")
	}
}

func TestUpdate_NewerVersionMissingAsset(t *testing.T) {
	c := NewChecker()
	c.tag = "v1.3.0"
	c.assets = map[string]bool{"nodeagent-v1.3.0-linux-arm64": true} // no amd64 build
	c.fetched = time.Now()
	if _, _, ok := c.Update("v1.2.3", "amd64"); ok {
		t.Error("must not report an update when the matching asset is missing")
	}
}

func TestUpdate_NewerVersionAvailable(t *testing.T) {
	c := NewChecker()
	c.tag = "v1.3.0"
	c.assets = map[string]bool{"nodeagent-v1.3.0-linux-amd64": true}
	c.fetched = time.Now()
	latest, url, ok := c.Update("v1.2.3", "amd64")
	if !ok {
		t.Fatal("expected an update to be reported")
	}
	if latest != "v1.3.0" {
		t.Errorf("latest = %q, want v1.3.0", latest)
	}
	want := "https://github.com/o6ez9na/beauty-awg/releases/download/v1.3.0/nodeagent-v1.3.0-linux-amd64"
	if url != want {
		t.Errorf("url = %q, want %q", url, want)
	}
}
