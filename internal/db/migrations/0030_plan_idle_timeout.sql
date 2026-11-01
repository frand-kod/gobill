-- Idle-Timeout per plan, in seconds: the NAS drops a session that sends no traffic for this long. NULL = not sent.
ALTER TABLE plans ADD COLUMN idle_timeout INTEGER;
