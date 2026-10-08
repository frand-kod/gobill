-- name: CreateCustomer :one
INSERT INTO customers (username, password_hash, fullname, address, phone, email, service_type,
                       pppoe_username, pppoe_ip, secret_enc, auto_renewal, status, created_by)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
    pppoe_username = ?, pppoe_ip = ?, secret_enc = ?, auto_renewal = ?, status = ?
WHERE id = ?;

-- name: SetCustomerPassword :exec
UPDATE customers SET password_hash = ? WHERE id = ?;

-- name: TouchCustomerLogin :exec
UPDATE customers SET last_login_at = unixepoch() WHERE id = ?;

-- name: AdjustBalance :one
-- delta may be negative; CHECK (balance >= 0) rejects overdraw.
UPDATE customers SET balance = balance + sqlc.arg(delta) WHERE id = sqlc.arg(id)
RETURNING balance;

-- name: DeleteCustomer :exec
DELETE FROM customers WHERE id = ?;
