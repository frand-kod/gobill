-- Header search palette (internal/web/admin_search.go). The text is matched literally with instr/substr,
-- so % and _ are plain characters (sqlc's SQLite parser does not take LIKE ... ESCAPE). Case-insensitive.

-- name: SearchCustomersContains :many
SELECT id, username, fullname, phone, pppoe_username, email, status FROM customers
WHERE instr(lower(username), lower(sqlc.arg(q))) > 0 OR instr(lower(fullname), lower(sqlc.arg(q))) > 0
   OR instr(lower(phone), lower(sqlc.arg(q))) > 0 OR instr(lower(pppoe_username), lower(sqlc.arg(q))) > 0
   OR instr(lower(email), lower(sqlc.arg(q))) > 0
ORDER BY username LIMIT sqlc.arg(limit);

-- name: SearchPlansContains :many
SELECT id, name, type, price FROM plans WHERE instr(lower(name), lower(sqlc.arg(q))) > 0 ORDER BY name LIMIT sqlc.arg(limit);

-- name: SearchRoutersContains :many
SELECT id, name, host FROM routers
WHERE instr(lower(name), lower(sqlc.arg(q))) > 0 OR instr(lower(host), lower(sqlc.arg(q))) > 0
ORDER BY name LIMIT sqlc.arg(limit);

-- name: SearchNASContains :many
SELECT id, name, ip FROM nas
WHERE instr(lower(name), lower(sqlc.arg(q))) > 0 OR instr(lower(ip), lower(sqlc.arg(q))) > 0
ORDER BY name LIMIT sqlc.arg(limit);

-- name: SearchVouchersPrefix :many
SELECT v.id, v.code, v.status, p.name AS plan_name FROM vouchers v JOIN plans p ON p.id = v.plan_id
WHERE lower(substr(v.code, 1, length(sqlc.arg(q)))) = lower(sqlc.arg(q))
ORDER BY v.id DESC LIMIT sqlc.arg(limit);

-- name: SearchTransactionsPrefix :many
SELECT id, invoice, username, plan_name, price FROM transactions
WHERE lower(substr(invoice, 1, length(sqlc.arg(q)))) = lower(sqlc.arg(q))
ORDER BY id DESC LIMIT sqlc.arg(limit);

-- name: SearchSubscriptionsByUsername :many
SELECT s.id, s.status, s.expires_at, c.id AS customer_id, c.username, p.name AS plan_name
FROM subscriptions s
JOIN customers c ON c.id = s.customer_id
JOIN plans p ON p.id = s.plan_id
WHERE instr(lower(c.username), lower(sqlc.arg(q))) > 0
ORDER BY s.id DESC LIMIT sqlc.arg(limit);
