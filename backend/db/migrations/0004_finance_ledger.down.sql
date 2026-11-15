DROP TRIGGER IF EXISTS journal_lines_balanced ON journal_lines;
DROP FUNCTION IF EXISTS check_entry_balanced();
DROP TABLE IF EXISTS account_reconciliations, period_locks, journal_lines, journal_entries, other_income,
  income_categories, money_transfers, payment_allocations, payments, money_accounts, ledger_accounts CASCADE;
