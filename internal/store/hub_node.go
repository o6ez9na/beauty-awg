package store

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// SetWanIface overwrites the hub's WAN interface (for internet-exit masquerade)
// when a non-empty value is provided.
func (s *Store) SetWanIface(ctx context.Context, iface string) error {
	if iface == "" {
		return nil
	}
	_, err := s.Pool.Exec(ctx, `UPDATE hub SET wan_iface = $1 WHERE id = 1`, iface)
	return err
}

// EnsureHubNode creates the virtual "internet exit" node representing the hub
// itself (subnet 0.0.0.0/0). Idempotent. Granting a client this node gives it a
// full tunnel to the internet via the hub.
func (s *Store) EnsureHubNode(ctx context.Context) error {
	var exists bool
	if err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM nodes WHERE is_hub)`).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	var hubAddr string
	if err := s.Pool.QueryRow(ctx, `SELECT host(address) FROM hub WHERE id = 1`).Scan(&hubAddr); err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		var id uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO nodes(name, address, lan_iface, private_key, public_key, status, is_hub)
			VALUES ('panel (internet exit)', $1::inet, '', '', '', 'active', true)
			RETURNING id`, hubAddr).Scan(&id); err != nil {
			return err
		}
		_, err := tx.Exec(ctx,
			`INSERT INTO node_subnets(node_id, subnet) VALUES ($1, '0.0.0.0/0'::cidr)`, id)
		return err
	})
}
