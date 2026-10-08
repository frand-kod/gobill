-- name: CreatePool :one
INSERT INTO pools (name, local_ip, range_ip, router_id) VALUES (?, ?, ?, ?)
RETURNING *;

-- name: GetPool :one
SELECT * FROM pools WHERE id = ?;

-- name: ListPools :many
SELECT * FROM pools ORDER BY name LIMIT ? OFFSET ?;

-- name: ListPoolsByRouter :many
SELECT * FROM pools WHERE router_id = ? ORDER BY name;

-- name: SearchPools :many
SELECT * FROM pools WHERE name LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%' ORDER BY name LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: UpdatePool :exec
UPDATE pools SET name = ?, local_ip = ?, range_ip = ?, router_id = ? WHERE id = ?;

-- name: DeletePool :exec
DELETE FROM pools WHERE id = ?;
