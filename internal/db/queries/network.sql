-- name: ListAllRouters :many
SELECT * FROM routers ORDER BY name;

-- name: ListNASPacketStats :many
-- Per NAS packet source IP: newest packet seen and sessions still open.
SELECT nas_ip, CAST(MAX(updated_at) AS INTEGER) AS last_seen,
       CAST(COUNT(CASE WHEN stopped_at IS NULL THEN 1 END) AS INTEGER) AS active
FROM radius_sessions GROUP BY nas_ip;
