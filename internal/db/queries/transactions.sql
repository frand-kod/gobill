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
WHERE (invoice LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%'
   OR username LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%'
   OR plan_name LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%')
  AND (CAST(sqlc.arg(customer_id) AS INTEGER) = 0 OR customer_id = sqlc.arg(customer_id)) -- 0 = all customers
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
-- Agent consignment: redeeming one of its codes is the agent's sale; the deposits (agent_ledger payments) are the income.
SELECT CAST(COALESCE(SUM(price), 0) AS INTEGER) + CAST((SELECT COALESCE(SUM(amount), 0) FROM agent_ledger
    WHERE kind = 'payment' AND agent_ledger.created_at >= sqlc.arg(from_ts) AND agent_ledger.created_at < sqlc.arg(to_ts)) AS INTEGER)
FROM transactions WHERE transactions.created_at >= sqlc.arg(from_ts) AND transactions.created_at < sqlc.arg(to_ts)
  AND method <> 'Customer - Balance' AND method NOT LIKE 'Balance - Gift from%'
  AND NOT (method LIKE 'Voucher - %' AND EXISTS (SELECT 1 FROM vouchers v JOIN agent_batch_vouchers bv ON bv.voucher_id = v.id WHERE v.code = substr(transactions.method, 11)));

-- name: ListActivationsByCustomer :many
-- Plan purchases and activations of the customer; balance moves are not activations.
SELECT * FROM transactions WHERE customer_id = ? AND type <> 'Balance' ORDER BY id DESC LIMIT ? OFFSET ?;

-- name: NextInvoiceNumber :one
UPDATE invoice_seq SET n = n + 1 WHERE id = 1 RETURNING n;
