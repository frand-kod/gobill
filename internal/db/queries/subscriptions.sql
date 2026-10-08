-- name: CreateSubscription :one
INSERT INTO subscriptions (customer_id, plan_id, router_id, type, started_at, expires_at, method, admin_id)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetSubscription :one
SELECT * FROM subscriptions WHERE id = ?;

-- name: ListSubscriptions :many
SELECT * FROM subscriptions ORDER BY id DESC LIMIT ? OFFSET ?;

-- name: ListSubscriptionsByCustomer :many
SELECT * FROM subscriptions WHERE customer_id = ? ORDER BY id DESC LIMIT ? OFFSET ?;

-- name: SearchSubscriptions :many
SELECT s.* FROM subscriptions s JOIN customers c ON c.id = s.customer_id
WHERE c.username LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%' OR c.fullname LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%'
ORDER BY s.id DESC LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: ListExpiredActiveSubscriptions :many
SELECT * FROM subscriptions WHERE status = 'active' AND expires_at <= sqlc.arg(now) ORDER BY expires_at;

-- name: ListActiveExpiringBetween :many
SELECT * FROM subscriptions WHERE status = 'active' AND expires_at >= sqlc.arg(from_ts) AND expires_at < sqlc.arg(to_ts) ORDER BY expires_at;

-- name: ExpireSubscription :execrows
UPDATE subscriptions SET status = 'expired' WHERE id = ? AND status = 'active';

-- name: RenewSubscription :exec
UPDATE subscriptions SET plan_id = ?, router_id = ?, type = ?, started_at = ?, expires_at = ?,
    status = 'active', method = ?, admin_id = ?
WHERE id = ?;

-- name: DeleteSubscription :exec
DELETE FROM subscriptions WHERE id = ?;

-- name: FilterSubscriptions :many
-- Empty status/type and router_id 0 = any.
SELECT s.id, s.type, s.started_at, s.expires_at, s.status, s.method, c.username, p.name AS plan_name, r.name AS router_name
FROM subscriptions s JOIN customers c ON c.id = s.customer_id JOIN plans p ON p.id = s.plan_id JOIN routers r ON r.id = s.router_id
WHERE (c.username LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%' OR c.fullname LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%'
       OR p.name LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%')
  AND (CAST(sqlc.arg(status) AS TEXT) = '' OR s.status = sqlc.arg(status))
  AND (CAST(sqlc.arg(type) AS TEXT) = '' OR s.type = sqlc.arg(type))
  AND (CAST(sqlc.arg(router_id) AS INTEGER) = 0 OR s.router_id = sqlc.arg(router_id))
ORDER BY s.expires_at DESC, s.id DESC LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: UpdateSubscription :exec
UPDATE subscriptions SET plan_id = ?, router_id = ?, type = ?, expires_at = ?, status = ?, admin_id = ? WHERE id = ?;

-- name: DeactivateSubscription :execrows
-- Expires the subscription now (never before it started); 0 rows = already inactive.
UPDATE subscriptions SET status = 'expired', expires_at = MAX(started_at, sqlc.arg(now)) WHERE id = sqlc.arg(id) AND status = 'active';
