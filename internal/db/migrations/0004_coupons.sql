CREATE TABLE coupons (
    id           INTEGER PRIMARY KEY,
    code         TEXT    NOT NULL UNIQUE,
    type         TEXT    NOT NULL CHECK (type IN ('fixed', 'percent')),
    value        INTEGER NOT NULL CHECK (value > 0),
    description  TEXT    NOT NULL DEFAULT '',
    max_usage    INTEGER NOT NULL DEFAULT 0 CHECK (max_usage >= 0), -- 0 = unlimited
    used         INTEGER NOT NULL DEFAULT 0 CHECK (used >= 0),
    min_order    INTEGER NOT NULL DEFAULT 0 CHECK (min_order >= 0),
    max_discount INTEGER NOT NULL DEFAULT 0 CHECK (max_discount >= 0), -- percent cap, 0 = none
    start_date   TEXT    NOT NULL, -- YYYY-MM-DD, inclusive
    end_date     TEXT    NOT NULL,
    status       TEXT    NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive')),
    created_at   INTEGER NOT NULL DEFAULT (unixepoch())
);
