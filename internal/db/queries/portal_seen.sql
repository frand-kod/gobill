-- name: ListPortalSeen :many
SELECT item, version FROM portal_seen WHERE customer_id = ?;

-- name: UpsertPortalSeen :exec
INSERT INTO portal_seen (customer_id, item, version) VALUES (?, ?, ?)
ON CONFLICT (customer_id, item) DO UPDATE SET version = excluded.version, seen_at = unixepoch();
