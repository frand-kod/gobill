-- name: RenameCustomer :exec
UPDATE customers SET username = ? WHERE id = ?;

-- name: FindLoginNameOwner :one
-- Another customer that already uses name as username or pppoe_username (RADIUS accepts either).
SELECT id FROM customers
WHERE id <> sqlc.arg(self) AND (username = sqlc.arg(name) OR (pppoe_username <> '' AND pppoe_username = sqlc.arg(name)))
LIMIT 1;
