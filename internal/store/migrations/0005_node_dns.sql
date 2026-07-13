-- Per-node DNS server. When a client is granted a node that has a DNS set, the
-- hub force-redirects that client's port-53 traffic to the node's DNS (DNAT), so
-- internal domains resolve regardless of what the client honours. The client's
-- config DNS is also set to this value.
ALTER TABLE nodes ADD COLUMN dns text NOT NULL DEFAULT '';
