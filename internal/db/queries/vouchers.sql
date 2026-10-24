-- name: CreateVoucher :one
INSERT INTO vouchers (code, plan_id, generated_by) VALUES (?, ?, ?)
RETURNING *;

-- name: FindVoucherCodeFold :one
-- Case-insensitive: a voucher with this code exists (self-registration must not take a code as username).
SELECT id FROM vouchers WHERE lower(code) = lower(sqlc.arg(code)) LIMIT 1;

-- name: GetVoucher :one
SELECT * FROM vouchers WHERE id = ?;

-- name: GetVoucherByCode :one
-- A code the operator cancelled (agent return) no longer exists for redemption.
SELECT * FROM vouchers WHERE code = ?
  AND NOT EXISTS (SELECT 1 FROM agent_ledger l WHERE l.voucher_id = vouchers.id);

-- name: LastVoucherUsedBy :one
-- The voucher a customer redeemed last; a voucher account is one that redeemed a voucher.
SELECT v.code, p.name AS plan_name FROM vouchers v JOIN plans p ON p.id = v.plan_id
WHERE v.used_by = ? ORDER BY v.used_at DESC, v.id DESC LIMIT 1;

-- name: ListVouchers :many
SELECT * FROM vouchers ORDER BY id DESC LIMIT ? OFFSET ?;

-- name: SearchVouchers :many
SELECT * FROM vouchers WHERE code LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%'
  AND (CAST(sqlc.arg(status) AS TEXT) = '' OR status = sqlc.arg(status))
  AND (CAST(sqlc.arg(plan_id) AS INTEGER) = 0 OR plan_id = sqlc.arg(plan_id))
  AND NOT EXISTS (SELECT 1 FROM agent_ledger l WHERE l.voucher_id = vouchers.id)
ORDER BY id DESC LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: UseVoucher :execrows
-- Returns 1 if claimed, 0 if already used or missing.
UPDATE vouchers SET status = 'used', used_by = ?, used_at = unixepoch()
WHERE id = ? AND status = 'unused'
  AND NOT EXISTS (SELECT 1 FROM agent_ledger l WHERE l.voucher_id = vouchers.id);

-- name: DeleteVoucher :exec
DELETE FROM vouchers WHERE id = ? AND status = 'unused';

-- name: DeleteVouchersByIDs :execrows
-- Bulk delete from the list; like old PHP voucher-delete-many, used vouchers may go too (history lives in transactions).
DELETE FROM vouchers WHERE id IN (sqlc.slice('ids'));

-- name: DeleteOldUsedVouchers :execrows
-- Old PHP remove-voucher: used before the cutoff, unless a subscription still shows "Voucher - CODE" as its method.
DELETE FROM vouchers WHERE status = 'used' AND used_at < sqlc.arg(cutoff)
  AND NOT EXISTS (SELECT 1 FROM agent_batch_vouchers bv WHERE bv.voucher_id = vouchers.id)
  AND NOT EXISTS (SELECT 1 FROM subscriptions s WHERE s.method = 'Voucher - ' || vouchers.code);
