DROP INDEX IF EXISTS goods_receipt_items_expiry_idx;
DROP INDEX IF EXISTS stock_entry_items_expiry_idx;
ALTER TABLE goods_receipt_items DROP COLUMN IF EXISTS expiry_date, DROP COLUMN IF EXISTS batch_no;
ALTER TABLE stock_entry_items   DROP COLUMN IF EXISTS expiry_date, DROP COLUMN IF EXISTS batch_no;
