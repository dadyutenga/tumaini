-- =====================================================================
-- 070_finance.sql — accounts, day closing, ledger, payments in/out,
-- allocations, contractor bills, expenses, petty cash, transfers
-- =====================================================================
SET search_path = app, public;

ALTER TABLE app.invoices ADD COLUMN design_job_id uuid;
ALTER TABLE app.invoices ADD CONSTRAINT invoices_design_job_fk
  FOREIGN KEY (tenant_id, design_job_id) REFERENCES app.design_jobs(tenant_id, id);
CREATE INDEX invoices_design_job_idx ON app.invoices (tenant_id, design_job_id) WHERE design_job_id IS NOT NULL;

-- ---------- Financial accounts ---------------------------------------
CREATE TYPE app.account_kind AS ENUM ('cash', 'bank', 'mobile_money', 'petty_cash');

CREATE TABLE app.financial_accounts (
  id                     uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id              uuid NOT NULL REFERENCES app.tenants(id),
  name                   app.short_text NOT NULL,
  kind                   app.account_kind NOT NULL,
  provider               text CHECK (length(provider) <= 60),  -- bank / M-Pesa / Airtel Money...
  account_number_masked  text CHECK (account_number_masked ~ '^[*0-9 -]{4,40}$'),
  location_id            uuid,
  business_unit_id       uuid,
  currency               app.currency NOT NULL DEFAULT 'TZS',
  current_balance        app.money NOT NULL DEFAULT 0,  -- maintained by ledger trigger only
  allow_negative         boolean NOT NULL DEFAULT false,
  show_on_invoices       boolean NOT NULL DEFAULT false,
  status                 app.record_status NOT NULL DEFAULT 'active',
  created_at             timestamptz NOT NULL DEFAULT now(),
  created_by             uuid DEFAULT app.current_membership_id(),
  updated_at             timestamptz NOT NULL DEFAULT now(),
  updated_by             uuid,
  row_version            integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, name),
  CHECK (allow_negative OR current_balance >= 0),
  CHECK (kind NOT IN ('cash', 'petty_cash') OR location_id IS NOT NULL),
  FOREIGN KEY (tenant_id, location_id)      REFERENCES app.locations(tenant_id, id),
  FOREIGN KEY (tenant_id, business_unit_id) REFERENCES app.business_units(tenant_id, id)
);
COMMENT ON TABLE app.financial_accounts IS 'Cash drawers, bank and mobile-money accounts (spec §8). Balance = opening txn + sum of ledger; never edited directly.';

-- ---------- End-of-day closing ---------------------------------------
CREATE TYPE app.day_status AS ENUM ('open', 'closed', 'reopened');
CREATE TABLE app.day_closings (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id     uuid NOT NULL REFERENCES app.tenants(id),
  location_id   uuid NOT NULL,
  business_date date NOT NULL,
  status        app.day_status NOT NULL DEFAULT 'open',
  expected_cash app.money,
  counted_cash  app.money,
  difference    numeric(18,2) GENERATED ALWAYS AS (counted_cash - expected_cash) STORED,
  note          text CHECK (length(note) <= 2000),
  closed_by     uuid,
  closed_at     timestamptz,
  reopened_by   uuid,
  reopened_at   timestamptz,
  reopen_reason text CHECK (length(reopen_reason) <= 1000),
  created_at    timestamptz NOT NULL DEFAULT now(),
  created_by    uuid DEFAULT app.current_membership_id(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  updated_by    uuid,
  row_version   integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, location_id, business_date),
  CHECK (status <> 'closed' OR (closed_by IS NOT NULL AND closed_at IS NOT NULL AND counted_cash IS NOT NULL AND expected_cash IS NOT NULL)),
  CHECK (status <> 'closed' OR counted_cash = expected_cash OR note IS NOT NULL),
  CHECK (status <> 'reopened' OR (reopened_by IS NOT NULL AND reopen_reason IS NOT NULL)),
  FOREIGN KEY (tenant_id, location_id) REFERENCES app.locations(tenant_id, id),
  FOREIGN KEY (tenant_id, closed_by)   REFERENCES app.memberships(tenant_id, id),
  FOREIGN KEY (tenant_id, reopened_by) REFERENCES app.memberships(tenant_id, id)
);

CREATE TABLE app.day_closing_lines (
  tenant_id       uuid NOT NULL,
  day_closing_id  uuid NOT NULL,
  account_id      uuid NOT NULL,
  expected_amount app.money NOT NULL,
  counted_amount  app.money NOT NULL,
  PRIMARY KEY (tenant_id, day_closing_id, account_id),
  FOREIGN KEY (tenant_id, day_closing_id) REFERENCES app.day_closings(tenant_id, id) ON DELETE CASCADE
);

-- Called by triggers on every money / stock document.
CREATE FUNCTION app.assert_day_open(p_tenant uuid, p_location uuid, p_date date) RETURNS void
LANGUAGE plpgsql STABLE AS $$
BEGIN
  IF EXISTS (SELECT 1 FROM app.day_closings
             WHERE tenant_id = p_tenant AND location_id = p_location
               AND business_date = p_date AND status = 'closed') THEN
    RAISE EXCEPTION 'business day % is closed for this location', p_date USING ERRCODE = 'BM002';
  END IF;
END $$;

-- ---------- Payments received --------------------------------------
CREATE TYPE app.payment_method AS ENUM ('cash', 'mobile_money', 'bank_transfer', 'card', 'cheque');
CREATE TYPE app.payment_status AS ENUM ('posted', 'voided');
CREATE TYPE app.payment_purpose AS ENUM ('sale', 'deposit', 'installment', 'final', 'debt_payment', 'advance');

CREATE TABLE app.payments_received (
  id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id        uuid NOT NULL REFERENCES app.tenants(id),
  receipt_no       text NOT NULL,
  customer_id      uuid,                  -- null only for walk-in sale payments
  business_unit_id uuid NOT NULL,
  location_id      uuid NOT NULL,
  account_id       uuid NOT NULL,
  method           app.payment_method NOT NULL,
  purpose          app.payment_purpose NOT NULL,
  amount           app.money_pos NOT NULL,
  reference        text CHECK (reference ~ '^[A-Za-z0-9/_.-]{3,60}$'),  -- M-Pesa code, bank ref
  received_at      timestamptz NOT NULL DEFAULT now(),
  business_date    date NOT NULL,
  received_by      uuid NOT NULL DEFAULT app.current_membership_id(),
  client_ref       uuid,                  -- idempotency key (POS offline / double-submit)
  status           app.payment_status NOT NULL DEFAULT 'posted',
  voided_at        timestamptz,
  voided_by        uuid,
  void_reason      text CHECK (length(void_reason) <= 1000),
  notes            text CHECK (length(notes) <= 1000),
  created_at       timestamptz NOT NULL DEFAULT now(),
  created_by       uuid DEFAULT app.current_membership_id(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  updated_by       uuid,
  row_version      integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, receipt_no),
  UNIQUE (tenant_id, client_ref),
  CHECK (method = 'cash' OR reference IS NOT NULL),
  CHECK (customer_id IS NOT NULL OR purpose = 'sale'),
  CHECK (status <> 'voided' OR (voided_at IS NOT NULL AND voided_by IS NOT NULL AND void_reason IS NOT NULL)),
  FOREIGN KEY (tenant_id, customer_id)      REFERENCES app.customers(tenant_id, id),
  FOREIGN KEY (tenant_id, business_unit_id) REFERENCES app.business_units(tenant_id, id),
  FOREIGN KEY (tenant_id, location_id)      REFERENCES app.locations(tenant_id, id),
  FOREIGN KEY (tenant_id, account_id)       REFERENCES app.financial_accounts(tenant_id, id),
  FOREIGN KEY (tenant_id, received_by)      REFERENCES app.memberships(tenant_id, id),
  FOREIGN KEY (tenant_id, voided_by)        REFERENCES app.memberships(tenant_id, id)
);
-- The same mobile-money / bank reference cannot be recorded twice.
CREATE UNIQUE INDEX payments_received_ref_uq ON app.payments_received (tenant_id, method, reference)
  WHERE reference IS NOT NULL AND status = 'posted';
CREATE INDEX payments_received_customer_idx ON app.payments_received (tenant_id, customer_id, received_at DESC);
CREATE INDEX payments_received_date_idx     ON app.payments_received (tenant_id, business_date, location_id);
CREATE INDEX payments_received_by_idx       ON app.payments_received (tenant_id, received_by, business_date DESC);

CREATE TABLE app.payment_allocations (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  payment_received_id uuid NOT NULL,
  sale_id             uuid,
  invoice_id          uuid,
  amount              app.money_pos NOT NULL,
  created_at          timestamptz NOT NULL DEFAULT now(),
  created_by          uuid DEFAULT app.current_membership_id(),
  UNIQUE (tenant_id, id),
  UNIQUE NULLS NOT DISTINCT (tenant_id, payment_received_id, sale_id, invoice_id),
  CHECK (num_nonnulls(sale_id, invoice_id) = 1),
  FOREIGN KEY (tenant_id, payment_received_id) REFERENCES app.payments_received(tenant_id, id),
  FOREIGN KEY (tenant_id, sale_id)             REFERENCES app.sales(tenant_id, id),
  FOREIGN KEY (tenant_id, invoice_id)          REFERENCES app.invoices(tenant_id, id)
);
COMMENT ON TABLE app.payment_allocations IS 'Links a receipt to the sale(s)/invoice(s) it settles. Split tender = several payments allocated to one sale; one payment can settle several debts.';
CREATE INDEX payment_allocations_sale_idx    ON app.payment_allocations (tenant_id, sale_id) WHERE sale_id IS NOT NULL;
CREATE INDEX payment_allocations_invoice_idx ON app.payment_allocations (tenant_id, invoice_id) WHERE invoice_id IS NOT NULL;

-- ---------- Contractor bills (payables to contractors) ----------------
CREATE TYPE app.bill_status AS ENUM ('open', 'partially_paid', 'paid', 'void');
CREATE TABLE app.contractor_bills (
  id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id        uuid NOT NULL REFERENCES app.tenants(id),
  bill_no          text NOT NULL,
  contractor_id    uuid NOT NULL,
  business_unit_id uuid NOT NULL,
  project_id       uuid,
  design_job_id    uuid,
  print_order_id   uuid,
  description      app.short_text NOT NULL,
  amount           app.money_pos NOT NULL,
  amount_paid      app.money_nn NOT NULL DEFAULT 0,
  balance_due      numeric(18,2) GENERATED ALWAYS AS (amount - amount_paid) STORED,
  bill_date        date NOT NULL,
  due_date         date,
  status           app.bill_status NOT NULL DEFAULT 'open',
  created_at       timestamptz NOT NULL DEFAULT now(),
  created_by       uuid DEFAULT app.current_membership_id(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  updated_by       uuid,
  row_version      integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, bill_no),
  UNIQUE (tenant_id, print_order_id),
  CHECK (amount_paid <= amount),
  FOREIGN KEY (tenant_id, contractor_id)    REFERENCES app.contractors(tenant_id, id),
  FOREIGN KEY (tenant_id, business_unit_id) REFERENCES app.business_units(tenant_id, id),
  FOREIGN KEY (tenant_id, project_id)       REFERENCES app.projects(tenant_id, id),
  FOREIGN KEY (tenant_id, design_job_id)    REFERENCES app.design_jobs(tenant_id, id),
  FOREIGN KEY (tenant_id, print_order_id)   REFERENCES app.print_orders(tenant_id, id)
);
CREATE INDEX contractor_bills_open_idx ON app.contractor_bills (tenant_id, contractor_id) WHERE status IN ('open', 'partially_paid');
CREATE INDEX contractor_bills_project_idx ON app.contractor_bills (tenant_id, project_id) WHERE project_id IS NOT NULL;
CREATE INDEX contractor_bills_job_idx ON app.contractor_bills (tenant_id, design_job_id) WHERE design_job_id IS NOT NULL;

-- ---------- Expenses ---------------------------------------------------
CREATE TABLE app.expense_categories (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES app.tenants(id),
  name        app.short_text NOT NULL,
  is_project_cost boolean NOT NULL DEFAULT false,   -- government fees, materials...
  status      app.record_status NOT NULL DEFAULT 'active',
  created_at  timestamptz NOT NULL DEFAULT now(),
  created_by  uuid DEFAULT app.current_membership_id(),
  UNIQUE (tenant_id, name),
  UNIQUE (tenant_id, id)
);

CREATE TABLE app.recurring_expenses (
  id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id        uuid NOT NULL REFERENCES app.tenants(id),
  category_id      uuid NOT NULL,
  description      app.short_text NOT NULL,
  amount           app.money_pos NOT NULL,
  business_unit_id uuid NOT NULL,
  location_id      uuid NOT NULL,
  account_id       uuid,
  day_of_month     smallint NOT NULL CHECK (day_of_month BETWEEN 1 AND 28),
  next_run_on      date NOT NULL,
  active           boolean NOT NULL DEFAULT true,
  created_at       timestamptz NOT NULL DEFAULT now(),
  created_by       uuid DEFAULT app.current_membership_id(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  updated_by       uuid,
  row_version      integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id, category_id)      REFERENCES app.expense_categories(tenant_id, id),
  FOREIGN KEY (tenant_id, business_unit_id) REFERENCES app.business_units(tenant_id, id),
  FOREIGN KEY (tenant_id, location_id)      REFERENCES app.locations(tenant_id, id),
  FOREIGN KEY (tenant_id, account_id)       REFERENCES app.financial_accounts(tenant_id, id)
);
CREATE INDEX recurring_expenses_due_idx ON app.recurring_expenses (next_run_on) WHERE active;

CREATE TYPE app.expense_status AS ENUM ('submitted', 'approved', 'rejected', 'paid', 'cancelled');
CREATE TABLE app.expenses (
  id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id            uuid NOT NULL REFERENCES app.tenants(id),
  expense_no           text NOT NULL,
  category_id          uuid NOT NULL,
  description          app.short_text NOT NULL,
  amount               app.money_pos NOT NULL,
  expense_date         date NOT NULL,
  business_unit_id     uuid NOT NULL,
  location_id          uuid NOT NULL,
  project_id           uuid,
  design_job_id        uuid,
  account_id           uuid,               -- "paid from"; required once paid
  receipt_file_id      uuid,
  status               app.expense_status NOT NULL DEFAULT 'submitted',
  requested_by         uuid NOT NULL DEFAULT app.current_membership_id(),
  decided_by           uuid,
  decided_at           timestamptz,
  rejection_reason     text CHECK (length(rejection_reason) <= 1000),
  paid_at              timestamptz,
  paid_business_date   date,
  recurring_expense_id uuid,
  created_at           timestamptz NOT NULL DEFAULT now(),
  created_by           uuid DEFAULT app.current_membership_id(),
  updated_at           timestamptz NOT NULL DEFAULT now(),
  updated_by           uuid,
  row_version          integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, expense_no),
  CHECK (status NOT IN ('approved', 'rejected', 'paid') OR (decided_by IS NOT NULL AND decided_at IS NOT NULL)),
  CHECK (status <> 'rejected' OR rejection_reason IS NOT NULL),
  CHECK (status <> 'paid' OR (account_id IS NOT NULL AND paid_at IS NOT NULL AND paid_business_date IS NOT NULL)),
  CHECK (decided_by IS NULL OR decided_by <> requested_by OR recurring_expense_id IS NOT NULL),
  FOREIGN KEY (tenant_id, category_id)          REFERENCES app.expense_categories(tenant_id, id),
  FOREIGN KEY (tenant_id, business_unit_id)     REFERENCES app.business_units(tenant_id, id),
  FOREIGN KEY (tenant_id, location_id)          REFERENCES app.locations(tenant_id, id),
  FOREIGN KEY (tenant_id, project_id)           REFERENCES app.projects(tenant_id, id),
  FOREIGN KEY (tenant_id, design_job_id)        REFERENCES app.design_jobs(tenant_id, id),
  FOREIGN KEY (tenant_id, account_id)           REFERENCES app.financial_accounts(tenant_id, id),
  FOREIGN KEY (tenant_id, receipt_file_id)      REFERENCES app.files(tenant_id, id),
  FOREIGN KEY (tenant_id, requested_by)         REFERENCES app.memberships(tenant_id, id),
  FOREIGN KEY (tenant_id, decided_by)           REFERENCES app.memberships(tenant_id, id),
  FOREIGN KEY (tenant_id, recurring_expense_id) REFERENCES app.recurring_expenses(tenant_id, id)
);
CREATE UNIQUE INDEX expenses_recurring_once ON app.expenses (tenant_id, recurring_expense_id, expense_date)
  WHERE recurring_expense_id IS NOT NULL;
CREATE INDEX expenses_status_idx  ON app.expenses (tenant_id, status, expense_date DESC);
CREATE INDEX expenses_project_idx ON app.expenses (tenant_id, project_id) WHERE project_id IS NOT NULL;
CREATE INDEX expenses_job_idx     ON app.expenses (tenant_id, design_job_id) WHERE design_job_id IS NOT NULL;
CREATE INDEX expenses_requester_idx ON app.expenses (tenant_id, requested_by);

-- ---------- Payments made ----------------------------------------------
CREATE TABLE app.payments_made (
  id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id        uuid NOT NULL REFERENCES app.tenants(id),
  payment_no       text NOT NULL,
  business_unit_id uuid NOT NULL,
  location_id      uuid NOT NULL,
  account_id       uuid NOT NULL,
  method           app.payment_method NOT NULL,
  amount           app.money_pos NOT NULL,
  reference        text CHECK (reference ~ '^[A-Za-z0-9/_.-]{3,60}$'),
  paid_at          timestamptz NOT NULL DEFAULT now(),
  business_date    date NOT NULL,
  supplier_id      uuid,
  contractor_id    uuid,
  employee_id      uuid,
  customer_id      uuid,          -- refunds
  other_payee_name text CHECK (length(btrim(other_payee_name)) BETWEEN 2 AND 200),
  status           app.payment_status NOT NULL DEFAULT 'posted',
  voided_at        timestamptz,
  voided_by        uuid,
  void_reason      text CHECK (length(void_reason) <= 1000),
  client_ref       uuid,
  notes            text CHECK (length(notes) <= 1000),
  created_at       timestamptz NOT NULL DEFAULT now(),
  created_by       uuid DEFAULT app.current_membership_id(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  updated_by       uuid,
  row_version      integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, payment_no),
  UNIQUE (tenant_id, client_ref),
  CHECK (num_nonnulls(supplier_id, contractor_id, employee_id, customer_id, other_payee_name) = 1),
  CHECK (status <> 'voided' OR (voided_at IS NOT NULL AND voided_by IS NOT NULL AND void_reason IS NOT NULL)),
  FOREIGN KEY (tenant_id, business_unit_id) REFERENCES app.business_units(tenant_id, id),
  FOREIGN KEY (tenant_id, location_id)      REFERENCES app.locations(tenant_id, id),
  FOREIGN KEY (tenant_id, account_id)       REFERENCES app.financial_accounts(tenant_id, id),
  FOREIGN KEY (tenant_id, supplier_id)      REFERENCES app.suppliers(tenant_id, id),
  FOREIGN KEY (tenant_id, contractor_id)    REFERENCES app.contractors(tenant_id, id),
  FOREIGN KEY (tenant_id, employee_id)      REFERENCES app.employees(tenant_id, id),
  FOREIGN KEY (tenant_id, customer_id)      REFERENCES app.customers(tenant_id, id),
  FOREIGN KEY (tenant_id, voided_by)        REFERENCES app.memberships(tenant_id, id)
);
CREATE INDEX payments_made_date_idx ON app.payments_made (tenant_id, business_date, location_id);
CREATE INDEX payments_made_supplier_idx ON app.payments_made (tenant_id, supplier_id) WHERE supplier_id IS NOT NULL;
CREATE INDEX payments_made_contractor_idx ON app.payments_made (tenant_id, contractor_id) WHERE contractor_id IS NOT NULL;

CREATE TABLE app.payment_made_allocations (
  id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id          uuid NOT NULL REFERENCES app.tenants(id),
  payment_made_id    uuid NOT NULL,
  purchase_id        uuid,
  contractor_bill_id uuid,
  sale_return_id     uuid,
  amount             app.money_pos NOT NULL,
  created_at         timestamptz NOT NULL DEFAULT now(),
  created_by         uuid DEFAULT app.current_membership_id(),
  UNIQUE (tenant_id, id),
  UNIQUE NULLS NOT DISTINCT (tenant_id, payment_made_id, purchase_id, contractor_bill_id, sale_return_id),
  CHECK (num_nonnulls(purchase_id, contractor_bill_id, sale_return_id) = 1),
  FOREIGN KEY (tenant_id, payment_made_id)    REFERENCES app.payments_made(tenant_id, id),
  FOREIGN KEY (tenant_id, purchase_id)        REFERENCES app.purchases(tenant_id, id),
  FOREIGN KEY (tenant_id, contractor_bill_id) REFERENCES app.contractor_bills(tenant_id, id),
  FOREIGN KEY (tenant_id, sale_return_id)     REFERENCES app.sale_returns(tenant_id, id)
);

-- ---------- Transfers between accounts (incl. petty cash top-up) -------
CREATE TABLE app.account_transfers (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id       uuid NOT NULL REFERENCES app.tenants(id),
  transfer_no     text NOT NULL,
  from_account_id uuid NOT NULL,
  to_account_id   uuid NOT NULL,
  amount          app.money_pos NOT NULL,
  location_id     uuid NOT NULL,
  business_date   date NOT NULL,
  note            text CHECK (length(note) <= 1000),
  created_at      timestamptz NOT NULL DEFAULT now(),
  created_by      uuid DEFAULT app.current_membership_id(),
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, transfer_no),
  CHECK (from_account_id <> to_account_id),
  FOREIGN KEY (tenant_id, from_account_id) REFERENCES app.financial_accounts(tenant_id, id),
  FOREIGN KEY (tenant_id, to_account_id)   REFERENCES app.financial_accounts(tenant_id, id),
  FOREIGN KEY (tenant_id, location_id)     REFERENCES app.locations(tenant_id, id)
);

CREATE TABLE app.petty_cash_reconciliations (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id       uuid NOT NULL REFERENCES app.tenants(id),
  account_id      uuid NOT NULL,
  period_start    date NOT NULL,
  period_end      date NOT NULL,
  opening_balance app.money NOT NULL,
  expected_closing app.money NOT NULL,
  counted_closing app.money NOT NULL,
  difference      numeric(18,2) GENERATED ALWAYS AS (counted_closing - expected_closing) STORED,
  note            text CHECK (length(note) <= 1000),
  created_at      timestamptz NOT NULL DEFAULT now(),
  created_by      uuid DEFAULT app.current_membership_id(),
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, account_id, period_end),
  CHECK (period_end >= period_start),
  CHECK (counted_closing = expected_closing OR note IS NOT NULL),
  FOREIGN KEY (tenant_id, account_id) REFERENCES app.financial_accounts(tenant_id, id)
);

-- ---------- The ledger --------------------------------------------------
CREATE TYPE app.txn_direction AS ENUM ('in', 'out');
CREATE TYPE app.txn_type AS ENUM ('opening_balance', 'customer_receipt', 'supplier_payment', 'contractor_payment',
  'refund', 'other_payment', 'expense', 'salary', 'transfer', 'correction', 'reversal');

CREATE TABLE app.financial_transactions (
  id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id               uuid NOT NULL REFERENCES app.tenants(id),
  account_id              uuid NOT NULL,
  direction               app.txn_direction NOT NULL,
  amount                  app.money_pos NOT NULL,
  txn_type                app.txn_type NOT NULL,
  business_unit_id        uuid NOT NULL,
  location_id             uuid NOT NULL,
  business_date           date NOT NULL,
  occurred_at             timestamptz NOT NULL DEFAULT now(),
  description             text NOT NULL CHECK (length(description) BETWEEN 1 AND 500),
  project_id              uuid,
  design_job_id           uuid,
  payment_received_id     uuid,
  payment_made_id         uuid,
  expense_id              uuid,
  account_transfer_id     uuid,
  payroll_line_id         uuid,     -- FK added in 080
  reverses_transaction_id uuid,     -- reversal / correction of an earlier row
  approval_request_id     uuid,     -- FK added in 090 (required for manual corrections)
  created_at              timestamptz NOT NULL DEFAULT now(),
  created_by              uuid DEFAULT app.current_membership_id(),
  UNIQUE (tenant_id, id),
  CHECK (num_nonnulls(payment_received_id, payment_made_id, expense_id, account_transfer_id, payroll_line_id)
         <= 1),
  CHECK (txn_type NOT IN ('reversal', 'correction') OR reverses_transaction_id IS NOT NULL),
  CHECK (txn_type <> 'correction' OR approval_request_id IS NOT NULL),
  CHECK (txn_type <> 'customer_receipt' OR (payment_received_id IS NOT NULL AND direction = 'in')),
  CHECK (txn_type <> 'expense'          OR expense_id IS NOT NULL),
  CHECK (txn_type <> 'transfer'         OR account_transfer_id IS NOT NULL),
  CHECK (txn_type <> 'salary'           OR payroll_line_id IS NOT NULL),
  FOREIGN KEY (tenant_id, account_id)              REFERENCES app.financial_accounts(tenant_id, id),
  FOREIGN KEY (tenant_id, business_unit_id)        REFERENCES app.business_units(tenant_id, id),
  FOREIGN KEY (tenant_id, location_id)             REFERENCES app.locations(tenant_id, id),
  FOREIGN KEY (tenant_id, project_id)              REFERENCES app.projects(tenant_id, id),
  FOREIGN KEY (tenant_id, design_job_id)           REFERENCES app.design_jobs(tenant_id, id),
  FOREIGN KEY (tenant_id, payment_received_id)     REFERENCES app.payments_received(tenant_id, id),
  FOREIGN KEY (tenant_id, payment_made_id)         REFERENCES app.payments_made(tenant_id, id),
  FOREIGN KEY (tenant_id, expense_id)              REFERENCES app.expenses(tenant_id, id),
  FOREIGN KEY (tenant_id, account_transfer_id)     REFERENCES app.account_transfers(tenant_id, id),
  FOREIGN KEY (tenant_id, reverses_transaction_id) REFERENCES app.financial_transactions(tenant_id, id)
);
COMMENT ON TABLE app.financial_transactions IS 'Append-only cash-book ledger (spec §8: transactions cannot be deleted; mistakes fixed by an approved correction entry). Posted by triggers from source documents.';
CREATE INDEX fin_txn_account_idx ON app.financial_transactions (tenant_id, account_id, occurred_at DESC);
CREATE INDEX fin_txn_date_idx    ON app.financial_transactions (tenant_id, business_date, business_unit_id);
CREATE INDEX fin_txn_project_idx ON app.financial_transactions (tenant_id, project_id) WHERE project_id IS NOT NULL;
-- A transaction can be reversed only once.
CREATE UNIQUE INDEX fin_txn_reversed_once ON app.financial_transactions (tenant_id, reverses_transaction_id)
  WHERE txn_type = 'reversal';
