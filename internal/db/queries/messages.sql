-- name: CreateInboxMessage :exec
INSERT INTO customers_inbox (customer_id, from_name, subject, body) VALUES (?, ?, ?, ?);

-- name: ListInboxByCustomer :many
SELECT * FROM customers_inbox WHERE customer_id = ? ORDER BY id DESC LIMIT ?;

-- name: GetInboxMessage :one
SELECT * FROM customers_inbox WHERE id = ? AND customer_id = ?;

-- name: MarkInboxRead :exec
UPDATE customers_inbox SET read_at = unixepoch() WHERE id = ? AND customer_id = ? AND read_at IS NULL;

-- name: CountUnreadInbox :one
SELECT COUNT(*) FROM customers_inbox WHERE customer_id = ? AND read_at IS NULL;

-- name: ListMessageRecipients :many
-- Empty service_type/sub_status and router_id 0 = any. Filters on router/status match a subscription.
SELECT c.* FROM customers c
WHERE (CAST(sqlc.arg(service_type) AS TEXT) = '' OR c.service_type = sqlc.arg(service_type))
  AND ((CAST(sqlc.arg(router_id) AS INTEGER) = 0 AND CAST(sqlc.arg(sub_status) AS TEXT) = '') OR EXISTS (
        SELECT 1 FROM subscriptions s
        WHERE s.customer_id = c.id
          AND (CAST(sqlc.arg(router_id) AS INTEGER) = 0 OR s.router_id = CAST(sqlc.arg(router_id) AS INTEGER))
          AND (CAST(sqlc.arg(sub_status) AS TEXT) = '' OR s.status = sqlc.arg(sub_status))))
ORDER BY c.id;
