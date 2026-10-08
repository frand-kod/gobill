-- name: CreateBandwidth :one
INSERT INTO bandwidths (name, rate_down, rate_down_unit, rate_up, rate_up_unit, burst)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetBandwidth :one
SELECT * FROM bandwidths WHERE id = ?;

-- name: ListBandwidths :many
SELECT * FROM bandwidths ORDER BY name LIMIT ? OFFSET ?;

-- name: SearchBandwidths :many
SELECT * FROM bandwidths WHERE name LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%' ORDER BY name LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: UpdateBandwidth :exec
UPDATE bandwidths SET name = ?, rate_down = ?, rate_down_unit = ?, rate_up = ?, rate_up_unit = ?, burst = ?
WHERE id = ?;

-- name: DeleteBandwidth :exec
DELETE FROM bandwidths WHERE id = ?;
