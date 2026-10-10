-- name: AddAppEvent :exec
INSERT INTO app_events (app_id, at, kind, message) VALUES (?, ?, ?, ?);

-- name: PruneAppEvents :exec
DELETE FROM app_events
WHERE app_events.app_id = ?1
  AND app_events.id NOT IN (SELECT e.id FROM app_events e WHERE e.app_id = ?1 ORDER BY e.id DESC LIMIT 200);

-- name: ListAppEvents :many
SELECT * FROM app_events WHERE app_id = ? ORDER BY id DESC LIMIT ?;
