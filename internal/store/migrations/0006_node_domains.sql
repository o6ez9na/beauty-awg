-- Local domains a node's DNS server is authoritative for. The hub resolver
-- forwards queries for these domains to the node's DNS (split-horizon).
CREATE TABLE node_domains (
    node_id uuid NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    domain  text NOT NULL,
    PRIMARY KEY (node_id, domain)
);
