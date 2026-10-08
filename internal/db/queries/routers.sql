-- name: CreateRouter :one
INSERT INTO routers (name, host, port, username, password_enc, description, enabled)
VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetRouter :one
SELECT * FROM routers WHERE id = ?;

-- name: ListRouters :many
SELECT * FROM routers ORDER BY name LIMIT ? OFFSET ?;

-- name: ListEnabledRouters :many
SELECT * FROM routers WHERE enabled = 1 ORDER BY name;

-- name: SearchRouters :many
SELECT * FROM routers
WHERE name LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%' OR host LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%'
ORDER BY name LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: UpdateRouter :exec
UPDATE routers SET name = ?, host = ?, port = ?, username = ?, password_enc = ?, description = ?, enabled = ?
WHERE id = ?;

-- name: DeleteRouter :exec
DELETE FROM routers WHERE id = ?;
