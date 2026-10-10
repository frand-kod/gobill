-- name: GetAdminByUsername :one
SELECT * FROM admins WHERE username = ? LIMIT 1;

-- name: GetAdmin :one
SELECT * FROM admins WHERE id = ? LIMIT 1;

-- name: CountAdmins :one
SELECT count(*) FROM admins;

-- name: CreateAdmin :one
INSERT INTO admins (username, fullname, password_hash, role, email, phone, city, root_id)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: TouchAdminLogin :exec
UPDATE admins SET last_login_at = unixepoch() WHERE id = ?;

-- scope: 'all' (SuperAdmin), 'admin' (Admin: lower roles + self), 'agent' (self + own Sales).
-- name: SearchAdmins :many
SELECT * FROM admins
WHERE (username LIKE '%' || sqlc.arg(q) || '%' OR fullname LIKE '%' || sqlc.arg(q) || '%')
  AND (sqlc.arg(scope) = 'all'
    OR (sqlc.arg(scope) = 'admin' AND (role IN ('Report', 'Agent', 'Sales') OR id = sqlc.arg(actor)))
    OR (sqlc.arg(scope) = 'agent' AND (id = sqlc.arg(actor) OR root_id = sqlc.arg(actor))))
ORDER BY id
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: ListAgents :many
SELECT * FROM admins WHERE role = 'Agent' ORDER BY username;

-- An empty password_hash keeps the current one. The last active SuperAdmin cannot be
-- demoted or deactivated: the row is then not updated (0 rows affected).
-- name: UpdateAdmin :execrows
UPDATE admins SET username = sqlc.arg(username), fullname = sqlc.arg(fullname), email = sqlc.arg(email),
  phone = sqlc.arg(phone), city = sqlc.arg(city), role = sqlc.arg(role), status = sqlc.arg(status),
  root_id = sqlc.arg(root_id),
  password_hash = CASE WHEN sqlc.arg(password_hash) <> '' THEN sqlc.arg(password_hash) ELSE admins.password_hash END,
  legacy_sha1 = CASE WHEN sqlc.arg(password_hash) <> '' THEN '' ELSE admins.legacy_sha1 END,
  session_version = admins.session_version + sqlc.arg(bump)
WHERE admins.id = sqlc.arg(id)
  AND (NOT (admins.role = 'SuperAdmin' AND admins.status = 'Active')
    OR (sqlc.arg(role) = 'SuperAdmin' AND sqlc.arg(status) = 'Active')
    OR (SELECT count(*) FROM admins AS sa WHERE sa.role = 'SuperAdmin' AND sa.status = 'Active') > 1);

-- Sets the password and bumps session_version, ending every other session.
-- name: SetAdminPassword :one
UPDATE admins SET password_hash = ?, legacy_sha1 = '', session_version = admins.session_version + 1 WHERE admins.id = ?
RETURNING session_version;

-- Refuses (0 rows) to delete the last active SuperAdmin.
-- name: DeleteAdmin :execrows
DELETE FROM admins WHERE admins.id = ?
  AND (NOT (admins.role = 'SuperAdmin' AND admins.status = 'Active')
    OR (SELECT count(*) FROM admins AS sa WHERE sa.role = 'SuperAdmin' AND sa.status = 'Active') > 1);

-- Ends every other session of the admin (single_session login).
-- name: BumpAdminSession :one
UPDATE admins SET session_version = admins.session_version + 1 WHERE admins.id = ?
RETURNING session_version;

-- Stores a pending (not yet confirmed) secret. Refused once 2FA is enabled.
-- name: SetAdminTOTPSecret :exec
UPDATE admins SET totp_secret_enc = ? WHERE id = ? AND totp_enabled = 0;

-- name: EnableAdminTOTP :exec
UPDATE admins SET totp_enabled = 1 WHERE id = ?;

-- name: ClearAdminTOTP :exec
UPDATE admins SET totp_secret_enc = '', totp_enabled = 0 WHERE id = ?;

-- name: DeleteRecoveryCodes :exec
DELETE FROM admin_recovery_codes WHERE admin_id = ?;

-- name: CreateRecoveryCode :exec
INSERT INTO admin_recovery_codes (admin_id, code_hash) VALUES (?, ?);

-- name: ListUnusedRecoveryCodes :many
SELECT id, code_hash FROM admin_recovery_codes WHERE admin_id = ? AND used_at IS NULL ORDER BY id;

-- 0 rows = the code was already spent, so a recovery code works once even under parallel logins.
-- name: UseRecoveryCode :execrows
UPDATE admin_recovery_codes SET used_at = unixepoch() WHERE id = ? AND used_at IS NULL;
