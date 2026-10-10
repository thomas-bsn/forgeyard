-- What happened to the external containers of a node, by container name (docker compose recreates a
-- container under the same name). Only the latest are kept.
CREATE TABLE container_events (
    id       INTEGER PRIMARY KEY,
    node_id  INTEGER NOT NULL REFERENCES nodes (id) ON DELETE CASCADE,
    name     TEXT NOT NULL,
    at       INTEGER NOT NULL,
    kind     TEXT NOT NULL, -- info, success, warning, error
    message  TEXT NOT NULL
);

CREATE INDEX container_events_node_name ON container_events (node_id, name, id);
