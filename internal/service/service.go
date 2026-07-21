package service

import (
	"context"
	"net/netip"
	"sync"

	"6ers3rk/internal/awg"
	"6ers3rk/internal/resolver"
	"6ers3rk/internal/store"
)

// Applier is the slice of awg.Applier that Reconcile drives. It is an interface
// so the wiring — which grants become exit routes, what the hub config says —
// can be tested without an awg interface or root on the machine running tests.
type Applier interface {
	Apply(hubConf, nftRules string) error
	EnsureRoutes(subnets []netip.Prefix) error
	EnsureExitRoutes(hubAddr netip.Addr, mesh []netip.Prefix, routes []awg.ExitRoute) error
	// IfaceName is the awg interface, needed by handlers that read handshakes.
	IfaceName() string
}

// Service is the glue: it turns current DB state into live hub config + nft
// rules and applies them. Every mutation handler calls Reconcile afterward.
type Service struct {
	St       *store.Store
	Applier  Applier
	Resolver *resolver.Resolver // optional split-horizon DNS resolver
	Upstream string             // default DNS upstream, e.g. "1.1.1.1:53"

	mu sync.Mutex // serialize reconciles so awg syncconf calls don't overlap
}

// Reconcile renders the hub config + nft ACL from the DB snapshot and applies
// them to the running system. Safe to call after any change.
func (s *Service) Reconcile(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	hub, nodes, clients, grants, links, err := s.St.Snapshot(ctx)
	if err != nil {
		return err
	}
	hubConf := awg.RenderHub(hub, nodes, clients, grants)
	nftRules := awg.RenderNFT(hub, nodes, grants, links)
	if err := s.Applier.Apply(hubConf, nftRules); err != nil {
		return err
	}

	// Pin each real node's LAN subnet to the awg interface (syncconf doesn't).
	var routes []netip.Prefix
	for _, n := range nodes {
		if n.IsHub {
			continue
		}
		routes = append(routes, n.Subnets...)
	}
	if err := s.Applier.EnsureRoutes(routes); err != nil {
		return err
	}

	// Policy-route each node-exit client's whole traffic into a per-node IPIP
	// tunnel, so it egresses via that exit node's home internet instead of the
	// hub's WAN. Different clients may target different exit nodes simultaneously.
	//
	// Destinations inside the VPN are carved back out of that: the node LANs
	// above plus the client pool. Without them an exit client would lose the
	// other sites and the rest of the pool, since the source rule captures every
	// packet it sends.
	mesh := append([]netip.Prefix(nil), routes...)
	if hub.PoolCIDR.IsValid() {
		mesh = append(mesh, hub.PoolCIDR)
	}
	var exitRoutes []awg.ExitRoute
	for _, g := range grants {
		if g.NodeExit {
			exitRoutes = append(exitRoutes, awg.ExitRoute{Client: g.ClientAddr, Node: g.NodeAddr})
		}
	}
	if err := s.Applier.EnsureExitRoutes(hub.Address, mesh, exitRoutes); err != nil {
		return err
	}

	// Rebuild the split-horizon resolver's domain->node-DNS map.
	if s.Resolver != nil {
		dr, err := s.St.DomainRoutes(ctx)
		if err != nil {
			return err
		}
		s.Resolver.SetRoutes(dr, s.Upstream)
	}
	return nil
}
