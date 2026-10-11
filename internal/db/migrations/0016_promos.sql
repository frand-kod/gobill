-- Promo blocks shown on the isolir page (show_isolir) and the portal dashboard (show_portal).
-- image is the stored upload name (see uploads.go); starts_at/ends_at are unix times, NULL = open.
CREATE TABLE promos (
    id          INTEGER PRIMARY KEY,
    title       TEXT    NOT NULL,
    body        TEXT    NOT NULL DEFAULT '',
    image       TEXT    NOT NULL DEFAULT '',
    link        TEXT    NOT NULL DEFAULT '',
    show_isolir INTEGER NOT NULL DEFAULT 1 CHECK (show_isolir IN (0, 1)),
    show_portal INTEGER NOT NULL DEFAULT 0 CHECK (show_portal IN (0, 1)),
    active      INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0, 1)),
    starts_at   INTEGER,
    ends_at     INTEGER,
    position    INTEGER NOT NULL DEFAULT 0,
    updated_at  INTEGER NOT NULL
);
