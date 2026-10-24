-- Quota carried over by an early renewal of a limited plan: unused data (bytes) rolls over; online time does not (quota_carry_seconds is the time already used, so the full limit is left). Both reset to 0 on plan change, expiry renewal and deactivation.
ALTER TABLE subscriptions ADD COLUMN quota_carry_bytes INTEGER NOT NULL DEFAULT 0;
ALTER TABLE subscriptions ADD COLUMN quota_carry_seconds INTEGER NOT NULL DEFAULT 0;
