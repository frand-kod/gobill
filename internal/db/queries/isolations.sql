-- name: CreateIsolation :one
INSERT INTO isolations (customer_id, started_at, ends_at)
VALUES (?, ?, ?)
RETURNING *;

-- name: GetRunningIsolation :one
-- The isolation of the customer that is running at now, if any.
SELECT * FROM isolations
WHERE customer_id = sqlc.arg(customer_id) AND ended_at = 0 AND ends_at > sqlc.arg(now)
ORDER BY id DESC LIMIT 1;

-- name: ListRunningIsolations :many
SELECT * FROM isolations WHERE ended_at = 0 ORDER BY id;

-- name: ListDueIsolations :many
SELECT * FROM isolations WHERE ended_at = 0 AND ends_at <= sqlc.arg(now) ORDER BY ends_at;

-- name: EndIsolation :execrows
-- 0 rows = already ended.
UPDATE isolations SET ended_at = sqlc.arg(now), end_reason = sqlc.arg(reason)
WHERE id = sqlc.arg(id) AND ended_at = 0;

-- name: EndCustomerIsolations :execrows
UPDATE isolations SET ended_at = sqlc.arg(now), end_reason = sqlc.arg(reason)
WHERE customer_id = sqlc.arg(customer_id) AND ended_at = 0;
