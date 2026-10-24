-- A request stores an immutable configuration; no app or name is reserved before approval.
CREATE TABLE bind_requests (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    author_id INTEGER NOT NULL REFERENCES users(id),
    node_id INTEGER NOT NULL REFERENCES nodes(id),
    config TEXT NOT NULL,
    env_sealed TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending',
    reason TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    decided_at INTEGER NOT NULL DEFAULT 0,
    decided_by INTEGER REFERENCES users(id)
);
CREATE INDEX bind_requests_author ON bind_requests(author_id, created_at);
