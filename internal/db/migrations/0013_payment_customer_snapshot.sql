-- Rebuild payment_requests (same pattern as 0010). customer_id becomes nullable with ON DELETE SET NULL,
-- so deleting a customer keeps the payment history; username is a snapshot of the customer's login name.
CREATE TABLE payment_requests_new (
    id          INTEGER PRIMARY KEY,
    ref         TEXT    NOT NULL UNIQUE,
    gateway     TEXT    NOT NULL DEFAULT 'tripay',
    gateway_ref TEXT    NOT NULL DEFAULT '',
    customer_id INTEGER REFERENCES customers (id) ON DELETE SET NULL,
    username    TEXT    NOT NULL DEFAULT '',
    plan_id     INTEGER NOT NULL DEFAULT 0 CHECK (plan_id >= 0),
    amount      INTEGER NOT NULL CHECK (amount > 0),
    coupon      TEXT    NOT NULL DEFAULT '',
    channel     TEXT    NOT NULL DEFAULT '',
    pay_url     TEXT    NOT NULL DEFAULT '',
    status      TEXT    NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'paid', 'failed', 'expired')),
    created_at  INTEGER NOT NULL DEFAULT (unixepoch()),
    paid_at     INTEGER,
    expires_at  INTEGER NOT NULL
);
-- Duplicate non-empty gateway_ref values would break the unique index below. The lowest id keeps its
-- gateway_ref; later duplicates are copied with gateway_ref = '' (it is only used to poll the gateway).
INSERT INTO payment_requests_new SELECT p.id, p.ref, p.gateway,
    CASE WHEN p.gateway_ref <> '' AND p.id <> (SELECT MIN(q.id) FROM payment_requests q WHERE q.gateway = p.gateway AND q.gateway_ref = p.gateway_ref)
         THEN '' ELSE p.gateway_ref END,
    p.customer_id, COALESCE(c.username, ''), p.plan_id, p.amount, p.coupon, p.channel, p.pay_url, p.status, p.created_at, p.paid_at, p.expires_at
FROM payment_requests p LEFT JOIN customers c ON c.id = p.customer_id;
DROP TABLE payment_requests;
ALTER TABLE payment_requests_new RENAME TO payment_requests;
CREATE INDEX payment_requests_customer_idx ON payment_requests (customer_id, id);
CREATE INDEX payment_requests_status_idx ON payment_requests (status, expires_at);
CREATE UNIQUE INDEX IF NOT EXISTS payment_requests_gateway_ref_uniq ON payment_requests (gateway, gateway_ref) WHERE gateway_ref <> '';
