-- name: CreateVoucher :one
INSERT INTO vouchers (code, plan_id, generated_by) VALUES (?, ?, ?)
RETURNING *;

-- name: GetVoucher :one
SELECT * FROM vouchers WHERE id = ?;

-- name: GetVoucherByCode :one
SELECT * FROM vouchers WHERE code = ?;

-- name: ListVouchers :many
SELECT * FROM vouchers ORDER BY id DESC LIMIT ? OFFSET ?;

-- name: SearchVouchers :many
SELECT * FROM vouchers WHERE code LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%'
  AND (CAST(sqlc.arg(status) AS TEXT) = '' OR status = sqlc.arg(status))
  AND (CAST(sqlc.arg(plan_id) AS INTEGER) = 0 OR plan_id = sqlc.arg(plan_id))
ORDER BY id DESC LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: UseVoucher :execrows
-- Returns 1 if claimed, 0 if already used or missing.
UPDATE vouchers SET status = 'used', used_by = ?, used_at = unixepoch()
WHERE id = ? AND status = 'unused';

-- name: DeleteVoucher :exec
DELETE FROM vouchers WHERE id = ? AND status = 'unused';
