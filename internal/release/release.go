// Package release checks GitHub Releases for the latest published nodeagent
// build, so the panel can tell a polling node when an update is available.
// Results are cached well past GitHub's unauthenticated rate limit, since
// every enrolled node polls the panel every ~10s.
package release

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

const repoSlug = "o6ez9na/beauty-awg"

// Checker caches the latest GitHub release tag and its asset list.
type Checker struct {
	client *http.Client
	ttl    time.Duration

	mu      sync.Mutex
	fetched time.Time
	tag     string
	assets  map[string]bool
	err     error
}

func NewChecker() *Checker {
	return &Checker{client: &http.Client{Timeout: 10 * time.Second}, ttl: 30 * time.Minute}
}

type ghRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name string `json:"name"`
	} `json:"assets"`
}

func (c *Checker) refreshLocked() {
	c.fetched = time.Now()
	req, err := http.NewRequest(http.MethodGet, "https://api.github.com/repos/"+repoSlug+"/releases/latest", nil)
	if err != nil {
		c.err = err
		return
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := c.client.Do(req)
	if err != nil {
		c.err = err
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		c.err = fmt.Errorf("github releases/latest: %s", resp.Status)
		return
	}
	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		c.err = err
		return
	}
	assets := make(map[string]bool, len(rel.Assets))
	for _, a := range rel.Assets {
		assets[a.Name] = true
	}
	c.tag, c.assets, c.err = rel.TagName, assets, nil
}

// Update reports whether a nodeagent build newer than curVersion is available
// for arch (a Go GOARCH, e.g. "amd64"), and its download URL if so.
// curVersion == "" (unversioned/dev builds) never reports an update — they
// have no reliable baseline to compare against.
func (c *Checker) Update(curVersion, arch string) (latest, url string, available bool) {
	if curVersion == "" || curVersion == "dev" || arch == "" {
		return "", "", false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.fetched) > c.ttl {
		c.refreshLocked()
	}
	if c.err != nil || c.tag == "" || c.tag == curVersion {
		return c.tag, "", false
	}
	asset := fmt.Sprintf("nodeagent-%s-linux-%s", c.tag, arch)
	if !c.assets[asset] {
		return c.tag, "", false
	}
	return c.tag, fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", repoSlug, c.tag, asset), true
}
