-- Reverse enrollment: a node registers itself with the panel and waits for the
-- admin to approve before it gets an IP + is included in the hub config.

ALTER TABLE hub ADD COLUMN enroll_secret text NOT NULL DEFAULT '';

ALTER TABLE nodes
    ADD COLUMN status       text        NOT NULL DEFAULT 'active'
        CHECK (status IN ('pending', 'active', 'rejected')),
    ADD COLUMN enroll_token text,
    ADD COLUMN hostname     text        NOT NULL DEFAULT '',
    ADD COLUMN last_seen    timestamptz;

-- Pending nodes have no allocated IP yet; the private key lives on the node, so
-- the panel only ever stores the public key.
ALTER TABLE nodes ALTER COLUMN address DROP NOT NULL;

CREATE UNIQUE INDEX nodes_enroll_token_key ON nodes (enroll_token)
    WHERE enroll_token IS NOT NULL;
