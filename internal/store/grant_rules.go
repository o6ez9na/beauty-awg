package store

import (
	"context"
	"fmt"
	"net/netip"

	"beautifulwg/internal/awg"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// RuleDTO is the HTTP shape of a grant rule.
type RuleDTO struct {
	Dest     string `json:"dest"`      // CIDR or host/32
	Proto    string `json:"proto"`     // any|tcp|udp
	PortFrom int    `json:"port_from"` // 0 = all ports
	PortTo   int    `json:"port_to"`   // 0 = single (=port_from) or all
}

func grantKey(cid, nid uuid.UUID) string { return cid.String() + "|" + nid.String() }

// loadAllGrantRules returns every grant's rules keyed by "clientID|nodeID".
func (s *Store) loadAllGrantRules(ctx context.Context) (map[string][]awg.GrantRule, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT client_id, node_id, dest::text, proto, port_from, port_to FROM grant_rules`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]awg.GrantRule{}
	for rows.Next() {
		var cid, nid uuid.UUID
		var dest, proto string
		var pf, pt *int
		if err := rows.Scan(&cid, &nid, &dest, &proto, &pf, &pt); err != nil {
			return nil, err
		}
		r, err := toRule(dest, proto, pf, pt)
		if err != nil {
			return nil, err
		}
		k := grantKey(cid, nid)
		out[k] = append(out[k], r)
	}
	return out, rows.Err()
}

// GrantRules returns the rules for one grant as DTOs (for the editor).
func (s *Store) GrantRules(ctx context.Context, clientID, nodeID uuid.UUID) ([]RuleDTO, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT dest::text, proto, port_from, port_to FROM grant_rules
		 WHERE client_id=$1 AND node_id=$2 ORDER BY dest`, clientID, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RuleDTO
	for rows.Next() {
		var d RuleDTO
		var pf, pt *int
		if err := rows.Scan(&d.Dest, &d.Proto, &pf, &pt); err != nil {
			return nil, err
		}
		if pf != nil {
			d.PortFrom = *pf
		}
		if pt != nil {
			d.PortTo = *pt
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// SetGrantRules replaces the rule set for a grant (must exist). Empty = full access.
func (s *Store) SetGrantRules(ctx context.Context, clientID, nodeID uuid.UUID, rules []RuleDTO) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM grants WHERE client_id=$1 AND node_id=$2)`,
			clientID, nodeID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("grant does not exist")
		}
		if _, err := tx.Exec(ctx,
			`DELETE FROM grant_rules WHERE client_id=$1 AND node_id=$2`, clientID, nodeID); err != nil {
			return err
		}
		for _, r := range rules {
			if _, err := netip.ParsePrefix(r.Dest); err != nil {
				return fmt.Errorf("bad dest %q: %w", r.Dest, err)
			}
			if r.Proto != "any" && r.Proto != "tcp" && r.Proto != "udp" {
				return fmt.Errorf("bad proto %q", r.Proto)
			}
			pf := nullIfZero(r.PortFrom)
			pt := nullIfZero(r.PortTo)
			if _, err := tx.Exec(ctx,
				`INSERT INTO grant_rules(client_id, node_id, dest, proto, port_from, port_to)
				 VALUES ($1,$2,$3::cidr,$4,$5,$6)`,
				clientID, nodeID, r.Dest, r.Proto, pf, pt); err != nil {
				return err
			}
		}
		return nil
	})
}

func toRule(dest, proto string, pf, pt *int) (awg.GrantRule, error) {
	p, err := netip.ParsePrefix(dest)
	if err != nil {
		return awg.GrantRule{}, err
	}
	r := awg.GrantRule{Dest: p, Proto: proto}
	if pf != nil {
		r.PortFrom = *pf
	}
	if pt != nil {
		r.PortTo = *pt
	}
	return r, nil
}

func nullIfZero(n int) *int {
	if n == 0 {
		return nil
	}
	return &n
}
