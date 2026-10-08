-- name: ListExpiringSubscriptions :many
-- Active or just-expired subscriptions, soonest expiry first (dashboard widget).
SELECT s.id, s.customer_id, s.expires_at, s.status, c.username, c.fullname, p.name AS plan_name
FROM subscriptions s
JOIN customers c ON c.id = s.customer_id
JOIN plans p ON p.id = s.plan_id
WHERE s.status IN ('active', 'expired') AND s.expires_at >= sqlc.arg(since)
ORDER BY s.expires_at, s.id LIMIT sqlc.arg(page_limit);

-- name: VoucherStockByPlan :many
SELECT p.id, p.name,
  COUNT(CASE WHEN v.status = 'unused' THEN 1 END) AS unused,
  COUNT(CASE WHEN v.status = 'used' THEN 1 END) AS used
FROM plans p
LEFT JOIN vouchers v ON v.plan_id = p.id
GROUP BY p.id, p.name
ORDER BY p.name;
