-- The instance's own icon, shown in place of Forgeyard's logo and as the favicon.
CREATE TABLE instance_icon (
    id           INTEGER PRIMARY KEY CHECK (id = 1),
    content_type TEXT NOT NULL,
    data         BLOB NOT NULL,
    updated_at   INTEGER NOT NULL
);
