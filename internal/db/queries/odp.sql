-- name: CreateODP :one
INSERT INTO odps (name, coordinates, address, port_amount, attenuation, coverage, description, router_id)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetODP :one
SELECT * FROM odps WHERE id = ?;

-- name: SearchODPs :many
SELECT * FROM odps WHERE name LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%' ORDER BY name LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: UpdateODP :exec
UPDATE odps SET name = ?, coordinates = ?, address = ?, port_amount = ?, attenuation = ?, coverage = ?, description = ?, router_id = ?
WHERE id = ?;

-- name: DeleteODP :exec
DELETE FROM odps WHERE id = ?;

-- name: ListODPMarkers :many
SELECT * FROM odps WHERE coordinates != '' ORDER BY name;

-- name: ListRouterMarkers :many
SELECT id, name, host, description, coverage, enabled, coordinates FROM routers WHERE coordinates != '' ORDER BY name;

-- name: ListCustomerMarkers :many
SELECT c.id, c.fullname, c.status, c.coordinates, CAST(COALESCE(MAX(p.name), '') AS TEXT) AS plan_name
FROM customers c
LEFT JOIN subscriptions s ON s.customer_id = c.id AND s.status = 'active'
LEFT JOIN plans p ON p.id = s.plan_id
WHERE c.coordinates != ''
GROUP BY c.id
ORDER BY c.id;
