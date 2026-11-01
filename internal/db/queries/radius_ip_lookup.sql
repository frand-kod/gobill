-- name: ListRadiusSessionsByIPAt :many
-- Sessions that had the Framed-IP at the moment at_ts: started before it and not stopped before it (an open one is still running).
SELECT * FROM radius_sessions
WHERE framed_ip = sqlc.arg(ip) AND started_at <= sqlc.arg(at_ts) AND (stopped_at IS NULL OR stopped_at >= sqlc.arg(at_ts))
ORDER BY started_at DESC, id DESC LIMIT 50;
