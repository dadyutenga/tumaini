-- =====================================================================
-- 030_org_settings_parties.sql — business units, locations, files,
-- settings, numbering, customers/clients, suppliers, contractors
-- =====================================================================
SET search_path = app, public;

-- ---------- Business units & locations --------------------------------
CREATE TYPE app.business_unit_kind AS ENUM ('retail_shop', 'professional_services');

CREATE TABLE app.business_units (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES app.tenants(id),
  code        text NOT NULL CHECK (code ~ '^[A-Z0-9]{1,10}$'),
  name        app.short_text NOT NULL,
  kind        app.business_unit_kind NOT NULL,
  status      app.record_status NOT NULL DEFAULT 'active',
  created_at  timestamptz NOT NULL DEFAULT now(),
  created_by  uuid DEFAULT app.current_membership_id(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  updated_by  uuid,
  row_version integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, code),
  UNIQUE (tenant_id, id)
);
COMMENT ON TABLE app.business_units IS 'Business A (shop) and Business B (services). Every money record carries business_unit_id for the A/B/both filter.';

CREATE TYPE app.location_kind AS ENUM ('shop', 'office', 'warehouse');
CREATE TABLE app.locations (
  id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id        uuid NOT NULL REFERENCES app.tenants(id),
  business_unit_id uuid NOT NULL,
  name             app.short_text NOT NULL,
  kind             app.location_kind NOT NULL,
  address          text,
  phone            app.phone_e164,
  is_default       boolean NOT NULL DEFAULT false,
  status           app.record_status NOT NULL DEFAULT 'active',
  created_at       timestamptz NOT NULL DEFAULT now(),
  created_by       uuid DEFAULT app.current_membership_id(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  updated_by       uuid,
  row_version      integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, name),
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id, business_unit_id) REFERENCES app.business_units(tenant_id, id)
);
CREATE UNIQUE INDEX locations_one_default ON app.locations (tenant_id) WHERE is_default;
COMMENT ON TABLE app.locations IS 'Branch / shop / office / store. Needed for stock transfers (spec §2) and per-till end-of-day closing.';

ALTER TABLE app.memberships
  ADD CONSTRAINT memberships_default_location_fk
  FOREIGN KEY (tenant_id, default_location_id) REFERENCES app.locations(tenant_id, id);

-- ---------- Files (object storage metadata) ---------------------------
CREATE TYPE app.data_class AS ENUM ('public', 'internal', 'confidential', 'restricted');
CREATE TYPE app.scan_status AS ENUM ('pending', 'clean', 'infected', 'error');

CREATE TABLE app.files (
  id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id      uuid NOT NULL REFERENCES app.tenants(id),
  storage_key    text NOT NULL CHECK (storage_key ~ '^[A-Za-z0-9/_.-]+$' AND length(storage_key) BETWEEN 10 AND 512),
  original_name  text NOT NULL CHECK (length(original_name) BETWEEN 1 AND 255),
  mime_type      text NOT NULL CHECK (mime_type ~ '^[a-z]+/[a-z0-9.+-]+$'),
  size_bytes     bigint NOT NULL CHECK (size_bytes > 0 AND size_bytes <= 52428800), -- 50 MiB
  sha256         bytea NOT NULL CHECK (octet_length(sha256) = 32),
  classification app.data_class NOT NULL DEFAULT 'confidential',
  scan_status    app.scan_status NOT NULL DEFAULT 'pending',
  deleted_at     timestamptz,     -- object purged from storage (retention); row kept for audit
  created_at     timestamptz NOT NULL DEFAULT now(),
  created_by     uuid DEFAULT app.current_membership_id(),
  UNIQUE (tenant_id, storage_key),
  UNIQUE (tenant_id, id)
);
COMMENT ON TABLE app.files IS 'Metadata for objects in private, encrypted object storage (key prefix = tenant_id/). Bytes never live in Postgres. Download only through short-lived signed URLs after an authorization check.';
CREATE INDEX files_tenant_created_idx ON app.files (tenant_id, created_at DESC);

ALTER TABLE app.locations ADD COLUMN logo_file_id uuid; -- reserved; branch-specific receipt logo
ALTER TABLE app.locations ADD FOREIGN KEY (tenant_id, logo_file_id) REFERENCES app.files(tenant_id, id);

-- ---------- Settings --------------------------------------------------
CREATE TABLE app.tenant_settings (
  tenant_id                 uuid PRIMARY KEY REFERENCES app.tenants(id),
  business_name             app.short_text NOT NULL,
  logo_file_id              uuid,
  address                   text,
  phone                     app.phone_e164,
  email                     app.email,
  tin                       text CHECK (tin ~ '^[0-9]{3}-?[0-9]{3}-?[0-9]{3}$'),
  vrn                       text CHECK (length(vrn) <= 20),
  registration_no           text CHECK (length(registration_no) <= 50),
  currency                  app.currency NOT NULL DEFAULT 'TZS',
  date_format               text NOT NULL DEFAULT 'DD/MM/YYYY' CHECK (date_format IN ('DD/MM/YYYY','DD MMM YYYY','YYYY-MM-DD')),
  session_idle_minutes      smallint NOT NULL DEFAULT 30 CHECK (session_idle_minutes BETWEEN 5 AND 240),
  session_warning_seconds   smallint NOT NULL DEFAULT 60 CHECK (session_warning_seconds BETWEEN 15 AND 600),
  session_absolute_hours    smallint NOT NULL DEFAULT 12 CHECK (session_absolute_hours BETWEEN 1 AND 720),
  lockout_threshold         smallint NOT NULL DEFAULT 5  CHECK (lockout_threshold BETWEEN 3 AND 20),
  lockout_minutes           smallint NOT NULL DEFAULT 15 CHECK (lockout_minutes BETWEEN 5 AND 1440),
  mfa_required_for_admin    boolean NOT NULL DEFAULT true,
  low_stock_default         numeric(14,3) NOT NULL DEFAULT 5 CHECK (low_stock_default >= 0),
  allow_negative_stock      boolean NOT NULL DEFAULT false,
  approval_link_ttl_days    smallint NOT NULL DEFAULT 14 CHECK (approval_link_ttl_days BETWEEN 1 AND 60),
  renewal_reminder_days     smallint[] NOT NULL DEFAULT '{30,7}',
  debt_reminder_days        smallint[] NOT NULL DEFAULT '{0,7,30}',
  prices_include_tax        boolean NOT NULL DEFAULT true,  -- ASSUMPTION A-07
  require_sod_payroll       boolean NOT NULL DEFAULT true,  -- generator <> approver
  receipt_footer            text CHECK (length(receipt_footer) <= 500),
  invoice_footer            text CHECK (length(invoice_footer) <= 1000),
  quotation_terms           text CHECK (length(quotation_terms) <= 4000),
  payment_instructions      text CHECK (length(payment_instructions) <= 1000),
  updated_at                timestamptz NOT NULL DEFAULT now(),
  updated_by                uuid,
  row_version               integer NOT NULL DEFAULT 1,
  FOREIGN KEY (tenant_id, logo_file_id) REFERENCES app.files(tenant_id, id),
  CHECK (array_position(renewal_reminder_days, NULL) IS NULL)
);
COMMENT ON TABLE app.tenant_settings IS 'Settings screens (spec §10): Business Profile, Finance and Tax, Documents, System. One row per tenant.';

CREATE TABLE app.tax_rates (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES app.tenants(id),
  name        app.short_text NOT NULL,
  rate_pct    app.pct NOT NULL,
  is_default  boolean NOT NULL DEFAULT false,
  status      app.record_status NOT NULL DEFAULT 'active',
  created_at  timestamptz NOT NULL DEFAULT now(),
  created_by  uuid DEFAULT app.current_membership_id(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  updated_by  uuid,
  row_version integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, name),
  UNIQUE (tenant_id, id)
);
CREATE UNIQUE INDEX tax_rates_one_default ON app.tax_rates (tenant_id) WHERE is_default;

-- ---------- Gap-free document numbering -------------------------------
CREATE TYPE app.doc_type AS ENUM (
  'sale_receipt', 'payment_receipt', 'sale_return', 'invoice', 'quotation',
  'project', 'design_job', 'purchase', 'goods_receipt', 'stock_adjustment',
  'stock_transfer', 'expense', 'payment_made', 'account_transfer', 'payslip',
  'contractor_bill', 'employee');

CREATE TABLE app.document_sequences (
  tenant_id  uuid NOT NULL REFERENCES app.tenants(id),
  doc_type   app.doc_type NOT NULL,
  prefix     text NOT NULL CHECK (prefix ~ '^[A-Z]{1,6}-$'),
  next_value bigint NOT NULL DEFAULT 1 CHECK (next_value >= 1),
  pad_width  smallint NOT NULL DEFAULT 4 CHECK (pad_width BETWEEN 3 AND 10),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, doc_type)
);
COMMENT ON TABLE app.document_sequences IS 'R-0001, INV-0001, Q-0001... (spec §11). Row lock in app.next_document_number makes numbers gap-free and never repeated: a rolled-back transaction releases its number.';

CREATE FUNCTION app.next_document_number(p_type app.doc_type) RETURNS text
LANGUAGE plpgsql AS $$
DECLARE v_prefix text; v_val bigint; v_pad smallint;
BEGIN
  UPDATE app.document_sequences
     SET next_value = next_value + 1, updated_at = now()
   WHERE tenant_id = app.require_tenant() AND doc_type = p_type
  RETURNING prefix, next_value - 1, pad_width INTO v_prefix, v_val, v_pad;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'no sequence configured for %', p_type USING ERRCODE = 'BM008';
  END IF;
  RETURN v_prefix || lpad(v_val::text, greatest(v_pad, length(v_val::text)), '0');
END $$;

-- ---------- Customers & clients (one party table) ---------------------
-- Shop customers and service clients are the same real-world entity
-- (a person may buy a phone and register a company), so one table avoids
-- duplicate records and gives Finance a single "owed to us" party.
CREATE TYPE app.client_status AS ENUM ('lead', 'active', 'completed', 'inactive');

CREATE TABLE app.lead_sources (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES app.tenants(id),
  name        app.short_text NOT NULL,
  status      app.record_status NOT NULL DEFAULT 'active',
  created_at  timestamptz NOT NULL DEFAULT now(),
  created_by  uuid DEFAULT app.current_membership_id(),
  UNIQUE (tenant_id, name),
  UNIQUE (tenant_id, id)
);

CREATE TABLE app.customers (
  id                     uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id              uuid NOT NULL REFERENCES app.tenants(id),
  full_name              app.short_text NOT NULL,
  business_name          text CHECK (length(business_name) <= 200),
  phone                  app.phone_e164,
  email                  app.email,
  address                text CHECK (length(address) <= 500),
  -- National ID / passport: restricted PII. Ciphertext from API-side envelope
  -- encryption; HMAC blind index for exact-match search; last4 for display.
  national_id_ciphertext bytea,
  national_id_key_id     text,
  national_id_hmac       bytea CHECK (octet_length(national_id_hmac) = 32),
  national_id_last4      text CHECK (national_id_last4 ~ '^[A-Z0-9]{4}$'),
  is_shop_customer       boolean NOT NULL DEFAULT false,
  client_status          app.client_status,          -- null = not a services client
  lead_source_id         uuid,
  needs_summary          text CHECK (length(needs_summary) <= 2000),
  notes                  text CHECK (length(notes) <= 4000),
  credit_limit           app.money_nn,               -- null = no credit limit set
  registered_by          uuid NOT NULL DEFAULT app.current_membership_id(),
  assigned_to            uuid,
  archived_at            timestamptz,
  created_at             timestamptz NOT NULL DEFAULT now(),
  created_by             uuid DEFAULT app.current_membership_id(),
  updated_at             timestamptz NOT NULL DEFAULT now(),
  updated_by             uuid,
  row_version            integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id),
  CHECK (is_shop_customer OR client_status IS NOT NULL),
  CHECK ((national_id_ciphertext IS NULL) = (national_id_key_id IS NULL)),
  CHECK (national_id_ciphertext IS NULL OR national_id_hmac IS NOT NULL),
  FOREIGN KEY (tenant_id, lead_source_id) REFERENCES app.lead_sources(tenant_id, id),
  FOREIGN KEY (tenant_id, registered_by)  REFERENCES app.memberships(tenant_id, id),
  FOREIGN KEY (tenant_id, assigned_to)    REFERENCES app.memberships(tenant_id, id)
);
COMMENT ON TABLE app.customers IS 'Shop Customers + Services Clients and Leads (spec §6, §7). Walk-in sales have no customer row.';
-- Duplicate prevention: one live record per phone per tenant (ASSUMPTION A-05).
CREATE UNIQUE INDEX customers_phone_uq ON app.customers (tenant_id, phone) WHERE phone IS NOT NULL AND archived_at IS NULL;
CREATE UNIQUE INDEX customers_natid_uq ON app.customers (tenant_id, national_id_hmac) WHERE national_id_hmac IS NOT NULL AND archived_at IS NULL;
CREATE INDEX customers_name_trgm ON app.customers USING gin (full_name gin_trgm_ops);
CREATE INDEX customers_client_status_idx ON app.customers (tenant_id, client_status) WHERE client_status IS NOT NULL;
CREATE INDEX customers_assigned_idx ON app.customers (tenant_id, assigned_to) WHERE assigned_to IS NOT NULL;
CREATE INDEX customers_registered_idx ON app.customers (tenant_id, registered_by);

CREATE TABLE app.suppliers (
  id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id          uuid NOT NULL REFERENCES app.tenants(id),
  name               app.short_text NOT NULL,
  contact_person     text CHECK (length(contact_person) <= 200),
  phone              app.phone_e164,
  email              app.email,
  address            text CHECK (length(address) <= 500),
  tin                text CHECK (tin ~ '^[0-9]{3}-?[0-9]{3}-?[0-9]{3}$'),
  goods_supplied     text CHECK (length(goods_supplied) <= 1000),
  payment_terms_days smallint CHECK (payment_terms_days BETWEEN 0 AND 365),
  status             app.record_status NOT NULL DEFAULT 'active',
  created_at         timestamptz NOT NULL DEFAULT now(),
  created_by         uuid DEFAULT app.current_membership_id(),
  updated_at         timestamptz NOT NULL DEFAULT now(),
  updated_by         uuid,
  row_version        integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id)
);
CREATE UNIQUE INDEX suppliers_name_uq ON app.suppliers (tenant_id, lower(name));

CREATE TABLE app.contractors (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES app.tenants(id),
  name        app.short_text NOT NULL,
  trade       app.short_text NOT NULL,       -- printing, painting, carpentry...
  phone       app.phone_e164,
  email       app.email,
  tin         text CHECK (tin ~ '^[0-9]{3}-?[0-9]{3}-?[0-9]{3}$'),
  status      app.record_status NOT NULL DEFAULT 'active',
  created_at  timestamptz NOT NULL DEFAULT now(),
  created_by  uuid DEFAULT app.current_membership_id(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  updated_by  uuid,
  row_version integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id)
);
CREATE UNIQUE INDEX contractors_name_uq ON app.contractors (tenant_id, lower(name));
