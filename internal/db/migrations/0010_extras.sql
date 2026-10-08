-- Custom balance top-up through the gateway: payment_requests.plan_id = 0 means "no plan, top up
-- `amount` into the balance". SQLite cannot drop a FK, so the table is rebuilt without the plan FK
-- (nothing references payment_requests). Cost: deleting a plan no longer blocks on old payments.
CREATE TABLE payment_requests_new (
    id          INTEGER PRIMARY KEY,
    ref         TEXT    NOT NULL UNIQUE,
    gateway     TEXT    NOT NULL DEFAULT 'tripay',
    gateway_ref TEXT    NOT NULL DEFAULT '',
    customer_id INTEGER NOT NULL REFERENCES customers (id) ON DELETE CASCADE,
    plan_id     INTEGER NOT NULL DEFAULT 0,
    amount      INTEGER NOT NULL CHECK (amount > 0),
    coupon      TEXT    NOT NULL DEFAULT '',
    channel     TEXT    NOT NULL DEFAULT '',
    pay_url     TEXT    NOT NULL DEFAULT '',
    status      TEXT    NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'paid', 'failed', 'expired')),
    created_at  INTEGER NOT NULL DEFAULT (unixepoch()),
    paid_at     INTEGER,
    expires_at  INTEGER NOT NULL
);
INSERT INTO payment_requests_new SELECT id, ref, gateway, gateway_ref, customer_id, plan_id, amount, coupon, channel, pay_url, status, created_at, paid_at, expires_at FROM payment_requests;
DROP TABLE payment_requests;
ALTER TABLE payment_requests_new RENAME TO payment_requests;
CREATE INDEX payment_requests_customer_idx ON payment_requests (customer_id, id);
CREATE INDEX payment_requests_status_idx ON payment_requests (status, expires_at);
