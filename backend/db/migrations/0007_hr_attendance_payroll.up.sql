CREATE TABLE departments (
  id           uuid PRIMARY KEY,
  business_id  uuid NOT NULL REFERENCES businesses(id),
  name         text NOT NULL,
  head_id      uuid,
  created_at   timestamptz NOT NULL DEFAULT now(),
  UNIQUE (business_id, name)
);

CREATE TABLE positions (
  id            uuid PRIMARY KEY,
  business_id   uuid NOT NULL REFERENCES businesses(id),
  name          text NOT NULL,
  default_pay   numeric(18,2),
  created_at    timestamptz NOT NULL DEFAULT now(),
  UNIQUE (business_id, name)
);

CREATE TABLE employees (
  id               uuid PRIMARY KEY,
  business_id      uuid NOT NULL REFERENCES businesses(id),
  branch_id        uuid NOT NULL REFERENCES branches(id),
  department_id    uuid REFERENCES departments(id),
  position_id      uuid REFERENCES positions(id),
  employee_no      text NOT NULL,
  name             text NOT NULL,
  phone            text,
  email            citext,
  address          text,
  id_reference     text,
  photo_file       text,
  hire_date        date NOT NULL,
  employment_type  text NOT NULL CHECK (employment_type IN ('permanent','contract','casual')),
  pay_type         text NOT NULL CHECK (pay_type IN ('monthly','daily','hourly')),
  base_pay         numeric(18,2) NOT NULL DEFAULT 0 CHECK (base_pay >= 0),
  pay_method       text CHECK (pay_method IN ('cash','bank','mobile_money')),
  pay_account      text,
  emergency_contact text,
  status           text NOT NULL DEFAULT 'active' CHECK (status IN ('active','suspended','terminated')),
  terminated_on    date,
  termination_reason text,
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  UNIQUE (business_id, employee_no)
);
CREATE INDEX employees_branch_idx ON employees (branch_id);

ALTER TABLE users ADD CONSTRAINT users_employee_fk FOREIGN KEY (employee_id) REFERENCES employees(id);
ALTER TABLE branches ADD CONSTRAINT branches_manager_fk FOREIGN KEY (manager_id) REFERENCES employees(id);
ALTER TABLE departments ADD CONSTRAINT departments_head_fk FOREIGN KEY (head_id) REFERENCES employees(id);
ALTER TABLE sales ADD CONSTRAINT sales_employee_fk FOREIGN KEY (employee_id) REFERENCES employees(id);

CREATE TABLE leave_types (
  id            uuid PRIMARY KEY,
  business_id   uuid NOT NULL REFERENCES businesses(id),
  name          text NOT NULL,
  days_per_year numeric(5,1) NOT NULL DEFAULT 0 CHECK (days_per_year >= 0),
  is_paid       boolean NOT NULL DEFAULT true,
  is_active     boolean NOT NULL DEFAULT true,
  UNIQUE (business_id, name)
);

CREATE TABLE leave_requests (
  id            uuid PRIMARY KEY,
  business_id   uuid NOT NULL REFERENCES businesses(id),
  employee_id   uuid NOT NULL REFERENCES employees(id),
  leave_type_id uuid NOT NULL REFERENCES leave_types(id),
  start_date    date NOT NULL,
  end_date      date NOT NULL,
  days          numeric(5,1) NOT NULL CHECK (days > 0),
  reason        text,
  status        text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected','cancelled')),
  decided_by    uuid REFERENCES users(id),
  decided_at    timestamptz,
  created_by    uuid REFERENCES users(id),
  created_at    timestamptz NOT NULL DEFAULT now(),
  CHECK (end_date >= start_date)
);
CREATE INDEX leave_requests_emp_idx ON leave_requests (employee_id, start_date);

CREATE TABLE employee_loans (
  id                uuid PRIMARY KEY,
  business_id       uuid NOT NULL REFERENCES businesses(id),
  branch_id         uuid REFERENCES branches(id),
  number            text NOT NULL,
  employee_id       uuid NOT NULL REFERENCES employees(id),
  kind              text NOT NULL CHECK (kind IN ('loan','advance')),
  principal         numeric(18,2) NOT NULL CHECK (principal > 0),
  monthly_deduction numeric(18,2) NOT NULL CHECK (monthly_deduction > 0),
  balance           numeric(18,2) NOT NULL CHECK (balance >= 0),
  start_period      date NOT NULL,
  status            text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','disbursed','settled','rejected')),
  payment_id        uuid REFERENCES payments(id),
  approved_by       uuid REFERENCES users(id),
  created_by        uuid NOT NULL REFERENCES users(id),
  created_at        timestamptz NOT NULL DEFAULT now(),
  UNIQUE (business_id, number)
);
CREATE INDEX employee_loans_emp_idx ON employee_loans (employee_id);

CREATE TABLE loan_repayments (
  id             uuid PRIMARY KEY,
  business_id    uuid NOT NULL REFERENCES businesses(id),
  loan_id        uuid NOT NULL REFERENCES employee_loans(id),
  amount         numeric(18,2) NOT NULL CHECK (amount > 0),
  source         text NOT NULL CHECK (source IN ('payroll','cash')),
  payslip_id     uuid,
  payment_id     uuid REFERENCES payments(id),
  created_at     timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE commission_rules (
  id            uuid PRIMARY KEY,
  business_id   uuid NOT NULL REFERENCES businesses(id),
  employee_id   uuid REFERENCES employees(id),
  position_id   uuid REFERENCES positions(id),
  calc          text NOT NULL CHECK (calc IN ('percent_of_sales','fixed_per_sale')),
  value         numeric(18,4) NOT NULL CHECK (value >= 0),
  min_target    numeric(18,2) NOT NULL DEFAULT 0,
  is_active     boolean NOT NULL DEFAULT true,
  CHECK (employee_id IS NOT NULL OR position_id IS NOT NULL)
);

CREATE TABLE pay_adjustments (
  id            uuid PRIMARY KEY,
  business_id   uuid NOT NULL REFERENCES businesses(id),
  employee_id   uuid NOT NULL REFERENCES employees(id),
  period        date NOT NULL,
  kind          text NOT NULL CHECK (kind IN ('bonus','deduction')),
  amount        numeric(18,2) NOT NULL CHECK (amount > 0),
  reason        text NOT NULL,
  payslip_id    uuid,
  created_by    uuid NOT NULL REFERENCES users(id),
  created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE work_schedules (
  id            uuid PRIMARY KEY,
  business_id   uuid NOT NULL REFERENCES businesses(id),
  branch_id     uuid REFERENCES branches(id),
  employee_id   uuid REFERENCES employees(id),
  weekday       smallint NOT NULL CHECK (weekday BETWEEN 0 AND 6),
  is_working    boolean NOT NULL DEFAULT true,
  start_time    time,
  end_time      time,
  break_minutes integer NOT NULL DEFAULT 0 CHECK (break_minutes >= 0),
  CHECK (branch_id IS NOT NULL OR employee_id IS NOT NULL)
);
CREATE UNIQUE INDEX work_schedules_branch_uq ON work_schedules (branch_id, weekday) WHERE employee_id IS NULL AND branch_id IS NOT NULL;
CREATE UNIQUE INDEX work_schedules_emp_uq ON work_schedules (employee_id, weekday) WHERE employee_id IS NOT NULL;

CREATE TABLE holidays (
  id           uuid PRIMARY KEY,
  business_id  uuid NOT NULL REFERENCES businesses(id),
  day          date NOT NULL,
  name         text NOT NULL,
  UNIQUE (business_id, day)
);

CREATE TABLE attendance_events (
  id            uuid PRIMARY KEY,
  business_id   uuid NOT NULL REFERENCES businesses(id),
  branch_id     uuid NOT NULL REFERENCES branches(id),
  employee_id   uuid NOT NULL REFERENCES employees(id),
  type          text NOT NULL CHECK (type IN ('in','out')),
  at            timestamptz NOT NULL,
  source        text NOT NULL CHECK (source IN ('self','manager','manual')),
  note          text,
  ip            inet,
  created_by    uuid NOT NULL REFERENCES users(id),
  created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX attendance_events_emp_idx ON attendance_events (employee_id, at);

CREATE TABLE attendance_days (
  id               uuid PRIMARY KEY,
  business_id      uuid NOT NULL REFERENCES businesses(id),
  branch_id        uuid NOT NULL REFERENCES branches(id),
  employee_id      uuid NOT NULL REFERENCES employees(id),
  day              date NOT NULL,
  status           text NOT NULL CHECK (status IN ('present','late','absent','half_day','leave','holiday','day_off')),
  first_in         timestamptz,
  last_out         timestamptz,
  worked_minutes   integer NOT NULL DEFAULT 0,
  late_minutes     integer NOT NULL DEFAULT 0,
  overtime_minutes integer NOT NULL DEFAULT 0,
  overtime_status  text NOT NULL DEFAULT 'none' CHECK (overtime_status IN ('none','pending','approved','rejected')),
  needs_review     boolean NOT NULL DEFAULT false,
  is_locked        boolean NOT NULL DEFAULT false,
  note             text,
  UNIQUE (employee_id, day)
);
CREATE INDEX attendance_days_branch_idx ON attendance_days (branch_id, day);

CREATE TABLE salary_components (
  id            uuid PRIMARY KEY,
  business_id   uuid NOT NULL REFERENCES businesses(id),
  name          text NOT NULL,
  kind          text NOT NULL CHECK (kind IN ('allowance','deduction','employer_contribution')),
  calc          text NOT NULL CHECK (calc IN ('fixed','percent_of_base','percent_of_gross','bracket')),
  value         numeric(18,4) NOT NULL DEFAULT 0,
  brackets      jsonb,
  is_taxable    boolean NOT NULL DEFAULT true,
  is_statutory  boolean NOT NULL DEFAULT false,
  applies_to_all boolean NOT NULL DEFAULT false,
  sort_order    integer NOT NULL DEFAULT 0,
  is_active     boolean NOT NULL DEFAULT true,
  UNIQUE (business_id, name)
);

CREATE TABLE employee_components (
  employee_id   uuid NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
  component_id  uuid NOT NULL REFERENCES salary_components(id),
  business_id   uuid NOT NULL REFERENCES businesses(id),
  value         numeric(18,4),
  PRIMARY KEY (employee_id, component_id)
);

CREATE TABLE payroll_runs (
  id               uuid PRIMARY KEY,
  business_id      uuid NOT NULL REFERENCES businesses(id),
  branch_id        uuid REFERENCES branches(id),
  number           text NOT NULL,
  period_start     date NOT NULL,
  period_end       date NOT NULL,
  status           text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','calculated','pending_approval','approved','paid','locked','reversed')),
  gross_total      numeric(18,2) NOT NULL DEFAULT 0,
  deduction_total  numeric(18,2) NOT NULL DEFAULT 0,
  net_total        numeric(18,2) NOT NULL DEFAULT 0,
  employer_total   numeric(18,2) NOT NULL DEFAULT 0,
  created_by       uuid NOT NULL REFERENCES users(id),
  approved_by      uuid REFERENCES users(id),
  approved_at      timestamptz,
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  UNIQUE (business_id, number),
  CHECK (period_end >= period_start)
);
CREATE UNIQUE INDEX payroll_runs_period_uq ON payroll_runs (business_id, COALESCE(branch_id, '00000000-0000-0000-0000-000000000000'::uuid), period_start) WHERE status <> 'reversed';

CREATE TABLE payslips (
  id               uuid PRIMARY KEY,
  business_id      uuid NOT NULL REFERENCES businesses(id),
  run_id           uuid NOT NULL REFERENCES payroll_runs(id) ON DELETE CASCADE,
  employee_id      uuid NOT NULL REFERENCES employees(id),
  number           text NOT NULL,
  days_worked      numeric(5,1) NOT NULL DEFAULT 0,
  days_absent      numeric(5,1) NOT NULL DEFAULT 0,
  unpaid_leave_days numeric(5,1) NOT NULL DEFAULT 0,
  overtime_hours   numeric(7,2) NOT NULL DEFAULT 0,
  base_pay         numeric(18,2) NOT NULL,
  gross            numeric(18,2) NOT NULL,
  total_deductions numeric(18,2) NOT NULL,
  net              numeric(18,2) NOT NULL CHECK (net >= 0),
  paid_total       numeric(18,2) NOT NULL DEFAULT 0,
  status           text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','approved','partially_paid','paid')),
  UNIQUE (run_id, employee_id)
);
CREATE UNIQUE INDEX payslips_number_uq ON payslips (business_id, number);

CREATE TABLE payslip_lines (
  id            uuid PRIMARY KEY,
  business_id   uuid NOT NULL REFERENCES businesses(id),
  payslip_id    uuid NOT NULL REFERENCES payslips(id) ON DELETE CASCADE,
  kind          text NOT NULL CHECK (kind IN ('base','allowance','overtime','commission','bonus','deduction','statutory','loan','employer_contribution','absence')),
  component_id  uuid REFERENCES salary_components(id),
  loan_id       uuid REFERENCES employee_loans(id),
  label         text NOT NULL,
  amount        numeric(18,2) NOT NULL,
  is_manual     boolean NOT NULL DEFAULT false,
  note          text
);

ALTER TABLE loan_repayments ADD CONSTRAINT loan_repayments_payslip_fk FOREIGN KEY (payslip_id) REFERENCES payslips(id);
ALTER TABLE pay_adjustments ADD CONSTRAINT pay_adjustments_payslip_fk FOREIGN KEY (payslip_id) REFERENCES payslips(id);
