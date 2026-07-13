package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/netip"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func newToken() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// GetEnrollSecret returns the shared secret nodes must present to enroll. Empty
// means enrollment is disabled.
func (s *Store) GetEnrollSecret(ctx context.Context) (string, error) {
	var sec string
	err := s.Pool.QueryRow(ctx, `SELECT enroll_secret FROM hub WHERE id = 1`).Scan(&sec)
	return sec, err
}

// SetEnrollSecretIfEmpty seeds the enroll secret on first boot from env; it never
// overwrites an existing one.
func (s *Store) SetEnrollSecretIfEmpty(ctx context.Context, secret string) error {
	if secret == "" {
		return nil
	}
	_, err := s.Pool.Exec(ctx,
		`UPDATE hub SET enroll_secret = $1 WHERE id = 1 AND enroll_secret = ''`, secret)
	return err
}

// EnrollNode registers (or re-registers) a node as pending and returns a poll
// token. Dedupe is by public key: the node keeps its private key, so its pubkey
// is its stable identity.
func (s *Store) EnrollNode(ctx context.Context, name, hostname, lanIface, pubkey string, subnets []netip.Prefix) (token string, status string, err error) {
	err = pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		var id uuid.UUID
		var tok *string
		var st string
		row := tx.QueryRow(ctx,
			`SELECT id, enroll_token, status FROM nodes WHERE public_key = $1 FOR UPDATE`, pubkey)
		switch e := row.Scan(&id, &tok, &st); e {
		case nil:
			// existing node re-announcing
			token = derefOr(tok, newToken())
			status = st
			if st == "rejected" {
				status = "pending"
			}
			_, err := tx.Exec(ctx,
				`UPDATE nodes SET enroll_token=$2, hostname=$3, lan_iface=$4,
				        status = CASE WHEN status='rejected' THEN 'pending' ELSE status END
				 WHERE id=$1`, id, token, hostname, lanIface)
			return err
		case pgx.ErrNoRows:
			token = newToken()
			status = "pending"
			if err := tx.QueryRow(ctx,
				`INSERT INTO nodes(name, lan_iface, public_key, private_key, status, enroll_token, hostname)
				 VALUES ($1,$2,$3,'', 'pending', $4, $5) RETURNING id`,
				name, lanIface, pubkey, token, hostname,
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
		default:
			return e
		}
	})
	return token, status, err
}

// NodeByToken returns id + status for a polling node, and its allocated address
// if approved.
func (s *Store) NodeByToken(ctx context.Context, token string) (id uuid.UUID, status string, err error) {
	err = s.Pool.QueryRow(ctx,
		`SELECT id, status FROM nodes WHERE enroll_token = $1`, token).Scan(&id, &status)
	return
}

// TouchNodeByToken records a heartbeat.
func (s *Store) TouchNodeByToken(ctx context.Context, token string) error {
	_, err := s.Pool.Exec(ctx,
		`UPDATE nodes SET last_seen = now() WHERE enroll_token = $1`, token)
	return err
}

// ApproveNode allocates an IP for a pending node and marks it active.
func (s *Store) ApproveNode(ctx context.Context, id uuid.UUID) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		var status string
		var hasAddr bool
		if err := tx.QueryRow(ctx,
			`SELECT status, address IS NOT NULL FROM nodes WHERE id=$1 FOR UPDATE`, id,
		).Scan(&status, &hasAddr); err != nil {
			return err
		}
		if status == "active" && hasAddr {
			return nil // already approved
		}
		addr, err := AllocateIP(ctx, tx)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx,
			`UPDATE nodes SET address=$2::inet, status='active' WHERE id=$1`, id, addr.String())
		return err
	})
}

// RejectNode marks a node rejected (its token stops returning a config).
func (s *Store) RejectNode(ctx context.Context, id uuid.UUID) error {
	ct, err := s.Pool.Exec(ctx, `UPDATE nodes SET status='rejected' WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return fmt.Errorf("node not found")
	}
	return nil
}

func derefOr(p *string, def string) string {
	if p != nil && *p != "" {
		return *p
	}
	return def
}
