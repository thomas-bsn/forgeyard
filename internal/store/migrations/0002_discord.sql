ALTER TABLE users ADD COLUMN email TEXT;

-- A Discord account that signed in without having a Forgeyard account.
-- Accepting it creates the user and deletes the request; a refused request is kept so the person sees the refusal.
CREATE TABLE account_requests (
    id           INTEGER PRIMARY KEY,
    discord_id   TEXT NOT NULL UNIQUE,
    username     TEXT NOT NULL,
    display_name TEXT NOT NULL,
    email        TEXT,
    avatar       TEXT,
    status       TEXT NOT NULL CHECK (status IN ('pending', 'refused')),
    reason       TEXT,
    created_at   INTEGER NOT NULL,
    decided_at   INTEGER
);
