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

-- name: FilterIsolations :many
-- Empty status = any; "running" = ended_at 0, "ended" = already ended. Newest first.
SELECT i.id, i.started_at, i.ends_at, i.ended_at, i.end_reason, c.username, c.fullname
FROM isolations i JOIN customers c ON c.id = i.customer_id
WHERE (c.username LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%' OR c.fullname LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%')
  AND (CAST(sqlc.arg(status) AS TEXT) = ''
       OR (CAST(sqlc.arg(status) AS TEXT) = 'running' AND i.ended_at = 0)
       OR (CAST(sqlc.arg(status) AS TEXT) = 'ended' AND i.ended_at > 0))
ORDER BY i.id DESC LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: CountFilteredIsolations :one
SELECT COUNT(*) FROM isolations i JOIN customers c ON c.id = i.customer_id
WHERE (c.username LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%' OR c.fullname LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%')
  AND (CAST(sqlc.arg(status) AS TEXT) = ''
       OR (CAST(sqlc.arg(status) AS TEXT) = 'running' AND i.ended_at = 0)
       OR (CAST(sqlc.arg(status) AS TEXT) = 'ended' AND i.ended_at > 0));
