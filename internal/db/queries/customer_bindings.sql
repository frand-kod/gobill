-- name: GetCustomerBinding :one
SELECT mac, port_id FROM customer_bindings WHERE customer_id = ?;

-- name: SetCustomerBinding :exec
INSERT INTO customer_bindings (customer_id, mac, port_id) VALUES (?, ?, ?)
ON CONFLICT (customer_id) DO UPDATE SET mac = excluded.mac, port_id = excluded.port_id;

-- name: DeleteCustomerBinding :exec
DELETE FROM customer_bindings WHERE customer_id = ?;
