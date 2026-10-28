-- People other than the owner who may use an app or a project. The owner stays in apps.owner_id and
-- stacks.owner_id; only the roles below are stored here. An app of a project also gets the roles held on
-- the project, which is worked out when it is read, not copied.
CREATE TABLE resource_members (
    resource_kind TEXT    NOT NULL CHECK (resource_kind IN ('app', 'stack')),
    resource_id   INTEGER NOT NULL,
    user_id       INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role          TEXT    NOT NULL CHECK (role IN ('viewer', 'contributor')),
    added_by      INTEGER REFERENCES users (id) ON DELETE SET NULL,
    created_at    INTEGER NOT NULL,
    PRIMARY KEY (resource_kind, resource_id, user_id)
);

CREATE INDEX resource_members_user ON resource_members (user_id);

-- resource_id points at apps or stacks depending on the kind, so no foreign key can drop the rows of a
-- deleted resource: a trigger does, whichever code path deletes it.
CREATE TRIGGER apps_delete_members AFTER DELETE ON apps
BEGIN
    DELETE FROM resource_members WHERE resource_kind = 'app' AND resource_id = OLD.id;
END;

CREATE TRIGGER stacks_delete_members AFTER DELETE ON stacks
BEGIN
    DELETE FROM resource_members WHERE resource_kind = 'stack' AND resource_id = OLD.id;
END;

-- Who did it, for the events a person caused.
ALTER TABLE app_events ADD COLUMN actor_id INTEGER REFERENCES users (id) ON DELETE SET NULL;
