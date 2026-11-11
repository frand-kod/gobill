-- name: GetCustomerForRadius :one
SELECT * FROM customers
WHERE (username = sqlc.arg(name) OR (pppoe_username <> '' AND pppoe_username = sqlc.arg(name))) AND status = 'Active'
LIMIT 1;

-- name: GetRadiusPlan :one
SELECT s.started_at, s.quota_carry_bytes, s.quota_carry_seconds, s.used_in_octets, s.used_out_octets, s.used_seconds, s.expires_at, s.pending_start, s.plan_id, p.name AS plan_name, p.type AS plan_type, p.limited, p.limit_type,
       p.time_limit, p.time_unit, p.data_limit, p.data_unit, p.shared_users, p.idle_timeout, p.login_window, p.boost_window,
       b.rate_down, b.rate_down_unit, b.rate_up, b.rate_up_unit, b.burst,
       fb.rate_down AS fup_rate_down, fb.rate_down_unit AS fup_rate_down_unit, fb.rate_up AS fup_rate_up, fb.rate_up_unit AS fup_rate_up_unit, fb.burst AS fup_burst,
       xb.rate_down AS boost_rate_down, xb.rate_down_unit AS boost_rate_down_unit, xb.rate_up AS boost_rate_up, xb.rate_up_unit AS boost_rate_up_unit, xb.burst AS boost_burst,
       pl.name AS pool_name, COALESCE(cb.mac, '') AS bind_mac, COALESCE(cb.port_id, '') AS bind_port_id
FROM subscriptions s
JOIN plans p ON p.id = s.plan_id
LEFT JOIN customer_bindings cb ON cb.customer_id = s.customer_id
LEFT JOIN bandwidths b ON b.id = p.bandwidth_id
LEFT JOIN bandwidths fb ON fb.id = p.fup_bandwidth_id AND p.device = 'Radius'
LEFT JOIN bandwidths xb ON xb.id = p.boost_bandwidth_id AND p.device = 'Radius'
LEFT JOIN pools pl ON pl.id = p.pool_id
WHERE s.customer_id = ? AND s.status = 'active'
ORDER BY s.expires_at DESC LIMIT 1;

-- name: GetRadiusLogin :one
-- GetCustomerForRadius and GetRadiusPlan in one statement for the common case, an active customer with
-- an active plan: one read transaction per login instead of two. No row: use the two queries.
SELECT sqlc.embed(c), s.started_at, s.quota_carry_bytes, s.quota_carry_seconds, s.used_in_octets, s.used_out_octets, s.used_seconds, s.expires_at, s.pending_start, s.plan_id, p.name AS plan_name, p.type AS plan_type, p.limited, p.limit_type,
       p.time_limit, p.time_unit, p.data_limit, p.data_unit, p.shared_users, p.idle_timeout, p.login_window, p.boost_window,
       b.rate_down, b.rate_down_unit, b.rate_up, b.rate_up_unit, b.burst,
       fb.rate_down AS fup_rate_down, fb.rate_down_unit AS fup_rate_down_unit, fb.rate_up AS fup_rate_up, fb.rate_up_unit AS fup_rate_up_unit, fb.burst AS fup_burst,
       xb.rate_down AS boost_rate_down, xb.rate_down_unit AS boost_rate_down_unit, xb.rate_up AS boost_rate_up, xb.rate_up_unit AS boost_rate_up_unit, xb.burst AS boost_burst,
       pl.name AS pool_name, COALESCE(cb.mac, '') AS bind_mac, COALESCE(cb.port_id, '') AS bind_port_id
FROM customers c
LEFT JOIN customer_bindings cb ON cb.customer_id = c.id
JOIN subscriptions s ON s.id = (SELECT id FROM subscriptions WHERE customer_id = c.id AND status = 'active' ORDER BY expires_at DESC LIMIT 1)
JOIN plans p ON p.id = s.plan_id
LEFT JOIN bandwidths b ON b.id = p.bandwidth_id
LEFT JOIN bandwidths fb ON fb.id = p.fup_bandwidth_id AND p.device = 'Radius'
LEFT JOIN bandwidths xb ON xb.id = p.boost_bandwidth_id AND p.device = 'Radius'
LEFT JOIN pools pl ON pl.id = p.pool_id
WHERE (c.username = sqlc.arg(name) OR (c.pppoe_username <> '' AND c.pppoe_username = sqlc.arg(name))) AND c.status = 'Active'
LIMIT 1;

-- name: CountLiveRadiusSessions :one
-- Open sessions updated at or after the cutoff (older ones are treated as lost by the NAS).
SELECT COUNT(*) FROM radius_sessions WHERE stopped_at IS NULL AND updated_at >= ?;

-- name: ListOtherOpenRadiusSessions :many
-- Open, recently updated sessions of the user that are not the requesting device (same framed IP or MAC), oldest first.
SELECT * FROM radius_sessions
WHERE username = ? AND stopped_at IS NULL AND updated_at >= ?
  AND (framed_ip <> ? OR ? = '')
  AND (mac <> ? OR ? = '')
ORDER BY started_at ASC, id ASC;

-- name: StopRadiusSession :exec
UPDATE radius_sessions SET stopped_at = ? WHERE id = ? AND stopped_at IS NULL;

-- name: UpsertRadiusSession :exec
INSERT INTO radius_sessions (session_id, username, nas_ip, nas_ip_attr, nas_identifier, framed_ip, mac, started_at, updated_at, stopped_at, input_octets, output_octets, terminate_cause, nas_port_id)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (nas_ip, session_id) DO UPDATE SET
    nas_ip_attr = COALESCE(NULLIF(excluded.nas_ip_attr, ''), radius_sessions.nas_ip_attr),
    nas_identifier = COALESCE(NULLIF(excluded.nas_identifier, ''), radius_sessions.nas_identifier),
    nas_port_id = COALESCE(NULLIF(excluded.nas_port_id, ''), radius_sessions.nas_port_id),
    framed_ip = excluded.framed_ip, updated_at = excluded.updated_at,
    stopped_at = COALESCE(radius_sessions.stopped_at, excluded.stopped_at),
    terminate_cause = COALESCE(NULLIF(excluded.terminate_cause, ''), radius_sessions.terminate_cause),
    input_octets = MAX(radius_sessions.input_octets, excluded.input_octets), output_octets = MAX(radius_sessions.output_octets, excluded.output_octets);

-- name: CloseRadiusSessionsByNAS :exec
-- Accounting-On/Off: close what the NAS lost, but not sessions it updated after the cutoff. The NAS sends
-- Accounting-On and the first Start together, and packets are handled concurrently.
UPDATE radius_sessions SET stopped_at = ? WHERE nas_ip = ? AND stopped_at IS NULL AND updated_at < ?;

-- name: CloseStaleRadiusSessions :execrows
-- Open sessions whose last update is older than the cutoff: the NAS lost them. Stop them at their last update.
UPDATE radius_sessions SET stopped_at = updated_at WHERE stopped_at IS NULL AND updated_at < ?;

-- name: SearchOpenRadiusSessions :many
SELECT * FROM radius_sessions
WHERE stopped_at IS NULL
  AND (username LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%' OR framed_ip LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%' OR mac LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%')
ORDER BY started_at DESC LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: GetRadiusSession :one
SELECT * FROM radius_sessions WHERE id = ?;

-- name: ListOpenRadiusSessionsByUser :many
SELECT * FROM radius_sessions WHERE username = ? AND stopped_at IS NULL;

-- name: GetOpenRadiusSessionByFramedIP :one
-- The most recently started open session on a client IP (the /isolir page identifies the customer by it).
SELECT * FROM radius_sessions WHERE framed_ip = ? AND stopped_at IS NULL ORDER BY started_at DESC, id DESC LIMIT 1;

-- name: ListRecentRadiusSessionsByUser :many
SELECT * FROM radius_sessions WHERE username = ? ORDER BY started_at DESC LIMIT 10;

-- name: SearchRadiusLogs :many
-- All sessions, open and closed. from_ts/to_ts filter started_at (0 = open). page_limit -1 = all (CSV).
SELECT * FROM radius_sessions
WHERE (username LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%' OR nas_ip LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%')
  AND (CAST(sqlc.arg(from_ts) AS INTEGER) = 0 OR started_at >= sqlc.arg(from_ts))
  AND (CAST(sqlc.arg(to_ts) AS INTEGER) = 0 OR started_at < sqlc.arg(to_ts))
ORDER BY started_at DESC, id DESC LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: SumRadiusSessionTime :one
-- Seconds the user was online since from_ts (open sessions count up to their last update).
SELECT CAST(COALESCE(SUM(COALESCE(stopped_at, updated_at) - started_at), 0) AS INTEGER) FROM radius_sessions
WHERE username = ? AND started_at >= ?;

-- name: ActivePlanNameByLogin :one
-- Plan of the customer who logs in with this RADIUS user name (username or pppoe_username).
SELECT p.name FROM subscriptions s JOIN customers c ON c.id = s.customer_id JOIN plans p ON p.id = s.plan_id
WHERE s.status = 'active' AND (c.username = sqlc.arg(name) OR (c.pppoe_username <> '' AND c.pppoe_username = sqlc.arg(name)))
ORDER BY s.expires_at DESC LIMIT 1;

-- name: ListRadiusDropsSince :many
-- Users whose session on a NAS ended at or after the cutoff and who have no open session now (mass-drop alert).
SELECT DISTINCT nas_ip, username FROM radius_sessions s
WHERE stopped_at >= CAST(sqlc.arg(since) AS INTEGER)
  AND NOT EXISTS (SELECT 1 FROM radius_sessions o WHERE o.username = s.username AND o.stopped_at IS NULL)
ORDER BY nas_ip, username;

-- name: ListRadiusSessionsByUsers :many
-- One page of sessions under the customer's two login names (username, pppoe_username; the same name twice when there is no PPPoE name).
SELECT * FROM radius_sessions
WHERE username = sqlc.arg(name1) OR username = sqlc.arg(name2)
ORDER BY started_at DESC, id DESC LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: ListActivePlanRates :many
-- Active plan and its bandwidth per customer, for the live sessions view. Newest expiry first: the first row for a login is the plan that counts.
SELECT c.username, c.pppoe_username, p.name AS plan_name,
       COALESCE(b.rate_down, 0) AS rate_down, COALESCE(b.rate_down_unit, '') AS rate_down_unit,
       COALESCE(b.rate_up, 0) AS rate_up, COALESCE(b.rate_up_unit, '') AS rate_up_unit
FROM subscriptions s JOIN customers c ON c.id = s.customer_id JOIN plans p ON p.id = s.plan_id
LEFT JOIN bandwidths b ON b.id = p.bandwidth_id
WHERE s.status = 'active'
ORDER BY s.expires_at DESC;

-- name: GetRadiusSessionOctets :one
SELECT input_octets, output_octets, started_at, updated_at, stopped_at FROM radius_sessions WHERE nas_ip = ? AND session_id = ?;

-- name: AddSubscriptionUsage :exec
-- Adds a session's growth to every active subscription of the login whose usage window holds the
-- session (it started at or after the window), like the old sum over radius_sessions.
UPDATE subscriptions SET used_in_octets = used_in_octets + sqlc.arg(in_octets), used_out_octets = used_out_octets + sqlc.arg(out_octets),
    used_seconds = used_seconds + sqlc.arg(seconds)
WHERE status = 'active' AND started_at <= sqlc.arg(session_started)
  AND customer_id IN (SELECT c.id FROM customers c WHERE c.username = sqlc.arg(name) OR (c.pppoe_username <> '' AND c.pppoe_username = sqlc.arg(name)));

-- name: GetIsolirPlan :one
-- The isolir plan as a RADIUS plan (its profile and pool). The isolation window comes from isolations.
SELECT p.id AS plan_id, p.name AS plan_name, p.type AS plan_type, p.limited, p.limit_type,
       p.time_limit, p.time_unit, p.data_limit, p.data_unit, p.shared_users,
       b.rate_down, b.rate_down_unit, b.rate_up, b.rate_up_unit, b.burst,
       pl.name AS pool_name
FROM plans p
LEFT JOIN bandwidths b ON b.id = p.bandwidth_id
LEFT JOIN pools pl ON pl.id = p.pool_id
WHERE p.id = sqlc.arg(plan_id);

-- name: ListFUPSessions :many
-- Open sessions of customers whose active plan has a FUP (RADIUS, data limit): the FUP job checks these.
SELECT rs.* FROM radius_sessions rs
JOIN customers c ON c.status = 'Active' AND (c.username = rs.username OR (c.pppoe_username <> '' AND c.pppoe_username = rs.username))
JOIN subscriptions s ON s.id = (SELECT id FROM subscriptions WHERE customer_id = c.id AND status = 'active' ORDER BY expires_at DESC LIMIT 1)
JOIN plans p ON p.id = s.plan_id
WHERE rs.stopped_at IS NULL AND p.fup_bandwidth_id IS NOT NULL AND p.device = 'Radius' AND p.limited = 1;

-- name: ListBoostSessions :many
-- Open sessions of customers whose active plan has a scheduled boost (RADIUS): the boost job checks these.
SELECT rs.* FROM radius_sessions rs
JOIN customers c ON c.status = 'Active' AND (c.username = rs.username OR (c.pppoe_username <> '' AND c.pppoe_username = rs.username))
JOIN subscriptions s ON s.id = (SELECT id FROM subscriptions WHERE customer_id = c.id AND status = 'active' ORDER BY expires_at DESC LIMIT 1)
JOIN plans p ON p.id = s.plan_id
WHERE rs.stopped_at IS NULL AND p.boost_bandwidth_id IS NOT NULL AND p.boost_window <> '' AND p.device = 'Radius';
