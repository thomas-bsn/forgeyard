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

-- name: ClaimBindRequest :execrows
UPDATE bind_requests SET status = 'approving', reason = '', decided_at = ?, decided_by = ?
WHERE id = ? AND (status IN ('pending', 'failed') OR (status = 'approving' AND decided_at < ?));

-- name: FailBindRequest :exec
UPDATE bind_requests SET status = 'failed', reason = ? WHERE id = ? AND status = 'approving';

-- name: LinkBindRequestApp :exec
UPDATE bind_requests SET app_id = ?, approved_mounts = ? WHERE id = ? AND status = 'approving';

-- name: LinkBindRequestStack :exec
UPDATE bind_requests SET stack_id = ?, approved_mounts = ? WHERE id = ? AND status = 'approving';

-- name: CompleteBindRequest :execrows
UPDATE bind_requests SET status = 'created', reason = '' WHERE id = ? AND status = 'approving';

-- name: SetAppBindMounts :exec
UPDATE apps SET bind_mounts = ?, bind_authorized = ? WHERE id = ?;

-- name: RevokeAppBindMounts :one
UPDATE apps SET bind_authorized = 0, running = 0, updated_at = ? WHERE id = ? RETURNING *;
