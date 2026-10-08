-- name: CreateCoupon :one
INSERT INTO coupons (code, type, value, description, max_usage, min_order, max_discount, start_date, end_date, status)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING *;

-- name: GetCoupon :one
SELECT * FROM coupons WHERE id = ?;

-- name: GetCouponByCode :one
SELECT * FROM coupons WHERE code = ?;

-- name: SearchCoupons :many
SELECT * FROM coupons WHERE code LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%' OR description LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%'
ORDER BY id DESC LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: UpdateCoupon :exec
UPDATE coupons SET code = ?, type = ?, value = ?, description = ?, max_usage = ?, min_order = ?, max_discount = ?,
  start_date = ?, end_date = ?, status = ? WHERE id = ?;

-- name: ToggleCoupon :exec
UPDATE coupons SET status = CASE status WHEN 'active' THEN 'inactive' ELSE 'active' END WHERE id = ?;

-- name: DeleteCoupon :exec
DELETE FROM coupons WHERE id = ?;

-- name: UseCoupon :execrows
UPDATE coupons SET used = used + 1 WHERE id = ? AND (max_usage = 0 OR used < max_usage);

-- name: LockCouponByCode :execrows
-- no-op write: takes the SQLite write lock first so a deferred tx never upgrades from a stale read (SQLITE_BUSY)
UPDATE coupons SET used = used WHERE code = ?;
