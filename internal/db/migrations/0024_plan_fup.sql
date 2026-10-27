-- FUP: past the data limit a RADIUS plan can slow the customer down to this bandwidth instead of
-- rejecting the login. NULL = no FUP, the login is rejected as before.
ALTER TABLE plans ADD COLUMN fup_bandwidth_id INTEGER REFERENCES bandwidths (id) ON DELETE RESTRICT;
