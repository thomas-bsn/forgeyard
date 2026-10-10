-- App logos: "auto" uses the logo of the app's image on Docker Hub, "custom" an uploaded picture,
-- "initial" the app's initial on a colour (logo_color, or one derived from the name when empty).
ALTER TABLE apps ADD COLUMN logo_mode TEXT NOT NULL DEFAULT 'auto' CHECK (logo_mode IN ('auto', 'custom', 'initial'));
ALTER TABLE apps ADD COLUMN logo_color TEXT NOT NULL DEFAULT '';
ALTER TABLE apps ADD COLUMN logo_updated_at INTEGER NOT NULL DEFAULT 0;

CREATE TABLE app_logos (
    app_id       INTEGER PRIMARY KEY REFERENCES apps (id) ON DELETE CASCADE,
    content_type TEXT NOT NULL,
    data         BLOB NOT NULL
);

-- Logos fetched from Docker Hub, by repository (library/nginx), including the ones not found, so Docker
-- Hub is asked again only once in a while.
CREATE TABLE image_logos (
    repo         TEXT PRIMARY KEY,
    found        INTEGER NOT NULL,
    content_type TEXT NOT NULL DEFAULT '',
    data         BLOB,
    fetched_at   INTEGER NOT NULL
);
