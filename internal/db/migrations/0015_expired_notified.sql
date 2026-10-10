-- Unix time the expired message went out for the current period. NULL = not yet (the next period notifies again).
ALTER TABLE subscriptions ADD COLUMN expired_notified_at INTEGER;
