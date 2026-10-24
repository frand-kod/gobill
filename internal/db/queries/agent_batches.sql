-- name: CreateAgentBatch :one
INSERT INTO agent_batches (agent_id, plan_id, paid_count, bonus_count, unit_price, note, created_by)
VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: AddBatchVoucher :exec
INSERT INTO agent_batch_vouchers (voucher_id, batch_id) VALUES (?, ?);

-- name: AddAgentLedger :exec
INSERT INTO agent_ledger (batch_id, kind, amount, voucher_id, note, admin_id)
VALUES (sqlc.arg(batch_id), sqlc.arg(kind), sqlc.arg(amount), sqlc.narg(voucher_id), sqlc.arg(note), sqlc.narg(admin_id));

-- name: GetAgentBatch :one
SELECT * FROM agent_batches WHERE id = ?;

-- name: AgentBatchDebt :one
-- Debt of one batch, computed from the ledger.
SELECT CAST(COALESCE(SUM(CASE WHEN kind = 'debt' THEN amount ELSE -amount END), 0) AS INTEGER) AS debt
FROM agent_ledger WHERE batch_id = ?;

-- name: CountAgentReturns :one
SELECT COUNT(*) FROM agent_ledger WHERE batch_id = ? AND kind = 'return';

-- name: ReturnableBatchVoucher :one
-- The code belongs to the batch, is still unused and not yet returned: 1 row, else no rows.
SELECT v.id FROM agent_batch_vouchers bv JOIN vouchers v ON v.id = bv.voucher_id
WHERE bv.batch_id = ? AND v.id = ? AND v.status = 'unused'
  AND NOT EXISTS (SELECT 1 FROM agent_ledger l WHERE l.voucher_id = v.id);

-- name: ListAgentBatches :many
-- agent_id 0 = every agent. used = codes redeemed (a lower bound of what the agent sold),
-- returned = cancelled codes; the unused codes are paid + bonus - used - returned.
SELECT b.id, b.agent_id, a.username AS agent_name, p.name AS plan_name, b.paid_count, b.bonus_count, b.unit_price, b.note, b.created_at,
  CAST((SELECT COUNT(*) FROM agent_batch_vouchers bv JOIN vouchers v ON v.id = bv.voucher_id
        WHERE bv.batch_id = b.id AND v.status = 'used') AS INTEGER) AS used,
  CAST((SELECT COUNT(*) FROM agent_ledger l WHERE l.batch_id = b.id AND l.kind = 'return') AS INTEGER) AS returned,
  CAST((SELECT COALESCE(SUM(CASE WHEN l.kind = 'debt' THEN l.amount ELSE -l.amount END), 0) FROM agent_ledger l WHERE l.batch_id = b.id) AS INTEGER) AS debt
FROM agent_batches b JOIN admins a ON a.id = b.agent_id JOIN plans p ON p.id = b.plan_id
WHERE (CAST(sqlc.arg(agent_id) AS INTEGER) = 0 OR b.agent_id = sqlc.arg(agent_id))
  AND (CAST(sqlc.arg(batch_id) AS INTEGER) = 0 OR b.id = sqlc.arg(batch_id))
ORDER BY b.id DESC LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: CountAgentBatches :one
SELECT COUNT(*) FROM agent_batches WHERE CAST(sqlc.arg(agent_id) AS INTEGER) = 0 OR agent_id = sqlc.arg(agent_id);

-- name: ListBatchVouchers :many
SELECT v.id, v.code, v.status, v.used_at,
  CAST(EXISTS (SELECT 1 FROM agent_ledger l WHERE l.voucher_id = v.id) AS INTEGER) AS returned
FROM agent_batch_vouchers bv JOIN vouchers v ON v.id = bv.voucher_id
WHERE bv.batch_id = ? ORDER BY v.id LIMIT ? OFFSET ?;

-- name: CountBatchVouchers :one
SELECT COUNT(*) FROM agent_batch_vouchers WHERE batch_id = ?;

-- name: ListAgentPayments :many
-- Payments received in a period, for the operator's income view.
SELECT l.id, l.batch_id, l.amount, l.note, l.created_at, a.username AS agent_name
FROM agent_ledger l JOIN agent_batches b ON b.id = l.batch_id JOIN admins a ON a.id = b.agent_id
WHERE l.kind = 'payment' AND l.created_at >= sqlc.arg(from_ts) AND l.created_at < sqlc.arg(to_ts)
ORDER BY l.created_at, l.id;
