-- name: CreateTicket :one
INSERT INTO support_tickets (author_id, kind, app_id, app_name, subject, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetTicket :one
SELECT sqlc.embed(support_tickets), users.display_name AS author_name
FROM support_tickets
JOIN users ON users.id = support_tickets.author_id
WHERE support_tickets.id = ?;

-- name: ListTickets :many
SELECT sqlc.embed(support_tickets), users.display_name AS author_name,
       (SELECT COUNT(*) FROM support_messages m WHERE m.ticket_id = support_tickets.id) AS messages
FROM support_tickets
JOIN users ON users.id = support_tickets.author_id
ORDER BY support_tickets.updated_at DESC
LIMIT 500;

-- name: ListTicketsByAuthor :many
SELECT sqlc.embed(support_tickets), users.display_name AS author_name,
       (SELECT COUNT(*) FROM support_messages m WHERE m.ticket_id = support_tickets.id) AS messages
FROM support_tickets
JOIN users ON users.id = support_tickets.author_id
WHERE support_tickets.author_id = ?
ORDER BY support_tickets.updated_at DESC
LIMIT 500;

-- name: AddTicketMessage :one
INSERT INTO support_messages (ticket_id, author_id, body, created_at) VALUES (?, ?, ?, ?) RETURNING *;

-- name: ListTicketMessages :many
SELECT support_messages.id, support_messages.author_id, support_messages.body, support_messages.created_at,
       COALESCE(users.display_name, '') AS author_name, COALESCE(users.role, '') AS author_role
FROM support_messages
LEFT JOIN users ON users.id = support_messages.author_id
WHERE support_messages.ticket_id = ?
ORDER BY support_messages.id;

-- name: TouchTicket :exec
-- waiting: the author wrote last; a message reopens a closed ticket.
UPDATE support_tickets SET waiting = ?, status = 'open', updated_at = ? WHERE id = ?;

-- name: SetTicketStatus :exec
UPDATE support_tickets SET status = ?, updated_at = ? WHERE id = ?;

-- name: CountWaitingTickets :one
SELECT COUNT(*) FROM support_tickets WHERE status = 'open' AND waiting = 1;
