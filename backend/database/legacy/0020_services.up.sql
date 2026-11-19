-- Services business (Business B): clients, catalog, projects, quotations,
-- invoices, documents, tasks, renewals, contractors and design jobs.

-- ---------- ledger and numbering ----------
ALTER TABLE payments DROP CONSTRAINT IF EXISTS payments_kind_check;
ALTER TABLE payments ADD CONSTRAINT payments_kind_check CHECK (kind IN (
  'sale','customer_payment','sale_refund','supplier_payment','supplier_refund',
  'expense','salary','loan_disbursement','loan_repayment','income','drawing','capital','statutory',
  'service_payment','contractor_payment'));
ALTER TABLE payments DROP CONSTRAINT IF EXISTS payments_party_type_check;
ALTER TABLE payments ADD CONSTRAINT payments_party_type_check CHECK (party_type IN ('customer','supplier','employee','client','contractor'));
ALTER TABLE journal_lines DROP CONSTRAINT IF EXISTS journal_lines_party_type_check;
ALTER TABLE journal_lines ADD CONSTRAINT journal_lines_party_type_check CHECK (party_type IN ('customer','supplier','employee','client','contractor'));

INSERT INTO ledger_accounts (id, business_id, code, name, type, is_system)
SELECT gen_random_uuid(), b.id, a.code, a.name, a.type, true
FROM businesses b
CROSS JOIN (VALUES ('2400','Client deposits','liability'), ('4300','Service revenue','income'), ('5200','Service costs','expense')) AS a(code, name, type)
ON CONFLICT (business_id, code) DO NOTHING;

INSERT INTO document_sequences (business_id, branch_id, doc_type, prefix, next_number)
SELECT br.business_id, br.id, d.doc_type, d.prefix, 1
FROM branches br
CROSS JOIN (VALUES ('svc_project','PRJ'), ('svc_quotation','SQT'), ('svc_invoice','SIN'), ('design_job','DSJ'), ('svc_cost','SCT')) AS d(doc_type, prefix)
ON CONFLICT (branch_id, doc_type) DO NOTHING;

-- The business date for overdue and days-left checks. The services code sets
-- app.today per transaction; without it the database date is used.
CREATE OR REPLACE FUNCTION svc_today() RETURNS date
LANGUAGE sql STABLE
AS $$ SELECT COALESCE(NULLIF(current_setting('app.today', true), '')::date, CURRENT_DATE) $$;

-- ---------- clients ----------
CREATE TABLE svc_clients (
  id          uuid PRIMARY KEY,
  business_id uuid NOT NULL REFERENCES businesses(id),
  name        text NOT NULL,
  phone       text,
  email       citext,
  address     text,
  id_number   text,
  source      text,
  needs       text,
  notes       text,
  status      text NOT NULL DEFAULT 'lead' CHECK (status IN ('lead','active','completed')),
  is_active   boolean NOT NULL DEFAULT true,
  created_by  uuid REFERENCES users(id),
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX svc_clients_name_idx ON svc_clients (business_id, lower(name));
CREATE INDEX svc_clients_status_idx ON svc_clients (business_id, status);

-- ---------- service catalog ----------
CREATE TABLE svc_catalog (
  id                  uuid PRIMARY KEY,
  business_id         uuid NOT NULL REFERENCES businesses(id),
  name                text NOT NULL,
  category            text NOT NULL DEFAULT 'business' CHECK (category IN ('business','design','other')),
  description         text,
  standard_price      numeric(18,2) NOT NULL DEFAULT 0 CHECK (standard_price >= 0),
  government_fees     numeric(18,2) NOT NULL DEFAULT 0 CHECK (government_fees >= 0),
  included_revisions  integer NOT NULL DEFAULT 0 CHECK (included_revisions >= 0),
  is_package          boolean NOT NULL DEFAULT false,
  is_active           boolean NOT NULL DEFAULT true,
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  UNIQUE (business_id, name)
);
CREATE TABLE svc_catalog_stages (
  id          uuid PRIMARY KEY,
  business_id uuid NOT NULL REFERENCES businesses(id),
  service_id  uuid NOT NULL REFERENCES svc_catalog(id) ON DELETE CASCADE,
  position    integer NOT NULL CHECK (position >= 1),
  name        text NOT NULL,
  est_days    integer NOT NULL DEFAULT 0 CHECK (est_days >= 0),
  UNIQUE (service_id, position)
);
CREATE TABLE svc_catalog_documents (
  id             uuid PRIMARY KEY,
  business_id    uuid NOT NULL REFERENCES businesses(id),
  service_id     uuid NOT NULL REFERENCES svc_catalog(id) ON DELETE CASCADE,
  stage_position integer,
  name           text NOT NULL
);
CREATE TABLE svc_package_items (
  business_id uuid NOT NULL REFERENCES businesses(id),
  package_id  uuid NOT NULL REFERENCES svc_catalog(id) ON DELETE CASCADE,
  service_id  uuid NOT NULL REFERENCES svc_catalog(id),
  PRIMARY KEY (package_id, service_id),
  CHECK (package_id <> service_id)
);

-- ---------- contractors ----------
CREATE TABLE contractors (
  id          uuid PRIMARY KEY,
  business_id uuid NOT NULL REFERENCES businesses(id),
  name        text NOT NULL,
  trade       text,
  phone       text,
  email       citext,
  address     text,
  notes       text,
  is_active   boolean NOT NULL DEFAULT true,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  UNIQUE (business_id, name)
);

-- ---------- projects ----------
CREATE TABLE svc_projects (
  id           uuid PRIMARY KEY,
  business_id  uuid NOT NULL REFERENCES businesses(id),
  branch_id    uuid NOT NULL REFERENCES branches(id),
  number       text NOT NULL,
  client_id    uuid NOT NULL REFERENCES svc_clients(id),
  service_id   uuid REFERENCES svc_catalog(id),
  name         text NOT NULL,
  status       text NOT NULL DEFAULT 'in_progress' CHECK (status IN ('in_progress','on_hold','completed','cancelled')),
  start_date   date NOT NULL,
  owner_id     uuid REFERENCES users(id),
  total_price  numeric(18,2) NOT NULL DEFAULT 0 CHECK (total_price >= 0),
  notes        text,
  completed_at timestamptz,
  created_by   uuid REFERENCES users(id),
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now(),
  UNIQUE (business_id, number)
);
CREATE INDEX svc_projects_client_idx ON svc_projects (client_id);
CREATE INDEX svc_projects_status_idx ON svc_projects (business_id, status);

CREATE TABLE svc_project_stages (
  id           uuid PRIMARY KEY,
  business_id  uuid NOT NULL REFERENCES businesses(id),
  project_id   uuid NOT NULL REFERENCES svc_projects(id) ON DELETE CASCADE,
  position     integer NOT NULL CHECK (position >= 1),
  name         text NOT NULL,
  status       text NOT NULL DEFAULT 'not_started' CHECK (status IN ('not_started','in_progress','done','stuck')),
  owner_id     uuid REFERENCES users(id),
  deadline     date,
  est_days     integer NOT NULL DEFAULT 0,
  checklist    jsonb NOT NULL DEFAULT '[]',
  note         text,
  updated_by   uuid REFERENCES users(id),
  updated_at   timestamptz NOT NULL DEFAULT now(),
  completed_at timestamptz,
  UNIQUE (project_id, position)
);
CREATE TABLE svc_stage_events (
  id          uuid PRIMARY KEY,
  business_id uuid NOT NULL REFERENCES businesses(id),
  project_id  uuid NOT NULL REFERENCES svc_projects(id) ON DELETE CASCADE,
  stage_id    uuid NOT NULL REFERENCES svc_project_stages(id) ON DELETE CASCADE,
  from_status text,
  to_status   text NOT NULL,
  note        text,
  changed_by  uuid REFERENCES users(id),
  changed_at  timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE svc_project_notes (
  id          uuid PRIMARY KEY,
  business_id uuid NOT NULL REFERENCES businesses(id),
  project_id  uuid NOT NULL REFERENCES svc_projects(id) ON DELETE CASCADE,
  kind        text NOT NULL DEFAULT 'note' CHECK (kind IN ('call','message','meeting','note','update')),
  body        text NOT NULL,
  noted_on    date NOT NULL,
  created_by  uuid REFERENCES users(id),
  created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX svc_project_notes_idx ON svc_project_notes (project_id, noted_on DESC);

-- ---------- design jobs ----------
CREATE TABLE design_jobs (
  id                 uuid PRIMARY KEY,
  business_id        uuid NOT NULL REFERENCES businesses(id),
  branch_id          uuid NOT NULL REFERENCES branches(id),
  number             text NOT NULL,
  client_id          uuid NOT NULL REFERENCES svc_clients(id),
  project_id         uuid REFERENCES svc_projects(id),
  stage_id           uuid REFERENCES svc_project_stages(id) ON DELETE SET NULL,
  service_id         uuid REFERENCES svc_catalog(id),
  job_type           text NOT NULL CHECK (job_type IN ('logo','business_card','poster','flyer','banner','signage','social_media','packaging_label','branding_package','other')),
  title              text NOT NULL,
  brief              text,
  size_format        text,
  style_notes        text,
  text_to_include    text,
  deadline           date,
  priority           text NOT NULL DEFAULT 'normal' CHECK (priority IN ('normal','urgent')),
  designer_id        uuid REFERENCES users(id),
  price              numeric(18,2) NOT NULL DEFAULT 0 CHECK (price >= 0),
  deposit            numeric(18,2) NOT NULL DEFAULT 0 CHECK (deposit >= 0),
  revisions_included integer NOT NULL DEFAULT 3 CHECK (revisions_included >= 0),
  revisions_used     integer NOT NULL DEFAULT 0 CHECK (revisions_used >= 0),
  printing_needed    boolean NOT NULL DEFAULT false,
  status             text NOT NULL DEFAULT 'new_brief' CHECK (status IN ('new_brief','in_design','internal_review','with_client','changes_requested','approved','printing','delivered','cancelled')),
  approved_at        timestamptz,
  delivered_at       timestamptz,
  created_by         uuid REFERENCES users(id),
  created_at         timestamptz NOT NULL DEFAULT now(),
  updated_at         timestamptz NOT NULL DEFAULT now(),
  UNIQUE (business_id, number)
);
CREATE INDEX design_jobs_designer_idx ON design_jobs (designer_id, status);
CREATE INDEX design_jobs_project_idx ON design_jobs (project_id);
CREATE TABLE design_references (
  id          uuid PRIMARY KEY,
  business_id uuid NOT NULL REFERENCES businesses(id),
  job_id      uuid NOT NULL REFERENCES design_jobs(id) ON DELETE CASCADE,
  file_id     uuid NOT NULL REFERENCES files(id),
  note        text,
  created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE design_versions (
  id               uuid PRIMARY KEY,
  business_id      uuid NOT NULL REFERENCES businesses(id),
  job_id           uuid NOT NULL REFERENCES design_jobs(id) ON DELETE CASCADE,
  version_no       integer NOT NULL,
  file_id          uuid REFERENCES files(id),
  link             text,
  kind             text NOT NULL DEFAULT 'design' CHECK (kind IN ('design','print_ready','source')),
  notes            text,
  status           text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','sent','changes_requested','approved')),
  is_final         boolean NOT NULL DEFAULT false,
  token_hash       text,
  token_expires_at timestamptz,
  decided_at       timestamptz,
  uploaded_by      uuid REFERENCES users(id),
  created_at       timestamptz NOT NULL DEFAULT now(),
  CHECK (file_id IS NOT NULL OR link IS NOT NULL)
);
CREATE UNIQUE INDEX design_versions_no_idx ON design_versions (job_id, version_no) WHERE kind = 'design';
CREATE UNIQUE INDEX design_versions_token_idx ON design_versions (token_hash) WHERE token_hash IS NOT NULL;
CREATE TABLE design_feedback (
  id          uuid PRIMARY KEY,
  business_id uuid NOT NULL REFERENCES businesses(id),
  job_id      uuid NOT NULL REFERENCES design_jobs(id) ON DELETE CASCADE,
  version_id  uuid REFERENCES design_versions(id) ON DELETE SET NULL,
  author_type text NOT NULL CHECK (author_type IN ('client','staff')),
  author_id   uuid REFERENCES users(id),
  comment     text NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE design_time_entries (
  id          uuid PRIMARY KEY,
  business_id uuid NOT NULL REFERENCES businesses(id),
  job_id      uuid NOT NULL REFERENCES design_jobs(id) ON DELETE CASCADE,
  user_id     uuid NOT NULL REFERENCES users(id),
  started_at  timestamptz NOT NULL,
  stopped_at  timestamptz,
  minutes     integer
);
CREATE UNIQUE INDEX design_time_open_idx ON design_time_entries (user_id) WHERE stopped_at IS NULL;
CREATE TABLE design_print_orders (
  id            uuid PRIMARY KEY,
  business_id   uuid NOT NULL REFERENCES businesses(id),
  job_id        uuid NOT NULL REFERENCES design_jobs(id) ON DELETE CASCADE,
  contractor_id uuid REFERENCES contractors(id),
  quantity      integer NOT NULL CHECK (quantity > 0),
  cost          numeric(18,2) NOT NULL DEFAULT 0 CHECK (cost >= 0),
  due_date      date,
  status        text NOT NULL DEFAULT 'sent' CHECK (status IN ('sent','ready','delivered','cancelled')),
  cost_id       uuid,
  note          text,
  created_by    uuid REFERENCES users(id),
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now()
);

-- Resolves a client approval link to its business. The link holds a secret, so
-- only the hash is stored; this runs outside any tenant like job_businesses().
CREATE OR REPLACE FUNCTION design_token_business(h text)
RETURNS uuid
LANGUAGE sql
SECURITY DEFINER
SET search_path = public
AS $$
  SELECT business_id FROM design_versions WHERE token_hash = h LIMIT 1;
$$;
REVOKE ALL ON FUNCTION design_token_business(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION design_token_business(text) TO bms_app;

-- ---------- billing ----------
CREATE TABLE svc_quotations (
  id          uuid PRIMARY KEY,
  business_id uuid NOT NULL REFERENCES businesses(id),
  branch_id   uuid NOT NULL REFERENCES branches(id),
  number      text NOT NULL,
  client_id   uuid NOT NULL REFERENCES svc_clients(id),
  status      text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','sent','accepted','rejected')),
  valid_until date,
  subtotal    numeric(18,2) NOT NULL DEFAULT 0,
  discount    numeric(18,2) NOT NULL DEFAULT 0 CHECK (discount >= 0),
  total       numeric(18,2) NOT NULL DEFAULT 0,
  notes       text,
  invoice_id  uuid,
  project_id  uuid REFERENCES svc_projects(id),
  created_by  uuid REFERENCES users(id),
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  UNIQUE (business_id, number)
);
CREATE TABLE svc_invoices (
  id            uuid PRIMARY KEY,
  business_id   uuid NOT NULL REFERENCES businesses(id),
  branch_id     uuid NOT NULL REFERENCES branches(id),
  number        text NOT NULL,
  client_id     uuid NOT NULL REFERENCES svc_clients(id),
  project_id    uuid REFERENCES svc_projects(id),
  design_job_id uuid REFERENCES design_jobs(id),
  quotation_id  uuid REFERENCES svc_quotations(id),
  status        text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','issued','partial','paid','void')),
  issue_date    date,
  due_date      date,
  subtotal      numeric(18,2) NOT NULL DEFAULT 0,
  discount      numeric(18,2) NOT NULL DEFAULT 0 CHECK (discount >= 0),
  total         numeric(18,2) NOT NULL DEFAULT 0,
  paid_total    numeric(18,2) NOT NULL DEFAULT 0,
  balance       numeric(18,2) NOT NULL DEFAULT 0,
  notes         text,
  void_reason   text,
  voided_at     timestamptz,
  created_by    uuid REFERENCES users(id),
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  UNIQUE (business_id, number)
);
CREATE INDEX svc_invoices_client_idx ON svc_invoices (client_id, status);
CREATE INDEX svc_invoices_project_idx ON svc_invoices (project_id);
ALTER TABLE svc_quotations ADD CONSTRAINT svc_quotations_invoice_fk FOREIGN KEY (invoice_id) REFERENCES svc_invoices(id);
CREATE TABLE svc_lines (
  id          uuid PRIMARY KEY,
  business_id uuid NOT NULL REFERENCES businesses(id),
  doc_type    text NOT NULL CHECK (doc_type IN ('quotation','invoice')),
  doc_id      uuid NOT NULL,
  position    integer NOT NULL,
  service_id  uuid REFERENCES svc_catalog(id),
  description text NOT NULL,
  quantity    numeric(12,3) NOT NULL CHECK (quantity > 0),
  unit_price  numeric(18,2) NOT NULL CHECK (unit_price >= 0),
  total       numeric(18,2) NOT NULL
);
CREATE INDEX svc_lines_doc_idx ON svc_lines (doc_type, doc_id);
CREATE TABLE svc_invoice_installments (
  id          uuid PRIMARY KEY,
  business_id uuid NOT NULL REFERENCES businesses(id),
  invoice_id  uuid NOT NULL REFERENCES svc_invoices(id) ON DELETE CASCADE,
  position    integer NOT NULL,
  due_date    date NOT NULL,
  amount      numeric(18,2) NOT NULL CHECK (amount > 0)
);
CREATE TABLE svc_payments (
  id            uuid PRIMARY KEY REFERENCES payments(id),
  business_id   uuid NOT NULL REFERENCES businesses(id),
  branch_id     uuid NOT NULL REFERENCES branches(id),
  client_id     uuid NOT NULL REFERENCES svc_clients(id),
  project_id    uuid REFERENCES svc_projects(id),
  design_job_id uuid REFERENCES design_jobs(id),
  amount        numeric(18,2) NOT NULL CHECK (amount > 0),
  applied       numeric(18,2) NOT NULL DEFAULT 0 CHECK (applied >= 0),
  status        text NOT NULL DEFAULT 'posted' CHECK (status IN ('posted','voided')),
  created_by    uuid REFERENCES users(id),
  created_at    timestamptz NOT NULL DEFAULT now(),
  CHECK (applied <= amount)
);
CREATE INDEX svc_payments_project_idx ON svc_payments (project_id);
CREATE INDEX svc_payments_job_idx ON svc_payments (design_job_id);
CREATE INDEX svc_payments_client_idx ON svc_payments (client_id);
CREATE TABLE svc_payment_allocations (
  id          uuid PRIMARY KEY,
  business_id uuid NOT NULL REFERENCES businesses(id),
  payment_id  uuid NOT NULL REFERENCES svc_payments(id),
  invoice_id  uuid NOT NULL REFERENCES svc_invoices(id),
  amount      numeric(18,2) NOT NULL CHECK (amount > 0)
);
CREATE INDEX svc_alloc_invoice_idx ON svc_payment_allocations (invoice_id);
CREATE INDEX svc_alloc_payment_idx ON svc_payment_allocations (payment_id);

CREATE TABLE svc_costs (
  id               uuid PRIMARY KEY,
  business_id      uuid NOT NULL REFERENCES businesses(id),
  branch_id        uuid NOT NULL REFERENCES branches(id),
  number           text NOT NULL,
  project_id       uuid REFERENCES svc_projects(id),
  design_job_id    uuid REFERENCES design_jobs(id),
  contractor_id    uuid REFERENCES contractors(id),
  kind             text NOT NULL CHECK (kind IN ('government_fee','contractor','materials','printing','stock_images','fonts','designer_time','other')),
  description      text NOT NULL,
  amount           numeric(18,2) NOT NULL CHECK (amount > 0),
  cost_date        date NOT NULL,
  money_account_id uuid REFERENCES money_accounts(id),
  status           text NOT NULL DEFAULT 'paid' CHECK (status IN ('paid','owed','voided')),
  payment_id       uuid,
  settled          numeric(18,2) NOT NULL DEFAULT 0,
  created_by       uuid REFERENCES users(id),
  created_at       timestamptz NOT NULL DEFAULT now(),
  UNIQUE (business_id, number),
  CHECK (project_id IS NOT NULL OR design_job_id IS NOT NULL OR contractor_id IS NOT NULL),
  CHECK (status <> 'paid' OR money_account_id IS NOT NULL),
  CHECK (status <> 'owed' OR contractor_id IS NOT NULL)
);
CREATE INDEX svc_costs_project_idx ON svc_costs (project_id);
CREATE INDEX svc_costs_job_idx ON svc_costs (design_job_id);
CREATE INDEX svc_costs_contractor_idx ON svc_costs (contractor_id);
CREATE TABLE contractor_payments (
  id            uuid PRIMARY KEY REFERENCES payments(id),
  business_id   uuid NOT NULL REFERENCES businesses(id),
  contractor_id uuid NOT NULL REFERENCES contractors(id),
  amount        numeric(18,2) NOT NULL CHECK (amount > 0),
  status        text NOT NULL DEFAULT 'posted' CHECK (status IN ('posted','voided')),
  created_at    timestamptz NOT NULL DEFAULT now()
);

-- ---------- documents, tasks, renewals ----------
CREATE TABLE svc_documents (
  id          uuid PRIMARY KEY,
  business_id uuid NOT NULL REFERENCES businesses(id),
  client_id   uuid NOT NULL REFERENCES svc_clients(id),
  project_id  uuid REFERENCES svc_projects(id) ON DELETE CASCADE,
  stage_id    uuid REFERENCES svc_project_stages(id) ON DELETE SET NULL,
  design_job_id uuid REFERENCES design_jobs(id) ON DELETE SET NULL,
  name        text NOT NULL,
  doc_type    text,
  status      text NOT NULL DEFAULT 'missing' CHECK (status IN ('missing','received')),
  file_id     uuid REFERENCES files(id),
  received_at timestamptz,
  uploaded_by uuid REFERENCES users(id),
  created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX svc_documents_client_idx ON svc_documents (client_id);
CREATE INDEX svc_documents_project_idx ON svc_documents (project_id);
CREATE TABLE svc_tasks (
  id            uuid PRIMARY KEY,
  business_id   uuid NOT NULL REFERENCES businesses(id),
  title         text NOT NULL,
  description   text,
  project_id    uuid REFERENCES svc_projects(id) ON DELETE CASCADE,
  design_job_id uuid REFERENCES design_jobs(id) ON DELETE CASCADE,
  assignee_id   uuid REFERENCES users(id),
  due_date      date,
  priority      text NOT NULL DEFAULT 'normal' CHECK (priority IN ('normal','urgent')),
  status        text NOT NULL DEFAULT 'todo' CHECK (status IN ('todo','in_progress','done','cancelled')),
  completed_at  timestamptz,
  created_by    uuid REFERENCES users(id),
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX svc_tasks_assignee_idx ON svc_tasks (assignee_id, status);
CREATE INDEX svc_tasks_due_idx ON svc_tasks (business_id, due_date) WHERE status IN ('todo','in_progress');
CREATE TABLE svc_renewals (
  id             uuid PRIMARY KEY,
  business_id    uuid NOT NULL REFERENCES businesses(id),
  client_id      uuid NOT NULL REFERENCES svc_clients(id),
  project_id     uuid REFERENCES svc_projects(id) ON DELETE SET NULL,
  item           text NOT NULL,
  due_date       date NOT NULL,
  amount         numeric(18,2) NOT NULL DEFAULT 0 CHECK (amount >= 0),
  status         text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','renewed','cancelled')),
  reminded_30_at timestamptz,
  reminded_7_at  timestamptz,
  renewed_at     timestamptz,
  next_id        uuid,
  notes          text,
  created_by     uuid REFERENCES users(id),
  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX svc_renewals_due_idx ON svc_renewals (business_id, due_date) WHERE status = 'pending';

-- ---------- updated_at triggers ----------
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['svc_clients','svc_catalog','contractors','svc_projects','design_jobs','svc_quotations','svc_invoices','svc_tasks','svc_renewals','design_print_orders'] LOOP
    EXECUTE format('CREATE TRIGGER set_updated_at BEFORE UPDATE ON %I FOR EACH ROW EXECUTE FUNCTION set_updated_at()', t);
  END LOOP;
END $$;

-- ---------- row-level security ----------
DO $$
DECLARE r record;
BEGIN
  FOR r IN
    SELECT c.table_name
    FROM information_schema.columns c
    JOIN information_schema.tables t ON t.table_schema = c.table_schema AND t.table_name = c.table_name
    WHERE c.table_schema = 'public' AND c.column_name = 'business_id' AND t.table_type = 'BASE TABLE'
      AND (c.table_name LIKE 'svc\_%' OR c.table_name LIKE 'design\_%' OR c.table_name IN ('contractors','contractor_payments'))
  LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', r.table_name);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', r.table_name);
    EXECUTE format('DROP POLICY IF EXISTS tenant_isolation ON %I', r.table_name);
    EXECUTE format(
      'CREATE POLICY tenant_isolation ON %I USING (business_id = NULLIF(current_setting(''app.business_id'', true), '''')::uuid) WITH CHECK (business_id = NULLIF(current_setting(''app.business_id'', true), '''')::uuid)',
      r.table_name);
  END LOOP;
END $$;

-- ---------- permissions ----------
INSERT INTO permissions (key, module, description, is_sensitive, has_limit) VALUES
  ('clients.view','services','View service clients and leads', false, false),
  ('clients.manage','services','Add and edit service clients', false, false),
  ('projects.view','services','View projects', false, false),
  ('projects.manage','services','Create and run projects', false, false),
  ('service_catalog.manage','services','Manage the service catalog', false, false),
  ('service_billing.view','services','View service quotations, invoices and payments', false, false),
  ('service_billing.manage','services','Issue service quotations and invoices, receive payments and record costs', false, false),
  ('design.view','services','View design jobs', false, false),
  ('design.manage','services','Create and run design jobs', false, false),
  ('tasks.view','services','View tasks', false, false),
  ('tasks.manage','services','Create and assign tasks', false, false),
  ('renewals.view','services','View renewals', false, false),
  ('renewals.manage','services','Manage renewals', false, false),
  ('contractors.view','services','View contractors', false, false),
  ('contractors.manage','services','Manage contractors and pay them', false, false),
  ('reports.services','reports','View project and design reports', false, false)
ON CONFLICT (key) DO NOTHING;

INSERT INTO role_permissions (role_id, business_id, permission_key, scope)
SELECT r.id, r.business_id, p.key, 'branch'
FROM roles r CROSS JOIN permissions p
WHERE r.slug = 'manager' AND (p.module = 'services' OR p.key = 'reports.services')
ON CONFLICT DO NOTHING;
INSERT INTO role_permissions (role_id, business_id, permission_key, scope)
SELECT r.id, r.business_id, p.key, s.scope
FROM roles r
JOIN (VALUES ('clients.view','own'),('projects.view','own'),('design.view','own'),('tasks.view','own'),('renewals.view','branch')) AS s(key, scope) ON true
JOIN permissions p ON p.key = s.key
WHERE r.slug = 'staff'
ON CONFLICT DO NOTHING;
