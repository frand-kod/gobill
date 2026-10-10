-- name: ReportTransactions :many
-- Empty / zero filter args match everything. Used by the CSV export (all rows).
SELECT * FROM transactions
WHERE created_at >= sqlc.arg(from_ts) AND created_at < sqlc.arg(to_ts)
  AND (CAST(sqlc.arg(type) AS TEXT) = '' OR type = sqlc.arg(type))
  AND (CAST(sqlc.arg(method) AS TEXT) = '' OR method = sqlc.arg(method))
  AND (CAST(sqlc.arg(router_name) AS TEXT) = '' OR router_name = sqlc.arg(router_name))
  AND (CAST(sqlc.arg(plan_id) AS INTEGER) = 0 OR plan_id = sqlc.arg(plan_id))
  -- balance transfer / send-plan rows exist only in gobill (PHP never writes them): not income
  AND NOT (method = 'Customer - Balance' AND type = 'Balance')
ORDER BY created_at, id;

-- name: ReportTransactionsPage :many
-- Same filter and exclusions as ReportTransactions, one page of rows for the report table and print.
SELECT * FROM transactions
WHERE created_at >= sqlc.arg(from_ts) AND created_at < sqlc.arg(to_ts)
  AND (CAST(sqlc.arg(type) AS TEXT) = '' OR type = sqlc.arg(type))
  AND (CAST(sqlc.arg(method) AS TEXT) = '' OR method = sqlc.arg(method))
  AND (CAST(sqlc.arg(router_name) AS TEXT) = '' OR router_name = sqlc.arg(router_name))
  AND (CAST(sqlc.arg(plan_id) AS INTEGER) = 0 OR plan_id = sqlc.arg(plan_id))
  AND NOT (method = 'Customer - Balance' AND type = 'Balance')
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
  AND NOT (method = 'Customer - Balance' AND type = 'Balance');

-- name: ReportTotalsByType :many
SELECT type AS name, COUNT(*) AS cnt, CAST(COALESCE(SUM(price), 0) AS INTEGER) AS total
FROM transactions
WHERE created_at >= sqlc.arg(from_ts) AND created_at < sqlc.arg(to_ts)
  AND (CAST(sqlc.arg(type) AS TEXT) = '' OR type = sqlc.arg(type))
  AND (CAST(sqlc.arg(method) AS TEXT) = '' OR method = sqlc.arg(method))
  AND (CAST(sqlc.arg(router_name) AS TEXT) = '' OR router_name = sqlc.arg(router_name))
  AND (CAST(sqlc.arg(plan_id) AS INTEGER) = 0 OR plan_id = sqlc.arg(plan_id))
  AND NOT (method = 'Customer - Balance' AND type = 'Balance')
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
  AND NOT (method = 'Customer - Balance' AND type = 'Balance')
GROUP BY method
ORDER BY method;
