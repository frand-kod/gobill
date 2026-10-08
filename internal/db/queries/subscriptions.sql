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

-- name: ExpireSubscription :execrows
UPDATE subscriptions SET status = 'expired' WHERE id = ? AND status = 'active';

-- name: RenewSubscription :exec
UPDATE subscriptions SET plan_id = ?, router_id = ?, type = ?, started_at = ?, expires_at = ?,
    status = 'active', method = ?, admin_id = ?
WHERE id = ?;

-- name: DeleteSubscription :exec
DELETE FROM subscriptions WHERE id = ?;
