-- A voided payment keeps its repayment row as history; it just stops counting.
ALTER TABLE loan_repayments ADD COLUMN IF NOT EXISTS voided_at timestamptz;
