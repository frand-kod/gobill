-- Router removals that failed (router down): retried by a job with backoff until they work.
-- payload is JSON {"plan_id":N,"pppoe_username":"..."}; one row per (router, kind, user, payload).
CREATE TABLE router_pending (
    id         INTEGER PRIMARY KEY,
    router_id  INTEGER NOT NULL REFERENCES routers (id) ON DELETE CASCADE,
    kind       TEXT NOT NULL,
    username   TEXT NOT NULL,
    payload    TEXT NOT NULL DEFAULT '{}',
    attempts   INTEGER NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    tried_at   INTEGER NOT NULL DEFAULT 0,
    UNIQUE (router_id, kind, username, payload)
);

-- A deleted router leaves no per-router setting rows behind (WireGuard provisioning, RADIUS migration).
CREATE TRIGGER routers_drop_settings AFTER DELETE ON routers
BEGIN
    DELETE FROM settings WHERE key IN ('wg_pending_' || OLD.id, 'wg_prev_' || OLD.id, 'radius_mig_disabled_' || OLD.id);
END;
