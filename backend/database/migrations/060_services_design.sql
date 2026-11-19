-- =====================================================================
-- 060_services_design.sql — service catalogue, quotations, projects,
-- stages, client documents, invoices, tasks, renewals, design jobs
-- =====================================================================
SET search_path = app, public;

-- ---------- Service catalogue -----------------------------------------
CREATE TYPE app.service_category AS ENUM ('business_registration', 'design', 'consulting', 'other');

CREATE TABLE app.document_types (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id    uuid NOT NULL REFERENCES app.tenants(id),
  name         app.short_text NOT NULL,      -- "National ID copy", "TIN certificate"...
  description  text CHECK (length(description) <= 1000),
  contains_pii boolean NOT NULL DEFAULT true,
  status       app.record_status NOT NULL DEFAULT 'active',
  created_at   timestamptz NOT NULL DEFAULT now(),
  created_by   uuid DEFAULT app.current_membership_id(),
  UNIQUE (tenant_id, name),
  UNIQUE (tenant_id, id)
);

CREATE TABLE app.services (
  id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id          uuid NOT NULL REFERENCES app.tenants(id),
  code               text NOT NULL CHECK (code ~ '^[A-Z0-9_-]{2,30}$'),
  name               app.short_text NOT NULL,
  description        text CHECK (length(description) <= 4000),
  category           app.service_category NOT NULL,
  is_package         boolean NOT NULL DEFAULT false,
  standard_price     app.money_nn NOT NULL,
  government_fee     app.money_nn NOT NULL DEFAULT 0,   -- expected pass-through cost (estimate)
  estimated_days     smallint CHECK (estimated_days BETWEEN 0 AND 3650),
  revisions_included smallint NOT NULL DEFAULT 0 CHECK (revisions_included BETWEEN 0 AND 50),
  creates_renewal_months smallint CHECK (creates_renewal_months BETWEEN 1 AND 120), -- null = no renewal
  status             app.record_status NOT NULL DEFAULT 'active',
  created_at         timestamptz NOT NULL DEFAULT now(),
  created_by         uuid DEFAULT app.current_membership_id(),
  updated_at         timestamptz NOT NULL DEFAULT now(),
  updated_by         uuid,
  row_version        integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, code)
);

CREATE TABLE app.service_package_items (
  tenant_id            uuid NOT NULL,
  package_service_id   uuid NOT NULL,
  component_service_id uuid NOT NULL,
  seq                  smallint NOT NULL CHECK (seq > 0),
  PRIMARY KEY (tenant_id, package_service_id, component_service_id),
  UNIQUE (tenant_id, package_service_id, seq),
  CHECK (package_service_id <> component_service_id),
  FOREIGN KEY (tenant_id, package_service_id)   REFERENCES app.services(tenant_id, id) ON DELETE CASCADE,
  FOREIGN KEY (tenant_id, component_service_id) REFERENCES app.services(tenant_id, id)
);

CREATE TABLE app.service_stages (
  id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id         uuid NOT NULL REFERENCES app.tenants(id),
  service_id        uuid NOT NULL,
  seq               smallint NOT NULL CHECK (seq > 0),
  name              app.short_text NOT NULL,
  estimated_days    smallint NOT NULL DEFAULT 1 CHECK (estimated_days BETWEEN 0 AND 365),
  is_branding_stage boolean NOT NULL DEFAULT false,
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, service_id, seq),
  FOREIGN KEY (tenant_id, service_id) REFERENCES app.services(tenant_id, id) ON DELETE CASCADE
);

CREATE TABLE app.service_stage_checklist_items (
  id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id        uuid NOT NULL REFERENCES app.tenants(id),
  service_stage_id uuid NOT NULL,
  seq              smallint NOT NULL CHECK (seq > 0),
  label            app.short_text NOT NULL,
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, service_stage_id, seq),
  FOREIGN KEY (tenant_id, service_stage_id) REFERENCES app.service_stages(tenant_id, id) ON DELETE CASCADE
);

CREATE TABLE app.service_stage_documents (
  tenant_id        uuid NOT NULL,
  service_stage_id uuid NOT NULL,
  document_type_id uuid NOT NULL,
  is_mandatory     boolean NOT NULL DEFAULT true,
  PRIMARY KEY (tenant_id, service_stage_id, document_type_id),
  FOREIGN KEY (tenant_id, service_stage_id) REFERENCES app.service_stages(tenant_id, id) ON DELETE CASCADE,
  FOREIGN KEY (tenant_id, document_type_id) REFERENCES app.document_types(tenant_id, id)
);

-- ---------- Quotations -------------------------------------------------
CREATE TYPE app.quotation_status AS ENUM ('draft', 'sent', 'accepted', 'rejected', 'expired', 'converted');

CREATE TABLE app.quotations (
  id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id        uuid NOT NULL REFERENCES app.tenants(id),
  quotation_no     text NOT NULL,
  customer_id      uuid NOT NULL,
  business_unit_id uuid NOT NULL,
  status           app.quotation_status NOT NULL DEFAULT 'draft',
  issue_date       date NOT NULL,
  valid_until      date NOT NULL,
  subtotal         app.money_nn NOT NULL DEFAULT 0,
  discount_amount  app.money_nn NOT NULL DEFAULT 0,
  tax_amount       app.money_nn NOT NULL DEFAULT 0,
  total            app.money_nn NOT NULL DEFAULT 0,
  terms            text CHECK (length(terms) <= 4000),
  notes            text CHECK (length(notes) <= 2000),
  sent_at          timestamptz,
  decided_at       timestamptz,
  rejection_reason text CHECK (length(rejection_reason) <= 1000),
  prepared_by      uuid NOT NULL DEFAULT app.current_membership_id(),
  created_at       timestamptz NOT NULL DEFAULT now(),
  created_by       uuid DEFAULT app.current_membership_id(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  updated_by       uuid,
  row_version      integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, quotation_no),
  CHECK (valid_until >= issue_date),
  CHECK (total = subtotal - discount_amount + tax_amount),
  CHECK (discount_amount <= subtotal),
  CHECK (status NOT IN ('sent','accepted','rejected','converted') OR sent_at IS NOT NULL),
  FOREIGN KEY (tenant_id, customer_id)      REFERENCES app.customers(tenant_id, id),
  FOREIGN KEY (tenant_id, business_unit_id) REFERENCES app.business_units(tenant_id, id),
  FOREIGN KEY (tenant_id, prepared_by)      REFERENCES app.memberships(tenant_id, id)
);
CREATE INDEX quotations_customer_idx ON app.quotations (tenant_id, customer_id, issue_date DESC);
CREATE INDEX quotations_open_idx ON app.quotations (tenant_id, valid_until) WHERE status IN ('draft', 'sent');

CREATE TABLE app.quotation_lines (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id     uuid NOT NULL REFERENCES app.tenants(id),
  quotation_id  uuid NOT NULL,
  line_no       smallint NOT NULL CHECK (line_no > 0),
  service_id    uuid NOT NULL,
  description   app.short_text NOT NULL,
  quantity      app.qty_pos NOT NULL DEFAULT 1,
  unit_price    app.money_nn NOT NULL,
  line_discount app.money_nn NOT NULL DEFAULT 0,
  line_total    app.money_nn NOT NULL,
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, quotation_id, line_no),
  CHECK (line_total = round(quantity * unit_price, 2) - line_discount),
  FOREIGN KEY (tenant_id, quotation_id) REFERENCES app.quotations(tenant_id, id) ON DELETE CASCADE,
  FOREIGN KEY (tenant_id, service_id)   REFERENCES app.services(tenant_id, id)
);

-- ---------- Projects ---------------------------------------------------
CREATE TYPE app.project_status AS ENUM ('not_started', 'in_progress', 'on_hold', 'completed', 'cancelled');

CREATE TABLE app.projects (
  id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id        uuid NOT NULL REFERENCES app.tenants(id),
  project_no       text NOT NULL,
  customer_id      uuid NOT NULL,
  service_id       uuid NOT NULL,
  quotation_id     uuid,
  business_unit_id uuid NOT NULL,
  title            app.short_text NOT NULL,     -- e.g. client business name
  status           app.project_status NOT NULL DEFAULT 'not_started',
  owner_id         uuid NOT NULL,
  start_date       date NOT NULL,
  target_end_date  date,
  total_price      app.money_nn NOT NULL,
  completed_at     timestamptz,
  cancelled_at     timestamptz,
  cancel_reason    text CHECK (length(cancel_reason) <= 1000),
  created_at       timestamptz NOT NULL DEFAULT now(),
  created_by       uuid DEFAULT app.current_membership_id(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  updated_by       uuid,
  row_version      integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, project_no),
  UNIQUE (tenant_id, quotation_id),              -- a quotation converts to at most one project
  CHECK (target_end_date IS NULL OR target_end_date >= start_date),
  CHECK (status <> 'completed' OR completed_at IS NOT NULL),
  CHECK (status <> 'cancelled' OR (cancelled_at IS NOT NULL AND cancel_reason IS NOT NULL)),
  FOREIGN KEY (tenant_id, customer_id)      REFERENCES app.customers(tenant_id, id),
  FOREIGN KEY (tenant_id, service_id)       REFERENCES app.services(tenant_id, id),
  FOREIGN KEY (tenant_id, quotation_id)     REFERENCES app.quotations(tenant_id, id),
  FOREIGN KEY (tenant_id, business_unit_id) REFERENCES app.business_units(tenant_id, id),
  FOREIGN KEY (tenant_id, owner_id)         REFERENCES app.memberships(tenant_id, id)
);
CREATE INDEX projects_status_idx   ON app.projects (tenant_id, status);
CREATE INDEX projects_customer_idx ON app.projects (tenant_id, customer_id);
CREATE INDEX projects_owner_idx    ON app.projects (tenant_id, owner_id) WHERE status IN ('not_started','in_progress','on_hold');

CREATE TYPE app.stage_status AS ENUM ('not_started', 'in_progress', 'done', 'stuck');
CREATE TABLE app.project_stages (
  id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id         uuid NOT NULL REFERENCES app.tenants(id),
  project_id        uuid NOT NULL,
  service_stage_id  uuid,
  seq               smallint NOT NULL CHECK (seq > 0),
  name              app.short_text NOT NULL,
  status            app.stage_status NOT NULL DEFAULT 'not_started',
  owner_id          uuid,
  deadline          date,
  started_at        timestamptz,
  completed_at      timestamptz,
  is_branding_stage boolean NOT NULL DEFAULT false,
  created_at        timestamptz NOT NULL DEFAULT now(),
  created_by        uuid DEFAULT app.current_membership_id(),
  updated_at        timestamptz NOT NULL DEFAULT now(),
  updated_by        uuid,
  row_version       integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, project_id, seq),
  CHECK (status <> 'done' OR completed_at IS NOT NULL),
  FOREIGN KEY (tenant_id, project_id)       REFERENCES app.projects(tenant_id, id) ON DELETE CASCADE,
  FOREIGN KEY (tenant_id, service_stage_id) REFERENCES app.service_stages(tenant_id, id),
  FOREIGN KEY (tenant_id, owner_id)         REFERENCES app.memberships(tenant_id, id)
);
CREATE INDEX project_stages_owner_idx ON app.project_stages (tenant_id, owner_id) WHERE status <> 'done';
CREATE INDEX project_stages_deadline_idx ON app.project_stages (tenant_id, deadline) WHERE status <> 'done';

CREATE TABLE app.project_stage_checklist (
  id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id        uuid NOT NULL REFERENCES app.tenants(id),
  project_stage_id uuid NOT NULL,
  seq              smallint NOT NULL CHECK (seq > 0),
  label            app.short_text NOT NULL,
  is_done          boolean NOT NULL DEFAULT false,
  done_by          uuid,
  done_at          timestamptz,
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, project_stage_id, seq),
  CHECK (NOT is_done OR (done_by IS NOT NULL AND done_at IS NOT NULL)),
  FOREIGN KEY (tenant_id, project_stage_id) REFERENCES app.project_stages(tenant_id, id) ON DELETE CASCADE,
  FOREIGN KEY (tenant_id, done_by)          REFERENCES app.memberships(tenant_id, id)
);

CREATE TABLE app.project_stage_history (
  id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id        uuid NOT NULL REFERENCES app.tenants(id),
  project_stage_id uuid NOT NULL,
  from_status      app.stage_status NOT NULL,
  to_status        app.stage_status NOT NULL,
  note             text CHECK (length(note) <= 2000),
  created_at       timestamptz NOT NULL DEFAULT now(),
  created_by       uuid DEFAULT app.current_membership_id(),   -- NULL = system / client-link driven change
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id, project_stage_id) REFERENCES app.project_stages(tenant_id, id)
);
COMMENT ON TABLE app.project_stage_history IS 'Append-only, written by trigger on project_stages.status (spec §7: optional note, who and when).';
CREATE INDEX project_stage_history_stage_idx ON app.project_stage_history (tenant_id, project_stage_id, created_at);

-- ---------- Client documents ---------------------------------------
CREATE TYPE app.client_document_status AS ENUM ('missing', 'received', 'rejected', 'returned');

CREATE TABLE app.client_documents (
  id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id        uuid NOT NULL REFERENCES app.tenants(id),
  customer_id      uuid NOT NULL,
  project_id       uuid,
  project_stage_id uuid,
  document_type_id uuid,
  title            app.short_text NOT NULL,
  status           app.client_document_status NOT NULL DEFAULT 'missing',
  file_id          uuid,
  is_final_deliverable boolean NOT NULL DEFAULT false,  -- e.g. final design files (spec §7A file library)
  received_at      timestamptz,
  received_by      uuid,
  notes            text CHECK (length(notes) <= 1000),
  created_at       timestamptz NOT NULL DEFAULT now(),
  created_by       uuid DEFAULT app.current_membership_id(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  updated_by       uuid,
  row_version      integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id),
  CHECK (status <> 'received' OR (received_at IS NOT NULL AND received_by IS NOT NULL)),
  CHECK (project_stage_id IS NULL OR project_id IS NOT NULL),
  FOREIGN KEY (tenant_id, customer_id)      REFERENCES app.customers(tenant_id, id),
  FOREIGN KEY (tenant_id, project_id)       REFERENCES app.projects(tenant_id, id),
  FOREIGN KEY (tenant_id, project_stage_id) REFERENCES app.project_stages(tenant_id, id),
  FOREIGN KEY (tenant_id, document_type_id) REFERENCES app.document_types(tenant_id, id),
  FOREIGN KEY (tenant_id, file_id)          REFERENCES app.files(tenant_id, id),
  FOREIGN KEY (tenant_id, received_by)      REFERENCES app.memberships(tenant_id, id)
);
CREATE UNIQUE INDEX client_documents_required_uq ON app.client_documents (tenant_id, project_stage_id, document_type_id)
  WHERE project_stage_id IS NOT NULL AND document_type_id IS NOT NULL;
CREATE INDEX client_documents_customer_idx ON app.client_documents (tenant_id, customer_id, project_id);

-- ---------- Invoices --------------------------------------------------
CREATE TYPE app.invoice_status AS ENUM ('draft', 'issued', 'partially_paid', 'paid', 'void');

CREATE TABLE app.invoices (
  id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id        uuid NOT NULL REFERENCES app.tenants(id),
  invoice_no       text NOT NULL,
  customer_id      uuid NOT NULL,
  business_unit_id uuid NOT NULL,
  project_id       uuid,
  quotation_id     uuid,
  status           app.invoice_status NOT NULL DEFAULT 'draft',
  issue_date       date NOT NULL,
  due_date         date NOT NULL,
  subtotal         app.money_nn NOT NULL DEFAULT 0,
  discount_amount  app.money_nn NOT NULL DEFAULT 0,
  tax_amount       app.money_nn NOT NULL DEFAULT 0,
  total            app.money_nn NOT NULL DEFAULT 0,
  amount_paid      app.money_nn NOT NULL DEFAULT 0,
  balance_due      numeric(18,2) GENERATED ALWAYS AS (total - amount_paid) STORED,
  notes            text CHECK (length(notes) <= 2000),
  issued_at        timestamptz,
  voided_at        timestamptz,
  void_reason      text CHECK (length(void_reason) <= 1000),
  created_at       timestamptz NOT NULL DEFAULT now(),
  created_by       uuid DEFAULT app.current_membership_id(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  updated_by       uuid,
  row_version      integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, invoice_no),
  CHECK (due_date >= issue_date),
  CHECK (total = subtotal - discount_amount + tax_amount),
  CHECK (discount_amount <= subtotal),
  CHECK (amount_paid <= total),
  CHECK (status = 'draft' OR issued_at IS NOT NULL),
  CHECK (status <> 'void' OR (voided_at IS NOT NULL AND void_reason IS NOT NULL AND amount_paid = 0)),
  FOREIGN KEY (tenant_id, customer_id)      REFERENCES app.customers(tenant_id, id),
  FOREIGN KEY (tenant_id, business_unit_id) REFERENCES app.business_units(tenant_id, id),
  FOREIGN KEY (tenant_id, project_id)       REFERENCES app.projects(tenant_id, id),
  FOREIGN KEY (tenant_id, quotation_id)     REFERENCES app.quotations(tenant_id, id)
);
CREATE INDEX invoices_customer_idx ON app.invoices (tenant_id, customer_id, issue_date DESC);
CREATE INDEX invoices_project_idx  ON app.invoices (tenant_id, project_id) WHERE project_id IS NOT NULL;
CREATE INDEX invoices_open_idx     ON app.invoices (tenant_id, due_date) WHERE status IN ('issued', 'partially_paid');

CREATE TABLE app.invoice_lines (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id     uuid NOT NULL REFERENCES app.tenants(id),
  invoice_id    uuid NOT NULL,
  line_no       smallint NOT NULL CHECK (line_no > 0),
  service_id    uuid,
  description   app.short_text NOT NULL,
  quantity      app.qty_pos NOT NULL DEFAULT 1,
  unit_price    app.money_nn NOT NULL,
  line_discount app.money_nn NOT NULL DEFAULT 0,
  line_total    app.money_nn NOT NULL,
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, invoice_id, line_no),
  CHECK (line_total = round(quantity * unit_price, 2) - line_discount),
  FOREIGN KEY (tenant_id, invoice_id) REFERENCES app.invoices(tenant_id, id) ON DELETE CASCADE,
  FOREIGN KEY (tenant_id, service_id) REFERENCES app.services(tenant_id, id)
);

CREATE TABLE app.invoice_installments (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id  uuid NOT NULL REFERENCES app.tenants(id),
  invoice_id uuid NOT NULL,
  seq        smallint NOT NULL CHECK (seq > 0),
  label      text NOT NULL CHECK (label IN ('deposit', 'installment', 'final')),
  due_date   date NOT NULL,
  amount     app.money_pos NOT NULL,
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, invoice_id, seq),
  FOREIGN KEY (tenant_id, invoice_id) REFERENCES app.invoices(tenant_id, id) ON DELETE CASCADE
);

-- ---------- Client interactions (Notes tab) ----------------------------
CREATE TYPE app.interaction_kind AS ENUM ('call', 'message', 'meeting', 'note');
CREATE TABLE app.client_interactions (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES app.tenants(id),
  customer_id uuid NOT NULL,
  project_id  uuid,
  kind        app.interaction_kind NOT NULL,
  summary     text NOT NULL CHECK (length(btrim(summary)) BETWEEN 1 AND 4000),
  occurred_at timestamptz NOT NULL DEFAULT now(),
  created_at  timestamptz NOT NULL DEFAULT now(),
  created_by  uuid NOT NULL DEFAULT app.current_membership_id(),
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id, customer_id) REFERENCES app.customers(tenant_id, id),
  FOREIGN KEY (tenant_id, project_id)  REFERENCES app.projects(tenant_id, id)
);
CREATE INDEX client_interactions_idx ON app.client_interactions (tenant_id, customer_id, occurred_at DESC);

-- ---------- Design jobs ----------------------------------------------
CREATE TYPE app.design_job_type AS ENUM ('logo', 'business_card', 'poster', 'flyer', 'banner', 'signage',
  'social_media_post', 'packaging_label', 'branding_package', 'other');
CREATE TYPE app.design_job_status AS ENUM ('new_brief', 'in_design', 'internal_review', 'with_client',
  'approved', 'printing', 'delivered', 'cancelled');
CREATE TYPE app.design_medium AS ENUM ('print', 'digital', 'both');
CREATE TYPE app.priority AS ENUM ('normal', 'high', 'urgent');

CREATE TABLE app.design_jobs (
  id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id          uuid NOT NULL REFERENCES app.tenants(id),
  job_no             text NOT NULL,
  customer_id        uuid NOT NULL,
  business_unit_id   uuid NOT NULL,
  project_id         uuid,
  project_stage_id   uuid,
  service_id         uuid,
  job_type           app.design_job_type NOT NULL,
  title              app.short_text NOT NULL,
  brief              text NOT NULL CHECK (length(btrim(brief)) BETWEEN 1 AND 10000),
  size_format        text CHECK (length(size_format) <= 100),
  medium             app.design_medium NOT NULL DEFAULT 'print',
  style_notes        text CHECK (length(style_notes) <= 4000),
  text_to_include    text CHECK (length(text_to_include) <= 4000),
  deadline           date,
  priority           app.priority NOT NULL DEFAULT 'normal',
  designer_id        uuid,
  price              app.money_nn NOT NULL,
  revisions_included smallint NOT NULL DEFAULT 0 CHECK (revisions_included BETWEEN 0 AND 50),
  revisions_used     smallint NOT NULL DEFAULT 0 CHECK (revisions_used >= 0),
  extra_revision_fee app.money_nn NOT NULL DEFAULT 0,
  printing_needed    boolean NOT NULL DEFAULT false,
  status             app.design_job_status NOT NULL DEFAULT 'new_brief',
  final_version_id   uuid,
  approved_at        timestamptz,
  delivered_at       timestamptz,
  created_at         timestamptz NOT NULL DEFAULT now(),
  created_by         uuid DEFAULT app.current_membership_id(),
  updated_at         timestamptz NOT NULL DEFAULT now(),
  updated_by         uuid,
  row_version        integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, job_no),
  CHECK (project_stage_id IS NULL OR project_id IS NOT NULL),
  CHECK (status NOT IN ('approved', 'printing', 'delivered') OR (final_version_id IS NOT NULL AND approved_at IS NOT NULL)),
  CHECK (status <> 'delivered' OR delivered_at IS NOT NULL),
  CHECK (status NOT IN ('in_design', 'internal_review', 'with_client') OR designer_id IS NOT NULL),
  FOREIGN KEY (tenant_id, customer_id)      REFERENCES app.customers(tenant_id, id),
  FOREIGN KEY (tenant_id, business_unit_id) REFERENCES app.business_units(tenant_id, id),
  FOREIGN KEY (tenant_id, project_id)       REFERENCES app.projects(tenant_id, id),
  FOREIGN KEY (tenant_id, project_stage_id) REFERENCES app.project_stages(tenant_id, id),
  FOREIGN KEY (tenant_id, service_id)       REFERENCES app.services(tenant_id, id),
  FOREIGN KEY (tenant_id, designer_id)      REFERENCES app.memberships(tenant_id, id)
);
CREATE INDEX design_jobs_board_idx    ON app.design_jobs (tenant_id, status, deadline);
CREATE INDEX design_jobs_designer_idx ON app.design_jobs (tenant_id, designer_id, status);
CREATE INDEX design_jobs_stage_idx    ON app.design_jobs (tenant_id, project_stage_id) WHERE project_stage_id IS NOT NULL;

CREATE TYPE app.design_version_status AS ENUM ('draft', 'sent_to_client', 'changes_requested', 'approved', 'superseded');
CREATE TABLE app.design_versions (
  id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id        uuid NOT NULL REFERENCES app.tenants(id),
  design_job_id    uuid NOT NULL,
  version_no       smallint NOT NULL CHECK (version_no > 0),
  preview_file_id  uuid NOT NULL,
  working_file_url app.https_url,
  status           app.design_version_status NOT NULL DEFAULT 'draft',
  sent_at          timestamptz,
  decided_at       timestamptz,
  created_at       timestamptz NOT NULL DEFAULT now(),
  created_by       uuid NOT NULL DEFAULT app.current_membership_id(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  updated_by       uuid,
  row_version      integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, design_job_id, version_no),
  FOREIGN KEY (tenant_id, design_job_id)   REFERENCES app.design_jobs(tenant_id, id),
  FOREIGN KEY (tenant_id, preview_file_id) REFERENCES app.files(tenant_id, id)
);
CREATE UNIQUE INDEX design_versions_one_approved ON app.design_versions (tenant_id, design_job_id) WHERE status = 'approved';

ALTER TABLE app.design_jobs ADD CONSTRAINT design_jobs_final_version_fk
  FOREIGN KEY (tenant_id, final_version_id) REFERENCES app.design_versions(tenant_id, id);

CREATE TYPE app.design_asset_kind AS ENUM ('print_pdf', 'png', 'source_link', 'other');
CREATE TABLE app.design_final_assets (
  id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id         uuid NOT NULL REFERENCES app.tenants(id),
  design_version_id uuid NOT NULL,
  asset_kind        app.design_asset_kind NOT NULL,
  file_id           uuid,
  url               app.https_url,
  created_at        timestamptz NOT NULL DEFAULT now(),
  created_by        uuid DEFAULT app.current_membership_id(),
  UNIQUE (tenant_id, id),
  CHECK (num_nonnulls(file_id, url) = 1),
  CHECK (asset_kind <> 'source_link' OR url IS NOT NULL),
  FOREIGN KEY (tenant_id, design_version_id) REFERENCES app.design_versions(tenant_id, id),
  FOREIGN KEY (tenant_id, file_id)           REFERENCES app.files(tenant_id, id)
);

CREATE TYPE app.message_channel AS ENUM ('sms', 'whatsapp', 'email');
CREATE TYPE app.client_decision AS ENUM ('approved', 'changes_requested');

CREATE TABLE app.design_approval_links (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  design_job_id       uuid NOT NULL,
  design_version_id   uuid NOT NULL,
  token_hash          bytea NOT NULL CHECK (octet_length(token_hash) = 32),
  expires_at          timestamptz NOT NULL,
  sent_via            app.message_channel NOT NULL,
  first_opened_at     timestamptz,
  decided_at          timestamptz,
  decision            app.client_decision,
  decision_ip         inet,
  decision_user_agent text CHECK (length(decision_user_agent) <= 512),
  revoked_at          timestamptz,
  created_at          timestamptz NOT NULL DEFAULT now(),
  created_by          uuid NOT NULL DEFAULT app.current_membership_id(),
  UNIQUE (tenant_id, id),
  UNIQUE (token_hash),
  CHECK (expires_at > created_at),
  CHECK ((decision IS NULL) = (decided_at IS NULL)),
  FOREIGN KEY (tenant_id, design_job_id)     REFERENCES app.design_jobs(tenant_id, id),
  FOREIGN KEY (tenant_id, design_version_id) REFERENCES app.design_versions(tenant_id, id)
);
COMMENT ON TABLE app.design_approval_links IS 'Client approval page (spec §7A): no login, shows one version, valid 14 days (tenant setting), single decision. Token stored as SHA-256 only.';
CREATE UNIQUE INDEX design_links_one_live ON app.design_approval_links (tenant_id, design_version_id)
  WHERE decided_at IS NULL AND revoked_at IS NULL;

CREATE TYPE app.feedback_author AS ENUM ('client', 'staff');
CREATE TABLE app.design_feedback (
  id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id         uuid NOT NULL REFERENCES app.tenants(id),
  design_job_id     uuid NOT NULL,
  design_version_id uuid NOT NULL,
  author_type       app.feedback_author NOT NULL,
  author_id         uuid,
  approval_link_id  uuid,
  comment           text NOT NULL CHECK (length(btrim(comment)) BETWEEN 1 AND 5000),
  created_at        timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, id),
  CHECK ((author_type = 'client' AND approval_link_id IS NOT NULL AND author_id IS NULL)
      OR (author_type = 'staff'  AND author_id IS NOT NULL)),
  FOREIGN KEY (tenant_id, design_job_id)     REFERENCES app.design_jobs(tenant_id, id),
  FOREIGN KEY (tenant_id, design_version_id) REFERENCES app.design_versions(tenant_id, id),
  FOREIGN KEY (tenant_id, author_id)         REFERENCES app.memberships(tenant_id, id),
  FOREIGN KEY (tenant_id, approval_link_id)  REFERENCES app.design_approval_links(tenant_id, id)
);
CREATE INDEX design_feedback_job_idx ON app.design_feedback (tenant_id, design_job_id, created_at);

CREATE TABLE app.design_time_logs (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id     uuid NOT NULL REFERENCES app.tenants(id),
  design_job_id uuid NOT NULL,
  membership_id uuid NOT NULL DEFAULT app.current_membership_id(),
  started_at    timestamptz NOT NULL DEFAULT now(),
  ended_at      timestamptz,
  UNIQUE (tenant_id, id),
  CHECK (ended_at IS NULL OR ended_at > started_at),
  FOREIGN KEY (tenant_id, design_job_id) REFERENCES app.design_jobs(tenant_id, id),
  FOREIGN KEY (tenant_id, membership_id) REFERENCES app.memberships(tenant_id, id)
);
CREATE UNIQUE INDEX design_time_one_running ON app.design_time_logs (tenant_id, membership_id) WHERE ended_at IS NULL;

CREATE TYPE app.print_order_status AS ENUM ('sent_to_print', 'ready', 'delivered', 'cancelled');
CREATE TABLE app.print_orders (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id     uuid NOT NULL REFERENCES app.tenants(id),
  design_job_id uuid NOT NULL,
  contractor_id uuid NOT NULL,
  quantity      integer NOT NULL CHECK (quantity > 0),
  cost          app.money_nn NOT NULL,
  due_date      date,
  status        app.print_order_status NOT NULL DEFAULT 'sent_to_print',
  notes         text CHECK (length(notes) <= 1000),
  created_at    timestamptz NOT NULL DEFAULT now(),
  created_by    uuid DEFAULT app.current_membership_id(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  updated_by    uuid,
  row_version   integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id, design_job_id) REFERENCES app.design_jobs(tenant_id, id),
  FOREIGN KEY (tenant_id, contractor_id) REFERENCES app.contractors(tenant_id, id)
);

-- ---------- Tasks & renewals -----------------------------------------
CREATE TYPE app.task_status AS ENUM ('open', 'in_progress', 'done', 'cancelled');
CREATE TABLE app.tasks (
  id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id        uuid NOT NULL REFERENCES app.tenants(id),
  title            app.short_text NOT NULL,
  description      text CHECK (length(description) <= 4000),
  customer_id      uuid,
  project_id       uuid,
  project_stage_id uuid,
  design_job_id    uuid,
  assignee_id      uuid NOT NULL,
  due_at           timestamptz,
  priority         app.priority NOT NULL DEFAULT 'normal',
  status           app.task_status NOT NULL DEFAULT 'open',
  completed_at     timestamptz,
  created_at       timestamptz NOT NULL DEFAULT now(),
  created_by       uuid DEFAULT app.current_membership_id(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  updated_by       uuid,
  row_version      integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id),
  CHECK (status <> 'done' OR completed_at IS NOT NULL),
  CHECK (project_stage_id IS NULL OR project_id IS NOT NULL),
  FOREIGN KEY (tenant_id, customer_id)      REFERENCES app.customers(tenant_id, id),
  FOREIGN KEY (tenant_id, project_id)       REFERENCES app.projects(tenant_id, id),
  FOREIGN KEY (tenant_id, project_stage_id) REFERENCES app.project_stages(tenant_id, id),
  FOREIGN KEY (tenant_id, design_job_id)    REFERENCES app.design_jobs(tenant_id, id),
  FOREIGN KEY (tenant_id, assignee_id)      REFERENCES app.memberships(tenant_id, id)
);
CREATE INDEX tasks_assignee_idx ON app.tasks (tenant_id, assignee_id, due_at) WHERE status IN ('open', 'in_progress');
CREATE INDEX tasks_overdue_idx  ON app.tasks (tenant_id, due_at) WHERE status IN ('open', 'in_progress');
CREATE INDEX tasks_project_idx  ON app.tasks (tenant_id, project_id) WHERE project_id IS NOT NULL;

CREATE TYPE app.renewal_item AS ENUM ('business_license', 'annual_return', 'annual_fee', 'tax_filing', 'permit', 'other');
CREATE TYPE app.renewal_status AS ENUM ('upcoming', 'renewed', 'lapsed', 'cancelled');
CREATE TABLE app.renewals (
  id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id        uuid NOT NULL REFERENCES app.tenants(id),
  customer_id      uuid NOT NULL,
  project_id       uuid,
  item_type        app.renewal_item NOT NULL,
  description      app.short_text NOT NULL,
  due_date         date NOT NULL,
  status           app.renewal_status NOT NULL DEFAULT 'upcoming',
  renewed_at       timestamptz,
  next_renewal_id  uuid,
  notes            text CHECK (length(notes) <= 1000),
  created_at       timestamptz NOT NULL DEFAULT now(),
  created_by       uuid DEFAULT app.current_membership_id(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  updated_by       uuid,
  row_version      integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id),
  CHECK (status <> 'renewed' OR renewed_at IS NOT NULL),
  CHECK (next_renewal_id IS DISTINCT FROM id),
  FOREIGN KEY (tenant_id, customer_id)     REFERENCES app.customers(tenant_id, id),
  FOREIGN KEY (tenant_id, project_id)      REFERENCES app.projects(tenant_id, id),
  FOREIGN KEY (tenant_id, next_renewal_id) REFERENCES app.renewals(tenant_id, id)
);
CREATE INDEX renewals_due_idx ON app.renewals (tenant_id, due_date) WHERE status = 'upcoming';
CREATE UNIQUE INDEX renewals_project_item_uq ON app.renewals (tenant_id, project_id, item_type, due_date) WHERE project_id IS NOT NULL;
