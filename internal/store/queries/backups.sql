-- name: ListBackupPolicies :many
SELECT * FROM backup_policies ORDER BY id;

-- name: ListStackBackupPolicies :many
SELECT * FROM backup_policies WHERE stack_id = ? ORDER BY id;

-- name: GetBackupPolicy :one
SELECT * FROM backup_policies WHERE id = ?;

-- name: UpsertBackupPolicy :one
INSERT INTO backup_policies (stack_id, volume, schedule, hour, keep, enabled, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (stack_id, volume) DO UPDATE SET schedule = excluded.schedule, hour = excluded.hour,
    keep = excluded.keep, enabled = excluded.enabled
RETURNING *;

-- name: DeleteBackupPolicy :exec
DELETE FROM backup_policies WHERE id = ?;

-- name: DeleteStackBackupPolicies :exec
DELETE FROM backup_policies WHERE stack_id = ?;

-- name: StartBackupRun :exec
UPDATE backup_policies SET last_run_at = ?, last_status = 'running', last_error = '' WHERE id = ?;

-- name: FinishBackupRun :exec
UPDATE backup_policies SET last_status = ?, last_error = ?, last_snapshot = ? WHERE id = ?;

-- name: CreateBackupRestore :one
INSERT INTO backup_restores (policy_id, snapshot, volume, created_at) VALUES (?, ?, ?, ?) RETURNING *;

-- name: FinishBackupRestore :exec
UPDATE backup_restores SET status = ?, error = ? WHERE id = ?;

-- name: ListBackupRestores :many
SELECT * FROM backup_restores WHERE policy_id = ? ORDER BY id DESC LIMIT 10;

-- name: GetBackupRestore :one
SELECT * FROM backup_restores WHERE id = ?;
