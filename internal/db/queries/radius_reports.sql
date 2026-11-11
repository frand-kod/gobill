-- name: SumRadiusUsage :one
SELECT CAST(COALESCE(SUM(input_octets + output_octets), 0) AS INTEGER) FROM radius_sessions
WHERE username = ? AND started_at >= ?;

-- name: AddRadiusUsageDaily :exec
-- Adds the growth of a session's counters to its user's day (YYYY-MM-DD in the app time zone).
INSERT INTO radius_usage_daily (username, day, input_octets, output_octets) VALUES (?, ?, ?, ?)
ON CONFLICT (username, day) DO UPDATE SET
    input_octets = radius_usage_daily.input_octets + excluded.input_octets,
    output_octets = radius_usage_daily.output_octets + excluded.output_octets;

-- name: ListRadiusUsageDaily :many
-- Octets of the customer's two login names per day since day, for the daily usage chart.
SELECT day, CAST(SUM(input_octets) AS INTEGER) AS input, CAST(SUM(output_octets) AS INTEGER) AS output FROM radius_usage_daily
WHERE (username = sqlc.arg(name1) OR username = sqlc.arg(name2)) AND day >= sqlc.arg(since)
GROUP BY day;

-- name: SumRadiusUsageDailyByDay :many
-- All users' octets per day from since to until (YYYY-MM-DD, both included).
SELECT day, CAST(SUM(input_octets) AS INTEGER) AS input, CAST(SUM(output_octets) AS INTEGER) AS output FROM radius_usage_daily
WHERE day >= sqlc.arg(since) AND day <= sqlc.arg(until)
GROUP BY day ORDER BY day;

-- name: TopRadiusUsageDaily :many
-- The users with the most octets (up + down) from since to until.
SELECT username, CAST(SUM(input_octets) AS INTEGER) AS input, CAST(SUM(output_octets) AS INTEGER) AS output FROM radius_usage_daily
WHERE day >= sqlc.arg(since) AND day <= sqlc.arg(until)
GROUP BY username ORDER BY SUM(input_octets + output_octets) DESC LIMIT sqlc.arg(row_limit);

-- name: ListRadiusUsageDailyRange :many
-- One row per user per day from since to until, for the usage CSV.
SELECT day, username, input_octets, output_octets FROM radius_usage_daily
WHERE day >= sqlc.arg(since) AND day <= sqlc.arg(until)
ORDER BY day, username;
