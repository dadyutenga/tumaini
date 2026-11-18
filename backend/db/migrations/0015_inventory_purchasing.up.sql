-- Indexes for the inventory and purchasing list, detail and statement queries.
CREATE INDEX IF NOT EXISTS stock_movements_list_idx ON stock_movements (business_id, created_at DESC);
CREATE INDEX IF NOT EXISTS stock_adjustments_list_idx ON stock_adjustments (business_id, branch_id, created_at DESC);
CREATE INDEX IF NOT EXISTS stock_entry_items_entry_idx ON stock_entry_items (entry_id);
CREATE INDEX IF NOT EXISTS goods_receipts_list_idx ON goods_receipts (business_id, branch_id, received_date DESC);
CREATE INDEX IF NOT EXISTS goods_receipts_invoice_idx ON goods_receipts (invoice_id) WHERE invoice_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS goods_receipt_items_receipt_idx ON goods_receipt_items (receipt_id);
CREATE INDEX IF NOT EXISTS purchase_invoices_list_idx ON purchase_invoices (business_id, branch_id, invoice_date DESC);
CREATE INDEX IF NOT EXISTS purchase_invoices_supplier_idx ON purchase_invoices (supplier_id, invoice_date);
CREATE INDEX IF NOT EXISTS purchase_returns_list_idx ON purchase_returns (business_id, branch_id, created_at DESC);
CREATE INDEX IF NOT EXISTS purchase_returns_supplier_idx ON purchase_returns (supplier_id);
CREATE INDEX IF NOT EXISTS purchase_returns_receipt_idx ON purchase_returns (receipt_id);
CREATE INDEX IF NOT EXISTS purchase_returns_invoice_idx ON purchase_returns (invoice_id) WHERE invoice_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS purchase_return_items_return_idx ON purchase_return_items (return_id);
