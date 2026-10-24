-- name: CreateBindRequest :one
INSERT INTO bind_requests(author_id, node_id, config, env_sealed, created_at, change_resource_type, change_resource_id)
VALUES (?, ?, ?, ?, ?, ?, ?) RETURNING *;

-- name: GetBindRequest :one
SELECT * FROM bind_requests WHERE id = ?;

-- name: ListOwnBindRequests :many
SELECT * FROM bind_requests WHERE author_id = ? ORDER BY id DESC;

-- name: ListBindRequests :many
SELECT * FROM bind_requests ORDER BY id DESC;

-- name: DecideBindRequest :execrows
UPDATE bind_requests SET status = ?, reason = ?, decided_at = ?, decided_by = ?
WHERE id = ? AND status = 'pending';

-- name: CancelFailedBindChange :execrows
UPDATE bind_requests SET status = 'cancelled', reason = '', decided_at = ?, decided_by = ?
WHERE id = ? AND author_id = ? AND change_resource_type != '' AND status = 'failed';

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

-- name: ApplyBindChangeApp :execrows
UPDATE apps SET bind_mounts = ?, bind_authorized = 1, node_id = ?, running = ?, generation = generation + 1, updated_at = ?
WHERE id = ? AND bind_mounts = ? AND node_id = ? AND bind_authorized = 1;

-- name: HasPendingBindChange :one
SELECT CAST(EXISTS(SELECT 1 FROM bind_requests WHERE change_resource_type=? AND change_resource_id=? AND status NOT IN ('cancelled','refused','created')) AS INTEGER);

-- name: ApplyBindChangeAppAfterRevocation :execrows
UPDATE apps SET bind_mounts = ?, bind_authorized = 1, node_id = ?, running = ?, generation = generation + 1, updated_at = ?
WHERE id = ? AND bind_mounts = ? AND node_id = ? AND bind_authorized = 0;

-- name: ApplyBindChangeStackService :execrows
UPDATE apps SET bind_mounts = ?, bind_authorized = 1, node_id = ?, running = 0, generation = generation + 1, updated_at = ?
WHERE id = ? AND bind_mounts = ? AND node_id = ? AND bind_authorized = 1;

-- name: ApplyBindChangeStackServiceAfterRevocation :execrows
UPDATE apps SET bind_mounts = ?, bind_authorized = 1, node_id = ?, running = 0, generation = generation + 1, updated_at = ?
WHERE id = ? AND bind_mounts = ? AND node_id = ? AND bind_authorized = 0;

-- name: ApplyBindChangeStackServiceWithoutAuthorization :execrows
UPDATE apps SET bind_mounts = ?, node_id = ?, running = 0, generation = generation + 1, updated_at = ?
WHERE id = ? AND bind_mounts = ? AND node_id = ? AND bind_authorized = 0;

-- name: RevokeAppBindMounts :one
UPDATE apps SET bind_authorized = 0, running = 0, updated_at = ? WHERE id = ? RETURNING *;
