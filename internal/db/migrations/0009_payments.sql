-- F4 online payment (Tripay). Times = unix seconds UTC. Money = rupiah.
-- ref is our merchant reference; gateway_ref is the gateway's own id (needed to poll status).
CREATE TABLE payment_requests (
    id          INTEGER PRIMARY KEY,
    ref         TEXT    NOT NULL UNIQUE,
    gateway     TEXT    NOT NULL DEFAULT 'tripay',
    gateway_ref TEXT    NOT NULL DEFAULT '',
    customer_id INTEGER NOT NULL REFERENCES customers (id) ON DELETE CASCADE,
    plan_id     INTEGER NOT NULL REFERENCES plans (id) ON DELETE RESTRICT,
    amount      INTEGER NOT NULL CHECK (amount > 0),
    coupon      TEXT    NOT NULL DEFAULT '',
    channel     TEXT    NOT NULL DEFAULT '',
    pay_url     TEXT    NOT NULL DEFAULT '',
    status      TEXT    NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'paid', 'failed', 'expired')),
    created_at  INTEGER NOT NULL DEFAULT (unixepoch()),
    paid_at     INTEGER,
    expires_at  INTEGER NOT NULL
);
CREATE INDEX payment_requests_customer_idx ON payment_requests (customer_id, id);
CREATE INDEX payment_requests_status_idx ON payment_requests (status, expires_at);
