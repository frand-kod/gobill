-- One-line provisioning (see internal/web/provision.go). One token per router: provisioning again
-- replaces it. Only the sha256 of the token is stored. used_ip / used_at say who fetched the script and when.
CREATE TABLE wg_tokens (
    router_id  INTEGER PRIMARY KEY REFERENCES routers (id) ON DELETE CASCADE,
    token_hash TEXT    NOT NULL, -- hex sha256 of the random part of the token
    expires_at INTEGER NOT NULL,
    used_at    INTEGER NOT NULL DEFAULT 0,
    used_ip    TEXT    NOT NULL DEFAULT ''
);
