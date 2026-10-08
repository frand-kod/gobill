-- name: CountCustomers :one
SELECT COUNT(*) FROM customers;

-- name: CountCustomersBetween :one
SELECT COUNT(*) FROM customers WHERE created_at >= ? AND created_at < ?;

-- name: CountSubscriptionsByStatus :one
SELECT COUNT(*) FROM subscriptions WHERE status = ?;
