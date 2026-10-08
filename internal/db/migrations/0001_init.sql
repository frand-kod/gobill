CREATE TABLE admins (
    id            INTEGER PRIMARY KEY,
    username      TEXT    NOT NULL UNIQUE,
    fullname      TEXT    NOT NULL DEFAULT '',
    password_hash TEXT    NOT NULL,
    role          TEXT    NOT NULL CHECK (role IN ('SuperAdmin', 'Admin', 'Report', 'Agent', 'Sales')),
    status        TEXT    NOT NULL DEFAULT 'Active' CHECK (status IN ('Active', 'Inactive')),
    email         TEXT    NOT NULL DEFAULT '',
    phone         TEXT    NOT NULL DEFAULT '',
    city          TEXT    NOT NULL DEFAULT '',
    -- Agent that owns a Sales admin.
    root_id       INTEGER REFERENCES admins (id) ON DELETE SET NULL,
    -- Bumped on password/role/status change; a session with an older value is rejected.
    session_version INTEGER NOT NULL DEFAULT 0,
    last_login_at INTEGER,
    created_at    INTEGER NOT NULL DEFAULT (unixepoch())
);

CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
) WITHOUT ROWID;

-- Schema required by github.com/alexedwards/scs/sqlite3store.
CREATE TABLE sessions (
    token  TEXT PRIMARY KEY,
    data   BLOB NOT NULL,
    expiry REAL NOT NULL
);
CREATE INDEX sessions_expiry_idx ON sessions (expiry);
