-- name: CreateTransaction :one
INSERT INTO transactions (invoice, customer_id, plan_id, username, plan_name, router_name, type,
                          price, method, note, admin_id, period_start, period_end)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetTransaction :one
SELECT * FROM transactions WHERE id = ?;

-- name: GetTransactionByInvoice :one
SELECT * FROM transactions WHERE invoice = ?;

-- name: ListTransactions :many
SELECT * FROM transactions ORDER BY id DESC LIMIT ? OFFSET ?;

-- name: ListTransactionsByCustomer :many
SELECT * FROM transactions WHERE customer_id = ? ORDER BY id DESC LIMIT ? OFFSET ?;

-- name: SearchTransactions :many
SELECT *, CAST(sqlc.arg(sort) AS TEXT) AS sort_key -- e.g. date_asc; anything else = newest first
FROM transactions
WHERE invoice LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%'
   OR username LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%'
   OR plan_name LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%'
ORDER BY
  CASE WHEN sort_key = 'date_asc' THEN created_at END ASC,
  CASE WHEN sort_key = 'date_desc' THEN created_at END DESC,
  CASE WHEN sort_key = 'amount_asc' THEN price END ASC,
  CASE WHEN sort_key = 'amount_desc' THEN price END DESC,
  CASE WHEN sort_key = 'username_asc' THEN username END ASC,
  CASE WHEN sort_key = 'username_desc' THEN username END DESC,
  id DESC LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: SumTransactionsBetween :one
-- Income: like PHP top_widget, purchases paid from balance are not income (the top-up already was).
SELECT CAST(COALESCE(SUM(price), 0) AS INTEGER) FROM transactions WHERE created_at >= ? AND created_at < ?
  AND method <> 'Customer - Balance' AND method NOT LIKE 'Balance - Gift from%';
