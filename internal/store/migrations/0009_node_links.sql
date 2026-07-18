-- Node-to-node routing (site-to-site between spokes). A directed link
-- (src -> dst) means: hosts on node src's LAN(s) may initiate to node dst's
-- subnet(s). The return path is handled by conntrack on the hub, so one row
-- grants one-way initiation; a bidirectional link is two rows.
--
-- Enforced (no-NAT routing preserves real source IPs):
--   * both endpoints carry each other's subnets in their hub-peer AllowedIPs
--     (added by RenderNode) so WireGuard accepts the cross-site source and the
--     return route exists;
--   * the hub nft forward chain gets one accept per (src subnet x dst subnet).
-- Overlapping src/dst subnets are rejected by the API (no NAT to disambiguate).
CREATE TABLE node_links (
    src_node_id uuid NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    dst_node_id uuid NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    PRIMARY KEY (src_node_id, dst_node_id),
    CHECK (src_node_id <> dst_node_id)
);
