-- One-time sign-in links created from the server's command line, for an admin locked out of the UI.
CREATE TABLE login_links (
    token_hash TEXT PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    expires_at INTEGER NOT NULL
);
