CREATE TABLE ledger_accounts (
  id           uuid PRIMARY KEY,
  business_id  uuid NOT NULL REFERENCES businesses(id),
  code         text NOT NULL,
  name         text NOT NULL,
  type         text NOT NULL CHECK (type IN ('asset','liability','equity','income','expense')),
  is_system    boolean NOT NULL DEFAULT false,
  is_active    boolean NOT NULL DEFAULT true,
  parent_code  text,
  created_at   timestamptz NOT NULL DEFAULT now(),
  UNIQUE (business_id, code)
);

CREATE TABLE money_accounts (
  id                uuid PRIMARY KEY,
  business_id       uuid NOT NULL REFERENCES businesses(id),
  branch_id         uuid REFERENCES branches(id),
  ledger_account_id uuid NOT NULL REFERENCES ledger_accounts(id),
  name              text NOT NULL,
  type              text NOT NULL CHECK (type IN ('cash','bank','mobile_money','petty_cash')),
  provider          text,
  account_number    text,
  custodian_id      uuid REFERENCES users(id),
  allow_overdraft   boolean NOT NULL DEFAULT false,
  is_active         boolean NOT NULL DEFAULT true,
  created_at        timestamptz NOT NULL DEFAULT now(),
  updated_at        timestamptz NOT NULL DEFAULT now(),
  UNIQUE (business_id, name)
);
CREATE INDEX money_accounts_branch_idx ON money_accounts (branch_id);

CREATE TABLE payments (
  id               uuid PRIMARY KEY,
  business_id      uuid NOT NULL REFERENCES businesses(id),
  branch_id        uuid NOT NULL REFERENCES branches(id),
  number           text NOT NULL,
  direction        text NOT NULL CHECK (direction IN ('in','out')),
  kind             text NOT NULL CHECK (kind IN (
                     'sale','customer_payment','sale_refund','supplier_payment','supplier_refund',
                     'expense','salary','loan_disbursement','loan_repayment','income','drawing','capital','statutory')),
  party_type       text CHECK (party_type IN ('customer','supplier','employee')),
  party_id         uuid,
  money_account_id uuid NOT NULL REFERENCES money_accounts(id),
  method           text NOT NULL CHECK (method IN ('cash','mobile_money','bank','customer_credit')),
  amount           numeric(18,2) NOT NULL CHECK (amount > 0),
  reference        text,
  payment_date     date NOT NULL,
  till_session_id  uuid,
  status           text NOT NULL DEFAULT 'posted' CHECK (status IN ('posted','voided')),
  note             text,
  voided_by        uuid REFERENCES users(id),
  voided_at        timestamptz,
  void_reason      text,
  created_by       uuid NOT NULL REFERENCES users(id),
  created_at       timestamptz NOT NULL DEFAULT now(),
  UNIQUE (business_id, number)
);
CREATE INDEX payments_list_idx ON payments (business_id, branch_id, payment_date DESC);
CREATE INDEX payments_party_idx ON payments (party_type, party_id);
CREATE INDEX payments_account_idx ON payments (money_account_id, payment_date DESC);

CREATE TABLE payment_allocations (
  id             uuid PRIMARY KEY,
  business_id    uuid NOT NULL REFERENCES businesses(id),
  payment_id     uuid NOT NULL REFERENCES payments(id),
  document_type  text NOT NULL CHECK (document_type IN ('sale','purchase_invoice','payslip','expense','loan')),
  document_id    uuid NOT NULL,
  amount         numeric(18,2) NOT NULL CHECK (amount > 0),
  created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX payment_allocations_doc_idx ON payment_allocations (document_type, document_id);
CREATE INDEX payment_allocations_payment_idx ON payment_allocations (payment_id);

CREATE TABLE money_transfers (
  id               uuid PRIMARY KEY,
  business_id      uuid NOT NULL REFERENCES businesses(id),
  number           text NOT NULL,
  from_account_id  uuid NOT NULL REFERENCES money_accounts(id),
  to_account_id    uuid NOT NULL REFERENCES money_accounts(id),
  amount           numeric(18,2) NOT NULL CHECK (amount > 0),
  fee              numeric(18,2) NOT NULL DEFAULT 0 CHECK (fee >= 0),
  transfer_date    date NOT NULL,
  reference        text,
  note             text,
  created_by       uuid NOT NULL REFERENCES users(id),
  created_at       timestamptz NOT NULL DEFAULT now(),
  CHECK (from_account_id <> to_account_id),
  UNIQUE (business_id, number)
);

CREATE TABLE income_categories (
  id                uuid PRIMARY KEY,
  business_id       uuid NOT NULL REFERENCES businesses(id),
  name              text NOT NULL,
  ledger_account_id uuid NOT NULL REFERENCES ledger_accounts(id),
  is_active         boolean NOT NULL DEFAULT true,
  UNIQUE (business_id, name)
);

CREATE TABLE other_income (
  id               uuid PRIMARY KEY,
  business_id      uuid NOT NULL REFERENCES businesses(id),
  branch_id        uuid NOT NULL REFERENCES branches(id),
  number           text NOT NULL,
  category_id      uuid REFERENCES income_categories(id),
  kind             text NOT NULL CHECK (kind IN ('income','capital')),
  amount           numeric(18,2) NOT NULL CHECK (amount > 0),
  income_date      date NOT NULL,
  money_account_id uuid NOT NULL REFERENCES money_accounts(id),
  description      text NOT NULL,
  payment_id       uuid REFERENCES payments(id),
  created_by       uuid NOT NULL REFERENCES users(id),
  created_at       timestamptz NOT NULL DEFAULT now(),
  UNIQUE (business_id, number)
);

CREATE TABLE journal_entries (
  id            uuid PRIMARY KEY,
  business_id   uuid NOT NULL REFERENCES businesses(id),
  entry_date    date NOT NULL,
  source_type   text NOT NULL,
  source_id     uuid NOT NULL,
  description   text NOT NULL,
  reverses_id   uuid REFERENCES journal_entries(id),
  created_by    uuid REFERENCES users(id),
  created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX journal_entries_source_idx ON journal_entries (source_type, source_id);
CREATE INDEX journal_entries_date_idx ON journal_entries (business_id, entry_date);

CREATE TABLE journal_lines (
  id                uuid PRIMARY KEY,
  business_id       uuid NOT NULL REFERENCES businesses(id),
  entry_id          uuid NOT NULL REFERENCES journal_entries(id),
  branch_id         uuid REFERENCES branches(id),
  ledger_account_id uuid NOT NULL REFERENCES ledger_accounts(id),
  money_account_id  uuid REFERENCES money_accounts(id),
  party_type        text CHECK (party_type IN ('customer','supplier','employee')),
  party_id          uuid,
  debit             numeric(18,2) NOT NULL DEFAULT 0 CHECK (debit >= 0),
  credit            numeric(18,2) NOT NULL DEFAULT 0 CHECK (credit >= 0),
  entry_date        date NOT NULL,
  CHECK ((debit = 0) <> (credit = 0))
);
CREATE INDEX journal_lines_account_idx ON journal_lines (business_id, ledger_account_id, entry_date);
CREATE INDEX journal_lines_money_idx ON journal_lines (money_account_id, entry_date) WHERE money_account_id IS NOT NULL;
CREATE INDEX journal_lines_party_idx ON journal_lines (party_type, party_id, entry_date) WHERE party_id IS NOT NULL;
CREATE INDEX journal_lines_entry_idx ON journal_lines (entry_id);

CREATE OR REPLACE FUNCTION check_entry_balanced() RETURNS trigger AS $$
BEGIN
  IF (SELECT COALESCE(SUM(debit),0) - COALESCE(SUM(credit),0)
        FROM journal_lines WHERE entry_id = NEW.entry_id) <> 0 THEN
    RAISE EXCEPTION 'journal entry % is not balanced', NEW.entry_id;
  END IF;
  RETURN NULL;
END $$ LANGUAGE plpgsql;

CREATE CONSTRAINT TRIGGER journal_lines_balanced
  AFTER INSERT ON journal_lines
  DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW EXECUTE FUNCTION check_entry_balanced();

CREATE TABLE period_locks (
  business_id   uuid PRIMARY KEY REFERENCES businesses(id),
  locked_until  date NOT NULL,
  locked_by     uuid NOT NULL REFERENCES users(id),
  locked_at     timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE account_reconciliations (
  id               uuid PRIMARY KEY,
  business_id      uuid NOT NULL REFERENCES businesses(id),
  money_account_id uuid NOT NULL REFERENCES money_accounts(id),
  as_of_date       date NOT NULL,
  system_balance   numeric(18,2) NOT NULL,
  actual_balance   numeric(18,2) NOT NULL,
  difference       numeric(18,2) NOT NULL,
  note             text,
  created_by       uuid NOT NULL REFERENCES users(id),
  created_at       timestamptz NOT NULL DEFAULT now()
);
