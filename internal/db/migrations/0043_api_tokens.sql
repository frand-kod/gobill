-- API tokens for /api/v1. Only the sha256 of the token is stored; the plaintext is shown once.
-- expires_at 0 = never. revoked_at NULL = still valid.
CREATE TABLE IF NOT EXISTS api_tokens (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    admin_id     INTEGER NOT NULL REFERENCES admins(id) ON DELETE CASCADE,
    name         TEXT NOT NULL DEFAULT '',
    token_hash   TEXT NOT NULL UNIQUE,
    created_at   INTEGER NOT NULL DEFAULT (unixepoch()),
    last_used_at INTEGER,
    expires_at   INTEGER NOT NULL DEFAULT 0,
    revoked_at   INTEGER
);
CREATE INDEX IF NOT EXISTS idx_api_tokens_admin ON api_tokens(admin_id);
