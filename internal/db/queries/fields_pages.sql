-- name: ListCustomFields :many
SELECT * FROM custom_fields ORDER BY sort_order, id;

-- name: GetCustomField :one
SELECT * FROM custom_fields WHERE id = ?;

-- name: CreateCustomField :one
INSERT INTO custom_fields (name, type, options, required, sort_order)
VALUES (?, ?, ?, ?, ?)
RETURNING *;

-- name: UpdateCustomField :exec
UPDATE custom_fields SET name = ?, type = ?, options = ?, required = ?, sort_order = ?
WHERE id = ?;

-- name: DeleteCustomField :exec
DELETE FROM custom_fields WHERE id = ?;

-- name: ListCustomerFieldValues :many
SELECT * FROM customer_field_values WHERE customer_id = ?;

-- name: UpsertCustomerFieldValue :exec
INSERT INTO customer_field_values (customer_id, field_id, value) VALUES (?, ?, ?)
ON CONFLICT (customer_id, field_id) DO UPDATE SET value = excluded.value;

-- name: GetPage :one
SELECT * FROM pages WHERE slug = ?;

-- name: UpdatePageBody :exec
UPDATE pages SET body = ? WHERE slug = ?;
