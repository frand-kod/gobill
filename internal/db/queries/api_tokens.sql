-- name: CreateApiToken :one
INSERT INTO api_tokens (admin_id, name, token_hash, expires_at) VALUES (?, ?, ?, ?) RETURNING *;

-- name: GetApiTokenByHash :one
SELECT * FROM api_tokens WHERE token_hash = ?;

-- name: ListApiTokensByAdmin :many
SELECT * FROM api_tokens WHERE admin_id = ? ORDER BY id DESC;

-- name: TouchApiToken :exec
UPDATE api_tokens SET last_used_at = unixepoch() WHERE id = ?;

-- name: RevokeApiToken :execrows
UPDATE api_tokens SET revoked_at = unixepoch() WHERE id = ? AND revoked_at IS NULL;
