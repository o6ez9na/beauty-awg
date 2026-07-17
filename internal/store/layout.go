package store

import "context"

// GetGraphLayout returns the saved node positions as a JSON object string
// ({"c:uuid":{"x":..,"y":..}, ...}).
func (s *Store) GetGraphLayout(ctx context.Context) (string, error) {
	var positions string
	err := s.Pool.QueryRow(ctx, `SELECT positions::text FROM graph_layout WHERE id = 1`).Scan(&positions)
	if err != nil {
		return "{}", err
	}
	return positions, nil
}

// SetGraphLayout stores the node positions (a JSON object).
func (s *Store) SetGraphLayout(ctx context.Context, positions string) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO graph_layout (id, positions) VALUES (1, $1::jsonb)
		ON CONFLICT (id) DO UPDATE SET positions = EXCLUDED.positions`, positions)
	return err
}
