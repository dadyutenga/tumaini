-- =====================================================================
-- 080_payroll.sql — commissions, advances, adjustments, payroll, payslips
-- =====================================================================
SET search_path = app, public;

CREATE TYPE app.commission_basis AS ENUM ('sale_percent', 'project_fixed');
CREATE TABLE app.commission_rules (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  name                app.short_text NOT NULL,
  basis               app.commission_basis NOT NULL,
  percent             app.pct,
  fixed_amount        app.money_pos,
  product_category_id uuid,      -- optional scope
  service_id          uuid,      -- optional scope
  employee_id         uuid,      -- optional: rule for one person
  effective_from      date NOT NULL,
  effective_to        date,
  active              boolean NOT NULL DEFAULT true,
  created_at          timestamptz NOT NULL DEFAULT now(),
  created_by          uuid DEFAULT app.current_membership_id(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  updated_by          uuid,
  row_version         integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id),
  CHECK ((basis = 'sale_percent'  AND percent IS NOT NULL AND fixed_amount IS NULL AND service_id IS NULL)
      OR (basis = 'project_fixed' AND fixed_amount IS NOT NULL AND percent IS NULL AND product_category_id IS NULL)),
  CHECK (effective_to IS NULL OR effective_to > effective_from),
  FOREIGN KEY (tenant_id, product_category_id) REFERENCES app.product_categories(tenant_id, id),
  FOREIGN KEY (tenant_id, service_id)          REFERENCES app.services(tenant_id, id),
  FOREIGN KEY (tenant_id, employee_id)         REFERENCES app.employees(tenant_id, id)
);

CREATE TYPE app.payroll_status AS ENUM ('draft', 'approved', 'paid', 'cancelled');
CREATE TABLE app.payroll_runs (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id    uuid NOT NULL REFERENCES app.tenants(id),
  period_year  smallint NOT NULL CHECK (period_year BETWEEN 2020 AND 2100),
  period_month smallint NOT NULL CHECK (period_month BETWEEN 1 AND 12),
  status       app.payroll_status NOT NULL DEFAULT 'draft',
  account_id   uuid,                  -- pay-from account, required when paid
  location_id  uuid NOT NULL,         -- for day-lock + ledger posting
  business_unit_id uuid NOT NULL,     -- ledger attribution (ASSUMPTION A-11)
  generated_by uuid NOT NULL DEFAULT app.current_membership_id(),
  generated_at timestamptz NOT NULL DEFAULT now(),
  approved_by  uuid,
  approved_at  timestamptz,
  paid_by      uuid,
  paid_at      timestamptz,
  paid_business_date date,
  notes        text CHECK (length(notes) <= 2000),
  created_at   timestamptz NOT NULL DEFAULT now(),
  created_by   uuid DEFAULT app.current_membership_id(),
  updated_at   timestamptz NOT NULL DEFAULT now(),
  updated_by   uuid,
  row_version  integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id),
  CHECK (status NOT IN ('approved', 'paid') OR (approved_by IS NOT NULL AND approved_at IS NOT NULL)),
  CHECK (status <> 'paid' OR (paid_by IS NOT NULL AND paid_at IS NOT NULL AND account_id IS NOT NULL AND paid_business_date IS NOT NULL)),
  FOREIGN KEY (tenant_id, account_id)       REFERENCES app.financial_accounts(tenant_id, id),
  FOREIGN KEY (tenant_id, location_id)      REFERENCES app.locations(tenant_id, id),
  FOREIGN KEY (tenant_id, business_unit_id) REFERENCES app.business_units(tenant_id, id),
  FOREIGN KEY (tenant_id, generated_by)     REFERENCES app.memberships(tenant_id, id),
  FOREIGN KEY (tenant_id, approved_by)      REFERENCES app.memberships(tenant_id, id),
  FOREIGN KEY (tenant_id, paid_by)          REFERENCES app.memberships(tenant_id, id)
);
CREATE UNIQUE INDEX payroll_runs_one_per_period ON app.payroll_runs (tenant_id, period_year, period_month)
  WHERE status <> 'cancelled';

CREATE TABLE app.payroll_lines (
  id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id            uuid NOT NULL REFERENCES app.tenants(id),
  payroll_run_id       uuid NOT NULL,
  employee_id          uuid NOT NULL,
  basic_salary         app.money_nn NOT NULL DEFAULT 0,
  allowances           app.money_nn NOT NULL DEFAULT 0,
  commission           app.money_nn NOT NULL DEFAULT 0,
  overtime             app.money_nn NOT NULL DEFAULT 0,
  bonuses              app.money_nn NOT NULL DEFAULT 0,
  deductions           app.money_nn NOT NULL DEFAULT 0,
  advance_repayment    app.money_nn NOT NULL DEFAULT 0,
  statutory_deductions app.money_nn NOT NULL DEFAULT 0,  -- PAYE/NSSF etc. (D-08: rules not in spec)
  gross_pay            numeric(18,2) GENERATED ALWAYS AS (basic_salary + allowances + commission + overtime + bonuses) STORED,
  net_pay              numeric(18,2) GENERATED ALWAYS AS
                         (basic_salary + allowances + commission + overtime + bonuses
                          - deductions - advance_repayment - statutory_deductions) STORED,
  pay_method           app.pay_method NOT NULL,
  payment_reference    text CHECK (length(payment_reference) <= 60),
  paid_at              timestamptz,
  adjustment_note      text CHECK (length(adjustment_note) <= 1000),
  created_at           timestamptz NOT NULL DEFAULT now(),
  created_by           uuid DEFAULT app.current_membership_id(),
  updated_at           timestamptz NOT NULL DEFAULT now(),
  updated_by           uuid,
  row_version          integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, payroll_run_id, employee_id),
  CHECK (basic_salary + allowances + commission + overtime + bonuses
         - deductions - advance_repayment - statutory_deductions >= 0),
  FOREIGN KEY (tenant_id, payroll_run_id) REFERENCES app.payroll_runs(tenant_id, id) ON DELETE CASCADE,
  FOREIGN KEY (tenant_id, employee_id)    REFERENCES app.employees(tenant_id, id)
);
CREATE INDEX payroll_lines_employee_idx ON app.payroll_lines (tenant_id, employee_id);

CREATE TABLE app.payroll_statutory_items (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id       uuid NOT NULL REFERENCES app.tenants(id),
  payroll_line_id uuid NOT NULL,
  code            text NOT NULL CHECK (code ~ '^[A-Z_]{2,20}$'),  -- e.g. PAYE, NSSF, WCF, SDL
  employee_amount app.money_nn NOT NULL DEFAULT 0,
  employer_amount app.money_nn NOT NULL DEFAULT 0,
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, payroll_line_id, code),
  FOREIGN KEY (tenant_id, payroll_line_id) REFERENCES app.payroll_lines(tenant_id, id) ON DELETE CASCADE
);
COMMENT ON TABLE app.payroll_statutory_items IS 'Breakdown of payroll_lines.statutory_deductions. Rates are NOT encoded here: Tanzanian PAYE/NSSF/WCF/SDL rules need a stakeholder-approved calculator (decision D-08).';

CREATE TYPE app.commission_status AS ENUM ('accrued', 'included', 'reversed');
CREATE TABLE app.commission_entries (
  id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id          uuid NOT NULL REFERENCES app.tenants(id),
  employee_id        uuid NOT NULL,
  commission_rule_id uuid NOT NULL,
  sale_id            uuid,
  project_id         uuid,
  sale_return_id     uuid,           -- set on negative reversal entries
  amount             app.money NOT NULL CHECK (amount <> 0),
  earned_on          date NOT NULL,
  status             app.commission_status NOT NULL DEFAULT 'accrued',
  payroll_line_id    uuid,
  created_at         timestamptz NOT NULL DEFAULT now(),
  created_by         uuid DEFAULT app.current_membership_id(),
  UNIQUE (tenant_id, id),
  CHECK (num_nonnulls(sale_id, project_id) = 1),
  CHECK (status <> 'included' OR payroll_line_id IS NOT NULL),
  CHECK (amount > 0 OR sale_return_id IS NOT NULL),   -- negatives only as return reversals
  FOREIGN KEY (tenant_id, employee_id)        REFERENCES app.employees(tenant_id, id),
  FOREIGN KEY (tenant_id, commission_rule_id) REFERENCES app.commission_rules(tenant_id, id),
  FOREIGN KEY (tenant_id, sale_id)            REFERENCES app.sales(tenant_id, id),
  FOREIGN KEY (tenant_id, project_id)         REFERENCES app.projects(tenant_id, id),
  FOREIGN KEY (tenant_id, sale_return_id)     REFERENCES app.sale_returns(tenant_id, id),
  FOREIGN KEY (tenant_id, payroll_line_id)    REFERENCES app.payroll_lines(tenant_id, id)
);
-- One positive accrual per rule per source per employee (idempotent accrual).
CREATE UNIQUE INDEX commission_once ON app.commission_entries
  (tenant_id, commission_rule_id, employee_id, coalesce(sale_id, project_id))
  WHERE sale_return_id IS NULL AND amount > 0;
CREATE INDEX commission_open_idx ON app.commission_entries (tenant_id, employee_id) WHERE status = 'accrued';

CREATE TYPE app.advance_kind AS ENUM ('advance', 'loan');
CREATE TYPE app.advance_status AS ENUM ('active', 'settled', 'written_off');
CREATE TABLE app.employee_advances (
  id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id         uuid NOT NULL REFERENCES app.tenants(id),
  employee_id       uuid NOT NULL,
  kind              app.advance_kind NOT NULL,
  principal         app.money_pos NOT NULL,
  issued_on         date NOT NULL,
  monthly_deduction app.money_pos NOT NULL,
  amount_repaid     app.money_nn NOT NULL DEFAULT 0,
  balance           numeric(18,2) GENERATED ALWAYS AS (principal - amount_repaid) STORED,
  status            app.advance_status NOT NULL DEFAULT 'active',
  approved_by       uuid NOT NULL,
  payment_made_id   uuid,            -- money out when issued
  notes             text CHECK (length(notes) <= 1000),
  created_at        timestamptz NOT NULL DEFAULT now(),
  created_by        uuid DEFAULT app.current_membership_id(),
  updated_at        timestamptz NOT NULL DEFAULT now(),
  updated_by        uuid,
  row_version       integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, payment_made_id),
  CHECK (monthly_deduction <= principal),
  CHECK (amount_repaid <= principal),
  CHECK (status <> 'settled' OR amount_repaid = principal),
  FOREIGN KEY (tenant_id, employee_id)     REFERENCES app.employees(tenant_id, id),
  FOREIGN KEY (tenant_id, approved_by)     REFERENCES app.memberships(tenant_id, id),
  FOREIGN KEY (tenant_id, payment_made_id) REFERENCES app.payments_made(tenant_id, id)
);
CREATE INDEX employee_advances_active_idx ON app.employee_advances (tenant_id, employee_id) WHERE status = 'active';

CREATE TABLE app.advance_repayments (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id       uuid NOT NULL REFERENCES app.tenants(id),
  advance_id      uuid NOT NULL,
  payroll_line_id uuid NOT NULL,
  amount          app.money_pos NOT NULL,
  created_at      timestamptz NOT NULL DEFAULT now(),
  created_by      uuid DEFAULT app.current_membership_id(),
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, advance_id, payroll_line_id),
  FOREIGN KEY (tenant_id, advance_id)      REFERENCES app.employee_advances(tenant_id, id),
  FOREIGN KEY (tenant_id, payroll_line_id) REFERENCES app.payroll_lines(tenant_id, id) ON DELETE CASCADE
);

CREATE TYPE app.pay_adjustment_kind AS ENUM ('bonus', 'deduction');
CREATE TABLE app.employee_pay_adjustments (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id       uuid NOT NULL REFERENCES app.tenants(id),
  employee_id     uuid NOT NULL,
  kind            app.pay_adjustment_kind NOT NULL,
  amount          app.money_pos NOT NULL,
  reason          text NOT NULL CHECK (length(btrim(reason)) >= 3),
  period_year     smallint NOT NULL CHECK (period_year BETWEEN 2020 AND 2100),
  period_month    smallint NOT NULL CHECK (period_month BETWEEN 1 AND 12),
  payroll_line_id uuid,
  created_at      timestamptz NOT NULL DEFAULT now(),
  created_by      uuid DEFAULT app.current_membership_id(),
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id, employee_id)     REFERENCES app.employees(tenant_id, id),
  FOREIGN KEY (tenant_id, payroll_line_id) REFERENCES app.payroll_lines(tenant_id, id)
);

CREATE TABLE app.payslips (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id       uuid NOT NULL REFERENCES app.tenants(id),
  payslip_no      text NOT NULL,
  payroll_line_id uuid NOT NULL,
  file_id         uuid,
  issued_at       timestamptz NOT NULL DEFAULT now(),
  created_at      timestamptz NOT NULL DEFAULT now(),
  created_by      uuid DEFAULT app.current_membership_id(),
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, payslip_no),
  UNIQUE (tenant_id, payroll_line_id),
  FOREIGN KEY (tenant_id, payroll_line_id) REFERENCES app.payroll_lines(tenant_id, id),
  FOREIGN KEY (tenant_id, file_id)         REFERENCES app.files(tenant_id, id)
);

ALTER TABLE app.financial_transactions ADD CONSTRAINT fin_txn_payroll_line_fk
  FOREIGN KEY (tenant_id, payroll_line_id) REFERENCES app.payroll_lines(tenant_id, id);
CREATE UNIQUE INDEX fin_txn_salary_once ON app.financial_transactions (tenant_id, payroll_line_id)
  WHERE txn_type = 'salary';
