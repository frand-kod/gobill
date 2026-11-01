-- name: AddRouterPending :exec
-- A repeat of the same removal only refreshes the error.
INSERT INTO router_pending (router_id, kind, username, payload, last_error, created_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (router_id, kind, username, payload) DO UPDATE SET last_error = excluded.last_error;

-- name: ListRouterPending :many
SELECT * FROM router_pending ORDER BY router_id, id;

-- name: BumpRouterPending :exec
UPDATE router_pending SET attempts = attempts + 1, last_error = ?, tried_at = ? WHERE id = ?;

-- name: DeleteRouterPending :exec
DELETE FROM router_pending WHERE id = ?;

-- name: CountRouterPending :one
SELECT COUNT(*) FROM router_pending;
