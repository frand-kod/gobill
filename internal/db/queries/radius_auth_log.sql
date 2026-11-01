-- name: InsertRadiusAuthLog :exec
INSERT INTO radius_auth_log (at, username, nas_ip, mac, accepted, reason, message) VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: SearchRadiusAuthLog :many
-- from_ts/to_ts filter at (0 = open). page_limit -1 = all (CSV).
SELECT * FROM radius_auth_log
WHERE (username LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%' OR nas_ip LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%'
       OR mac LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%' OR reason LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%')
  AND (CAST(sqlc.arg(from_ts) AS INTEGER) = 0 OR at >= sqlc.arg(from_ts))
  AND (CAST(sqlc.arg(to_ts) AS INTEGER) = 0 OR at < sqlc.arg(to_ts))
ORDER BY at DESC, id DESC LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: CountRadiusAuthLog :one
SELECT COUNT(*) FROM radius_auth_log
WHERE (username LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%' OR nas_ip LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%'
       OR mac LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%' OR reason LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%')
  AND (CAST(sqlc.arg(from_ts) AS INTEGER) = 0 OR at >= sqlc.arg(from_ts))
  AND (CAST(sqlc.arg(to_ts) AS INTEGER) = 0 OR at < sqlc.arg(to_ts));

-- name: DeleteRadiusAuthLogBefore :execrows
DELETE FROM radius_auth_log WHERE at < ?;
