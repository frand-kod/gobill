-- name: CreateNAS :one
INSERT INTO nas (name, ip, secret_enc, description) VALUES (?, ?, ?, ?) RETURNING *;

-- name: ListNAS :many
SELECT * FROM nas ORDER BY name;

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

-- name: CountOpenRadiusSessions :one
SELECT COUNT(*) FROM radius_sessions WHERE username = ? AND stopped_at IS NULL;

-- name: UpsertRadiusSession :exec
INSERT INTO radius_sessions (session_id, username, nas_ip, framed_ip, mac, started_at, updated_at, stopped_at, input_octets, output_octets)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (nas_ip, session_id) DO UPDATE SET
    framed_ip = excluded.framed_ip, updated_at = excluded.updated_at,
    stopped_at = COALESCE(radius_sessions.stopped_at, excluded.stopped_at),
    input_octets = excluded.input_octets, output_octets = excluded.output_octets;

-- name: CloseRadiusSessionsByNAS :exec
UPDATE radius_sessions SET stopped_at = ? WHERE nas_ip = ? AND stopped_at IS NULL;
