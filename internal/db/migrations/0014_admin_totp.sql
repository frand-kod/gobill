-- Optional TOTP two-factor login for admins. The secret is sealed with the app key (internal/secret).
ALTER TABLE admins ADD COLUMN totp_secret_enc TEXT NOT NULL DEFAULT '';
ALTER TABLE admins ADD COLUMN totp_enabled INTEGER NOT NULL DEFAULT 0 CHECK (totp_enabled IN (0, 1));
-- Recovery codes are bcrypt hashes; used_at marks a code as spent.
CREATE TABLE admin_recovery_codes (
    id        INTEGER PRIMARY KEY,
    admin_id  INTEGER NOT NULL REFERENCES admins (id) ON DELETE CASCADE,
    code_hash TEXT    NOT NULL,
    used_at   INTEGER
);
CREATE INDEX admin_recovery_codes_admin_idx ON admin_recovery_codes (admin_id);
