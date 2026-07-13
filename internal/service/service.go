package service

import (
	"context"
	"net/netip"
	"sync"

	"beautifulwg/internal/awg"
	"beautifulwg/internal/store"
)

// Service is the glue: it turns current DB state into live hub config + nft
// rules and applies them. Every mutation handler calls Reconcile afterward.
type Service struct {
	St      *store.Store
	Applier awg.Applier

	mu sync.Mutex // serialize reconciles so awg syncconf calls don't overlap
}

// Reconcile renders the hub config + nft ACL from the DB snapshot and applies
// them to the running system. Safe to call after any change.
func (s *Service) Reconcile(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	hub, nodes, clients, grants, err := s.St.Snapshot(ctx)
	if err != nil {
		return err
	}
	hubConf := awg.RenderHub(hub, nodes, clients, grants)
	nftRules := awg.RenderNFT(hub, grants)
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
	return s.Applier.EnsureRoutes(routes)
}
