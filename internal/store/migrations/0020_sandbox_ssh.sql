-- kind: "web" for an app served at its subdomain, "sandbox" for a Linux box people reach over SSH,
-- without a web address.
ALTER TABLE apps ADD COLUMN kind TEXT NOT NULL DEFAULT 'web';

-- Public keys people sign in to the SSH gateway with. A key belongs to one account.
CREATE TABLE ssh_keys (
    id            INTEGER PRIMARY KEY,
    user_id       INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    public_key    TEXT NOT NULL,        -- authorized_keys form, without the comment
    fingerprint   TEXT NOT NULL UNIQUE, -- SHA256:…
    created_at    INTEGER NOT NULL,
    last_used_at  INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX ssh_keys_user ON ssh_keys (user_id);
