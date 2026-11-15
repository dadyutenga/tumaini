ALTER TABLE payments DROP CONSTRAINT IF EXISTS payments_till_fk;
DROP TABLE IF EXISTS sale_return_items, sale_returns, sale_items, sales, till_sessions, customers CASCADE;
