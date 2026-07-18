package store

import (
	"context"
	"fmt"
	"net/netip"

	"beautifulwg/internal/awg"

	"github.com/google/uuid"
)

// NodeLinkDTO is the HTTP shape of a directed node-to-node link.
type NodeLinkDTO struct {
	Src string `json:"src"`
	Dst string `json:"dst"`
}

// nodeMeta is the subset of a node needed to validate a link.
type nodeMeta struct {
	isHub   bool
	subnets []netip.Prefix
}

// loadNodeMeta returns is_hub + subnets for the given node ids.
func (s *Store) loadNodeMeta(ctx context.Context, ids ...uuid.UUID) (map[uuid.UUID]nodeMeta, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT n.id, n.is_hub,
		       COALESCE(array_agg(ns.subnet::text) FILTER (WHERE ns.subnet IS NOT NULL), '{}')
		FROM nodes n LEFT JOIN node_subnets ns ON ns.node_id = n.id
		WHERE n.id = ANY($1) AND n.status = 'active'
		GROUP BY n.id`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uuid.UUID]nodeMeta{}
	for rows.Next() {
		var id uuid.UUID
		var m nodeMeta
		var subs []string
		if err := rows.Scan(&id, &m.isHub, &subs); err != nil {
			return nil, err
		}
		for _, s := range subs {
			if p, err := netip.ParsePrefix(s); err == nil {
				m.subnets = append(m.subnets, p)
			}
		}
		out[id] = m
	}
	return out, rows.Err()
}

// SetNodeLink creates a directed link src -> dst (hosts on src's LAN may reach
// dst's subnets). Idempotent. Rejects self-links, links touching the hub node,
// and links whose src/dst subnets overlap (no-NAT routing can't disambiguate).
func (s *Store) SetNodeLink(ctx context.Context, srcID, dstID uuid.UUID) error {
	if srcID == dstID {
		return fmt.Errorf("a node cannot link to itself")
	}
	meta, err := s.loadNodeMeta(ctx, srcID, dstID)
	if err != nil {
		return err
	}
	src, ok := meta[srcID]
	dst, ok2 := meta[dstID]
	if !ok || !ok2 {
		return fmt.Errorf("both nodes must exist and be active")
	}
	if src.isHub || dst.isHub {
		return fmt.Errorf("the internet-exit hub node cannot be part of a site-to-site link")
	}
	if len(src.subnets) == 0 || len(dst.subnets) == 0 {
		return fmt.Errorf("both nodes must have at least one LAN subnet")
	}
	for _, a := range src.subnets {
		for _, b := range dst.subnets {
			if a.Overlaps(b) {
				return fmt.Errorf("subnets overlap (%s vs %s): node-to-node routing has no NAT to disambiguate", a, b)
			}
		}
	}
	_, err = s.Pool.Exec(ctx,
		`INSERT INTO node_links(src_node_id, dst_node_id) VALUES ($1, $2)
		 ON CONFLICT DO NOTHING`, srcID, dstID)
	return err
}

// DeleteNodeLink removes a directed link.
func (s *Store) DeleteNodeLink(ctx context.Context, srcID, dstID uuid.UUID) error {
	_, err := s.Pool.Exec(ctx,
		`DELETE FROM node_links WHERE src_node_id = $1 AND dst_node_id = $2`, srcID, dstID)
	return err
}

// ListNodeLinks returns every directed link as DTOs (for the graph UI).
func (s *Store) ListNodeLinks(ctx context.Context) ([]NodeLinkDTO, error) {
	rows, err := s.Pool.Query(ctx, `SELECT src_node_id, dst_node_id FROM node_links`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NodeLinkDTO
	for rows.Next() {
		var src, dst uuid.UUID
		if err := rows.Scan(&src, &dst); err != nil {
			return nil, err
		}
		out = append(out, NodeLinkDTO{Src: src.String(), Dst: dst.String()})
	}
	return out, rows.Err()
}

// loadNodeLinks resolves every link into an awg.NodeLink using the given
// node-by-id map (subnets). Links whose endpoints are missing/inactive or have
// no subnets are skipped.
func (s *Store) loadNodeLinks(ctx context.Context, nodeByID map[uuid.UUID]awg.Node) ([]awg.NodeLink, error) {
	rows, err := s.Pool.Query(ctx, `SELECT src_node_id, dst_node_id FROM node_links`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []awg.NodeLink
	for rows.Next() {
		var srcID, dstID uuid.UUID
		if err := rows.Scan(&srcID, &dstID); err != nil {
			return nil, err
		}
		src, ok := nodeByID[srcID]
		dst, ok2 := nodeByID[dstID]
		if !ok || !ok2 || len(src.Subnets) == 0 || len(dst.Subnets) == 0 {
			continue
		}
		out = append(out, awg.NodeLink{SrcSubnets: src.Subnets, DstSubnets: dst.Subnets})
	}
	return out, rows.Err()
}

// nodeReachSubnets returns the subnets a node may route to/from over the tunnel
// via its links: the union of the OTHER endpoint's subnets across all links
// where this node is either src or dst (both directions need the routes — one
// for the outbound path, one for the return path).
func (s *Store) nodeReachSubnets(ctx context.Context, nodeID uuid.UUID) ([]netip.Prefix, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT DISTINCT ns.subnet::text
		FROM node_links nl
		JOIN node_subnets ns
		  ON ns.node_id = CASE WHEN nl.src_node_id = $1 THEN nl.dst_node_id ELSE nl.src_node_id END
		WHERE nl.src_node_id = $1 OR nl.dst_node_id = $1`, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []netip.Prefix
	for rows.Next() {
		var sub string
		if err := rows.Scan(&sub); err != nil {
			return nil, err
		}
		if p, err := netip.ParsePrefix(sub); err == nil {
			out = append(out, p)
		}
	}
	return out, rows.Err()
}
