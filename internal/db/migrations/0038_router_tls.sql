-- Router API connection mode: 0 = plain (port 8728), 1 = TLS (port 8729). It used to be implied by port 8729.
ALTER TABLE routers ADD COLUMN tls INTEGER NOT NULL DEFAULT 0;
UPDATE routers SET tls = 1 WHERE port = 8729;
