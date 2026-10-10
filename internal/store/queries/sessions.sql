-- name: CreateSession :exec
INSERT INTO sessions (token_hash, user_id, created_at, expires_at, ip, user_agent)
VALUES (?, ?, ?, ?, ?, ?);

-- name: ListUserSessions :many
SELECT * FROM sessions WHERE user_id = ? AND expires_at > ? ORDER BY created_at DESC;

-- name: DeleteOtherSessions :exec
DELETE FROM sessions WHERE user_id = ? AND token_hash != ?;

-- name: DeleteUserSession :exec
DELETE FROM sessions WHERE user_id = ? AND token_hash = ?;

-- name: GetSessionUser :one
SELECT users.*
FROM sessions
JOIN users ON users.id = sessions.user_id
WHERE sessions.token_hash = ? AND sessions.expires_at > ? AND users.disabled = 0;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE token_hash = ?;

-- name: DeleteExpiredSessions :exec
DELETE FROM sessions WHERE expires_at <= ?;

-- name: DeleteUserSessions :exec
DELETE FROM sessions WHERE user_id = ?;
