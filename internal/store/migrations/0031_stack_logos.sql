-- Project logos: "auto" shows the logos of the project's first services, "custom" an uploaded picture,
-- "initial" the project's initial on a colour (logo_color, or one derived from the name when empty).
ALTER TABLE stacks ADD COLUMN logo_mode TEXT NOT NULL DEFAULT 'auto' CHECK (logo_mode IN ('auto', 'custom', 'initial'));
ALTER TABLE stacks ADD COLUMN logo_color TEXT NOT NULL DEFAULT '';
ALTER TABLE stacks ADD COLUMN logo_updated_at INTEGER NOT NULL DEFAULT 0;

CREATE TABLE stack_logos (
    stack_id     INTEGER PRIMARY KEY REFERENCES stacks (id) ON DELETE CASCADE,
    content_type TEXT NOT NULL,
    data         BLOB NOT NULL
);
