CREATE TABLE businesses (
  id            uuid PRIMARY KEY,
  name          text NOT NULL,
  legal_name    text,
  tax_number    text,
  reg_number    text,
  phone         text,
  email         citext,
  address       text,
  logo_file     text,
  currency      char(3) NOT NULL DEFAULT 'TZS',
  timezone      text NOT NULL DEFAULT 'Africa/Dar_es_Salaam',
  is_active     boolean NOT NULL DEFAULT true,
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE branches (
  id            uuid PRIMARY KEY,
  business_id   uuid NOT NULL REFERENCES businesses(id),
  name          text NOT NULL,
  code          text NOT NULL,
  address       text,
  phone         text,
  manager_id    uuid,
  is_main       boolean NOT NULL DEFAULT false,
  is_active     boolean NOT NULL DEFAULT true,
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  UNIQUE (business_id, code),
  UNIQUE (business_id, name)
);

CREATE TABLE roles (
  id            uuid PRIMARY KEY,
  business_id   uuid NOT NULL REFERENCES businesses(id),
  name          text NOT NULL,
  slug          text NOT NULL,
  is_system     boolean NOT NULL DEFAULT false,
  perm_version  integer NOT NULL DEFAULT 1,
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  UNIQUE (business_id, slug)
);

CREATE TABLE permissions (
  key           text PRIMARY KEY,
  module        text NOT NULL,
  description   text NOT NULL,
  is_sensitive  boolean NOT NULL DEFAULT false,
  has_limit     boolean NOT NULL DEFAULT false
);

CREATE TABLE role_permissions (
  role_id        uuid NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
  business_id    uuid NOT NULL REFERENCES businesses(id),
  permission_key text NOT NULL REFERENCES permissions(key),
  scope          text NOT NULL CHECK (scope IN ('all','branch','own')),
  approval_limit numeric(18,2),
  PRIMARY KEY (role_id, permission_key)
);

CREATE TABLE users (
  id              uuid PRIMARY KEY,
  business_id     uuid NOT NULL REFERENCES businesses(id),
  role_id         uuid NOT NULL REFERENCES roles(id),
  employee_id     uuid,
  name            text NOT NULL,
  phone           text,
  email           citext,
  password_hash   text NOT NULL,
  pin_hash        text,
  language        text NOT NULL DEFAULT 'en' CHECK (language IN ('en','sw')),
  is_active       boolean NOT NULL DEFAULT true,
  must_change_password boolean NOT NULL DEFAULT false,
  locked_until    timestamptz,
  last_login_at   timestamptz,
  created_by      uuid REFERENCES users(id),
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  CHECK (phone IS NOT NULL OR email IS NOT NULL)
);
CREATE UNIQUE INDEX users_phone_uq ON users (phone) WHERE phone IS NOT NULL;
CREATE UNIQUE INDEX users_email_uq ON users (email) WHERE email IS NOT NULL;

CREATE TABLE user_branches (
  user_id      uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  branch_id    uuid NOT NULL REFERENCES branches(id),
  business_id  uuid NOT NULL REFERENCES businesses(id),
  is_default   boolean NOT NULL DEFAULT false,
  PRIMARY KEY (user_id, branch_id)
);

CREATE TABLE sessions (
  id            uuid PRIMARY KEY,
  business_id   uuid NOT NULL REFERENCES businesses(id),
  user_id       uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  refresh_hash  bytea NOT NULL UNIQUE,
  prev_hash     bytea,
  user_agent    text,
  ip            inet,
  expires_at    timestamptz NOT NULL,
  created_at    timestamptz NOT NULL DEFAULT now(),
  last_used_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX sessions_user_idx ON sessions (user_id);

CREATE TABLE login_history (
  id           uuid PRIMARY KEY,
  business_id  uuid REFERENCES businesses(id),
  user_id      uuid REFERENCES users(id),
  identifier   text NOT NULL,
  success      boolean NOT NULL,
  reason       text,
  ip           inet,
  user_agent   text,
  created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX login_history_user_idx ON login_history (user_id, created_at DESC);
CREATE INDEX login_history_ident_idx ON login_history (identifier, created_at DESC);

CREATE TABLE password_resets (
  id           uuid PRIMARY KEY,
  business_id  uuid NOT NULL REFERENCES businesses(id),
  user_id      uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  code_hash    bytea NOT NULL,
  expires_at   timestamptz NOT NULL,
  used_at      timestamptz,
  created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX password_resets_user_idx ON password_resets (user_id, created_at DESC);

CREATE TABLE settings (
  business_id  uuid NOT NULL REFERENCES businesses(id),
  key          text NOT NULL,
  value        jsonb NOT NULL,
  updated_by   uuid REFERENCES users(id),
  updated_at   timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (business_id, key)
);

CREATE TABLE tax_rates (
  id           uuid PRIMARY KEY,
  business_id  uuid NOT NULL REFERENCES businesses(id),
  name         text NOT NULL,
  rate         numeric(7,4) NOT NULL CHECK (rate >= 0),
  is_default   boolean NOT NULL DEFAULT false,
  is_active    boolean NOT NULL DEFAULT true,
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now(),
  UNIQUE (business_id, name)
);

CREATE TABLE document_sequences (
  business_id  uuid NOT NULL REFERENCES businesses(id),
  branch_id    uuid NOT NULL REFERENCES branches(id),
  doc_type     text NOT NULL,
  prefix       text NOT NULL,
  next_number  bigint NOT NULL DEFAULT 1,
  PRIMARY KEY (branch_id, doc_type)
);

CREATE TABLE idempotency_keys (
  business_id  uuid NOT NULL REFERENCES businesses(id),
  user_id      uuid NOT NULL REFERENCES users(id),
  key          text NOT NULL,
  request_hash bytea NOT NULL,
  status_code  integer,
  response     jsonb,
  created_at   timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, key)
);

INSERT INTO permissions (key, module, description, is_sensitive, has_limit) VALUES
  ('users.view','identity','View users', false, false),
  ('users.manage','identity','Create and edit users', true, false),
  ('roles.view','identity','View roles', false, false),
  ('roles.manage','identity','Create and edit roles', true, false),
  ('sessions.manage','identity','End other users sessions', true, false),
  ('login_history.view','identity','View login history', true, false),
  ('business.manage','org','Edit the business profile', true, false),
  ('branches.view','org','View branches', false, false),
  ('branches.manage','org','Create and edit branches', false, false),
  ('settings.view','org','View settings', false, false),
  ('settings.manage','org','Change settings', true, false),
  ('products.view','catalog','View products', false, false),
  ('products.manage','catalog','Create and edit products', false, false),
  ('products.view_cost','catalog','View cost and margin', true, false),
  ('categories.manage','catalog','Manage categories and units', false, false),
  ('products.import','catalog','Import products', false, false),
  ('stock.view','inventory','View stock', false, false),
  ('stock.in','inventory','Post stock in', false, false),
  ('stock.out','inventory','Post stock out', false, false),
  ('stock.adjust','inventory','Adjust stock', false, true),
  ('stock.adjust_approve','inventory','Approve stock adjustments', false, false),
  ('transfers.view','inventory','View transfers', false, false),
  ('transfers.create','inventory','Create transfers', false, false),
  ('transfers.dispatch','inventory','Dispatch transfers', false, false),
  ('transfers.receive','inventory','Receive transfers', false, false),
  ('valuation.view','inventory','View inventory valuation', true, false),
  ('sales.create','sales','Create sales', false, false),
  ('sales.view','sales','View sales', false, false),
  ('sales.discount','sales','Apply a discount', false, true),
  ('sales.void','sales','Void a sale', false, true),
  ('sales.refund_request','sales','Request a refund', false, false),
  ('sales.refund_approve','sales','Approve a refund', false, true),
  ('tills.open_close','sales','Open and close a till', false, false),
  ('tills.view','sales','View till sessions', false, false),
  ('quotations.manage','sales','Manage quotations', false, false),
  ('customers.view','customers','View customers', false, false),
  ('customers.manage','customers','Create and edit customers', false, false),
  ('customers.credit_limit','customers','Set credit limits', false, false),
  ('customers.statement','customers','View customer statements', false, false),
  ('customers.write_off','customers','Write off bad debt', true, false),
  ('suppliers.view','purchasing','View suppliers', false, false),
  ('suppliers.manage','purchasing','Create and edit suppliers', false, false),
  ('po.view','purchasing','View purchase orders', false, false),
  ('po.create','purchasing','Create purchase orders', false, true),
  ('po.approve','purchasing','Approve purchase orders', false, false),
  ('grn.create','purchasing','Receive goods', false, false),
  ('purchase_invoices.manage','purchasing','Record purchase invoices', false, false),
  ('purchase_returns.create','purchasing','Return goods to a supplier', false, false),
  ('expenses.view','expenses','View expenses', false, false),
  ('expenses.create','expenses','Submit expenses', false, true),
  ('expenses.approve','expenses','Approve expenses', false, true),
  ('expenses.pay','expenses','Pay expenses', false, false),
  ('expenses.reverse','expenses','Reverse expenses', false, false),
  ('expense_categories.manage','expenses','Manage expense categories', false, false),
  ('petty_cash.manage','expenses','Manage petty cash', false, false),
  ('recurring_expenses.manage','expenses','Manage recurring expenses', false, false),
  ('accounts.view','finance','View money accounts', false, false),
  ('accounts.view_balance','finance','View bank and mobile money balances', true, false),
  ('accounts.manage','finance','Manage money accounts', true, false),
  ('payments.view','finance','View payments', false, false),
  ('payments.receive','finance','Receive payments', false, false),
  ('payments.pay','finance','Make payments', false, true),
  ('payments.void','finance','Void payments', false, true),
  ('transfers_money.create','finance','Transfer money between accounts', false, false),
  ('income.create','finance','Record other income', false, false),
  ('drawings.create','finance','Record owner drawings', true, false),
  ('reconcile.manage','finance','Reconcile accounts', false, false),
  ('periods.lock','finance','Lock accounting periods', true, false),
  ('transactions.view','finance','View the transaction history', true, false),
  ('employees.view','hr','View employees', false, false),
  ('employees.manage','hr','Manage employees', false, false),
  ('employees.view_salary','hr','View salaries', true, false),
  ('departments.manage','hr','Manage departments and positions', false, false),
  ('leave.request','hr','Request leave', false, false),
  ('leave.approve','hr','Approve leave', false, false),
  ('loans.view','hr','View staff loans', true, false),
  ('loans.create','hr','Create staff loans', true, true),
  ('loans.approve','hr','Approve and disburse loans', true, true),
  ('adjustments.manage','hr','Manage bonuses and deductions', true, false),
  ('attendance.clock','attendance','Clock in and out', false, false),
  ('attendance.view','attendance','View attendance', false, false),
  ('attendance.edit','attendance','Correct attendance', false, false),
  ('overtime.approve','attendance','Approve overtime', false, false),
  ('schedules.manage','attendance','Manage schedules and holidays', false, false),
  ('payroll.view','payroll','View payroll', true, false),
  ('payroll.prepare','payroll','Prepare payroll', true, false),
  ('payroll.approve','payroll','Approve payroll', true, false),
  ('payroll.pay','payroll','Pay salaries', true, false),
  ('payroll.reverse','payroll','Reverse a payroll run', true, false),
  ('payslips.view','payroll','View payslips', false, false),
  ('components.manage','payroll','Manage salary components', true, false),
  ('approvals.view','approvals','View approval requests', false, false),
  ('approvals.decide','approvals','Decide approval requests', false, false),
  ('audit.view','audit','View the audit log', true, false),
  ('backup.manage','audit','Export business data', true, false),
  ('reports.sales','reports','Sales reports', false, false),
  ('reports.inventory','reports','Inventory reports', false, false),
  ('reports.purchases','reports','Purchase reports', false, false),
  ('reports.expenses','reports','Expense reports', false, false),
  ('reports.debts','reports','Debt reports', false, false),
  ('reports.payroll','reports','Payroll reports', true, false),
  ('reports.staff','reports','Staff performance reports', false, false),
  ('reports.branches','reports','Branch comparison', false, false),
  ('reports.financial','reports','Financial reports', true, false),
  ('dashboard.view','reports','View the operations dashboard', false, false),
  ('dashboard.view_profit','reports','View profit on the dashboard', true, false);

CREATE OR REPLACE FUNCTION auth_find_user(p_identifier text)
RETURNS TABLE (
  id uuid,
  business_id uuid,
  role_id uuid,
  name text,
  password_hash text,
  is_active boolean,
  must_change_password boolean,
  locked_until timestamptz,
  business_active boolean
)
LANGUAGE sql
SECURITY DEFINER
SET search_path = public
AS $$
  SELECT u.id, u.business_id, u.role_id, u.name, u.password_hash, u.is_active,
         u.must_change_password, u.locked_until, b.is_active
  FROM users u
  JOIN businesses b ON b.id = u.business_id
  WHERE (u.phone IS NOT NULL AND u.phone = p_identifier)
     OR (u.email IS NOT NULL AND u.email = p_identifier::citext)
  LIMIT 1;
$$;

CREATE OR REPLACE FUNCTION auth_record_login(
  p_id uuid,
  p_business_id uuid,
  p_user_id uuid,
  p_identifier text,
  p_success boolean,
  p_reason text,
  p_ip text,
  p_user_agent text
) RETURNS void
LANGUAGE sql
SECURITY DEFINER
SET search_path = public
AS $$
  INSERT INTO login_history (id, business_id, user_id, identifier, success, reason, ip, user_agent)
  VALUES (p_id, p_business_id, p_user_id, p_identifier, p_success, p_reason, NULLIF(p_ip,'')::inet, p_user_agent);
$$;

CREATE OR REPLACE FUNCTION auth_set_lock(p_user_id uuid, p_until timestamptz)
RETURNS void
LANGUAGE sql
SECURITY DEFINER
SET search_path = public
AS $$
  UPDATE users SET locked_until = p_until WHERE id = p_user_id;
$$;

CREATE OR REPLACE FUNCTION auth_failure_count(p_user_id uuid, p_since timestamptz)
RETURNS integer
LANGUAGE sql
SECURITY DEFINER
SET search_path = public
AS $$
  SELECT count(*)::integer FROM login_history
  WHERE user_id = p_user_id AND success = false AND created_at >= p_since;
$$;

CREATE OR REPLACE FUNCTION auth_find_reset_user(p_identifier text)
RETURNS TABLE (id uuid, business_id uuid, phone text, email citext, is_active boolean)
LANGUAGE sql
SECURITY DEFINER
SET search_path = public
AS $$
  SELECT u.id, u.business_id, u.phone, u.email, u.is_active
  FROM users u
  WHERE (u.phone IS NOT NULL AND u.phone = p_identifier)
     OR (u.email IS NOT NULL AND u.email = p_identifier::citext)
  LIMIT 1;
$$;

REVOKE ALL ON FUNCTION auth_find_user(text) FROM PUBLIC;
REVOKE ALL ON FUNCTION auth_record_login(uuid, uuid, uuid, text, boolean, text, text, text) FROM PUBLIC;
REVOKE ALL ON FUNCTION auth_set_lock(uuid, timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION auth_failure_count(uuid, timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION auth_find_reset_user(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION auth_find_user(text) TO bms_app;
GRANT EXECUTE ON FUNCTION auth_record_login(uuid, uuid, uuid, text, boolean, text, text, text) TO bms_app;
GRANT EXECUTE ON FUNCTION auth_set_lock(uuid, timestamptz) TO bms_app;
GRANT EXECUTE ON FUNCTION auth_failure_count(uuid, timestamptz) TO bms_app;
GRANT EXECUTE ON FUNCTION auth_find_reset_user(text) TO bms_app;
