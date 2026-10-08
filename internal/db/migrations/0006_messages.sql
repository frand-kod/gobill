-- F5 messages: per-customer inbox. Times = unix seconds UTC.

CREATE TABLE customers_inbox (
    id          INTEGER PRIMARY KEY,
    customer_id INTEGER NOT NULL REFERENCES customers (id) ON DELETE CASCADE,
    from_name   TEXT    NOT NULL DEFAULT 'System',
    subject     TEXT    NOT NULL DEFAULT '',
    body        TEXT    NOT NULL DEFAULT '',
    read_at     INTEGER,
    created_at  INTEGER NOT NULL DEFAULT (unixepoch())
);
CREATE INDEX customers_inbox_customer_idx ON customers_inbox (customer_id, id);
