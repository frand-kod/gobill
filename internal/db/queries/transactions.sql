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
SELECT * FROM transactions
WHERE invoice LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%'
   OR username LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%'
   OR plan_name LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%'
ORDER BY id DESC LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: SumTransactionsBetween :one
SELECT CAST(COALESCE(SUM(price), 0) AS INTEGER) FROM transactions WHERE created_at >= ? AND created_at < ?;
