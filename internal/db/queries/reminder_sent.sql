-- name: ClaimReminder :execrows
-- 0 rows = this reminder already went out (or is going out) for this period.
INSERT OR IGNORE INTO reminder_sent (subscription_id, expires_at, days) VALUES (?, ?, ?);

-- name: ReleaseReminder :exec
DELETE FROM reminder_sent WHERE subscription_id = ? AND expires_at = ? AND days = ?;

-- name: PruneReminders :exec
DELETE FROM reminder_sent WHERE sent_at < ?;
