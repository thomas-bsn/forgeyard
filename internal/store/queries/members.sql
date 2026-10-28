-- name: SetResourceMember :exec
INSERT INTO resource_members (resource_kind, resource_id, user_id, role, added_by, created_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (resource_kind, resource_id, user_id) DO UPDATE SET role = excluded.role;

-- name: RemoveResourceMember :execrows
DELETE FROM resource_members WHERE resource_kind = ? AND resource_id = ? AND user_id = ?;

-- name: GetResourceMemberRole :one
SELECT role FROM resource_members WHERE resource_kind = ? AND resource_id = ? AND user_id = ?;

-- name: ListResourceMembers :many
SELECT resource_members.role, resource_members.created_at, sqlc.embed(users)
FROM resource_members
JOIN users ON users.id = resource_members.user_id
WHERE resource_members.resource_kind = ? AND resource_members.resource_id = ?
ORDER BY users.display_name COLLATE NOCASE;

-- name: ListMembershipsOfUser :many
SELECT resource_kind, resource_id, role FROM resource_members WHERE user_id = ?;

-- name: ListAppContributors :many
SELECT DISTINCT user_id FROM resource_members
WHERE role = 'contributor'
  AND ((resource_kind = 'app' AND resource_id = sqlc.arg(app_id))
    OR (resource_kind = 'stack' AND resource_id = sqlc.arg(stack_id) AND sqlc.arg(stack_id) != 0));

-- name: ListStackContributors :many
SELECT user_id FROM resource_members
WHERE resource_kind = 'stack' AND resource_id = ? AND role = 'contributor';

-- name: ClearResourceMembers :exec
DELETE FROM resource_members WHERE resource_kind = ? AND resource_id = ?;

-- name: SetAppOwner :exec
UPDATE apps SET owner_id = ?, updated_at = ? WHERE id = ?;

-- name: SetStackOwner :exec
UPDATE stacks SET owner_id = ?, updated_at = ? WHERE id = ?;

-- name: SetStackAppsOwner :exec
UPDATE apps SET owner_id = ?, updated_at = ? WHERE stack_id = ?;
