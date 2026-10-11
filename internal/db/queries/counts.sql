-- Row counts for the paginated lists. Each one repeats the WHERE of its list query (same filters, same joins).

-- name: CountCoupons :one
SELECT COUNT(*) FROM coupons WHERE code LIKE '%' || CAST(sqlc.arg(term) AS TEXT) || '%' OR description LIKE '%' || CAST(sqlc.arg(term) AS TEXT) || '%';

-- name: CountFilteredCustomers :one
SELECT COUNT(*) FROM customers c
WHERE (c.username LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%' OR c.fullname LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%'
       OR c.phone LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%' OR c.pppoe_username LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%')
  AND (CAST(sqlc.arg(service_type) AS TEXT) = '' OR c.service_type = sqlc.arg(service_type))
  AND (CAST(sqlc.arg(status) AS TEXT) = '' OR c.status = sqlc.arg(status));

-- name: CountPools :one
SELECT COUNT(*) FROM pools WHERE name LIKE '%' || CAST(sqlc.arg(term) AS TEXT) || '%';

-- name: CountPaymentRequests :one
SELECT COUNT(*) FROM payment_requests p
LEFT JOIN plans pl ON pl.id = p.plan_id
WHERE (CAST(sqlc.arg(q) AS TEXT) = '' OR p.ref LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%' OR p.gateway_ref LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%'
       OR p.username LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%' OR COALESCE(pl.name, 'Custom Balance') LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%')
  AND (CAST(sqlc.arg(status) AS TEXT) = '' OR p.status = sqlc.arg(status))
  AND (CAST(sqlc.arg(from_ts) AS INTEGER) = 0 OR p.created_at >= sqlc.arg(from_ts))
  AND (CAST(sqlc.arg(to_ts) AS INTEGER) = 0 OR p.created_at < sqlc.arg(to_ts));

-- name: CountPlans :one
SELECT COUNT(*) FROM plans WHERE name LIKE '%' || CAST(sqlc.arg(term) AS TEXT) || '%' AND id != sqlc.arg(hidden_id);

-- name: CountRouters :one
SELECT COUNT(*) FROM routers
WHERE name LIKE '%' || CAST(sqlc.arg(term) AS TEXT) || '%' OR host LIKE '%' || CAST(sqlc.arg(term) AS TEXT) || '%';

-- name: CountODPs :one
SELECT COUNT(*) FROM odps WHERE name LIKE '%' || CAST(sqlc.arg(term) AS TEXT) || '%';

-- name: CountVouchers :one
SELECT COUNT(*) FROM vouchers WHERE code LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%'
  AND (CAST(sqlc.arg(status) AS TEXT) = '' OR status = sqlc.arg(status))
  AND (CAST(sqlc.arg(plan_id) AS INTEGER) = 0 OR plan_id = sqlc.arg(plan_id));

-- name: CountFilteredAdmins :one
SELECT COUNT(*) FROM admins
WHERE (username LIKE '%' || sqlc.arg(q) || '%' OR fullname LIKE '%' || sqlc.arg(q) || '%')
  AND (sqlc.arg(scope) = 'all'
    OR (sqlc.arg(scope) = 'admin' AND (role IN ('Report', 'Agent', 'Sales') OR id = sqlc.arg(actor)))
    OR (sqlc.arg(scope) = 'agent' AND (id = sqlc.arg(actor) OR root_id = sqlc.arg(actor))));

-- name: CountFilteredSubscriptions :one
SELECT COUNT(*) FROM subscriptions s JOIN customers c ON c.id = s.customer_id JOIN plans p ON p.id = s.plan_id
WHERE (c.username LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%' OR c.fullname LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%'
       OR p.name LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%')
  AND (CAST(sqlc.arg(status) AS TEXT) = '' OR s.status = sqlc.arg(status))
  AND (CAST(sqlc.arg(type) AS TEXT) = '' OR s.type = sqlc.arg(type))
  AND (CAST(sqlc.arg(router_id) AS INTEGER) = 0 OR s.router_id = sqlc.arg(router_id))
  AND (CAST(sqlc.arg(plan_id) AS INTEGER) = 0 OR s.plan_id = sqlc.arg(plan_id));

-- name: CountBandwidths :one
SELECT COUNT(*) FROM bandwidths WHERE name LIKE '%' || CAST(sqlc.arg(term) AS TEXT) || '%' AND id != sqlc.arg(hidden_id);

-- name: CountActivityLogs :one
SELECT COUNT(*) FROM activity_logs
WHERE action LIKE '%' || CAST(sqlc.arg(term) AS TEXT) || '%' OR description LIKE '%' || CAST(sqlc.arg(term) AS TEXT) || '%';

-- name: CountRadiusLogs :one
SELECT COUNT(*) FROM radius_sessions
WHERE (username LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%' OR nas_ip LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%')
  AND (CAST(sqlc.arg(from_ts) AS INTEGER) = 0 OR started_at >= sqlc.arg(from_ts))
  AND (CAST(sqlc.arg(to_ts) AS INTEGER) = 0 OR started_at < sqlc.arg(to_ts));

-- name: CountMessageLogs :one
SELECT COUNT(*) FROM message_logs
WHERE (recipient LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%' OR subject LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%'
       OR body LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%' OR channel LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%')
  AND (CAST(sqlc.arg(from_ts) AS INTEGER) = 0 OR created_at >= sqlc.arg(from_ts))
  AND (CAST(sqlc.arg(to_ts) AS INTEGER) = 0 OR created_at < sqlc.arg(to_ts));

-- name: CountOpenRadiusSessions :one
SELECT COUNT(*) FROM radius_sessions
WHERE stopped_at IS NULL
  AND (username LIKE '%' || CAST(sqlc.arg(term) AS TEXT) || '%' OR framed_ip LIKE '%' || CAST(sqlc.arg(term) AS TEXT) || '%' OR mac LIKE '%' || CAST(sqlc.arg(term) AS TEXT) || '%');

-- name: CountSearchTransactions :one
SELECT COUNT(*) FROM transactions
WHERE (invoice LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%'
   OR username LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%'
   OR plan_name LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%')
  AND (CAST(sqlc.arg(customer_id) AS INTEGER) = 0 OR customer_id = sqlc.arg(customer_id));

-- name: CountActivationsByCustomer :one
-- Plan purchases and activations of the customer; balance moves are not activations.
SELECT COUNT(*) FROM transactions WHERE customer_id = ? AND type <> 'Balance';

-- name: CountTransactionsByCustomer :one
SELECT COUNT(*) FROM transactions WHERE customer_id = ?;

-- name: CountRadiusSessionsByUsers :one
SELECT COUNT(*) FROM radius_sessions WHERE username = sqlc.arg(name1) OR username = sqlc.arg(name2);
