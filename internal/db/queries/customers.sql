-- name: CreateCustomer :one
INSERT INTO customers (username, password_hash, fullname, address, phone, email, service_type,
                       pppoe_username, pppoe_ip, secret_enc, auto_renewal, status, created_by, billing_day, coordinates)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetCustomer :one
SELECT * FROM customers WHERE id = ?;

-- name: GetCustomerByUsername :one
SELECT * FROM customers WHERE username = ?;

-- name: ListCustomers :many
SELECT * FROM customers ORDER BY id DESC LIMIT ? OFFSET ?;

-- name: SearchCustomers :many
SELECT * FROM customers
WHERE username LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%'
   OR fullname LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%'
   OR phone LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%'
ORDER BY id DESC LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: UpdateCustomer :exec
UPDATE customers SET fullname = ?, address = ?, phone = ?, email = ?, service_type = ?,
    pppoe_username = ?, pppoe_ip = ?, secret_enc = ?, auto_renewal = ?, status = ?, billing_day = ?, coordinates = ?
WHERE id = ?;

-- name: SetCustomerPassword :exec
UPDATE customers SET password_hash = ?, session_version = session_version + 1 WHERE id = ?;

-- name: TouchCustomerLogin :exec
UPDATE customers SET last_login_at = unixepoch() WHERE id = ?;

-- name: AdjustBalance :one
-- delta may be negative; CHECK (balance >= 0) rejects overdraw.
UPDATE customers SET balance = balance + sqlc.arg(delta) WHERE id = sqlc.arg(id)
RETURNING balance;

-- name: DeleteCustomer :exec
DELETE FROM customers WHERE id = ?;

-- name: FilterCustomers :many
-- Empty service_type/status = any. page_limit -1 = all rows (CSV).
SELECT c.*, CAST(COALESCE((SELECT group_concat(p.name, ', ') FROM subscriptions s JOIN plans p ON p.id = s.plan_id
                           WHERE s.customer_id = c.id AND s.status = 'active'), '') AS TEXT) AS packages,
       CAST(sqlc.arg(sort) AS TEXT) AS sort_key -- e.g. username_desc; anything else = newest first
FROM customers c
WHERE (c.username LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%' OR c.fullname LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%'
       OR c.phone LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%')
  AND (CAST(sqlc.arg(service_type) AS TEXT) = '' OR c.service_type = sqlc.arg(service_type))
  AND (CAST(sqlc.arg(status) AS TEXT) = '' OR c.status = sqlc.arg(status))
ORDER BY
  CASE WHEN sort_key = 'username_asc' THEN c.username END ASC,
  CASE WHEN sort_key = 'username_desc' THEN c.username END DESC,
  CASE WHEN sort_key = 'fullname_asc' THEN c.fullname END ASC,
  CASE WHEN sort_key = 'fullname_desc' THEN c.fullname END DESC,
  CASE WHEN sort_key = 'balance_asc' THEN c.balance END ASC,
  CASE WHEN sort_key = 'balance_desc' THEN c.balance END DESC,
  CASE WHEN sort_key = 'status_asc' THEN c.status END ASC,
  CASE WHEN sort_key = 'status_desc' THEN c.status END DESC,
  c.id DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);
