-- Unused data quota carried over by an early renewal of a limited plan, in bytes. Reset to 0 on plan change, expiry renewal and deactivation.
ALTER TABLE subscriptions ADD COLUMN quota_carry_bytes INTEGER NOT NULL DEFAULT 0;
