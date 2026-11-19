-- =====================================================================
-- 090_approvals_notifications_governance.sql — approval ladder,
-- notifications, outbound messaging outbox, idempotency, governance
-- evidence (backups, access reviews, privacy requests, retention)
-- =====================================================================
SET search_path = app, public;

-- ---------- Approval ladder (spec §2A) ------------------------------
-- Expenses, returns/refunds and leave carry their own requested/approved
-- status and decided_by columns (they ARE the request), so they are not
-- duplicated here; app.v_pending_approvals unions all four sources.
CREATE TYPE app.approval_type AS ENUM (
  'sale_discount',        -- discount above staff limit           (Staff -> Manager)
  'job_price_change',     -- price change on project / design job (Staff -> Manager)
  'sale_void',            -- void a completed sale                (Manager -> Admin)
  'transaction_correction', -- correction entry on the ledger     (Manager -> Admin)
  'payment_void',         -- void a payment received/made         (Manager -> Admin)
  'day_reopen',           -- reopen a closed day                  (Manager -> Admin)
  'product_price_change'  -- change a product selling price       (Manager -> Admin)
);
CREATE TYPE app.approval_status AS ENUM ('pending', 'approved', 'rejected', 'cancelled', 'expired');

CREATE TABLE app.approval_requests (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  approval_type       app.approval_type NOT NULL,
  status              app.approval_status NOT NULL DEFAULT 'pending',
  reason              text NOT NULL CHECK (length(btrim(reason)) >= 3),
  requested_amount    numeric(18,2) CHECK (requested_amount >= 0),  -- refund / expense / new price
  requested_percent   numeric(5,2)  CHECK (requested_percent BETWEEN 0 AND 100),
  current_amount      numeric(18,2),                                  -- e.g. old price
  sale_id             uuid,
  project_id          uuid,
  design_job_id       uuid,
  financial_transaction_id uuid,
  payment_received_id uuid,
  payment_made_id     uuid,
  day_closing_id      uuid,
  product_id          uuid,
  requested_by        uuid NOT NULL DEFAULT app.current_membership_id(),
  requested_at        timestamptz NOT NULL DEFAULT now(),
  decided_by          uuid,
  decided_at          timestamptz,
  decision_note       text CHECK (length(decision_note) <= 1000),
  expires_at          timestamptz,
  created_at          timestamptz NOT NULL DEFAULT now(),
  created_by          uuid DEFAULT app.current_membership_id(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  updated_by          uuid,
  row_version         integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id),
  CHECK (num_nonnulls(sale_id, project_id, design_job_id,
                      financial_transaction_id, payment_received_id, payment_made_id,
                      day_closing_id, product_id) = 1),
  CHECK (CASE approval_type
     WHEN 'sale_discount'          THEN sale_id IS NOT NULL AND requested_percent IS NOT NULL
     WHEN 'job_price_change'       THEN (project_id IS NOT NULL OR design_job_id IS NOT NULL) AND requested_amount IS NOT NULL
     WHEN 'sale_void'              THEN sale_id IS NOT NULL
     WHEN 'transaction_correction' THEN financial_transaction_id IS NOT NULL AND requested_amount IS NOT NULL
     WHEN 'payment_void'           THEN payment_received_id IS NOT NULL OR payment_made_id IS NOT NULL
     WHEN 'day_reopen'             THEN day_closing_id IS NOT NULL
     WHEN 'product_price_change'   THEN product_id IS NOT NULL AND requested_amount IS NOT NULL
   END),
  CHECK (status IN ('pending', 'cancelled', 'expired') OR (decided_by IS NOT NULL AND decided_at IS NOT NULL)),
  CHECK (status <> 'rejected' OR decision_note IS NOT NULL),
  -- Segregation of duties: nobody approves their own request.
  CONSTRAINT approval_no_self_approval CHECK (decided_by IS NULL OR decided_by <> requested_by),
  FOREIGN KEY (tenant_id, sale_id)             REFERENCES app.sales(tenant_id, id),
  FOREIGN KEY (tenant_id, project_id)          REFERENCES app.projects(tenant_id, id),
  FOREIGN KEY (tenant_id, design_job_id)       REFERENCES app.design_jobs(tenant_id, id),
  FOREIGN KEY (tenant_id, financial_transaction_id) REFERENCES app.financial_transactions(tenant_id, id),
  FOREIGN KEY (tenant_id, payment_received_id) REFERENCES app.payments_received(tenant_id, id),
  FOREIGN KEY (tenant_id, payment_made_id)     REFERENCES app.payments_made(tenant_id, id),
  FOREIGN KEY (tenant_id, day_closing_id)      REFERENCES app.day_closings(tenant_id, id),
  FOREIGN KEY (tenant_id, product_id)          REFERENCES app.products(tenant_id, id),
  FOREIGN KEY (tenant_id, requested_by)        REFERENCES app.memberships(tenant_id, id),
  FOREIGN KEY (tenant_id, decided_by)          REFERENCES app.memberships(tenant_id, id)
);
-- One open request per target per type (duplicate-submission guard).
CREATE UNIQUE INDEX approval_one_pending ON app.approval_requests (tenant_id, approval_type,
  coalesce(sale_id, project_id, design_job_id, financial_transaction_id,
           payment_received_id, payment_made_id, day_closing_id, product_id))
  WHERE status = 'pending';
CREATE INDEX approval_pending_idx ON app.approval_requests (tenant_id, approval_type, requested_at) WHERE status = 'pending';
CREATE INDEX approval_requester_idx ON app.approval_requests (tenant_id, requested_by, requested_at DESC);

ALTER TABLE app.financial_transactions ADD CONSTRAINT fin_txn_approval_fk
  FOREIGN KEY (tenant_id, approval_request_id) REFERENCES app.approval_requests(tenant_id, id);
ALTER TABLE app.day_closings ADD COLUMN reopen_approval_id uuid;
ALTER TABLE app.day_closings ADD CONSTRAINT day_closings_reopen_approval_fk
  FOREIGN KEY (tenant_id, reopen_approval_id) REFERENCES app.approval_requests(tenant_id, id);
ALTER TABLE app.day_closings ADD CONSTRAINT day_closings_reopen_needs_approval
  CHECK (status <> 'reopened' OR reopen_approval_id IS NOT NULL);
ALTER TABLE app.product_price_changes ADD COLUMN approval_request_id uuid;
ALTER TABLE app.product_price_changes ADD CONSTRAINT price_change_approval_fk
  FOREIGN KEY (tenant_id, approval_request_id) REFERENCES app.approval_requests(tenant_id, id);

-- ---------- In-app notifications ------------------------------------
CREATE TYPE app.notification_type AS ENUM (
  'low_stock', 'debt_due', 'task_overdue', 'task_assigned', 'renewal_due', 'salary_due',
  'approval_waiting', 'approval_decided', 'design_brief_assigned', 'design_feedback',
  'design_deadline', 'design_approved', 'print_ready', 'account_locked', 'day_closed',
  'project_stage_changed', 'payment_received');

CREATE TABLE app.notifications (
  id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id         uuid NOT NULL REFERENCES app.tenants(id),
  recipient_id      uuid NOT NULL,
  notification_type app.notification_type NOT NULL,
  title             app.short_text NOT NULL,
  body              text CHECK (length(body) <= 1000),   -- never salaries, IDs or document contents
  link_path         text CHECK (link_path ~ '^/[A-Za-z0-9/_?=&.-]{0,300}$'),
  dedupe_key        text NOT NULL CHECK (length(dedupe_key) <= 200),
  read_at           timestamptz,
  created_at        timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, recipient_id, dedupe_key),
  FOREIGN KEY (tenant_id, recipient_id) REFERENCES app.memberships(tenant_id, id)
);
CREATE INDEX notifications_inbox_idx ON app.notifications (tenant_id, recipient_id, created_at DESC);
CREATE INDEX notifications_unread_idx ON app.notifications (tenant_id, recipient_id) WHERE read_at IS NULL;

CREATE TABLE app.notification_rules (
  tenant_id         uuid NOT NULL REFERENCES app.tenants(id),
  notification_type app.notification_type NOT NULL,
  enabled           boolean NOT NULL DEFAULT true,
  in_app            boolean NOT NULL DEFAULT true,
  sms               boolean NOT NULL DEFAULT false,
  whatsapp          boolean NOT NULL DEFAULT false,
  email             boolean NOT NULL DEFAULT false,
  updated_at        timestamptz NOT NULL DEFAULT now(),
  updated_by        uuid,
  PRIMARY KEY (tenant_id, notification_type)
);

CREATE TABLE app.notification_rule_roles (
  tenant_id         uuid NOT NULL,
  notification_type app.notification_type NOT NULL,
  role_id           uuid NOT NULL,
  PRIMARY KEY (tenant_id, notification_type, role_id),
  FOREIGN KEY (tenant_id, notification_type) REFERENCES app.notification_rules(tenant_id, notification_type) ON DELETE CASCADE,
  FOREIGN KEY (tenant_id, role_id) REFERENCES app.roles(tenant_id, id) ON DELETE CASCADE
);

-- ---------- Message templates & outbound outbox ---------------------
CREATE TABLE app.message_templates (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES app.tenants(id),
  code        text NOT NULL CHECK (code ~ '^[a-z_]{3,60}$'),
  channel     app.message_channel NOT NULL,
  locale      text NOT NULL CHECK (locale IN ('sw', 'en')),
  subject     text CHECK (length(subject) <= 200),
  body        text NOT NULL CHECK (length(body) BETWEEN 1 AND 2000),
  active      boolean NOT NULL DEFAULT true,
  created_at  timestamptz NOT NULL DEFAULT now(),
  created_by  uuid DEFAULT app.current_membership_id(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  updated_by  uuid,
  row_version integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, code, channel, locale)
);

CREATE TYPE app.outbound_status AS ENUM ('queued', 'sending', 'sent', 'delivered', 'failed', 'cancelled');
CREATE TABLE app.outbound_messages (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  channel             app.message_channel NOT NULL,
  recipient           text NOT NULL CHECK (length(recipient) BETWEEN 5 AND 254),
  template_id         uuid,
  subject             text CHECK (length(subject) <= 200),
  body                text NOT NULL CHECK (length(body) BETWEEN 1 AND 4000),
  attachment_file_id  uuid,
  status              app.outbound_status NOT NULL DEFAULT 'queued',
  attempts            smallint NOT NULL DEFAULT 0 CHECK (attempts BETWEEN 0 AND 20),
  next_attempt_at     timestamptz NOT NULL DEFAULT now(),
  locked_until        timestamptz,
  last_error          text CHECK (length(last_error) <= 1000),
  provider            text CHECK (length(provider) <= 40),
  provider_message_id text CHECK (length(provider_message_id) <= 120),
  idempotency_key     text NOT NULL CHECK (length(idempotency_key) <= 200),
  sent_at             timestamptz,
  delivered_at        timestamptz,
  customer_id         uuid,
  sale_id             uuid,
  invoice_id          uuid,
  quotation_id        uuid,
  payment_received_id uuid,
  project_id          uuid,
  design_job_id       uuid,
  renewal_id          uuid,
  payslip_id          uuid,
  created_at          timestamptz NOT NULL DEFAULT now(),
  created_by          uuid DEFAULT app.current_membership_id(),
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, idempotency_key),
  CHECK (num_nonnulls(customer_id, sale_id, invoice_id, quotation_id, payment_received_id, project_id,
                      design_job_id, renewal_id, payslip_id) >= 1),
  CHECK (status NOT IN ('sent', 'delivered') OR sent_at IS NOT NULL),
  FOREIGN KEY (tenant_id, template_id)         REFERENCES app.message_templates(tenant_id, id),
  FOREIGN KEY (tenant_id, attachment_file_id)  REFERENCES app.files(tenant_id, id),
  FOREIGN KEY (tenant_id, customer_id)         REFERENCES app.customers(tenant_id, id),
  FOREIGN KEY (tenant_id, sale_id)             REFERENCES app.sales(tenant_id, id),
  FOREIGN KEY (tenant_id, invoice_id)          REFERENCES app.invoices(tenant_id, id),
  FOREIGN KEY (tenant_id, quotation_id)        REFERENCES app.quotations(tenant_id, id),
  FOREIGN KEY (tenant_id, payment_received_id) REFERENCES app.payments_received(tenant_id, id),
  FOREIGN KEY (tenant_id, project_id)          REFERENCES app.projects(tenant_id, id),
  FOREIGN KEY (tenant_id, design_job_id)       REFERENCES app.design_jobs(tenant_id, id),
  FOREIGN KEY (tenant_id, renewal_id)          REFERENCES app.renewals(tenant_id, id),
  FOREIGN KEY (tenant_id, payslip_id)          REFERENCES app.payslips(tenant_id, id)
);
COMMENT ON TABLE app.outbound_messages IS 'Transactional outbox for SMS/WhatsApp/email. Written in the same transaction as the business change; a worker claims rows with FOR UPDATE SKIP LOCKED and retries with backoff.';
CREATE INDEX outbound_queue_idx ON app.outbound_messages (next_attempt_at) WHERE status IN ('queued', 'failed');

CREATE TABLE app.renewal_reminders (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  renewal_id          uuid NOT NULL,
  days_before         smallint NOT NULL CHECK (days_before BETWEEN 0 AND 365),
  outbound_message_id uuid,
  created_at          timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, renewal_id, days_before),
  FOREIGN KEY (tenant_id, renewal_id)          REFERENCES app.renewals(tenant_id, id),
  FOREIGN KEY (tenant_id, outbound_message_id) REFERENCES app.outbound_messages(tenant_id, id)
);

-- ---------- Generic attachments ("any record can have files", spec §12) --
CREATE TABLE app.attachments (
  id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id          uuid NOT NULL REFERENCES app.tenants(id),
  file_id            uuid NOT NULL,
  caption            text CHECK (length(caption) <= 200),
  sale_id            uuid,
  purchase_id        uuid,
  expense_id         uuid,
  payment_received_id uuid,
  payment_made_id    uuid,
  customer_id        uuid,
  supplier_id        uuid,
  contractor_id      uuid,
  project_id         uuid,
  design_job_id      uuid,
  task_id            uuid,
  employee_id        uuid,
  contractor_bill_id uuid,
  stock_adjustment_id uuid,
  created_at         timestamptz NOT NULL DEFAULT now(),
  created_by         uuid DEFAULT app.current_membership_id(),
  UNIQUE (tenant_id, id),
  CHECK (num_nonnulls(sale_id, purchase_id, expense_id, payment_received_id, payment_made_id, customer_id,
                      supplier_id, contractor_id, project_id, design_job_id, task_id, employee_id,
                      contractor_bill_id, stock_adjustment_id) = 1),
  FOREIGN KEY (tenant_id, file_id)             REFERENCES app.files(tenant_id, id),
  FOREIGN KEY (tenant_id, sale_id)             REFERENCES app.sales(tenant_id, id),
  FOREIGN KEY (tenant_id, purchase_id)         REFERENCES app.purchases(tenant_id, id),
  FOREIGN KEY (tenant_id, expense_id)          REFERENCES app.expenses(tenant_id, id),
  FOREIGN KEY (tenant_id, payment_received_id) REFERENCES app.payments_received(tenant_id, id),
  FOREIGN KEY (tenant_id, payment_made_id)     REFERENCES app.payments_made(tenant_id, id),
  FOREIGN KEY (tenant_id, customer_id)         REFERENCES app.customers(tenant_id, id),
  FOREIGN KEY (tenant_id, supplier_id)         REFERENCES app.suppliers(tenant_id, id),
  FOREIGN KEY (tenant_id, contractor_id)       REFERENCES app.contractors(tenant_id, id),
  FOREIGN KEY (tenant_id, project_id)          REFERENCES app.projects(tenant_id, id),
  FOREIGN KEY (tenant_id, design_job_id)       REFERENCES app.design_jobs(tenant_id, id),
  FOREIGN KEY (tenant_id, task_id)             REFERENCES app.tasks(tenant_id, id),
  FOREIGN KEY (tenant_id, employee_id)         REFERENCES app.employees(tenant_id, id),
  FOREIGN KEY (tenant_id, contractor_bill_id)  REFERENCES app.contractor_bills(tenant_id, id),
  FOREIGN KEY (tenant_id, stock_adjustment_id) REFERENCES app.stock_adjustments(tenant_id, id)
);
COMMENT ON TABLE app.attachments IS 'Typed exclusive-arc association (exactly one parent FK) instead of a polymorphic entity_type/entity_id pair, so every attachment has real referential integrity.';

-- ---------- API idempotency ------------------------------------------
CREATE TABLE app.idempotency_keys (
  tenant_id       uuid NOT NULL REFERENCES app.tenants(id),
  idempotency_key text NOT NULL CHECK (length(idempotency_key) BETWEEN 8 AND 100),
  membership_id   uuid NOT NULL,
  request_method  text NOT NULL,
  request_path    text NOT NULL,
  request_hash    bytea NOT NULL CHECK (octet_length(request_hash) = 32),
  response_status smallint,
  response_body   jsonb,          -- resource ids only; never PII payloads
  created_at      timestamptz NOT NULL DEFAULT now(),
  expires_at      timestamptz NOT NULL DEFAULT now() + interval '24 hours',
  PRIMARY KEY (tenant_id, idempotency_key),
  FOREIGN KEY (tenant_id, membership_id) REFERENCES app.memberships(tenant_id, id)
);
CREATE INDEX idempotency_expiry_idx ON app.idempotency_keys (expires_at);

-- ---------- Governance & compliance evidence -------------------------
CREATE TYPE app.backup_status AS ENUM ('running', 'succeeded', 'failed');
CREATE TABLE app.backup_runs (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  backup_kind     text NOT NULL CHECK (backup_kind IN ('full', 'incremental', 'wal_archive_check', 'restore_test', 'tenant_export')),
  tenant_id       uuid REFERENCES app.tenants(id),   -- null = platform-wide backup
  status          app.backup_status NOT NULL,
  started_at      timestamptz NOT NULL DEFAULT now(),
  finished_at     timestamptz,
  size_bytes      bigint CHECK (size_bytes >= 0),
  storage_uri     text CHECK (length(storage_uri) <= 500),
  checksum_sha256 bytea CHECK (octet_length(checksum_sha256) = 32),
  verified_at     timestamptz,
  verified_by     text,
  error           text CHECK (length(error) <= 2000),
  CHECK (status = 'running' OR finished_at IS NOT NULL)
);
COMMENT ON TABLE app.backup_runs IS 'Backup screen + SOC 2 A1.2/A1.3 evidence. Platform rows (tenant_id null) are visible only to platform operators; tenant Admins see their own exports.';

CREATE TYPE app.access_review_status AS ENUM ('open', 'completed');
CREATE TABLE app.access_reviews (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id    uuid NOT NULL REFERENCES app.tenants(id),
  period_label text NOT NULL CHECK (length(period_label) <= 40),
  status       app.access_review_status NOT NULL DEFAULT 'open',
  reviewer_id  uuid NOT NULL,
  started_at   timestamptz NOT NULL DEFAULT now(),
  completed_at timestamptz,
  UNIQUE (tenant_id, id),
  CHECK (status <> 'completed' OR completed_at IS NOT NULL),
  FOREIGN KEY (tenant_id, reviewer_id) REFERENCES app.memberships(tenant_id, id)
);
CREATE TABLE app.access_review_items (
  tenant_id        uuid NOT NULL,
  access_review_id uuid NOT NULL,
  membership_id    uuid NOT NULL,
  role_code        text NOT NULL,
  overrides        text[] NOT NULL DEFAULT '{}',
  decision         text CHECK (decision IN ('keep', 'change', 'revoke')),
  note             text CHECK (length(note) <= 1000),
  decided_at       timestamptz,
  PRIMARY KEY (tenant_id, access_review_id, membership_id),
  FOREIGN KEY (tenant_id, access_review_id) REFERENCES app.access_reviews(tenant_id, id) ON DELETE CASCADE,
  FOREIGN KEY (tenant_id, membership_id)    REFERENCES app.memberships(tenant_id, id)
);

CREATE TYPE app.privacy_request_type AS ENUM ('access', 'correction', 'deletion', 'portability', 'objection');
CREATE TYPE app.privacy_request_status AS ENUM ('received', 'verifying', 'in_progress', 'completed', 'rejected');
CREATE TABLE app.privacy_requests (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id     uuid NOT NULL REFERENCES app.tenants(id),
  request_type  app.privacy_request_type NOT NULL,
  customer_id   uuid,
  employee_id   uuid,
  status        app.privacy_request_status NOT NULL DEFAULT 'received',
  received_at   timestamptz NOT NULL DEFAULT now(),
  due_at        timestamptz NOT NULL,     -- statutory deadline: decision D-12
  completed_at  timestamptz,
  outcome_note  text CHECK (length(outcome_note) <= 2000),
  handled_by    uuid,
  created_at    timestamptz NOT NULL DEFAULT now(),
  created_by    uuid DEFAULT app.current_membership_id(),
  UNIQUE (tenant_id, id),
  CHECK (num_nonnulls(customer_id, employee_id) = 1),
  CHECK (status NOT IN ('completed', 'rejected') OR completed_at IS NOT NULL),
  FOREIGN KEY (tenant_id, customer_id) REFERENCES app.customers(tenant_id, id),
  FOREIGN KEY (tenant_id, employee_id) REFERENCES app.employees(tenant_id, id),
  FOREIGN KEY (tenant_id, handled_by)  REFERENCES app.memberships(tenant_id, id)
);

CREATE TABLE app.legal_holds (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES app.tenants(id),
  customer_id uuid,
  employee_id uuid,
  project_id  uuid,
  reason      text NOT NULL CHECK (length(btrim(reason)) >= 5),
  placed_at   timestamptz NOT NULL DEFAULT now(),
  placed_by   uuid NOT NULL DEFAULT app.current_membership_id(),
  released_at timestamptz,
  released_by uuid,
  UNIQUE (tenant_id, id),
  CHECK (num_nonnulls(customer_id, employee_id, project_id) = 1),
  FOREIGN KEY (tenant_id, customer_id) REFERENCES app.customers(tenant_id, id),
  FOREIGN KEY (tenant_id, employee_id) REFERENCES app.employees(tenant_id, id),
  FOREIGN KEY (tenant_id, project_id)  REFERENCES app.projects(tenant_id, id),
  FOREIGN KEY (tenant_id, placed_by)   REFERENCES app.memberships(tenant_id, id),
  FOREIGN KEY (tenant_id, released_by) REFERENCES app.memberships(tenant_id, id)
);
COMMENT ON TABLE app.legal_holds IS 'While an active hold exists, retention/purge jobs and privacy deletions skip the held subject.';

-- Retention schedule is DATA, not code: periods are business/legal decisions (D-10).
CREATE TABLE app.retention_policies (
  data_category    text PRIMARY KEY CHECK (data_category ~ '^[a-z_]{3,60}$'),
  description      text NOT NULL,
  retention_period interval,          -- NULL = not yet decided; purge jobs do nothing
  legal_basis      text,
  disposal_method  text NOT NULL CHECK (disposal_method IN ('delete', 'anonymise', 'archive_then_delete', 'none')),
  approved_by      text,
  approved_at      timestamptz,
  CHECK (retention_period IS NULL OR approved_by IS NOT NULL)
);
