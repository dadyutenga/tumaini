-- FR-INV-32: batch number and expiry date recorded per stock receipt line.
ALTER TABLE stock_entry_items   ADD COLUMN IF NOT EXISTS batch_no text, ADD COLUMN IF NOT EXISTS expiry_date date;
ALTER TABLE goods_receipt_items ADD COLUMN IF NOT EXISTS batch_no text, ADD COLUMN IF NOT EXISTS expiry_date date;
CREATE INDEX IF NOT EXISTS stock_entry_items_expiry_idx   ON stock_entry_items (expiry_date)   WHERE expiry_date IS NOT NULL;
CREATE INDEX IF NOT EXISTS goods_receipt_items_expiry_idx ON goods_receipt_items (expiry_date) WHERE expiry_date IS NOT NULL;
