-- Public profiles: a banner (Discord's, or an uploaded one), what the user shows to other members, and
-- which apps appear on their profile.
ALTER TABLE users ADD COLUMN discord_banner TEXT NOT NULL DEFAULT '';        -- banner hash on Discord's CDN
ALTER TABLE users ADD COLUMN discord_accent INTEGER NOT NULL DEFAULT -1;     -- Discord's profile colour, -1: none
ALTER TABLE users ADD COLUMN banner_updated_at INTEGER NOT NULL DEFAULT 0;   -- 0: no uploaded banner
ALTER TABLE users ADD COLUMN show_apps INTEGER NOT NULL DEFAULT 1;
ALTER TABLE users ADD COLUMN show_email INTEGER NOT NULL DEFAULT 0;

CREATE TABLE banners (
    user_id      INTEGER PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    content_type TEXT NOT NULL,
    data         BLOB NOT NULL
);

-- Whether the app shows on its owner's public profile.
ALTER TABLE apps ADD COLUMN public INTEGER NOT NULL DEFAULT 1;
