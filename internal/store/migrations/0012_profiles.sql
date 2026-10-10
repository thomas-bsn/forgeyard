-- Profiles: Discord's name and avatar are kept apart from what the user chose, so a Discord sign-in
-- refreshes them without overwriting the user's own choices.
ALTER TABLE users ADD COLUMN discord_name TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN discord_avatar TEXT NOT NULL DEFAULT ''; -- avatar hash on Discord's CDN
ALTER TABLE users ADD COLUMN name_from_discord INTEGER NOT NULL DEFAULT 1;
ALTER TABLE users ADD COLUMN bio TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN avatar_updated_at INTEGER NOT NULL DEFAULT 0; -- 0: no uploaded picture

-- A picture the user uploaded, resized by the browser. Only raster formats are accepted (no SVG).
CREATE TABLE avatars (
    user_id      INTEGER PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    content_type TEXT NOT NULL,
    data         BLOB NOT NULL
);

-- Where each session was opened, so users can recognize and sign out their other devices.
ALTER TABLE sessions ADD COLUMN ip TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN user_agent TEXT NOT NULL DEFAULT '';
