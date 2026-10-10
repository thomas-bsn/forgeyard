-- name: CreateApp :one
INSERT INTO apps (name, owner_id, node_id, image, port, env_sealed, memory_mb, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetApp :one
SELECT * FROM apps WHERE id = ?;

-- name: ListApps :many
SELECT apps.*, users.display_name AS owner_name, users.apps_suspended AS owner_suspended, nodes.name AS node_name
FROM apps
JOIN users ON users.id = apps.owner_id
JOIN nodes ON nodes.id = apps.node_id
ORDER BY apps.name;

-- name: ListAppsByOwner :many
SELECT apps.*, users.display_name AS owner_name, users.apps_suspended AS owner_suspended, nodes.name AS node_name
FROM apps
JOIN users ON users.id = apps.owner_id
JOIN nodes ON nodes.id = apps.node_id
WHERE apps.owner_id = ?
ORDER BY apps.name;

-- name: ListAppsByNode :many
SELECT * FROM apps WHERE node_id = ? ORDER BY id;

-- name: CountAppsByNode :many
SELECT node_id, COUNT(*) AS count FROM apps GROUP BY node_id;

-- name: UpdateAppConfig :one
UPDATE apps SET image = ?, port = ?, env_sealed = ?, memory_mb = ?, generation = generation + 1, updated_at = ?
WHERE id = ?
RETURNING *;

-- name: SetAppRunning :one
UPDATE apps SET running = ?, generation = generation + ?, updated_at = ? WHERE id = ? RETURNING *;

-- name: SetAppDNSName :exec
UPDATE apps SET dns_name = ? WHERE id = ?;

-- name: DeleteApp :exec
DELETE FROM apps WHERE id = ?;

-- name: UpdateNodeIngress :one
UPDATE nodes SET public_ip = ?, ingress_mode = ?, ingress_http_port = ? WHERE id = ? RETURNING *;

-- name: AppExistsByName :one
SELECT EXISTS (SELECT 1 FROM apps WHERE name = ?);

-- name: ListAppsByOwnerID :many
SELECT * FROM apps WHERE owner_id = ? ORDER BY id;

-- name: StopAppsByOwner :exec
UPDATE apps SET running = 0, updated_at = ? WHERE owner_id = ?;

-- name: SetAppPublic :exec
UPDATE apps SET public = ? WHERE id = ?;

-- name: ListPublicAppsByOwner :many
SELECT * FROM apps WHERE owner_id = ? AND public = 1 ORDER BY name;

-- name: SetAppLogo :exec
UPDATE apps SET logo_mode = ?, logo_color = ?, logo_updated_at = ? WHERE id = ?;

-- name: PutAppLogoImage :exec
INSERT INTO app_logos (app_id, content_type, data) VALUES (?, ?, ?)
ON CONFLICT (app_id) DO UPDATE SET content_type = excluded.content_type, data = excluded.data;

-- name: GetAppLogoImage :one
SELECT * FROM app_logos WHERE app_id = ?;

-- name: DeleteAppLogoImage :exec
DELETE FROM app_logos WHERE app_id = ?;

-- name: GetImageLogo :one
SELECT * FROM image_logos WHERE repo = ?;

-- name: PutImageLogo :exec
INSERT INTO image_logos (repo, found, content_type, data, fetched_at) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (repo) DO UPDATE SET found = excluded.found, content_type = excluded.content_type,
    data = excluded.data, fetched_at = excluded.fetched_at;
