ALTER TABLE stock_transfers DROP CONSTRAINT IF EXISTS stock_transfers_status_check;
ALTER TABLE stock_transfers ADD CONSTRAINT stock_transfers_status_check
  CHECK (status IN ('draft','dispatched','received','received_with_difference','cancelled'));
DROP INDEX IF EXISTS purchase_return_items_return_idx;
DROP INDEX IF EXISTS purchase_returns_invoice_idx;
DROP INDEX IF EXISTS purchase_returns_receipt_idx;
DROP INDEX IF EXISTS purchase_returns_supplier_idx;
DROP INDEX IF EXISTS purchase_returns_list_idx;
DROP INDEX IF EXISTS purchase_invoices_supplier_idx;
DROP INDEX IF EXISTS purchase_invoices_list_idx;
DROP INDEX IF EXISTS goods_receipt_items_receipt_idx;
DROP INDEX IF EXISTS goods_receipts_invoice_idx;
DROP INDEX IF EXISTS goods_receipts_list_idx;
DROP INDEX IF EXISTS stock_entry_items_entry_idx;
DROP INDEX IF EXISTS stock_adjustments_list_idx;
DROP INDEX IF EXISTS stock_movements_list_idx;
