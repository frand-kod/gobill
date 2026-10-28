-- name: ListWgPeers :many
SELECT * FROM wg_peers ORDER BY router_id;

-- name: GetWgPeer :one
SELECT * FROM wg_peers WHERE router_id = ?;

-- name: UpsertWgPeer :exec
INSERT INTO wg_peers (router_id, ip, public_key, private_enc, endpoint, stale, provisioned_at)
VALUES (?, ?, ?, ?, '', 0, ?)
ON CONFLICT (router_id) DO UPDATE SET ip = excluded.ip, public_key = excluded.public_key, private_enc = excluded.private_enc,
    endpoint = '', stale = 0, provisioned_at = excluded.provisioned_at;

-- name: DeleteWgPeer :exec
DELETE FROM wg_peers WHERE router_id = ?;

-- name: SetWgPeerEndpoint :exec
UPDATE wg_peers SET endpoint = ? WHERE router_id = ?;

-- name: MarkWgPeersStale :execrows
UPDATE wg_peers SET stale = 1;

-- name: CountWgPeers :one
SELECT COUNT(*) FROM wg_peers;
