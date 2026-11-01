-- Per-plan schedules, evaluated in the app time zone (see radius.ParseWindow for the syntax).
-- login_window: when the plan may log in; Session-Timeout is capped to the window end. '' = always.
-- boost_window + boost_bandwidth_id: during the window the plan gets this bandwidth instead of its own (RADIUS plans). '' / NULL = no boost.
ALTER TABLE plans ADD COLUMN login_window TEXT NOT NULL DEFAULT '';
ALTER TABLE plans ADD COLUMN boost_window TEXT NOT NULL DEFAULT '';
ALTER TABLE plans ADD COLUMN boost_bandwidth_id INTEGER REFERENCES bandwidths (id) ON DELETE RESTRICT;
