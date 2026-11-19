-- FR-SET-05 / FR-SAL-12: an invoice has its own number, from the branch's invoice sequence,
-- assigned the first time the invoice is produced and never changed after.
ALTER TABLE sales ADD COLUMN IF NOT EXISTS invoice_number text;
CREATE UNIQUE INDEX IF NOT EXISTS sales_invoice_number_uq ON sales (business_id, invoice_number) WHERE invoice_number IS NOT NULL;
