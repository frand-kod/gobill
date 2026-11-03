-- name: ReportTransactions :many
-- Empty / zero filter args match everything. Used by the CSV export (all rows).
SELECT * FROM transactions
WHERE created_at >= sqlc.arg(from_ts) AND created_at < sqlc.arg(to_ts)
  AND (CAST(sqlc.arg(type) AS TEXT) = '' OR type = sqlc.arg(type))
  AND (CAST(sqlc.arg(method) AS TEXT) = '' OR method = sqlc.arg(method))
  AND (CAST(sqlc.arg(router_name) AS TEXT) = '' OR router_name = sqlc.arg(router_name))
  AND (CAST(sqlc.arg(plan_id) AS INTEGER) = 0 OR plan_id = sqlc.arg(plan_id))
  -- income = SumTransactionsBetween: purchases paid from balance (the top-up already counted) and gifts are not income
  AND method <> 'Customer - Balance' AND method NOT LIKE 'Balance - Gift from%'
  -- redeeming a consignment voucher is the agent's sale; the operator's income is the deposit (ReportAgentPayments)
  AND NOT (method LIKE 'Voucher - %' AND EXISTS (SELECT 1 FROM vouchers v JOIN agent_batch_vouchers bv ON bv.voucher_id = v.id WHERE v.code = substr(transactions.method, 11)))
ORDER BY created_at, id;

-- name: ReportTransactionsPage :many
-- Same filter and exclusions as ReportTransactions, one page of rows for the report table and print.
SELECT * FROM transactions
WHERE created_at >= sqlc.arg(from_ts) AND created_at < sqlc.arg(to_ts)
  AND (CAST(sqlc.arg(type) AS TEXT) = '' OR type = sqlc.arg(type))
  AND (CAST(sqlc.arg(method) AS TEXT) = '' OR method = sqlc.arg(method))
  AND (CAST(sqlc.arg(router_name) AS TEXT) = '' OR router_name = sqlc.arg(router_name))
  AND (CAST(sqlc.arg(plan_id) AS INTEGER) = 0 OR plan_id = sqlc.arg(plan_id))
  AND method <> 'Customer - Balance' AND method NOT LIKE 'Balance - Gift from%'
  -- redeeming a consignment voucher is the agent's sale; the operator's income is the deposit (ReportAgentPayments)
  AND NOT (method LIKE 'Voucher - %' AND EXISTS (SELECT 1 FROM vouchers v JOIN agent_batch_vouchers bv ON bv.voucher_id = v.id WHERE v.code = substr(transactions.method, 11)))
ORDER BY created_at, id
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: ReportTotals :one
-- Count and sum for the report filter. Same filter as ReportTransactions.
SELECT COUNT(*) AS cnt, CAST(COALESCE(SUM(price), 0) AS INTEGER) AS total
FROM transactions
WHERE created_at >= sqlc.arg(from_ts) AND created_at < sqlc.arg(to_ts)
  AND (CAST(sqlc.arg(type) AS TEXT) = '' OR type = sqlc.arg(type))
  AND (CAST(sqlc.arg(method) AS TEXT) = '' OR method = sqlc.arg(method))
  AND (CAST(sqlc.arg(router_name) AS TEXT) = '' OR router_name = sqlc.arg(router_name))
  AND (CAST(sqlc.arg(plan_id) AS INTEGER) = 0 OR plan_id = sqlc.arg(plan_id))
  AND method <> 'Customer - Balance' AND method NOT LIKE 'Balance - Gift from%'
  -- redeeming a consignment voucher is the agent's sale; the operator's income is the deposit (ReportAgentPayments)
  AND NOT (method LIKE 'Voucher - %' AND EXISTS (SELECT 1 FROM vouchers v JOIN agent_batch_vouchers bv ON bv.voucher_id = v.id WHERE v.code = substr(transactions.method, 11)));

-- name: ReportTotalsByType :many
SELECT type AS name, COUNT(*) AS cnt, CAST(COALESCE(SUM(price), 0) AS INTEGER) AS total
FROM transactions
WHERE created_at >= sqlc.arg(from_ts) AND created_at < sqlc.arg(to_ts)
  AND (CAST(sqlc.arg(type) AS TEXT) = '' OR type = sqlc.arg(type))
  AND (CAST(sqlc.arg(method) AS TEXT) = '' OR method = sqlc.arg(method))
  AND (CAST(sqlc.arg(router_name) AS TEXT) = '' OR router_name = sqlc.arg(router_name))
  AND (CAST(sqlc.arg(plan_id) AS INTEGER) = 0 OR plan_id = sqlc.arg(plan_id))
  AND method <> 'Customer - Balance' AND method NOT LIKE 'Balance - Gift from%'
  -- redeeming a consignment voucher is the agent's sale; the operator's income is the deposit (ReportAgentPayments)
  AND NOT (method LIKE 'Voucher - %' AND EXISTS (SELECT 1 FROM vouchers v JOIN agent_batch_vouchers bv ON bv.voucher_id = v.id WHERE v.code = substr(transactions.method, 11)))
GROUP BY type
ORDER BY type;

-- name: ReportTotalsByMethod :many
SELECT method AS name, COUNT(*) AS cnt, CAST(COALESCE(SUM(price), 0) AS INTEGER) AS total
FROM transactions
WHERE created_at >= sqlc.arg(from_ts) AND created_at < sqlc.arg(to_ts)
  AND (CAST(sqlc.arg(type) AS TEXT) = '' OR type = sqlc.arg(type))
  AND (CAST(sqlc.arg(method) AS TEXT) = '' OR method = sqlc.arg(method))
  AND (CAST(sqlc.arg(router_name) AS TEXT) = '' OR router_name = sqlc.arg(router_name))
  AND (CAST(sqlc.arg(plan_id) AS INTEGER) = 0 OR plan_id = sqlc.arg(plan_id))
  AND method <> 'Customer - Balance' AND method NOT LIKE 'Balance - Gift from%'
  -- redeeming a consignment voucher is the agent's sale; the operator's income is the deposit (ReportAgentPayments)
  AND NOT (method LIKE 'Voucher - %' AND EXISTS (SELECT 1 FROM vouchers v JOIN agent_batch_vouchers bv ON bv.voucher_id = v.id WHERE v.code = substr(transactions.method, 11)))
GROUP BY method
ORDER BY method;

-- name: ReportAgentPayments :one
-- Agent consignment deposits received in the period: their own income line, no transactions row.
SELECT COUNT(*) AS cnt, CAST(COALESCE(SUM(amount), 0) AS INTEGER) AS total
FROM agent_ledger
WHERE kind = 'payment' AND created_at >= sqlc.arg(from_ts) AND created_at < sqlc.arg(to_ts);
