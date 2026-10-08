-- F5 maps and ODP. Coordinates are "lat,lng"; empty when unset (old PHP: '').

ALTER TABLE customers ADD COLUMN coordinates TEXT NOT NULL DEFAULT '';
ALTER TABLE routers ADD COLUMN coordinates TEXT NOT NULL DEFAULT '';
ALTER TABLE routers ADD COLUMN coverage INTEGER NOT NULL DEFAULT 0; -- meters

-- Optical distribution point. router_id is optional; a deleted router leaves the ODP in place.
CREATE TABLE odps (
    id          INTEGER PRIMARY KEY,
    name        TEXT    NOT NULL UNIQUE,
    coordinates TEXT    NOT NULL DEFAULT '',
    address     TEXT    NOT NULL DEFAULT '',
    port_amount INTEGER NOT NULL DEFAULT 0,
    attenuation TEXT    NOT NULL DEFAULT '',
    coverage    INTEGER NOT NULL DEFAULT 0, -- meters
    description TEXT    NOT NULL DEFAULT '',
    router_id   INTEGER REFERENCES routers (id) ON DELETE SET NULL
);
