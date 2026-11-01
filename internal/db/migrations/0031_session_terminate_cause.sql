-- Why a session ended: Acct-Terminate-Cause of the Stop, by name ("User-Request", "Idle-Timeout"...). '' = unknown.
ALTER TABLE radius_sessions ADD COLUMN terminate_cause TEXT NOT NULL DEFAULT '';
