-- Post-auth log: one row per Access-Request decision (not Test login). Pruned by log_keep_days.
CREATE TABLE radius_auth_log (
    id       INTEGER PRIMARY KEY,
    at       INTEGER NOT NULL,
    username TEXT    NOT NULL,
    nas_ip   TEXT    NOT NULL DEFAULT '',
    mac      TEXT    NOT NULL DEFAULT '',
    accepted INTEGER NOT NULL,
    reason   TEXT    NOT NULL DEFAULT '', -- short class of a reject: bad_password, expired, limit, voucher, binding, other
    message  TEXT    NOT NULL DEFAULT ''  -- the Reply-Message of a reject
);
CREATE INDEX radius_auth_log_at_idx ON radius_auth_log (at);
