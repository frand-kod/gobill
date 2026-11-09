-- Income: the transactions that count as money earned. The one definition for the dashboard and the reports.
-- Purchases paid from balance (the top-up already counted) and gifts are not income. Redeeming a consignment
-- voucher is the agent's sale; the operator's income is the deposit (agent_ledger payments).
CREATE VIEW IF NOT EXISTS income_transactions AS
SELECT * FROM transactions
WHERE method <> 'Customer - Balance' AND method NOT LIKE 'Balance - Gift from%'
  AND NOT (method LIKE 'Voucher - %' AND EXISTS (SELECT 1 FROM vouchers v JOIN agent_batch_vouchers bv ON bv.voucher_id = v.id WHERE v.code = substr(transactions.method, 11)));
