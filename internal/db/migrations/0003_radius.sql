-- F3 built-in RADIUS. Times = unix seconds UTC.

CREATE TABLE nas (
    id          INTEGER PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE,
    ip          TEXT NOT NULL UNIQUE, -- single IP or CIDR
    secret_enc  BLOB NOT NULL,        -- AES-GCM
    description TEXT NOT NULL DEFAULT '',
    require_message_auth INTEGER NOT NULL DEFAULT 0 -- 1 = drop Access-Requests without Message-Authenticator
);

-- One row per accounting session; Interim updates the row in place.
CREATE TABLE radius_sessions (
    id             INTEGER PRIMARY KEY,
    session_id     TEXT    NOT NULL,
    username       TEXT    NOT NULL,
    nas_ip         TEXT    NOT NULL, -- packet source IP: finds the NAS row, secret and CoA destination
    nas_ip_attr    TEXT    NOT NULL DEFAULT '', -- NAS-IP-Address attribute as the NAS reports it
    nas_identifier TEXT    NOT NULL DEFAULT '',
    framed_ip      TEXT    NOT NULL DEFAULT '',
    mac            TEXT    NOT NULL DEFAULT '',
    started_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL,
    stopped_at     INTEGER,
    input_octets   INTEGER NOT NULL DEFAULT 0, -- includes Acct-Input-Gigawords
    output_octets  INTEGER NOT NULL DEFAULT 0,
    UNIQUE (nas_ip, session_id)
);
CREATE INDEX radius_sessions_user_idx ON radius_sessions (username, stopped_at);
