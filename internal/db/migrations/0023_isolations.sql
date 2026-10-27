-- Isolation is its own record, not a subscription on the isolir plan. The access state of an isolated
-- customer lives here: RADIUS, the /isolir page and the expiry job read it, and the plan list and the
-- subscription list no longer see it. ends_at is a real time, the isolir Forever date when isolated
-- until paid.
CREATE TABLE isolations (
    id          INTEGER PRIMARY KEY,
    customer_id INTEGER NOT NULL REFERENCES customers (id) ON DELETE CASCADE,
    started_at  INTEGER NOT NULL,
    ends_at     INTEGER NOT NULL CHECK (ends_at >= started_at),
    ended_at    INTEGER NOT NULL DEFAULT 0 CHECK (ended_at >= 0), -- 0 = running
    end_reason  TEXT    NOT NULL DEFAULT '' CHECK (end_reason IN ('', 'expired', 'paid', 'disabled'))
);
CREATE INDEX isolations_running_idx ON isolations (customer_id) WHERE ended_at = 0;

-- Isolations that were running as subscriptions on the isolir plan move over. Their subscriptions are
-- closed, so no active subscription is left on the isolir plan.
INSERT INTO isolations (customer_id, started_at, ends_at, ended_at, end_reason)
SELECT customer_id, started_at, expires_at, 0, ''
FROM subscriptions
WHERE status = 'active' AND plan_id = (SELECT CAST(value AS INTEGER) FROM settings WHERE key = 'isolir_plan_id')
  AND expires_at >= started_at;

UPDATE subscriptions SET status = 'expired'
WHERE status = 'active' AND plan_id = (SELECT CAST(value AS INTEGER) FROM settings WHERE key = 'isolir_plan_id');
