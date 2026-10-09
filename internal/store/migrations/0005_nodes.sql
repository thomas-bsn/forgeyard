-- A machine that runs containers. Created by an admin as "pending" with a one-time join token; the agent
-- exchanges the token for a client certificate and the node becomes "active".
CREATE TABLE nodes (
    id                   INTEGER PRIMARY KEY,
    name                 TEXT NOT NULL UNIQUE,
    status               TEXT NOT NULL CHECK (status IN ('pending', 'active')),
    join_token_hash      TEXT UNIQUE,
    join_expires_at      INTEGER,
    cert_serial          TEXT,
    hostname             TEXT NOT NULL DEFAULT '',
    os                   TEXT NOT NULL DEFAULT '',
    arch                 TEXT NOT NULL DEFAULT '',
    cpus                 INTEGER NOT NULL DEFAULT 0,
    memory_bytes         INTEGER NOT NULL DEFAULT 0,
    disk_bytes           INTEGER NOT NULL DEFAULT 0,
    docker_version       TEXT NOT NULL DEFAULT '',
    agent_version        TEXT NOT NULL DEFAULT '',
    last_seen_at         INTEGER,
    created_at           INTEGER NOT NULL
);
