-- Built-in WireGuard (userspace, see internal/wg). One peer per router. gobill makes the router's keypair so
-- the RouterOS command block can carry the private key; it is stored sealed (internal/secret).
-- endpoint is the last address the router was seen at: it is set on start so gobill sends the handshake
-- itself and a restart does not cut the tunnel. stale = port, endpoint or subnet changed, the router
-- needs provisioning again.
CREATE TABLE wg_peers (
    router_id      INTEGER PRIMARY KEY REFERENCES routers (id) ON DELETE CASCADE,
    ip             TEXT    NOT NULL UNIQUE, -- the only AllowedIP of the peer
    public_key     TEXT    NOT NULL UNIQUE, -- base64
    private_enc    BLOB    NOT NULL,        -- base64 private key, AES-GCM
    endpoint       TEXT    NOT NULL DEFAULT '',
    stale          INTEGER NOT NULL DEFAULT 0 CHECK (stale IN (0, 1)),
    provisioned_at INTEGER NOT NULL
);
