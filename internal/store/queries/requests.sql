-- name: GetAccountRequestByDiscordID :one
SELECT * FROM account_requests WHERE discord_id = ?;

-- name: GetAccountRequest :one
SELECT * FROM account_requests WHERE id = ?;

-- name: CreateAccountRequest :one
INSERT INTO account_requests (discord_id, username, display_name, email, avatar, status, created_at)
VALUES (?, ?, ?, ?, ?, 'pending', ?)
RETURNING *;

-- name: UpdateAccountRequestProfile :exec
UPDATE account_requests SET username = ?, display_name = ?, email = ?, avatar = ? WHERE id = ?;

-- name: ListPendingAccountRequests :many
SELECT * FROM account_requests WHERE status = 'pending' ORDER BY created_at;

-- name: RefuseAccountRequest :exec
UPDATE account_requests SET status = 'refused', reason = ?, decided_at = ? WHERE id = ? AND status = 'pending';

-- name: DeleteAccountRequest :exec
DELETE FROM account_requests WHERE id = ?;
