-- A backup policy saves a named volume of a stack on a schedule to the restic repository of the instance
-- (an S3-compatible storage). A volume a service of type postgres uses is saved with pg_dump.
CREATE TABLE backup_policies (
    id             INTEGER PRIMARY KEY,
    stack_id       INTEGER NOT NULL REFERENCES stacks (id) ON DELETE CASCADE,
    volume         TEXT NOT NULL, -- its name in the compose file
    schedule       TEXT NOT NULL CHECK (schedule IN ('hourly', 'daily')),
    hour           INTEGER NOT NULL DEFAULT 3, -- daily: the hour it runs at, server time
    keep           INTEGER NOT NULL DEFAULT 7, -- the last snapshots kept
    enabled        INTEGER NOT NULL DEFAULT 1,
    last_run_at    INTEGER NOT NULL DEFAULT 0,
    last_status    TEXT NOT NULL DEFAULT '', -- running, ok, error
    last_error     TEXT NOT NULL DEFAULT '',
    last_snapshot  TEXT NOT NULL DEFAULT '',
    created_at     INTEGER NOT NULL,
    UNIQUE (stack_id, volume)
);

-- A restore puts a snapshot into a new volume of the node; the volume in use is never overwritten.
CREATE TABLE backup_restores (
    id          INTEGER PRIMARY KEY,
    policy_id   INTEGER NOT NULL REFERENCES backup_policies (id) ON DELETE CASCADE,
    snapshot    TEXT NOT NULL,
    volume      TEXT NOT NULL, -- the new Docker volume
    status      TEXT NOT NULL DEFAULT 'running', -- running, ok, error
    error       TEXT NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL
);
