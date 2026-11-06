-- Data and online time used in the subscription's usage window (since started_at). Quota reads these
-- counters, not radius_sessions: closed sessions are pruned by log retention, and a sum over them
-- shrank the used quota of every window longer than the retention. Accounting adds the growth of each
-- session that started inside the window; a new window (new started_at) starts again at 0.
ALTER TABLE subscriptions ADD COLUMN used_in_octets INTEGER NOT NULL DEFAULT 0;
ALTER TABLE subscriptions ADD COLUMN used_out_octets INTEGER NOT NULL DEFAULT 0;
ALTER TABLE subscriptions ADD COLUMN used_seconds INTEGER NOT NULL DEFAULT 0;

-- Active subscriptions start from what the sessions still hold, the same sum quota used before.
UPDATE subscriptions SET
  used_in_octets = (SELECT COALESCE(SUM(r.input_octets), 0) FROM radius_sessions r, customers c
    WHERE c.id = subscriptions.customer_id AND r.started_at >= subscriptions.started_at
      AND (r.username = c.username OR (c.pppoe_username <> '' AND c.pppoe_username <> c.username AND r.username = c.pppoe_username))),
  used_out_octets = (SELECT COALESCE(SUM(r.output_octets), 0) FROM radius_sessions r, customers c
    WHERE c.id = subscriptions.customer_id AND r.started_at >= subscriptions.started_at
      AND (r.username = c.username OR (c.pppoe_username <> '' AND c.pppoe_username <> c.username AND r.username = c.pppoe_username))),
  used_seconds = (SELECT COALESCE(SUM(COALESCE(r.stopped_at, r.updated_at) - r.started_at), 0) FROM radius_sessions r, customers c
    WHERE c.id = subscriptions.customer_id AND r.started_at >= subscriptions.started_at
      AND (r.username = c.username OR (c.pppoe_username <> '' AND c.pppoe_username <> c.username AND r.username = c.pppoe_username)))
WHERE status = 'active';
