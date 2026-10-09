CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

-- A user signs in with exactly one method: a Discord account or a username and password.
CREATE TABLE users (
    id            INTEGER PRIMARY KEY,
    username      TEXT UNIQUE,
    password_hash TEXT,
    discord_id    TEXT UNIQUE,
    display_name  TEXT NOT NULL,
    role          TEXT NOT NULL CHECK (role IN ('superadmin', 'admin', 'user')),
    disabled      INTEGER NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL,
    CHECK (
        (username IS NOT NULL AND password_hash IS NOT NULL AND discord_id IS NULL)
        OR (username IS NULL AND password_hash IS NULL AND discord_id IS NOT NULL)
    )
);

-- Only a SHA-256 of the session token is stored, so a leaked database cannot be used to log in.
CREATE TABLE sessions (
    token_hash TEXT PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL
);

CREATE INDEX sessions_user_id ON sessions (user_id);
