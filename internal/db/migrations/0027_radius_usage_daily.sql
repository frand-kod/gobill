-- Per-day RADIUS usage, so a session that runs for days is counted on the days it moved the bytes, not on
-- the day it started. Accounting adds the growth of a session's counters to the day of each update.
-- Existing sessions are spread over their days once at startup (db.BackfillUsageDaily).
CREATE TABLE radius_usage_daily (
    username      TEXT    NOT NULL,
    day           TEXT    NOT NULL, -- YYYY-MM-DD in the app time zone
    input_octets  INTEGER NOT NULL DEFAULT 0,
    output_octets INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (username, day)
) WITHOUT ROWID;
CREATE INDEX idx_radius_usage_daily_day ON radius_usage_daily (day);
