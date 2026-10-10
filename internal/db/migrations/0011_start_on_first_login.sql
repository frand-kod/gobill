-- start_on_first_login: a RADIUS-plan subscription recharged with the setting on stays pending_start = 1
-- until the customer's first RADIUS login sets its real start and expiry (billing.StartPending).
ALTER TABLE subscriptions ADD COLUMN pending_start INTEGER NOT NULL DEFAULT 0 CHECK (pending_start IN (0, 1));
