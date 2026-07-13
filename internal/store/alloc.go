package store

import (
	"context"
	"fmt"
	"net/netip"

	"github.com/jackc/pgx/v5"
)

// allocLockKey is a fixed advisory-lock id so concurrent AllocateIP calls
// serialize instead of racing to grab the same free address.
const allocLockKey = 0x41574750 // "AWGP"

// AllocateIP reserves the lowest free host address in the hub pool and returns
// it. Caller inserts the node/client using this address in the SAME tx so the
// address is "used" before the lock releases. Skips network, broadcast, hub,
// and any address already held by a node or client.
func AllocateIP(ctx context.Context, tx pgx.Tx) (netip.Addr, error) {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, allocLockKey); err != nil {
		return netip.Addr{}, err
	}

	var poolStr, hubStr string
	if err := tx.QueryRow(ctx,
		`SELECT pool_cidr::text, host(address) FROM hub WHERE id = 1`,
	).Scan(&poolStr, &hubStr); err != nil {
		return netip.Addr{}, fmt.Errorf("load hub: %w", err)
	}
	pool, err := netip.ParsePrefix(poolStr)
	if err != nil {
		return netip.Addr{}, err
	}
	hubAddr, _ := netip.ParseAddr(hubStr)

	used := map[netip.Addr]bool{hubAddr: true}
	rows, err := tx.Query(ctx,
		`SELECT host(address) FROM nodes UNION ALL SELECT host(address) FROM clients`)
	if err != nil {
		return netip.Addr{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return netip.Addr{}, err
		}
		if a, err := netip.ParseAddr(s); err == nil {
			used[a] = true
		}
	}
	if err := rows.Err(); err != nil {
		return netip.Addr{}, err
	}

	network := pool.Masked().Addr()      // .0  — skip
	broadcast := lastAddr(pool)          // .255 for /24 — skip
	for a := network.Next(); a.IsValid() && pool.Contains(a); a = a.Next() {
		if a == broadcast || used[a] {
			continue
		}
		return a, nil
	}
	return netip.Addr{}, fmt.Errorf("pool %s exhausted", pool)
}

// lastAddr returns the highest address in a prefix (broadcast for IPv4).
func lastAddr(p netip.Prefix) netip.Addr {
	a := p.Masked().Addr()
	bytes := a.As4()
	hostBits := 32 - p.Bits()
	for i := 0; i < hostBits; i++ {
		byteIdx := 3 - i/8
		bytes[byteIdx] |= 1 << (i % 8)
	}
	return netip.AddrFrom4(bytes)
}
