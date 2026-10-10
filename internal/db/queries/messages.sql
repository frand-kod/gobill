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

-- name: DeleteReadInboxBefore :execrows
-- One batch of read messages; the caller loops until 0 rows.
DELETE FROM customers_inbox WHERE rowid IN (SELECT i.rowid FROM customers_inbox i WHERE i.read_at IS NOT NULL AND i.created_at < ? LIMIT 5000);

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

-- name: CreateMessageLog :exec
INSERT INTO message_logs (channel, recipient, subject, body, status, error) VALUES (?, ?, ?, ?, ?, ?);

-- name: SearchMessageLogs :many
-- created_at range: from_ts/to_ts 0 = open. page_limit -1 = all (CSV).
SELECT * FROM message_logs
WHERE (recipient LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%' OR subject LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%'
       OR body LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%' OR channel LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%')
  AND (CAST(sqlc.arg(from_ts) AS INTEGER) = 0 OR created_at >= sqlc.arg(from_ts))
  AND (CAST(sqlc.arg(to_ts) AS INTEGER) = 0 OR created_at < sqlc.arg(to_ts))
ORDER BY id DESC LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);
