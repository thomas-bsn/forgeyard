-- How a node receives web traffic for its apps, and the IP its app domains point to.
ALTER TABLE nodes ADD COLUMN public_ip TEXT NOT NULL DEFAULT '';
-- "traefik": Forgeyard's Traefik on 80/443 with automatic HTTPS. "proxy": Traefik in plain HTTP on
-- ingress_http_port, behind a proxy the user runs (Caddy…).
ALTER TABLE nodes ADD COLUMN ingress_mode TEXT NOT NULL DEFAULT 'traefik' CHECK (ingress_mode IN ('traefik', 'proxy'));
ALTER TABLE nodes ADD COLUMN ingress_http_port INTEGER NOT NULL DEFAULT 8090;

CREATE TABLE apps (
    id           INTEGER PRIMARY KEY,
    name         TEXT NOT NULL UNIQUE, -- also its subdomain
    owner_id     INTEGER NOT NULL REFERENCES users (id),
    node_id      INTEGER NOT NULL REFERENCES nodes (id),
    image        TEXT NOT NULL,
    port         INTEGER NOT NULL,
    env_sealed   TEXT NOT NULL DEFAULT '', -- encrypted JSON object
    running      INTEGER NOT NULL DEFAULT 1,
    memory_mb    INTEGER NOT NULL DEFAULT 512,
    generation   INTEGER NOT NULL DEFAULT 1,
    dns_name     TEXT NOT NULL DEFAULT '', -- record created at the DNS provider, to delete it with the app
    created_at   INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL
);

CREATE INDEX apps_node_id ON apps (node_id);
CREATE INDEX apps_owner_id ON apps (owner_id);
