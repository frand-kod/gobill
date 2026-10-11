-- Message-Authenticator policy: has this NAS sent a valid one? Auto mode requires it from then on.
ALTER TABLE nas ADD COLUMN ma_seen INTEGER NOT NULL DEFAULT 0;
