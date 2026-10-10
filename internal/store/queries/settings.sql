-- name: GetSetting :one
SELECT value FROM settings WHERE key = ?;

-- name: SetSetting :exec
INSERT INTO settings (key, value) VALUES (?, ?)
ON CONFLICT (key) DO UPDATE SET value = excluded.value;

-- name: DeleteSetting :exec
DELETE FROM settings WHERE key = ?;

-- name: GetInstanceIcon :one
SELECT * FROM instance_icon WHERE id = 1;

-- name: PutInstanceIcon :exec
INSERT INTO instance_icon (id, content_type, data, updated_at) VALUES (1, ?, ?, ?)
ON CONFLICT (id) DO UPDATE SET content_type = excluded.content_type, data = excluded.data, updated_at = excluded.updated_at;

-- name: DeleteInstanceIcon :exec
DELETE FROM instance_icon WHERE id = 1;
