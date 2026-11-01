-- Optional lock of a RADIUS customer to one device (MAC, Calling-Station-Id) and/or one port (NAS-Port-Id).
-- A separate table: most customers have no binding, and the customers table stays as it is.
CREATE TABLE customer_bindings (
    customer_id INTEGER PRIMARY KEY REFERENCES customers (id) ON DELETE CASCADE,
    mac         TEXT NOT NULL DEFAULT '', -- AA:BB:CC:DD:EE:FF, '' = not locked
    port_id     TEXT NOT NULL DEFAULT ''  -- '' = not locked
);
-- NAS-Port-Id of a session, as the NAS reports it in accounting.
ALTER TABLE radius_sessions ADD COLUMN nas_port_id TEXT NOT NULL DEFAULT '';
