package store

import (
	"context"
	"net/netip"
	"strings"
	"time"

	"beautifulwg/internal/awg"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// --- DTOs for the HTTP layer (carry DB ids the render models omit) ---

type NodeDTO struct {
	ID       uuid.UUID  `json:"id"`
	Name     string     `json:"name"`
	Address  string     `json:"address"`
	LANIface string     `json:"lan_iface"`
	Subnets  []string   `json:"subnets"`
	Status   string     `json:"status"`
	Hostname string     `json:"hostname"`
	LastSeen *time.Time `json:"last_seen"`
	IsHub    bool       `json:"is_hub"`
	DNS      string     `json:"dns"`
	Domains  []string   `json:"domains"`
}

type ClientDTO struct {
	ID           uuid.UUID   `json:"id"`
	Name         string      `json:"name"`
	Address      string      `json:"address"`
	DNS          string      `json:"dns"`
	Enabled      bool        `json:"enabled"`
	GrantedNodes []uuid.UUID `json:"granted_nodes"`
}

func (s *Store) ListNodes(ctx context.Context) ([]NodeDTO, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT n.id, n.name, COALESCE(host(n.address), ''), n.lan_iface,
		       COALESCE(array_agg(ns.subnet::text) FILTER (WHERE ns.subnet IS NOT NULL), '{}'),
		       n.status, n.hostname, n.last_seen, n.is_hub, n.dns,
		       ARRAY(SELECT domain FROM node_domains WHERE node_id = n.id)
		FROM nodes n
		LEFT JOIN node_subnets ns ON ns.node_id = n.id
		GROUP BY n.id ORDER BY n.is_hub DESC, n.status, n.address NULLS FIRST, n.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NodeDTO
	for rows.Next() {
		var d NodeDTO
		if err := rows.Scan(&d.ID, &d.Name, &d.Address, &d.LANIface, &d.Subnets,
			&d.Status, &d.Hostname, &d.LastSeen, &d.IsHub, &d.DNS, &d.Domains); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) ListClients(ctx context.Context) ([]ClientDTO, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT c.id, c.name, host(c.address), c.dns, c.enabled,
		       COALESCE(array_agg(g.node_id) FILTER (WHERE g.node_id IS NOT NULL), '{}')
		FROM clients c
		LEFT JOIN grants g ON g.client_id = c.id
		GROUP BY c.id ORDER BY c.address`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ClientDTO
	for rows.Next() {
		var d ClientDTO
		if err := rows.Scan(&d.ID, &d.Name, &d.Address, &d.DNS, &d.Enabled, &d.GrantedNodes); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) DeleteNode(ctx context.Context, id uuid.UUID) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM nodes WHERE id = $1`, id)
	return err
}

// SetNodeDNS sets a node's DNS server (empty string clears it).
func (s *Store) SetNodeDNS(ctx context.Context, id uuid.UUID, dns string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE nodes SET dns = $2 WHERE id = $1`, id, dns)
	return err
}

// SetNodeDomains replaces the set of local domains a node's DNS is authoritative for.
func (s *Store) SetNodeDomains(ctx context.Context, id uuid.UUID, domains []string) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM node_domains WHERE node_id = $1`, id); err != nil {
			return err
		}
		for _, d := range domains {
			d = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(d, ".")))
			// Accept wildcard syntax like "*.greeneye.top" — the resolver matches
			// by suffix, so strip a leading "*." (or bare "*.") to the zone.
			d = strings.TrimPrefix(d, "*.")
			if d == "" || d == "*" {
				continue
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO node_domains(node_id, domain) VALUES ($1, $2) ON CONFLICT DO NOTHING`, id, d); err != nil {
				return err
			}
		}
		return nil
	})
}

// DomainRoutes returns the domain -> node-DNS map for active nodes that have both
// a DNS server and at least one domain. Feeds the split-horizon resolver.
func (s *Store) DomainRoutes(ctx context.Context) (map[string]string, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT d.domain, n.dns
		FROM node_domains d
		JOIN nodes n ON n.id = d.node_id
		WHERE n.status = 'active' AND n.dns <> ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var domain, dns string
		if err := rows.Scan(&domain, &dns); err != nil {
			return nil, err
		}
		out[domain] = dns
	}
	return out, rows.Err()
}

func (s *Store) DeleteClient(ctx context.Context, id uuid.UUID) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM clients WHERE id = $1`, id)
	return err
}

// UpdateClient sets enabled + dns.
func (s *Store) UpdateClient(ctx context.Context, id uuid.UUID, enabled bool, dns string) error {
	_, err := s.Pool.Exec(ctx,
		`UPDATE clients SET enabled = $2, dns = $3 WHERE id = $1`, id, enabled, dns)
	return err
}

// GetNodeForExport returns hub + one node for RenderNode.
func (s *Store) GetNodeForExport(ctx context.Context, id uuid.UUID) (awg.Hub, awg.Node, error) {
	hub, err := s.GetHub(ctx)
	if err != nil {
		return awg.Hub{}, awg.Node{}, err
	}
	var n awg.Node
	var addr string
	var subnets []string
	err = s.Pool.QueryRow(ctx, `
		SELECT n.name, host(n.address), n.lan_iface, n.private_key, n.public_key, n.preshared,
		       COALESCE(array_agg(ns.subnet::text) FILTER (WHERE ns.subnet IS NOT NULL), '{}')
		FROM nodes n LEFT JOIN node_subnets ns ON ns.node_id = n.id
		WHERE n.id = $1 GROUP BY n.id`, id,
	).Scan(&n.Name, &addr, &n.LANIface, &n.Keys.Private, &n.Keys.Public, &n.Preshared, &subnets)
	if err != nil {
		return awg.Hub{}, awg.Node{}, err
	}
	n.Address, _ = netip.ParseAddr(addr)
	for _, sn := range subnets {
		if p, e := netip.ParsePrefix(sn); e == nil {
			n.Subnets = append(n.Subnets, p)
		}
	}
	return hub, n, nil
}

// GetClientForExport returns hub + client + its granted subnets for RenderClient.
func (s *Store) GetClientForExport(ctx context.Context, id uuid.UUID) (awg.Hub, awg.Client, []awg.Grant, error) {
	hub, err := s.GetHub(ctx)
	if err != nil {
		return awg.Hub{}, awg.Client{}, nil, err
	}
	var c awg.Client
	var addr string
	err = s.Pool.QueryRow(ctx, `
		SELECT name, host(address), private_key, public_key, preshared, dns
		FROM clients WHERE id = $1`, id,
	).Scan(&c.Name, &addr, &c.Keys.Private, &c.Keys.Public, &c.Preshared, &c.DNS)
	if err != nil {
		return awg.Hub{}, awg.Client{}, nil, err
	}
	c.Address, _ = netip.ParseAddr(addr)

	rows, err := s.Pool.Query(ctx, `
		SELECT n.id, host(n.address), n.dns,
		       COALESCE(array_agg(ns.subnet::text) FILTER (WHERE ns.subnet IS NOT NULL), '{}')
		FROM grants g
		JOIN nodes n ON n.id = g.node_id
		LEFT JOIN node_subnets ns ON ns.node_id = n.id
		WHERE g.client_id = $1 AND n.status = 'active' AND n.address IS NOT NULL
		GROUP BY n.id`, id)
	if err != nil {
		return awg.Hub{}, awg.Client{}, nil, err
	}
	defer rows.Close()
	rulesByGrant, err := s.loadAllGrantRules(ctx)
	if err != nil {
		return awg.Hub{}, awg.Client{}, nil, err
	}
	var grants []awg.Grant
	for rows.Next() {
		var nodeID uuid.UUID
		var naddr, ndns string
		var subnets []string
		if err := rows.Scan(&nodeID, &naddr, &ndns, &subnets); err != nil {
			return awg.Hub{}, awg.Client{}, nil, err
		}
		g := awg.Grant{ClientAddr: c.Address, Rules: rulesByGrant[grantKey(id, nodeID)], NodeDNS: ndns}
		g.NodeAddr, _ = netip.ParseAddr(naddr)
		for _, sn := range subnets {
			if p, e := netip.ParsePrefix(sn); e == nil {
				g.Subnets = append(g.Subnets, p)
			}
		}
		grants = append(grants, g)
	}
	return hub, c, grants, rows.Err()
}

// --- admin auth ---

func (s *Store) CreateAdmin(ctx context.Context, username, passwordHash string) error {
	_, err := s.Pool.Exec(ctx,
		`INSERT INTO admins(username, password_hash) VALUES ($1, $2)`, username, passwordHash)
	return err
}

func (s *Store) GetAdminHash(ctx context.Context, username string) (uuid.UUID, string, error) {
	var id uuid.UUID
	var hash string
	err := s.Pool.QueryRow(ctx,
		`SELECT id, password_hash FROM admins WHERE username = $1`, username,
	).Scan(&id, &hash)
	return id, hash, err
}

func (s *Store) AdminCount(ctx context.Context) (int, error) {
	var n int
	err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM admins`).Scan(&n)
	return n, err
}

// --- hub bootstrap ---

// EnsureHub inserts the singleton hub row with generated keys + obfuscation
// params if it does not exist yet. Returns whether it created it.
func (s *Store) EnsureHub(ctx context.Context, endpoint string, listenPort int, address netip.Addr, pool netip.Prefix, dns string) (bool, error) {
	var exists bool
	if err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM hub WHERE id=1)`).Scan(&exists); err != nil {
		return false, err
	}
	if exists {
		return false, nil
	}
	keys, err := awg.GenerateKeypair()
	if err != nil {
		return false, err
	}
	p, err := awg.NewRandomParams()
	if err != nil {
		return false, err
	}
	err = pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `
			INSERT INTO hub(id, endpoint, listen_port, address, pool_cidr,
			                private_key, public_key, dns,
			                jc, jmin, jmax, s1, s2, h1, h2, h3, h4)
			VALUES (1, $1, $2, $3::inet, $4::cidr, $5, $6, $7,
			        $8, $9, $10, $11, $12, $13, $14, $15, $16)`,
			endpoint, listenPort, address.String(), pool.String(),
			keys.Private, keys.Public, dns,
			p.Jc, p.Jmin, p.Jmax, p.S1, p.S2,
			int64(p.H1), int64(p.H2), int64(p.H3), int64(p.H4))
		return e
	})
	return err == nil, err
}
