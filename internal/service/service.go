package service

import (
	"context"
	"net/netip"
	"sync"

	"6ers3rk/internal/awg"
	"6ers3rk/internal/resolver"
	"6ers3rk/internal/store"
)

// Service is the glue: it turns current DB state into live hub config + nft
// rules and applies them. Every mutation handler calls Reconcile afterward.
type Service struct {
	St       *store.Store
	Applier  awg.Applier
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

	// Policy-route node-exit clients' whole traffic into the tunnel so it egresses
	// via the exit node's home internet instead of the hub's WAN.
	var exitClients []netip.Addr
	for _, g := range grants {
		if g.NodeExit {
			exitClients = append(exitClients, g.ClientAddr)
		}
	}
	if err := s.Applier.EnsureExitClients(exitClients); err != nil {
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
