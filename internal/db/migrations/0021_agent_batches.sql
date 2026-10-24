-- Agent consignment: a batch of vouchers handed to one Agent (admins.role = 'Agent') who pays later.
-- Debt is never stored as a balance: agent_ledger is append-only and debt = SUM(debt) - SUM(payment) - SUM(return).
CREATE TABLE agent_batches (
    id          INTEGER PRIMARY KEY,
    agent_id    INTEGER NOT NULL REFERENCES admins (id) ON DELETE RESTRICT,
    plan_id     INTEGER NOT NULL REFERENCES plans (id) ON DELETE RESTRICT,
    paid_count  INTEGER NOT NULL CHECK (paid_count >= 0),
    bonus_count INTEGER NOT NULL CHECK (bonus_count >= 0),
    unit_price  INTEGER NOT NULL CHECK (unit_price >= 0), -- rupiah per paid voucher
    note        TEXT    NOT NULL DEFAULT '',
    created_by  INTEGER REFERENCES admins (id) ON DELETE SET NULL,
    created_at  INTEGER NOT NULL DEFAULT (unixepoch()),
    CHECK (paid_count + bonus_count > 0)
);
CREATE INDEX agent_batches_agent_idx ON agent_batches (agent_id, id);

-- The codes of a batch (paid and bonus alike). Existing vouchers stay untouched.
CREATE TABLE agent_batch_vouchers (
    voucher_id INTEGER PRIMARY KEY REFERENCES vouchers (id) ON DELETE RESTRICT,
    batch_id   INTEGER NOT NULL REFERENCES agent_batches (id) ON DELETE RESTRICT
);
CREATE INDEX agent_batch_vouchers_batch_idx ON agent_batch_vouchers (batch_id);

CREATE TABLE agent_ledger (
    id         INTEGER PRIMARY KEY,
    batch_id   INTEGER NOT NULL REFERENCES agent_batches (id) ON DELETE RESTRICT,
    kind       TEXT    NOT NULL CHECK (kind IN ('debt', 'payment', 'return')),
    amount     INTEGER NOT NULL CHECK (amount >= 0 AND (kind <> 'payment' OR amount > 0)),
    -- A returned (cancelled) code; set exactly on 'return' rows, and a code returns once.
    voucher_id INTEGER REFERENCES vouchers (id) ON DELETE RESTRICT,
    note       TEXT    NOT NULL DEFAULT '',
    admin_id   INTEGER REFERENCES admins (id) ON DELETE SET NULL,
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    CHECK ((kind = 'return') = (voucher_id IS NOT NULL))
);
CREATE INDEX agent_ledger_batch_idx ON agent_ledger (batch_id, kind);
CREATE UNIQUE INDEX agent_ledger_voucher_idx ON agent_ledger (voucher_id) WHERE voucher_id IS NOT NULL;
CREATE INDEX agent_ledger_payment_idx ON agent_ledger (created_at) WHERE kind = 'payment';

-- Append-only: rows never change or go (admin_id may be nulled by the FK when an admin is deleted).
CREATE TRIGGER agent_ledger_no_update BEFORE UPDATE OF batch_id, kind, amount, voucher_id, note, created_at ON agent_ledger
BEGIN SELECT RAISE(ABORT, 'agent_ledger is append-only'); END;
CREATE TRIGGER agent_ledger_no_delete BEFORE DELETE ON agent_ledger
BEGIN SELECT RAISE(ABORT, 'agent_ledger is append-only'); END;

-- Debt never goes negative, a return is one code of its own batch at the unit price, and at most paid_count return.
CREATE TRIGGER agent_ledger_credit BEFORE INSERT ON agent_ledger WHEN NEW.kind <> 'debt'
BEGIN
    SELECT RAISE(ABORT, 'credit exceeds debt') WHERE NEW.amount >
        (SELECT COALESCE(SUM(CASE WHEN kind = 'debt' THEN amount ELSE -amount END), 0) FROM agent_ledger WHERE batch_id = NEW.batch_id);
    SELECT RAISE(ABORT, 'return does not match batch') WHERE NEW.kind = 'return' AND NOT EXISTS
        (SELECT 1 FROM agent_batch_vouchers v JOIN agent_batches b ON b.id = v.batch_id
         WHERE v.voucher_id = NEW.voucher_id AND v.batch_id = NEW.batch_id AND b.unit_price = NEW.amount);
    SELECT RAISE(ABORT, 'returns exceed paid count') WHERE NEW.kind = 'return' AND
        (SELECT COUNT(*) FROM agent_ledger WHERE batch_id = NEW.batch_id AND kind = 'return') >=
        (SELECT paid_count FROM agent_batches WHERE id = NEW.batch_id);
END;
