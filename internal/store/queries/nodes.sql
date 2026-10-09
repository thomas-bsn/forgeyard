-- name: CreateNode :one
INSERT INTO nodes (name, status, join_token_hash, join_expires_at, created_at)
VALUES (?, 'pending', ?, ?, ?)
RETURNING *;

-- name: ListNodes :many
SELECT * FROM nodes ORDER BY name;

-- name: GetNode :one
SELECT * FROM nodes WHERE id = ?;

-- name: GetPendingNodeByJoinToken :one
SELECT * FROM nodes WHERE join_token_hash = ? AND status = 'pending' AND join_expires_at > ?;

-- name: ActivateNode :exec
UPDATE nodes SET status = 'active', cert_serial = ?, join_token_hash = NULL, join_expires_at = NULL
WHERE id = ? AND status = 'pending';

-- name: ResetNodeJoinToken :exec
UPDATE nodes SET join_token_hash = ?, join_expires_at = ? WHERE id = ? AND status = 'pending';

-- name: UpdateNodeInfo :exec
UPDATE nodes
SET hostname = ?, os = ?, arch = ?, cpus = ?, memory_bytes = ?, disk_bytes = ?,
    docker_version = ?, agent_version = ?, last_seen_at = ?
WHERE id = ?;

-- name: TouchNode :exec
UPDATE nodes SET last_seen_at = ? WHERE id = ?;

-- name: DeleteNode :exec
DELETE FROM nodes WHERE id = ?;
