-- A stack is a docker-compose.yml and its variables, run on one node. Each of its services is an app of
-- the stack (apps.stack_id), named after the stack: the exposed service takes the stack's name, the others
-- <stack>-<service>.
-- Ids are never reused: a deleted stack's volumes (kept unless asked) and snapshots carry its id.
CREATE TABLE stacks (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    name             TEXT NOT NULL UNIQUE,
    owner_id         INTEGER NOT NULL REFERENCES users (id),
    node_id          INTEGER NOT NULL REFERENCES nodes (id),
    compose          TEXT NOT NULL,
    env_sealed       TEXT NOT NULL DEFAULT '', -- encrypted JSON object: the variables ${VAR} takes
    -- volume_overrides: a named volume of the compose file → the Docker volume used instead, after a
    -- restore into a new volume was put in its place (JSON object).
    volume_overrides TEXT NOT NULL DEFAULT '{}',
    created_at       INTEGER NOT NULL,
    updated_at       INTEGER NOT NULL
);

CREATE INDEX stacks_owner_id ON stacks (owner_id);

ALTER TABLE apps ADD COLUMN stack_id INTEGER NOT NULL DEFAULT 0;
ALTER TABLE apps ADD COLUMN service TEXT NOT NULL DEFAULT ''; -- its name in the compose file
-- service_spec: how the service runs, from the compose file (JSON: command, entrypoint, healthcheck,
-- restart, depends_on, volumes, job, backup).
ALTER TABLE apps ADD COLUMN service_spec TEXT NOT NULL DEFAULT '';

CREATE INDEX apps_stack_id ON apps (stack_id);
