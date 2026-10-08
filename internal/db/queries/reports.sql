-- name: ReportTransactions :many
-- Empty / zero filter args match everything.
SELECT * FROM transactions
WHERE created_at >= sqlc.arg(from_ts) AND created_at < sqlc.arg(to_ts)
  AND (CAST(sqlc.arg(type) AS TEXT) = '' OR type = sqlc.arg(type))
  AND (CAST(sqlc.arg(method) AS TEXT) = '' OR method = sqlc.arg(method))
  AND (CAST(sqlc.arg(router_name) AS TEXT) = '' OR router_name = sqlc.arg(router_name))
  AND (CAST(sqlc.arg(plan_id) AS INTEGER) = 0 OR plan_id = sqlc.arg(plan_id))
ORDER BY created_at, id;
