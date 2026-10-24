-- name: CreateBindRequest :one
INSERT INTO bind_requests(author_id, node_id, config, env_sealed, created_at)
VALUES (?, ?, ?, ?, ?) RETURNING *;

-- name: GetBindRequest :one
SELECT * FROM bind_requests WHERE id = ?;

-- name: ListOwnBindRequests :many
SELECT * FROM bind_requests WHERE author_id = ? ORDER BY id DESC;

-- name: ListBindRequests :many
SELECT * FROM bind_requests ORDER BY id DESC;

-- name: DecideBindRequest :execrows
UPDATE bind_requests SET status = ?, reason = ?, decided_at = ?, decided_by = ?
WHERE id = ? AND status = 'pending';
