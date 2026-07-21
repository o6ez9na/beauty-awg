package store

import (
	"context"
	"net/netip"

	"6ers3rk/internal/awg"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// GetHub loads the singleton hub row into an awg.Hub.
func (s *Store) GetHub(ctx context.Context) (awg.Hub, error) {
	var h awg.Hub
	var addr, pool string
	var h1, h2, h3, h4 int64
	err := s.Pool.QueryRow(ctx, `
		SELECT endpoint, listen_port, host(address), pool_cidr::text,
		       private_key, public_key, dns, wan_iface,
		       jc, jmin, jmax, s1, s2, h1, h2, h3, h4
		FROM hub WHERE id = 1`,
	).Scan(&h.Endpoint, &h.ListenPort, &addr, &pool,
		&h.Keys.Private, &h.Keys.Public, &h.DNS, &h.WANIface,
		&h.Params.Jc, &h.Params.Jmin, &h.Params.Jmax, &h.Params.S1, &h.Params.S2,
		&h1, &h2, &h3, &h4)
	if err != nil {
		return awg.Hub{}, err
	}
	h.Address, _ = netip.ParseAddr(addr)
	h.PoolCIDR, _ = netip.ParsePrefix(pool)
	// H1..H4 are AmneziaWG header magics: uint32 values persisted in int64 columns
	// (Postgres has no unsigned integers). They round-trip within uint32 range.
	// #nosec G115 -- values originate as uint32 (see awg.ObfuscationParams).
	h.Params.H1, h.Params.H2 = uint32(h1), uint32(h2)
	// #nosec G115 -- see above.
	h.Params.H3, h.Params.H4 = uint32(h3), uint32(h4)
	h.Resolver = s.ResolverOn
	h.ResolverPort = s.ResolverPort
	return h, nil
}

// CreateNode allocates a tunnel IP and inserts the node + its subnets atomically.
func (s *Store) CreateNode(ctx context.Context, name, lanIface string, subnets []netip.Prefix, keys awg.Keypair, psk string) (uuid.UUID, netip.Addr, error) {
	var id uuid.UUID
	var addr netip.Addr
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		a, err := AllocateIP(ctx, tx)
		if err != nil {
			return err
		}
		addr = a
		if err := tx.QueryRow(ctx, `
			INSERT INTO nodes(name, address, lan_iface, private_key, public_key, preshared)
			VALUES ($1, $2::inet, $3, $4, $5, $6) RETURNING id`,
			name, a.String(), lanIface, keys.Private, keys.Public, psk,
		).Scan(&id); err != nil {
			return err
		}
		for _, sn := range subnets {
			if _, err := tx.Exec(ctx,
				`INSERT INTO node_subnets(node_id, subnet) VALUES ($1, $2::cidr)`,
				id, sn.String()); err != nil {
				return err
			}
		}
		return nil
	})
	return id, addr, err
}

// CreateClient allocates a /32 and inserts the client.
func (s *Store) CreateClient(ctx context.Context, name, dns string, keys awg.Keypair, psk string) (uuid.UUID, netip.Addr, error) {
	var id uuid.UUID
	var addr netip.Addr
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		a, err := AllocateIP(ctx, tx)
		if err != nil {
			return err
		}
		addr = a
		return tx.QueryRow(ctx, `
			INSERT INTO clients(name, address, private_key, public_key, preshared, dns)
			VALUES ($1, $2::inet, $3, $4, $5, $6) RETURNING id`,
			name, a.String(), keys.Private, keys.Public, psk, dns,
		).Scan(&id)
	})
	return id, addr, err
}

// SetGrant grants client access to node (idempotent).
func (s *Store) SetGrant(ctx context.Context, clientID, nodeID uuid.UUID) error {
	_, err := s.Pool.Exec(ctx,
		`INSERT INTO grants(client_id, node_id) VALUES ($1, $2)
		 ON CONFLICT DO NOTHING`, clientID, nodeID)
	return err
}

// DeleteGrant revokes access.
func (s *Store) DeleteGrant(ctx context.Context, clientID, nodeID uuid.UUID) error {
	_, err := s.Pool.Exec(ctx,
		`DELETE FROM grants WHERE client_id = $1 AND node_id = $2`, clientID, nodeID)
	return err
}

// nodeRow is a node plus its DB id, so grants can be resolved by id.
type nodeRow struct {
	ID   uuid.UUID
	Node awg.Node
}

// listNodes returns all nodes with subnets, keyed for grant resolution.
func (s *Store) listNodes(ctx context.Context) ([]nodeRow, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT n.id, n.name, host(n.address), n.lan_iface,
		       n.private_key, n.public_key, n.preshared, n.is_hub, n.dns,
		       COALESCE(array_agg(ns.subnet::text) FILTER (WHERE ns.subnet IS NOT NULL), '{}'),
		       ARRAY(SELECT domain FROM node_domains WHERE node_id = n.id)
		FROM nodes n
		LEFT JOIN node_subnets ns ON ns.node_id = n.id
		WHERE n.status = 'active' AND n.address IS NOT NULL
		GROUP BY n.id
		ORDER BY n.address`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []nodeRow
	for rows.Next() {
		var nr nodeRow
		var addr string
		var subnets []string
		if err := rows.Scan(&nr.ID, &nr.Node.Name, &addr, &nr.Node.LANIface,
			&nr.Node.Keys.Private, &nr.Node.Keys.Public, &nr.Node.Preshared, &nr.Node.IsHub, &nr.Node.DNS, &subnets, &nr.Node.Domains); err != nil {
			return nil, err
		}
		nr.Node.Address, _ = netip.ParseAddr(addr)
		for _, s := range subnets {
			if p, err := netip.ParsePrefix(s); err == nil {
				nr.Node.Subnets = append(nr.Node.Subnets, p)
			}
		}
		out = append(out, nr)
	}
	return out, rows.Err()
}

// Snapshot assembles everything RenderHub + RenderNFT need: hub, all nodes, all
// ENABLED clients, grants (only for enabled clients), and node-to-node links.
func (s *Store) Snapshot(ctx context.Context) (awg.Hub, []awg.Node, []awg.Client, []awg.Grant, []awg.NodeLink, error) {
	hub, err := s.GetHub(ctx)
	if err != nil {
		return awg.Hub{}, nil, nil, nil, nil, err
	}

	nodeRows, err := s.listNodes(ctx)
	if err != nil {
		return awg.Hub{}, nil, nil, nil, nil, err
	}
	nodes := make([]awg.Node, len(nodeRows))
	nodeByID := map[uuid.UUID]awg.Node{}
	for i, nr := range nodeRows {
		nodes[i] = nr.Node
		nodeByID[nr.ID] = nr.Node
	}

	// enabled clients keyed by id
	crows, err := s.Pool.Query(ctx, `
		SELECT id, name, host(address), private_key, public_key, preshared, dns
		FROM clients WHERE enabled = true ORDER BY address`)
	if err != nil {
		return awg.Hub{}, nil, nil, nil, nil, err
	}
	defer crows.Close()
	var clients []awg.Client
	clientAddrByID := map[uuid.UUID]netip.Addr{}
	for crows.Next() {
		var id uuid.UUID
		var c awg.Client
		var addr string
		if err := crows.Scan(&id, &c.Name, &addr, &c.Keys.Private, &c.Keys.Public, &c.Preshared, &c.DNS); err != nil {
			return awg.Hub{}, nil, nil, nil, nil, err
		}
		c.Address, _ = netip.ParseAddr(addr)
		clients = append(clients, c)
		clientAddrByID[id] = c.Address
	}
	if err := crows.Err(); err != nil {
		return awg.Hub{}, nil, nil, nil, nil, err
	}

	rulesByGrant, err := s.loadAllGrantRules(ctx)
	if err != nil {
		return awg.Hub{}, nil, nil, nil, nil, err
	}

	// grants for enabled clients only
	grows, err := s.Pool.Query(ctx, `
		SELECT g.client_id, g.node_id, g.exit
		FROM grants g JOIN clients c ON c.id = g.client_id
		WHERE c.enabled = true`)
	if err != nil {
		return awg.Hub{}, nil, nil, nil, nil, err
	}
	defer grows.Close()
	var grants []awg.Grant
	for grows.Next() {
		var cid, nid uuid.UUID
		var exit bool
		if err := grows.Scan(&cid, &nid, &exit); err != nil {
			return awg.Hub{}, nil, nil, nil, nil, err
		}
		caddr, ok := clientAddrByID[cid]
		node, ok2 := nodeByID[nid]
		if !ok || !ok2 {
			continue
		}
		grants = append(grants, awg.Grant{
			ClientAddr: caddr,
			NodeAddr:   node.Address,
			Subnets:    node.Subnets,
			Rules:      rulesByGrant[grantKey(cid, nid)],
			IsExit:     node.IsHub,
			NodeExit:   exit && !node.IsHub,
			NodeDNS:    node.DNS,
		})
	}
	if err := grows.Err(); err != nil {
		return awg.Hub{}, nil, nil, nil, nil, err
	}

	links, err := s.loadNodeLinks(ctx, nodeByID)
	if err != nil {
		return awg.Hub{}, nil, nil, nil, nil, err
	}
	return hub, nodes, clients, grants, links, nil
}
