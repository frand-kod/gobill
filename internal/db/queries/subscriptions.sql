-- name: CreateSubscription :one
INSERT INTO subscriptions (customer_id, plan_id, router_id, type, started_at, expires_at, method, admin_id, pending_start)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetSubscription :one
SELECT * FROM subscriptions WHERE id = ?;

-- name: ListSubscriptionsByCustomer :many
SELECT * FROM subscriptions WHERE customer_id = ? ORDER BY id DESC LIMIT ? OFFSET ?;

-- name: ListExpiredActiveSubscriptions :many
SELECT * FROM subscriptions WHERE status = 'active' AND pending_start = 0 AND expires_at <= sqlc.arg(now) ORDER BY expires_at;

-- name: ListActiveExpiringBetween :many
SELECT * FROM subscriptions WHERE status = 'active' AND pending_start = 0 AND expires_at >= sqlc.arg(from_ts) AND expires_at < sqlc.arg(to_ts) ORDER BY expires_at;

-- name: ListSubscriptionsExpiringUnnotified :many
-- Active periods that end within the early-notice window and have not been notified yet.
SELECT * FROM subscriptions WHERE status = 'active' AND pending_start = 0 AND expired_notified_at IS NULL AND expires_at > sqlc.arg(now) AND expires_at <= sqlc.arg(until) ORDER BY expires_at;

-- name: ClaimExpiryNotice :execrows
-- Marks the period's expired message as sent before it goes out. 0 rows = already claimed, or the period was renewed.
UPDATE subscriptions SET expired_notified_at = CAST(sqlc.arg(now) AS INTEGER) WHERE id = sqlc.arg(id) AND expires_at = sqlc.arg(expires_at) AND expired_notified_at IS NULL;

-- name: ExpireSubscription :execrows
UPDATE subscriptions SET status = 'expired' WHERE id = sqlc.arg(id) AND status = 'active' AND pending_start = 0 AND expires_at <= sqlc.arg(now);

-- name: RenewSubscription :exec
UPDATE subscriptions SET plan_id = ?, router_id = ?, type = ?, started_at = ?, expires_at = ?,
    status = 'active', method = ?, admin_id = ?, pending_start = ?, expired_notified_at = NULL
WHERE id = ?;

-- name: FilterSubscriptions :many
-- Empty status/type and router_id/plan_id 0 = any.
SELECT s.id, s.type, s.started_at, s.expires_at, s.status, s.method, s.pending_start, c.username, p.name AS plan_name,
       CAST(COALESCE(r.name, '') AS TEXT) AS router_name,
       CAST(sqlc.arg(sort) AS TEXT) AS sort_key -- e.g. expires_asc; anything else = soonest-expiring last first
FROM subscriptions s JOIN customers c ON c.id = s.customer_id JOIN plans p ON p.id = s.plan_id LEFT JOIN routers r ON r.id = s.router_id
WHERE (c.username LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%' OR c.fullname LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%'
       OR p.name LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%')
  AND (CAST(sqlc.arg(status) AS TEXT) = '' OR s.status = sqlc.arg(status))
  AND (CAST(sqlc.arg(type) AS TEXT) = '' OR s.type = sqlc.arg(type))
  AND (CAST(sqlc.arg(router_id) AS INTEGER) = 0 OR s.router_id = sqlc.arg(router_id))
  AND (CAST(sqlc.arg(plan_id) AS INTEGER) = 0 OR s.plan_id = sqlc.arg(plan_id))
ORDER BY
  CASE WHEN sort_key = 'username_asc' THEN c.username END ASC,
  CASE WHEN sort_key = 'username_desc' THEN c.username END DESC,
  CASE WHEN sort_key = 'plan_asc' THEN p.name END ASC,
  CASE WHEN sort_key = 'plan_desc' THEN p.name END DESC,
  CASE WHEN sort_key = 'created_asc' THEN s.started_at END ASC,
  CASE WHEN sort_key = 'created_desc' THEN s.started_at END DESC,
  CASE WHEN sort_key = 'expires_asc' THEN s.expires_at END ASC,
  s.expires_at DESC, s.id DESC LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: ListActiveSubscriptionsWithPlan :many
-- Customer summary: the subscriptions still active (any expiry, as billing treats them) with the plan name.
SELECT s.id, s.plan_id, s.router_id, s.type, s.expires_at, s.pending_start, p.name AS plan_name
FROM subscriptions s JOIN plans p ON p.id = s.plan_id
WHERE s.customer_id = ? AND s.status = 'active' ORDER BY s.expires_at;

-- name: UpdateSubscription :exec
-- The expired notice is kept only while the expiry does not change, so an edit that keeps the date does not notify again.
UPDATE subscriptions SET plan_id = sqlc.arg(plan_id), router_id = sqlc.arg(router_id), type = sqlc.arg(type), expires_at = sqlc.arg(expires_at), status = sqlc.arg(status), admin_id = sqlc.arg(admin_id),
    expired_notified_at = CASE WHEN expires_at = sqlc.arg(expires_at) THEN expired_notified_at END
WHERE id = sqlc.arg(id);

-- name: DeactivateSubscription :execrows
-- Expires the subscription now (never before it started); 0 rows = already inactive.
UPDATE subscriptions SET status = 'expired', expires_at = MAX(started_at, sqlc.arg(now)) WHERE id = sqlc.arg(id) AND status = 'active';

-- name: RestartSubscription :exec
-- A dead subscription brought back to life starts a new usage window (data limit counts from started_at).
UPDATE subscriptions SET started_at = ?, expired_notified_at = NULL WHERE id = ?;

-- name: StartPendingSubscription :execrows
-- First RADIUS login of a start_on_first_login subscription: the usage window and expiry start now. 0 rows = already started.
UPDATE subscriptions SET pending_start = 0, started_at = sqlc.arg(started_at), expires_at = sqlc.arg(expires_at), expired_notified_at = NULL WHERE id = sqlc.arg(id) AND pending_start = 1;
