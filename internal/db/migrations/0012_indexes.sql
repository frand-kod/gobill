-- Indexes for the hot RADIUS, portal and log queries. Partial indexes keep the NULL/empty rows out.
CREATE INDEX customers_pppoe_username_idx ON customers (pppoe_username) WHERE pppoe_username <> '';
CREATE INDEX radius_sessions_user_started_idx ON radius_sessions (username, started_at);
CREATE INDEX radius_sessions_open_user_idx ON radius_sessions (username, updated_at) WHERE stopped_at IS NULL;
CREATE INDEX radius_sessions_open_updated_idx ON radius_sessions (updated_at) WHERE stopped_at IS NULL;
CREATE INDEX radius_sessions_stopped_idx ON radius_sessions (stopped_at) WHERE stopped_at IS NOT NULL;
CREATE INDEX radius_sessions_started_idx ON radius_sessions (started_at, id);
DROP INDEX radius_sessions_user_idx; -- replaced by radius_sessions_user_started_idx and radius_sessions_open_user_idx
CREATE INDEX subscriptions_plan_idx ON subscriptions (plan_id);
CREATE INDEX subscriptions_router_idx ON subscriptions (router_id) WHERE router_id IS NOT NULL;
CREATE INDEX subscriptions_method_idx ON subscriptions (method);
CREATE INDEX vouchers_used_by_idx ON vouchers (used_by) WHERE used_by IS NOT NULL;
CREATE INDEX vouchers_status_used_idx ON vouchers (status, used_at);
CREATE INDEX transactions_plan_idx ON transactions (plan_id);
CREATE INDEX customers_inbox_read_idx ON customers_inbox (read_at) WHERE read_at IS NOT NULL;
DROP INDEX customers_fullname_idx; -- LIKE '%x%' searches cannot use it
DROP INDEX customers_phone_idx;
