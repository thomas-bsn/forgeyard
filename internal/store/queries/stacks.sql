-- name: CreateStack :one
INSERT INTO stacks (name, owner_id, node_id, compose, env_sealed, registry_sealed, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetStack :one
SELECT * FROM stacks WHERE id = ?;

-- name: ListStacks :many
SELECT sqlc.embed(stacks), users.display_name AS owner_name, nodes.name AS node_name
FROM stacks
JOIN users ON users.id = stacks.owner_id
JOIN nodes ON nodes.id = stacks.node_id
ORDER BY stacks.name;

-- name: ListStacksByOwner :many
SELECT sqlc.embed(stacks), users.display_name AS owner_name, nodes.name AS node_name
FROM stacks
JOIN users ON users.id = stacks.owner_id
JOIN nodes ON nodes.id = stacks.node_id
WHERE stacks.owner_id = ?
ORDER BY stacks.name;

-- name: ListStacksByOwnerID :many
SELECT * FROM stacks WHERE owner_id = ? ORDER BY id;

-- name: UpdateStack :one
UPDATE stacks SET compose = ?, env_sealed = ?, registry_sealed = ?, updated_at = ? WHERE id = ? RETURNING *;

-- name: SetStackVolumeOverrides :exec
UPDATE stacks SET volume_overrides = ?, updated_at = ? WHERE id = ?;

-- name: DeleteStack :exec
DELETE FROM stacks WHERE id = ?;

-- name: ListStackApps :many
SELECT * FROM apps WHERE stack_id = ? ORDER BY id;

-- name: CreateStackApp :one
INSERT INTO apps (name, owner_id, node_id, kind, image, port, env_sealed, memory_mb, stack_id, service, service_spec, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: UpdateStackApp :one
UPDATE apps SET image = ?, port = ?, env_sealed = ?, memory_mb = ?, service_spec = ?, running = 1,
    generation = generation + 1, updated_at = ?
WHERE id = ?
RETURNING *;

-- name: SetStackRunning :exec
UPDATE apps SET running = ?, generation = generation + ?, updated_at = ? WHERE stack_id = ?;

-- name: NameTaken :one
SELECT CAST(EXISTS (SELECT 1 FROM apps a WHERE a.name = ?1) OR EXISTS (SELECT 1 FROM stacks s WHERE s.name = ?1) AS INTEGER) AS taken;

-- name: SetStackLogo :exec
UPDATE stacks SET logo_mode = ?, logo_color = ?, logo_updated_at = ? WHERE id = ?;

-- name: PutStackLogoImage :exec
INSERT INTO stack_logos (stack_id, content_type, data) VALUES (?, ?, ?)
ON CONFLICT (stack_id) DO UPDATE SET content_type = excluded.content_type, data = excluded.data;

-- name: GetStackLogoImage :one
SELECT * FROM stack_logos WHERE stack_id = ?;

-- name: DeleteStackLogoImage :exec
DELETE FROM stack_logos WHERE stack_id = ?;
