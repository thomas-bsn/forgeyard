-- name: CreateLoginLink :exec
INSERT INTO login_links (token_hash, user_id, expires_at) VALUES (?, ?, ?);

-- name: ConsumeLoginLink :one
DELETE FROM login_links WHERE token_hash = ? AND expires_at > ? RETURNING user_id;

-- name: DeleteExpiredLoginLinks :exec
DELETE FROM login_links WHERE expires_at <= ?;
