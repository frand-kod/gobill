-- name: CreateNAS :one
INSERT INTO nas (name, ip, secret_enc, description, require_message_auth) VALUES (?, ?, ?, ?, ?) RETURNING *;

-- name: ListNAS :many
SELECT * FROM nas ORDER BY name;

-- name: GetNAS :one
SELECT * FROM nas WHERE id = ?;

-- name: UpdateNAS :exec
UPDATE nas SET name = ?, ip = ?, secret_enc = ?, description = ?, require_message_auth = ? WHERE id = ?;

-- name: DeleteNAS :exec
DELETE FROM nas WHERE id = ?;

-- name: GetCustomerForRadius :one
SELECT * FROM customers
WHERE (username = sqlc.arg(name) OR (pppoe_username <> '' AND pppoe_username = sqlc.arg(name))) AND status = 'Active'
LIMIT 1;

-- name: GetRadiusPlan :one
SELECT s.started_at, s.expires_at, p.name AS plan_name, p.type AS plan_type, p.limited, p.limit_type,
       p.time_limit, p.time_unit, p.data_limit, p.data_unit, p.shared_users,
       b.rate_down, b.rate_down_unit, b.rate_up, b.rate_up_unit, b.burst,
       pl.name AS pool_name
FROM subscriptions s
JOIN plans p ON p.id = s.plan_id
LEFT JOIN bandwidths b ON b.id = p.bandwidth_id
LEFT JOIN pools pl ON pl.id = p.pool_id
WHERE s.customer_id = ? AND s.status = 'active'
ORDER BY s.expires_at DESC LIMIT 1;

-- name: SumRadiusUsage :one
SELECT CAST(COALESCE(SUM(input_octets + output_octets), 0) AS INTEGER) FROM radius_sessions
WHERE username = ? AND started_at >= ?;

-- name: CountOtherOpenRadiusSessions :one
-- Open, recently updated sessions of the user that are not the requesting device (same framed IP or MAC).
SELECT COUNT(*) FROM radius_sessions
WHERE username = ? AND stopped_at IS NULL AND updated_at >= ?
  AND (framed_ip <> ? OR ? = '')
  AND (mac <> ? OR ? = '');

-- name: UpsertRadiusSession :exec
INSERT INTO radius_sessions (session_id, username, nas_ip, nas_ip_attr, nas_identifier, framed_ip, mac, started_at, updated_at, stopped_at, input_octets, output_octets)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (nas_ip, session_id) DO UPDATE SET
    nas_ip_attr = COALESCE(NULLIF(excluded.nas_ip_attr, ''), radius_sessions.nas_ip_attr),
    nas_identifier = COALESCE(NULLIF(excluded.nas_identifier, ''), radius_sessions.nas_identifier),
    framed_ip = excluded.framed_ip, updated_at = excluded.updated_at,
    stopped_at = COALESCE(radius_sessions.stopped_at, excluded.stopped_at),
    input_octets = excluded.input_octets, output_octets = excluded.output_octets;

-- name: CloseRadiusSessionsByNAS :exec
UPDATE radius_sessions SET stopped_at = ? WHERE nas_ip = ? AND stopped_at IS NULL;

-- name: SearchOpenRadiusSessions :many
SELECT * FROM radius_sessions
WHERE stopped_at IS NULL
  AND (username LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%' OR framed_ip LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%' OR mac LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%')
ORDER BY started_at DESC LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: GetRadiusSession :one
SELECT * FROM radius_sessions WHERE id = ?;

-- name: ListOpenRadiusSessionsByUser :many
SELECT * FROM radius_sessions WHERE username = ? AND stopped_at IS NULL;

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
