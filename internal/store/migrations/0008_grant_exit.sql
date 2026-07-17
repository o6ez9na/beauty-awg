-- Per-grant internet exit: when true, this client routes ALL its traffic
-- (0.0.0.0/0) out through this node's home internet connection, so its public
-- IP becomes the node's. Only ONE node can be the active exit at a time
-- (WireGuard cryptokey routing is dst-based and global per interface); the API
-- enforces that invariant.
ALTER TABLE grants ADD COLUMN exit boolean NOT NULL DEFAULT false;
