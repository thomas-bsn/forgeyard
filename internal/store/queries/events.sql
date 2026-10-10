-- name: AddAppEvent :exec
INSERT INTO app_events (app_id, at, kind, message) VALUES (?, ?, ?, ?);

-- name: PruneAppEvents :exec
DELETE FROM app_events
WHERE app_events.app_id = ?1
  AND app_events.id NOT IN (SELECT e.id FROM app_events e WHERE e.app_id = ?1 ORDER BY e.id DESC LIMIT 200);

-- name: ListAppEvents :many
SELECT * FROM app_events WHERE app_id = ? ORDER BY id DESC LIMIT ?;

-- name: AddContainerEvent :exec
INSERT INTO container_events (node_id, name, at, kind, message) VALUES (?, ?, ?, ?, ?);

-- name: PruneContainerEvents :exec
DELETE FROM container_events
WHERE container_events.node_id = ?1 AND container_events.name = ?2
  AND container_events.id NOT IN (
    SELECT e.id FROM container_events e WHERE e.node_id = ?1 AND e.name = ?2 ORDER BY e.id DESC LIMIT 200
  );

-- name: ListContainerEvents :many
SELECT * FROM container_events WHERE node_id = ? AND name = ? ORDER BY id DESC LIMIT ?;
