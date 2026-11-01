-- Expiry reminders already sent, one row per subscription period (expires_at) and day (7/3/1).
-- A row is claimed before the send and deleted when the send fails, so the next run retries it.
CREATE TABLE reminder_sent (
    subscription_id INTEGER NOT NULL REFERENCES subscriptions (id) ON DELETE CASCADE,
    expires_at      INTEGER NOT NULL,
    days            INTEGER NOT NULL,
    sent_at         INTEGER NOT NULL DEFAULT (unixepoch()),
    PRIMARY KEY (subscription_id, expires_at, days)
) WITHOUT ROWID;
