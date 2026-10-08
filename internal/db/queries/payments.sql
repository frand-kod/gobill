-- name: CreatePaymentRequest :one
INSERT INTO payment_requests (ref, gateway, customer_id, plan_id, amount, coupon, channel, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?) RETURNING *;

-- name: SetPaymentGatewayRef :exec
UPDATE payment_requests SET gateway_ref = ?, pay_url = ? WHERE id = ?;

-- name: GetPaymentRequest :one
SELECT * FROM payment_requests WHERE id = ?;

-- name: GetPaymentRequestByRef :one
SELECT * FROM payment_requests WHERE ref = ?;

-- name: ClaimPaymentPaid :execrows
-- The idempotent claim: 0 rows = already handled.
UPDATE payment_requests SET status = 'paid', paid_at = unixepoch() WHERE ref = ? AND status IN ('pending', 'expired', 'failed');

-- name: ClosePaymentRequest :execrows
UPDATE payment_requests SET status = ? WHERE ref = ? AND status = 'pending';

-- name: ExpirePaymentRequests :execrows
UPDATE payment_requests SET status = 'expired' WHERE status = 'pending' AND expires_at < ?;

-- name: SearchPaymentRequests :many
-- status '' = any; created_at range: from_ts/to_ts 0 = open. page_limit -1 = all (CSV).
SELECT p.*, c.username, pl.name AS plan_name FROM payment_requests p
JOIN customers c ON c.id = p.customer_id JOIN plans pl ON pl.id = p.plan_id
WHERE (CAST(sqlc.arg(q) AS TEXT) = '' OR p.ref LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%' OR p.gateway_ref LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%'
       OR c.username LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%' OR pl.name LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%')
  AND (CAST(sqlc.arg(status) AS TEXT) = '' OR p.status = sqlc.arg(status))
  AND (CAST(sqlc.arg(from_ts) AS INTEGER) = 0 OR p.created_at >= sqlc.arg(from_ts))
  AND (CAST(sqlc.arg(to_ts) AS INTEGER) = 0 OR p.created_at < sqlc.arg(to_ts))
ORDER BY p.id DESC LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: GetPaymentAudit :one
SELECT p.*, c.username, pl.name AS plan_name FROM payment_requests p
JOIN customers c ON c.id = p.customer_id JOIN plans pl ON pl.id = p.plan_id WHERE p.id = ?;
