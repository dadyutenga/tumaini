-- Expenses: reversal details, one generated expense per template and date.
ALTER TABLE expenses ADD COLUMN IF NOT EXISTS reversed_at timestamptz;
ALTER TABLE expenses ADD COLUMN IF NOT EXISTS reversed_by uuid REFERENCES users(id);
ALTER TABLE expenses ADD COLUMN IF NOT EXISTS reverse_reason text;
CREATE UNIQUE INDEX IF NOT EXISTS expenses_recurring_date_uq ON expenses (recurring_id, expense_date) WHERE recurring_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS expenses_status_idx ON expenses (business_id, status, expense_date DESC);

ALTER TABLE recurring_expenses ADD COLUMN IF NOT EXISTS updated_at timestamptz NOT NULL DEFAULT now();
CREATE INDEX IF NOT EXISTS recurring_expenses_due_idx ON recurring_expenses (business_id, next_run) WHERE is_active;

-- Finance: list and statement access paths.
CREATE INDEX IF NOT EXISTS payments_kind_idx ON payments (business_id, kind, payment_date DESC);
CREATE INDEX IF NOT EXISTS money_transfers_date_idx ON money_transfers (business_id, transfer_date DESC);
CREATE INDEX IF NOT EXISTS money_transfers_from_idx ON money_transfers (from_account_id);
CREATE INDEX IF NOT EXISTS money_transfers_to_idx ON money_transfers (to_account_id);
CREATE INDEX IF NOT EXISTS other_income_date_idx ON other_income (business_id, income_date DESC);
CREATE INDEX IF NOT EXISTS account_reconciliations_account_idx ON account_reconciliations (money_account_id, as_of_date DESC);
CREATE INDEX IF NOT EXISTS journal_entries_source_id_idx ON journal_entries (source_id);
CREATE INDEX IF NOT EXISTS journal_entries_reverses_idx ON journal_entries (reverses_id) WHERE reverses_id IS NOT NULL;

DROP TRIGGER IF EXISTS set_updated_at ON recurring_expenses;
CREATE TRIGGER set_updated_at BEFORE UPDATE ON recurring_expenses FOR EACH ROW EXECUTE FUNCTION set_updated_at();
