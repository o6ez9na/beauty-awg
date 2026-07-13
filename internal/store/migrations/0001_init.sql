-- Singleton hub config (the VPS relay). Exactly one row, id = 1.
CREATE TABLE hub (
    id           smallint PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    endpoint     text     NOT NULL,           -- "IP:port" clients/nodes dial
    listen_port  integer  NOT NULL,
    address      inet     NOT NULL,           -- hub tunnel IP, e.g. 10.8.0.1
    pool_cidr    cidr     NOT NULL,           -- e.g. 10.8.0.0/24
    private_key  text     NOT NULL,
    public_key   text     NOT NULL,
    dns          text     NOT NULL DEFAULT '',-- global default DNS (may be empty)
    -- shared AmneziaWG obfuscation params (identical for all peers)
    jc   integer NOT NULL,
    jmin integer NOT NULL,
    jmax integer NOT NULL,
    s1   integer NOT NULL,
    s2   integer NOT NULL,
    h1   bigint  NOT NULL,
    h2   bigint  NOT NULL,
    h3   bigint  NOT NULL,
    h4   bigint  NOT NULL
);

-- Panel operators.
CREATE TABLE admins (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    username      text NOT NULL UNIQUE,
    password_hash text NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);

-- Home servers behind CGNAT.
CREATE TABLE nodes (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name        text NOT NULL UNIQUE,
    address     inet NOT NULL UNIQUE,   -- node tunnel IP, allocated from pool
    lan_iface   text NOT NULL,          -- LAN-facing iface for masquerade
    private_key text NOT NULL,
    public_key  text NOT NULL,
    preshared   text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- A node exposes one or more LAN subnets.
CREATE TABLE node_subnets (
    node_id uuid NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    subnet  cidr NOT NULL,
    PRIMARY KEY (node_id, subnet)
);

-- VPN users.
CREATE TABLE clients (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name        text NOT NULL,
    address     inet NOT NULL UNIQUE,   -- /32 tunnel IP, allocated from pool
    private_key text NOT NULL,
    public_key  text NOT NULL,
    preshared   text NOT NULL DEFAULT '',
    dns         text NOT NULL DEFAULT '', -- per-client override; empty = hub.dns
    enabled     boolean NOT NULL DEFAULT true,
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- Which client may reach which node. Presence = access granted.
CREATE TABLE grants (
    client_id  uuid NOT NULL REFERENCES clients(id) ON DELETE CASCADE,
    node_id    uuid NOT NULL REFERENCES nodes(id)   ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (client_id, node_id)
);
