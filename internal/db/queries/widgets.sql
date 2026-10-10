-- name: ListExpiringSubscriptions :many
-- Active subscriptions that end from now to the given limit, soonest first (dashboard widget).
SELECT s.id, s.customer_id, s.expires_at, s.status, c.username, c.fullname, c.phone, p.name AS plan_name
FROM subscriptions s
JOIN customers c ON c.id = s.customer_id
JOIN plans p ON p.id = s.plan_id
WHERE s.status = 'active' AND s.expires_at >= sqlc.arg(since) AND s.expires_at <= sqlc.arg(until)
ORDER BY s.expires_at, s.id LIMIT sqlc.arg(page_limit);

-- name: ListRecentlyExpiredSubscriptions :many
-- Subscriptions that ended inside the window and are not renewed, newest first (dashboard "just expired" card).
SELECT s.id, s.customer_id, s.expires_at, s.status, c.username, c.fullname, c.phone, p.name AS plan_name
FROM subscriptions s
JOIN customers c ON c.id = s.customer_id
JOIN plans p ON p.id = s.plan_id
WHERE s.status IN ('active', 'expired') AND s.expires_at >= sqlc.arg(since) AND s.expires_at < sqlc.arg(until)
ORDER BY s.expires_at DESC, s.id DESC LIMIT sqlc.arg(page_limit);

-- name: VoucherStockByPlan :many
SELECT p.id, p.name,
  COUNT(CASE WHEN v.status = 'unused' THEN 1 END) AS unused,
  COUNT(CASE WHEN v.status = 'used' THEN 1 END) AS used
FROM plans p
LEFT JOIN vouchers v ON v.plan_id = p.id
GROUP BY p.id, p.name
ORDER BY p.name;

-- name: CountSetup :one
-- Row counts the dashboard "quick start" checklist ticks against.
SELECT (SELECT COUNT(*) FROM routers) AS routers, (SELECT COUNT(*) FROM nas) AS nas,
  (SELECT COUNT(*) FROM bandwidths) AS bandwidths, (SELECT COUNT(*) FROM plans) AS plans,
  (SELECT COUNT(*) FROM customers) AS customers, (SELECT COUNT(*) FROM vouchers) AS vouchers;
