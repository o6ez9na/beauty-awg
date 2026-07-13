-- Per-grant access levels: restrict a client's access to a node to specific
-- destination subnets/hosts and ports. A grant with NO rules means full access
-- to all of the node's subnets (backward compatible).

CREATE TABLE grant_rules (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    client_id  uuid NOT NULL,
    node_id    uuid NOT NULL,
    dest       cidr NOT NULL,                       -- subnet or host (/32)
    proto      text NOT NULL DEFAULT 'any'
        CHECK (proto IN ('any', 'tcp', 'udp')),
    port_from  integer,                             -- NULL = all ports
    port_to    integer,
    FOREIGN KEY (client_id, node_id)
        REFERENCES grants(client_id, node_id) ON DELETE CASCADE,
    CHECK (port_from IS NULL OR (port_from BETWEEN 1 AND 65535)),
    CHECK (port_to   IS NULL OR (port_to   BETWEEN 1 AND 65535)),
    CHECK (port_to IS NULL OR port_from IS NULL OR port_to >= port_from)
);

CREATE INDEX grant_rules_grant_idx ON grant_rules (client_id, node_id);
