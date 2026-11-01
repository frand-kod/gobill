-- Invoice numbers come from a sequence, not max(transactions.id)+1 (a deleted last row reused its number).
CREATE TABLE invoice_seq (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    n  INTEGER NOT NULL
);
INSERT INTO invoice_seq (id, n) SELECT 1, COALESCE(MAX(id), 0) FROM transactions;

-- A gateway order with a coupon reserves one use when it is created (see web payOrder). The reservation
-- is released when the request is closed unpaid, and taken again if the gateway reports it paid later.
CREATE TRIGGER payment_coupon_release AFTER UPDATE OF status ON payment_requests
WHEN OLD.status = 'pending' AND NEW.status IN ('expired', 'failed') AND OLD.coupon <> ''
BEGIN
    UPDATE coupons SET used = used - 1 WHERE code = OLD.coupon AND used > 0;
END;
CREATE TRIGGER payment_coupon_retake AFTER UPDATE OF status ON payment_requests
WHEN OLD.status IN ('expired', 'failed') AND NEW.status = 'paid' AND OLD.coupon <> ''
BEGIN
    UPDATE coupons SET used = used + 1 WHERE code = OLD.coupon;
END;
CREATE TRIGGER payment_coupon_drop AFTER DELETE ON payment_requests
WHEN OLD.status = 'pending' AND OLD.coupon <> ''
BEGIN
    UPDATE coupons SET used = used - 1 WHERE code = OLD.coupon AND used > 0;
END;
