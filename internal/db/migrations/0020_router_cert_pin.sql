-- SHA-256 fingerprint (hex) of the router's API-SSL leaf certificate, pinned on first use. '' = not pinned yet.
ALTER TABLE routers ADD COLUMN cert_pin TEXT NOT NULL DEFAULT '';
