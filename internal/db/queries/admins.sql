-- name: GetAdminByUsername :one
SELECT * FROM admins WHERE username = ? LIMIT 1;

-- name: GetAdmin :one
SELECT * FROM admins WHERE id = ? LIMIT 1;

-- name: CountAdmins :one
SELECT count(*) FROM admins;

-- name: CreateAdmin :one
INSERT INTO admins (username, fullname, password_hash, role)
VALUES (?, ?, ?, ?)
RETURNING *;

-- name: TouchAdminLogin :exec
UPDATE admins SET last_login_at = unixepoch() WHERE id = ?;
