package resolver

import (
	"net"
	"testing"
	"time"

	"github.com/miekg/dns"
)

func TestPickLongestSuffix(t *testing.T) {
	r := New()
	r.SetRoutes(map[string]string{
		"slow.top":     "10.0.0.1",
		"mmop.gripe":   "10.0.0.2",
		"a.mmop.gripe": "10.0.0.3", // more specific
	}, "1.1.1.1")

	cases := map[string]string{
		"dns1.slow.top.":  "10.0.0.1:53",
		"akka.mmop.gripe.": "10.0.0.2:53",
		"x.a.mmop.gripe.": "10.0.0.3:53", // longest match wins
		"google.com.":     "1.1.1.1:53",  // default
		"slow.top.":       "10.0.0.1:53", // exact
	}
	for q, want := range cases {
		if got := r.pick(q); got != want {
			t.Errorf("pick(%q) = %q, want %q", q, got, want)
		}
	}
}

// TestForwardRouting starts a mock upstream and verifies the resolver routes a
// domain to it (and returns its answer).
func TestForwardRouting(t *testing.T) {
	// mock upstream: answers any A query with 9.9.9.9
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	mockAddr := pc.LocalAddr().String()
	mock := &dns.Server{PacketConn: pc, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
		m := new(dns.Msg)
		m.SetReply(req)
		m.Answer = append(m.Answer, &dns.A{
			Hdr: dns.RR_Header{Name: req.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
			A:   net.ParseIP("9.9.9.9"),
		})
		_ = w.WriteMsg(m)
	})}
	go mock.ActivateAndServe()
	defer mock.Shutdown()
	time.Sleep(50 * time.Millisecond)

	// resolver listening on a random UDP port, routing corp.local -> mock
	rpc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	rAddr := rpc.LocalAddr().String()
	res := New()
	res.SetRoutes(map[string]string{"corp.local": mockAddr}, "127.0.0.1:1") // default points nowhere
	rsrv := &dns.Server{PacketConn: rpc, Handler: dns.HandlerFunc(res.handle)}
	go rsrv.ActivateAndServe()
	defer rsrv.Shutdown()
	time.Sleep(50 * time.Millisecond)

	c := new(dns.Client)
	m := new(dns.Msg)
	m.SetQuestion("host.corp.local.", dns.TypeA)
	resp, _, err := c.Exchange(m, rAddr)
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if len(resp.Answer) != 1 {
		t.Fatalf("want 1 answer, got %d", len(resp.Answer))
	}
	if a, ok := resp.Answer[0].(*dns.A); !ok || a.A.String() != "9.9.9.9" {
		t.Fatalf("want 9.9.9.9, got %v", resp.Answer[0])
	}
}
