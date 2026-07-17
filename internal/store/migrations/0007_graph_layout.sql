-- Saved positions of graph nodes (admin layout), keyed by the graph's node id
-- ("c:<clientId>" / "n:<nodeId>"). One row for the single admin view.
CREATE TABLE graph_layout (
    id        smallint PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    positions jsonb NOT NULL DEFAULT '{}'
);
INSERT INTO graph_layout (id) VALUES (1) ON CONFLICT DO NOTHING;
