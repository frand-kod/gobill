-- F1 billing schema. Money = INTEGER rupiah, time = unix seconds UTC.

CREATE TABLE bandwidths (
    id             INTEGER PRIMARY KEY,
    name           TEXT    NOT NULL UNIQUE,
    rate_down      INTEGER NOT NULL CHECK (rate_down > 0),
    rate_down_unit TEXT    NOT NULL CHECK (rate_down_unit IN ('Kbps', 'Mbps')),
    rate_up        INTEGER NOT NULL CHECK (rate_up > 0),
    rate_up_unit   TEXT    NOT NULL CHECK (rate_up_unit IN ('Kbps', 'Mbps')),
    burst          TEXT    NOT NULL DEFAULT ''
);

CREATE TABLE routers (
    id           INTEGER PRIMARY KEY,
    name         TEXT    NOT NULL UNIQUE,
    host         TEXT    NOT NULL,
    port         INTEGER NOT NULL DEFAULT 8728 CHECK (port BETWEEN 1 AND 65535),
    username     TEXT    NOT NULL,
    password_enc BLOB    NOT NULL,
    description  TEXT    NOT NULL DEFAULT '',
    enabled      INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1))
);

CREATE TABLE pools (
    id        INTEGER PRIMARY KEY,
    name      TEXT    NOT NULL,
    local_ip  TEXT    NOT NULL DEFAULT '',
    range_ip  TEXT    NOT NULL,
    router_id INTEGER NOT NULL REFERENCES routers (id) ON DELETE CASCADE,
    UNIQUE (router_id, name)
);

CREATE TABLE plans (
    id            INTEGER PRIMARY KEY,
    name          TEXT    NOT NULL UNIQUE,
    type          TEXT    NOT NULL CHECK (type IN ('Hotspot', 'PPPoE', 'Balance')),
    billing       TEXT    NOT NULL DEFAULT 'prepaid' CHECK (billing IN ('prepaid', 'postpaid')),
    price         INTEGER NOT NULL CHECK (price >= 0),
    validity      INTEGER NOT NULL CHECK (validity > 0),
    validity_unit TEXT    NOT NULL CHECK (validity_unit IN ('Mins', 'Hrs', 'Days', 'Months', 'Period')),
    -- Hotspot quota; limited=0 means unlimited (old typebp).
    limited       INTEGER NOT NULL DEFAULT 0 CHECK (limited IN (0, 1)),
    limit_type    TEXT    CHECK (limit_type IN ('Time_Limit', 'Data_Limit', 'Both_Limit')),
    time_limit    INTEGER CHECK (time_limit > 0),
    time_unit     TEXT    CHECK (time_unit IN ('Mins', 'Hrs')),
    data_limit    INTEGER CHECK (data_limit > 0),
    data_unit     TEXT    CHECK (data_unit IN ('MB', 'GB')),
    shared_users  INTEGER CHECK (shared_users > 0),
    -- bandwidth NULL only for Balance plans; router NULL only for Radius plans (no router touched).
    bandwidth_id  INTEGER REFERENCES bandwidths (id) ON DELETE RESTRICT,
    router_id     INTEGER REFERENCES routers (id) ON DELETE RESTRICT,
    pool_id       INTEGER REFERENCES pools (id) ON DELETE RESTRICT,
    -- Profile an expired customer is moved to instead of being removed (old plan_expired).
    expired_plan_id INTEGER REFERENCES plans (id) ON DELETE SET NULL,
    -- Postpaid Period plans: day of month the bill is due (old expired_date).
    billing_day   INTEGER CHECK (billing_day BETWEEN 1 AND 31),
    on_login      TEXT    NOT NULL DEFAULT '',
    on_logout     TEXT    NOT NULL DEFAULT '',
    -- Driver name from system/devices; '' for Balance plans.
    device        TEXT    NOT NULL DEFAULT '' CHECK (device IN ('', 'MikrotikHotspot', 'MikrotikPppoe', 'Dummy', 'Radius')),
    enabled       INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    CHECK (type = 'Balance' OR (bandwidth_id IS NOT NULL AND (router_id IS NOT NULL OR device = 'Radius')))
);
CREATE INDEX plans_router_idx ON plans (router_id);

CREATE TABLE customers (
    id            INTEGER PRIMARY KEY,
    username      TEXT    NOT NULL UNIQUE,
    password_hash TEXT    NOT NULL, -- bcrypt, portal login
    fullname      TEXT    NOT NULL,
    address       TEXT    NOT NULL DEFAULT '',
    phone         TEXT    NOT NULL DEFAULT '',
    email         TEXT    NOT NULL DEFAULT '',
    balance       INTEGER NOT NULL DEFAULT 0 CHECK (balance >= 0),
    service_type  TEXT    NOT NULL DEFAULT 'Others' CHECK (service_type IN ('Hotspot', 'PPPoE', 'Others')),
    pppoe_username TEXT   NOT NULL DEFAULT '',
    pppoe_ip      TEXT    NOT NULL DEFAULT '',
    secret_enc    BLOB,             -- router-side hotspot/PPPoE password, AES-GCM
    billing_day   INTEGER CHECK (billing_day BETWEEN 1 AND 31), -- overrides plans.billing_day
    auto_renewal  INTEGER NOT NULL DEFAULT 1 CHECK (auto_renewal IN (0, 1)),
    status        TEXT    NOT NULL DEFAULT 'Active' CHECK (status IN ('Active', 'Banned', 'Disabled', 'Inactive', 'Limited', 'Suspended')),
    created_by    INTEGER REFERENCES admins (id) ON DELETE SET NULL,
    created_at    INTEGER NOT NULL DEFAULT (unixepoch()),
    last_login_at INTEGER
);
CREATE INDEX customers_fullname_idx ON customers (fullname);
CREATE INDEX customers_phone_idx ON customers (phone);

CREATE TABLE subscriptions (
    id          INTEGER PRIMARY KEY,
    customer_id INTEGER NOT NULL REFERENCES customers (id) ON DELETE CASCADE,
    plan_id     INTEGER NOT NULL REFERENCES plans (id) ON DELETE RESTRICT,
    router_id   INTEGER REFERENCES routers (id) ON DELETE RESTRICT, -- NULL for Radius plans
    type        TEXT    NOT NULL CHECK (type IN ('Hotspot', 'PPPoE')),
    started_at  INTEGER NOT NULL,
    expires_at  INTEGER NOT NULL,
    status      TEXT    NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'expired')),
    method      TEXT    NOT NULL DEFAULT '',
    admin_id    INTEGER REFERENCES admins (id) ON DELETE SET NULL,
    CHECK (expires_at >= started_at)
);
CREATE INDEX subscriptions_status_expires_idx ON subscriptions (status, expires_at);
-- Old Package.php: one row per customer + router + type; recharge updates it.
CREATE UNIQUE INDEX subscriptions_active_uniq ON subscriptions (customer_id, COALESCE(router_id, 0), type) WHERE status = 'active';
CREATE INDEX subscriptions_customer_idx ON subscriptions (customer_id);

CREATE TABLE transactions (
    id           INTEGER PRIMARY KEY,
    invoice      TEXT    NOT NULL UNIQUE,
    customer_id  INTEGER NOT NULL REFERENCES customers (id) ON DELETE RESTRICT,
    plan_id      INTEGER REFERENCES plans (id) ON DELETE SET NULL,
    username     TEXT    NOT NULL, -- snapshots: survive customer/plan edits
    plan_name    TEXT    NOT NULL,
    router_name  TEXT    NOT NULL DEFAULT '',
    type         TEXT    NOT NULL CHECK (type IN ('Hotspot', 'PPPoE', 'Balance')),
    price        INTEGER NOT NULL CHECK (price >= 0),
    method       TEXT    NOT NULL DEFAULT '',
    note         TEXT    NOT NULL DEFAULT '',
    admin_id     INTEGER REFERENCES admins (id) ON DELETE SET NULL,
    created_at   INTEGER NOT NULL DEFAULT (unixepoch()),
    period_start INTEGER NOT NULL,
    period_end   INTEGER NOT NULL
);
CREATE INDEX transactions_customer_idx ON transactions (customer_id, created_at);
CREATE INDEX transactions_created_idx ON transactions (created_at);

CREATE TABLE vouchers (
    id           INTEGER PRIMARY KEY,
    code         TEXT    NOT NULL UNIQUE,
    plan_id      INTEGER NOT NULL REFERENCES plans (id) ON DELETE RESTRICT,
    status       TEXT    NOT NULL DEFAULT 'unused' CHECK (status IN ('unused', 'used')),
    used_by      INTEGER REFERENCES customers (id) ON DELETE SET NULL,
    used_at      INTEGER,
    generated_by INTEGER REFERENCES admins (id) ON DELETE SET NULL,
    created_at   INTEGER NOT NULL DEFAULT (unixepoch()),
    CHECK ((status = 'used') = (used_at IS NOT NULL))
);
CREATE INDEX vouchers_plan_status_idx ON vouchers (plan_id, status);

CREATE TABLE activity_logs (
    id          INTEGER PRIMARY KEY,
    actor_type  TEXT    NOT NULL CHECK (actor_type IN ('admin', 'customer', 'system')),
    actor_id    INTEGER NOT NULL DEFAULT 0, -- polymorphic, so no FK
    action      TEXT    NOT NULL,
    description TEXT    NOT NULL DEFAULT '',
    ip          TEXT    NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL DEFAULT (unixepoch())
);
CREATE INDEX activity_logs_created_idx ON activity_logs (created_at);
