-- What happened to an app (deployments, crashes, changes), shown on its page. Only the latest are kept.
CREATE TABLE app_events (
    id         INTEGER PRIMARY KEY,
    app_id     INTEGER NOT NULL REFERENCES apps (id) ON DELETE CASCADE,
    at         INTEGER NOT NULL,
    kind       TEXT NOT NULL, -- info, success, warning, error
    message    TEXT NOT NULL
);

CREATE INDEX app_events_app_id ON app_events (app_id, id);

-- An admin can suspend a user's apps: they are stopped and cannot be started again until lifted.
ALTER TABLE users ADD COLUMN apps_suspended INTEGER NOT NULL DEFAULT 0;
