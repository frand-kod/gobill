-- F2 router monitor, F3 RADIUS log search, F4 message log. Times = unix seconds UTC.

ALTER TABLE routers ADD COLUMN last_seen_at INTEGER;
ALTER TABLE routers ADD COLUMN online INTEGER; -- NULL = never checked

CREATE TABLE message_logs (
    id         INTEGER PRIMARY KEY,
    channel    TEXT    NOT NULL, -- telegram, sms, wa, email
    recipient  TEXT    NOT NULL DEFAULT '',
    subject    TEXT    NOT NULL DEFAULT '',
    body       TEXT    NOT NULL DEFAULT '',
    status     TEXT    NOT NULL CHECK (status IN ('ok', 'error')),
    error      TEXT    NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL DEFAULT (unixepoch())
);
CREATE INDEX message_logs_created_idx ON message_logs (created_at);
