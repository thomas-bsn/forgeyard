-- name: CreateLocalUser :one
INSERT INTO users (username, password_hash, display_name, role, created_at)
VALUES (?, ?, ?, ?, ?)
RETURNING *;

-- name: GetUserByUsername :one
SELECT * FROM users WHERE username = ?;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = ?;

-- name: CountUsers :one
SELECT COUNT(*) FROM users;

-- name: CreateDiscordUser :one
INSERT INTO users (discord_id, display_name, email, role, created_at)
VALUES (?, ?, ?, ?, ?)
RETURNING *;

-- name: GetUserByDiscordID :one
SELECT * FROM users WHERE discord_id = ?;

-- name: UpdateDiscordProfile :exec
-- Refreshes what Discord says about the user: the display name only if the user follows Discord's, the
-- email only if the user has none.
UPDATE users
SET discord_name = sqlc.arg(discord_name),
    discord_avatar = sqlc.arg(discord_avatar),
    discord_banner = sqlc.arg(discord_banner),
    discord_accent = sqlc.arg(discord_accent),
    display_name = CASE WHEN name_from_discord = 1 THEN sqlc.arg(discord_name) ELSE display_name END,
    email = CASE WHEN email IS NULL OR email = '' THEN sqlc.narg(email) ELSE email END
WHERE id = sqlc.arg(id);

-- name: UpdateProfile :exec
UPDATE users SET display_name = ?, name_from_discord = ?, bio = ?, email = ?, show_apps = ?, show_email = ? WHERE id = ?;

-- name: SetBanner :exec
INSERT INTO banners (user_id, content_type, data) VALUES (?, ?, ?)
ON CONFLICT (user_id) DO UPDATE SET content_type = excluded.content_type, data = excluded.data;

-- name: GetBanner :one
SELECT * FROM banners WHERE user_id = ?;

-- name: DeleteBanner :exec
DELETE FROM banners WHERE user_id = ?;

-- name: SetBannerUpdatedAt :exec
UPDATE users SET banner_updated_at = ? WHERE id = ?;

-- name: ListMembers :many
SELECT users.*,
       (SELECT COUNT(*) FROM apps WHERE apps.owner_id = users.id AND apps.public = 1) AS public_apps
FROM users
WHERE users.disabled = 0
ORDER BY users.display_name COLLATE NOCASE;

-- name: ListMemberActivity :many
SELECT app_events.*, apps.name AS app_name
FROM app_events
JOIN apps ON apps.id = app_events.app_id
WHERE apps.owner_id = ? AND apps.public = 1
ORDER BY app_events.id DESC
LIMIT ?;

-- name: SetPasswordHash :exec
UPDATE users SET password_hash = ? WHERE id = ?;

-- name: SetAvatar :exec
INSERT INTO avatars (user_id, content_type, data) VALUES (?, ?, ?)
ON CONFLICT (user_id) DO UPDATE SET content_type = excluded.content_type, data = excluded.data;

-- name: GetAvatar :one
SELECT * FROM avatars WHERE user_id = ?;

-- name: DeleteAvatar :exec
DELETE FROM avatars WHERE user_id = ?;

-- name: SetAvatarUpdatedAt :exec
UPDATE users SET avatar_updated_at = ? WHERE id = ?;

-- name: GetSuperadmin :one
SELECT * FROM users WHERE role = 'superadmin' ORDER BY id LIMIT 1;

-- name: ListUsers :many
SELECT users.*, (SELECT COUNT(*) FROM apps WHERE apps.owner_id = users.id) AS app_count
FROM users
ORDER BY users.role = 'superadmin' DESC, users.display_name;

-- name: SetUserRole :exec
UPDATE users SET role = ? WHERE id = ?;

-- name: SetUserDisabled :exec
UPDATE users SET disabled = ? WHERE id = ?;

-- name: SetUserAppsSuspended :exec
UPDATE users SET apps_suspended = ? WHERE id = ?;

-- name: DeleteUser :exec
DELETE FROM users WHERE id = ?;
