DROP INDEX IF EXISTS sales_invoice_number_uq;
ALTER TABLE sales DROP COLUMN IF EXISTS invoice_number;
