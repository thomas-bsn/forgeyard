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
UPDATE users SET display_name = ?, email = ? WHERE id = ?;

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
