-- name: ListPromos :many
SELECT * FROM promos ORDER BY position, id;

-- name: GetPromo :one
SELECT * FROM promos WHERE id = ?;

-- name: MaxPromoPosition :one
SELECT CAST(COALESCE(MAX(position), 0) AS INTEGER) FROM promos;

-- name: CreatePromo :one
INSERT INTO promos (title, body, image, link, show_isolir, show_portal, active, starts_at, ends_at, position, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING *;

-- name: UpdatePromo :exec
UPDATE promos SET title = ?, body = ?, image = ?, link = ?, show_isolir = ?, show_portal = ?, active = ?,
  starts_at = ?, ends_at = ?, updated_at = ? WHERE id = ?;

-- name: SetPromoPosition :exec
UPDATE promos SET position = ? WHERE id = ?;

-- name: DeletePromo :exec
DELETE FROM promos WHERE id = ?;
