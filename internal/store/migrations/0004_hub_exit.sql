-- Hub as an internet exit node. The hub appears as a virtual node (is_hub) that
-- clients can be granted, giving them a full tunnel (0.0.0.0/0) that egresses to
-- the internet via the hub's WAN interface (masqueraded).

ALTER TABLE hub ADD COLUMN wan_iface text NOT NULL DEFAULT 'eth0';

ALTER TABLE nodes ADD COLUMN is_hub boolean NOT NULL DEFAULT false;

-- At most one hub node.
CREATE UNIQUE INDEX nodes_single_hub ON nodes ((true)) WHERE is_hub;
