-- name: ListSSHKeys :many
SELECT * FROM ssh_keys WHERE user_id = ? ORDER BY id;

-- name: CreateSSHKey :one
INSERT INTO ssh_keys (user_id, name, public_key, fingerprint, created_at) VALUES (?, ?, ?, ?, ?) RETURNING *;

-- name: DeleteSSHKey :execrows
DELETE FROM ssh_keys WHERE id = ? AND user_id = ?;

-- name: GetSSHKeyByFingerprint :one
SELECT * FROM ssh_keys WHERE fingerprint = ?;

-- name: TouchSSHKey :exec
UPDATE ssh_keys SET last_used_at = ? WHERE id = ?;
