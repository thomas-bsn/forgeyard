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
