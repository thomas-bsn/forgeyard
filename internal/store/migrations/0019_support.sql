-- Help requests: a user writes about anything, one of their apps or the infrastructure, and the admins
-- answer in the same thread.
CREATE TABLE support_tickets (
    id          INTEGER PRIMARY KEY,
    author_id   INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind        TEXT NOT NULL,                                     -- general, app, infra
    app_id      INTEGER REFERENCES apps (id) ON DELETE SET NULL,
    app_name    TEXT NOT NULL DEFAULT '',                          -- kept once the app is deleted
    subject     TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'open',                      -- open, closed
    waiting     INTEGER NOT NULL DEFAULT 1,                        -- 1: the author wrote last, an answer is due
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
);

CREATE INDEX support_tickets_author ON support_tickets (author_id);

CREATE TABLE support_messages (
    id          INTEGER PRIMARY KEY,
    ticket_id   INTEGER NOT NULL REFERENCES support_tickets (id) ON DELETE CASCADE,
    author_id   INTEGER REFERENCES users (id) ON DELETE SET NULL, -- NULL once the account is deleted
    body        TEXT NOT NULL,
    created_at  INTEGER NOT NULL
);

CREATE INDEX support_messages_ticket ON support_messages (ticket_id, id);
