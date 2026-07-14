// Package resolver is a split-horizon DNS forwarder that runs on the hub. Client
// configs point DNS at the hub tunnel IP; this resolver forwards each query to
// the DNS server of whichever node owns the queried domain, and everything else
// to a default upstream. That lets one VPN client resolve local domains that
// live behind different nodes (each with its own local DNS).
package resolver

import (
	"log"
	"strings"
	"sync/atomic"
	"time"

	"github.com/miekg/dns"
)

// routing is an immutable snapshot swapped atomically on reconcile.
type routing struct {
	// domain (lowercase, no trailing dot) -> upstream "ip:port"
	byDomain map[string]string
	upstream string // default upstream for everything else
}

type Resolver struct {
	cur    atomic.Pointer[routing]
	client *dns.Client
}

func New() *Resolver {
	r := &Resolver{client: &dns.Client{Timeout: 4 * time.Second}}
	r.cur.Store(&routing{byDomain: map[string]string{}, upstream: "1.1.1.1:53"})
	return r
}

// SetRoutes atomically replaces the domain->DNS map and default upstream.
func (r *Resolver) SetRoutes(byDomain map[string]string, defaultUpstream string) {
	if defaultUpstream == "" {
		defaultUpstream = "1.1.1.1:53"
	}
	norm := make(map[string]string, len(byDomain))
	for d, up := range byDomain {
		norm[strings.ToLower(strings.TrimSuffix(d, "."))] = withPort(up)
	}
	r.cur.Store(&routing{byDomain: norm, upstream: withPort(defaultUpstream)})
}

// pick returns the upstream for a query name using the longest matching domain
// suffix, else the default.
func (r *Resolver) pick(qname string) string {
	rt := r.cur.Load()
	name := strings.ToLower(strings.TrimSuffix(qname, "."))
	best, bestLen := rt.upstream, -1
	for domain, up := range rt.byDomain {
		if name == domain || strings.HasSuffix(name, "."+domain) {
			if len(domain) > bestLen {
				best, bestLen = up, len(domain)
			}
		}
	}
	return best
}

func (r *Resolver) handle(w dns.ResponseWriter, req *dns.Msg) {
	if len(req.Question) == 0 {
		dns.HandleFailed(w, req)
		return
	}
	up := r.pick(req.Question[0].Name)
	resp, _, err := r.client.Exchange(req, up)
	if err != nil || resp == nil {
		// Retry once over TCP (large answers / truncation), then fail.
		tcp := &dns.Client{Net: "tcp", Timeout: 4 * time.Second}
		resp, _, err = tcp.Exchange(req, up)
		if err != nil || resp == nil {
			dns.HandleFailed(w, req)
			return
		}
	}
	_ = w.WriteMsg(resp)
}

// Serve starts UDP+TCP listeners on addr (e.g. "10.8.0.1:53"). Blocks; run in a
// goroutine. Retries binding because the hub tunnel IP appears only once awg0 is
// up.
func (r *Resolver) Serve(addr string) {
	dns.HandleFunc(".", r.handle)
	for _, net := range []string{"udp", "tcp"} {
		go func(net string) {
			for {
				srv := &dns.Server{Addr: addr, Net: net}
				if err := srv.ListenAndServe(); err != nil {
					log.Printf("resolver %s on %s: %v (retrying in 5s)", net, addr, err)
					time.Sleep(5 * time.Second)
				}
			}
		}(net)
	}
}

func withPort(s string) string {
	if s == "" {
		return s
	}
	if strings.Contains(s, ":") && !strings.Contains(s, "]") {
		return s // already host:port (ipv4) — naive but fine for our inputs
	}
	return s + ":53"
}
