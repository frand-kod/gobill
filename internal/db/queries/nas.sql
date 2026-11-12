-- name: CreateNAS :one
INSERT INTO nas (name, ip, secret_enc, description, require_message_auth) VALUES (?, ?, ?, ?, ?) RETURNING *;

-- name: ListNAS :many
SELECT * FROM nas ORDER BY name;

-- name: GetNAS :one
SELECT * FROM nas WHERE id = ?;

-- name: UpdateNAS :exec
UPDATE nas SET name = ?, ip = ?, secret_enc = ?, description = ?, require_message_auth = ? WHERE id = ?;

-- name: SetNASType :exec
UPDATE nas SET type = ? WHERE id = ?;

-- name: DeleteNAS :exec
DELETE FROM nas WHERE id = ?;

-- name: MarkNASMASeen :exec
UPDATE nas SET ma_seen = 1 WHERE id = ?;
