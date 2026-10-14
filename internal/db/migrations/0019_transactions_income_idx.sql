-- Income sums (dashboard: today, this month and 12 monthly bars) read price and method of every
-- transaction in the range. With only created_at in the index each row was a table lookup; at
-- 60000 transactions the dashboard spent 80% of its time there. The covering index answers the sum
-- from the index alone (3x faster page). transactions_created_idx stays: it gives reports their
-- ORDER BY created_at, id without a sort.
CREATE INDEX transactions_income_idx ON transactions (created_at, method, price);
