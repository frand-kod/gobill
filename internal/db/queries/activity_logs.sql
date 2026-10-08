-- name: CreateActivityLog :exec
INSERT INTO activity_logs (actor_type, actor_id, action, description, ip) VALUES (?, ?, ?, ?, ?);

-- name: ListActivityLogs :many
SELECT * FROM activity_logs ORDER BY id DESC LIMIT ? OFFSET ?;

-- name: SearchActivityLogs :many
SELECT * FROM activity_logs
WHERE action LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%' OR description LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%'
ORDER BY id DESC LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: DeleteActivityLogsBefore :exec
DELETE FROM activity_logs WHERE created_at < ?;
