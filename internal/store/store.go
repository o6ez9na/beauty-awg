package store

import (
	"context"
	"embed"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Store wraps a pgx pool. All addresses are read/written as ::text and parsed
// with net/netip to avoid pgtype registration quirks for inet/cidr.
type Store struct {
	Pool *pgxpool.Pool
	// ResolverOn reflects whether the hub runs the split-horizon DNS resolver;
	// it flips client configs to use the hub tunnel IP as DNS.
	ResolverOn bool
}

func Open(ctx context.Context, dsn string) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return &Store{Pool: pool}, nil
}

func (s *Store) Close() { s.Pool.Close() }

// Migrate applies embedded migrations in filename order, tracked in
// schema_migrations. Idempotent.
func (s *Store) Migrate(ctx context.Context) error {
	_, err := s.Pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version text PRIMARY KEY,
		applied_at timestamptz NOT NULL DEFAULT now())`)
	if err != nil {
		return err
	}

	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		var exists bool
		if err := s.Pool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)`, name,
		).Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}
		body, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		if err := s.runMigration(ctx, name, string(body)); err != nil {
			return fmt.Errorf("migration %s: %w", name, err)
		}
	}
	return nil
}

func (s *Store) runMigration(ctx context.Context, name, body string) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, body); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO schema_migrations(version) VALUES($1)`, name)
		return err
	})
}
