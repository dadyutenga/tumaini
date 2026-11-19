-- =====================================================================
-- 040_catalog_hr_core.sql — product catalogue, serial units, employees,
-- compensation, attendance, leave
-- =====================================================================
SET search_path = app, public;

-- ---------- Product catalogue ----------------------------------------
CREATE TABLE app.product_categories (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES app.tenants(id),
  parent_id   uuid,
  name        app.short_text NOT NULL,
  status      app.record_status NOT NULL DEFAULT 'active',
  created_at  timestamptz NOT NULL DEFAULT now(),
  created_by  uuid DEFAULT app.current_membership_id(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  updated_by  uuid,
  row_version integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id),
  UNIQUE NULLS NOT DISTINCT (tenant_id, parent_id, name),
  CHECK (parent_id IS DISTINCT FROM id),
  FOREIGN KEY (tenant_id, parent_id) REFERENCES app.product_categories(tenant_id, id)
);

CREATE TABLE app.products (
  id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id            uuid NOT NULL REFERENCES app.tenants(id),
  business_unit_id     uuid NOT NULL,
  category_id          uuid,
  sku                  text CHECK (sku ~ '^[A-Za-z0-9._-]{1,40}$'),
  barcode              text CHECK (barcode ~ '^[0-9A-Za-z-]{4,48}$'),
  name                 app.short_text NOT NULL,
  description          text CHECK (length(description) <= 2000),
  unit_of_measure      text NOT NULL DEFAULT 'pcs' CHECK (unit_of_measure ~ '^[a-z]{1,10}$'),
  selling_price        app.money_nn NOT NULL,
  buying_price         app.money_nn NOT NULL DEFAULT 0,  -- last agreed purchase cost (management view)
  average_cost         numeric(18,4) NOT NULL DEFAULT 0 CHECK (average_cost >= 0), -- moving weighted average, maintained by stock trigger
  tax_rate_id          uuid,
  min_stock_level      numeric(14,3) CHECK (min_stock_level >= 0), -- null = tenant default
  warranty_months      smallint NOT NULL DEFAULT 0 CHECK (warranty_months BETWEEN 0 AND 120),
  tracks_serial        boolean NOT NULL DEFAULT false,
  allow_negative_stock boolean,                                    -- null = tenant default
  photo_file_id        uuid,
  status               app.record_status NOT NULL DEFAULT 'active',
  created_at           timestamptz NOT NULL DEFAULT now(),
  created_by           uuid DEFAULT app.current_membership_id(),
  updated_at           timestamptz NOT NULL DEFAULT now(),
  updated_by           uuid,
  row_version          integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id),
  CHECK (NOT tracks_serial OR unit_of_measure = 'pcs'),
  FOREIGN KEY (tenant_id, business_unit_id) REFERENCES app.business_units(tenant_id, id),
  FOREIGN KEY (tenant_id, category_id)      REFERENCES app.product_categories(tenant_id, id),
  FOREIGN KEY (tenant_id, tax_rate_id)      REFERENCES app.tax_rates(tenant_id, id),
  FOREIGN KEY (tenant_id, photo_file_id)    REFERENCES app.files(tenant_id, id)
);
COMMENT ON COLUMN app.products.buying_price IS 'CONFIDENTIAL: hidden from Staff (spec §2A). Staff read app.v_staff_catalog, never this table directly.';
CREATE UNIQUE INDEX products_barcode_uq ON app.products (tenant_id, barcode) WHERE barcode IS NOT NULL;
CREATE UNIQUE INDEX products_sku_uq     ON app.products (tenant_id, sku)     WHERE sku IS NOT NULL;
CREATE INDEX products_name_trgm  ON app.products USING gin (name gin_trgm_ops);
CREATE INDEX products_category_idx ON app.products (tenant_id, category_id) WHERE status = 'active';

-- Price changes are a "risky action" (spec §1) and keep history for profit-per-unit.
CREATE TABLE app.product_price_changes (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  product_id          uuid NOT NULL,
  old_selling_price   app.money_nn NOT NULL,
  new_selling_price   app.money_nn NOT NULL,
  old_buying_price    app.money_nn NOT NULL,
  new_buying_price    app.money_nn NOT NULL,
  reason              text NOT NULL CHECK (length(btrim(reason)) >= 3),
  effective_at        timestamptz NOT NULL DEFAULT now(),
  created_at          timestamptz NOT NULL DEFAULT now(),
  created_by          uuid DEFAULT app.current_membership_id(),
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id, product_id) REFERENCES app.products(tenant_id, id)
);
CREATE INDEX product_price_changes_product_idx ON app.product_price_changes (tenant_id, product_id, effective_at DESC);

-- Serialised units (phones etc.): serial number / IMEI tracking & warranty.
CREATE TYPE app.unit_status AS ENUM ('in_stock', 'sold', 'returned', 'defective', 'written_off', 'in_transit');
CREATE TABLE app.product_units (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  product_id          uuid NOT NULL,
  location_id         uuid NOT NULL,
  serial_no           text NOT NULL CHECK (serial_no ~ '^[A-Za-z0-9-]{3,64}$'),
  imei                text CHECK (imei ~ '^[0-9]{15}$'),
  status              app.unit_status NOT NULL DEFAULT 'in_stock',
  sold_at             timestamptz,
  warranty_expires_on date,
  created_at          timestamptz NOT NULL DEFAULT now(),
  created_by          uuid DEFAULT app.current_membership_id(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  updated_by          uuid,
  row_version         integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, product_id, serial_no),
  CHECK (status <> 'sold' OR sold_at IS NOT NULL),
  FOREIGN KEY (tenant_id, product_id)  REFERENCES app.products(tenant_id, id),
  FOREIGN KEY (tenant_id, location_id) REFERENCES app.locations(tenant_id, id)
);
CREATE UNIQUE INDEX product_units_imei_uq ON app.product_units (tenant_id, imei) WHERE imei IS NOT NULL;
CREATE INDEX product_units_available_idx ON app.product_units (tenant_id, product_id, location_id) WHERE status = 'in_stock';

-- ---------- Employees -------------------------------------------------
CREATE TYPE app.employee_status AS ENUM ('active', 'suspended', 'terminated');

CREATE TABLE app.employees (
  id                     uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id              uuid NOT NULL REFERENCES app.tenants(id),
  employee_no            text NOT NULL,
  membership_id          uuid,            -- linked login (optional)
  full_name              app.short_text NOT NULL,
  phone                  app.phone_e164,
  email                  app.email,
  position               app.short_text NOT NULL,
  location_id            uuid,
  start_date             date NOT NULL,
  end_date               date,
  status                 app.employee_status NOT NULL DEFAULT 'active',
  national_id_ciphertext bytea,
  national_id_key_id     text,
  national_id_last4      text CHECK (national_id_last4 ~ '^[A-Z0-9]{4}$'),
  photo_file_id          uuid,
  emergency_contact_name  text CHECK (length(emergency_contact_name) <= 200),
  emergency_contact_phone app.phone_e164,
  created_at             timestamptz NOT NULL DEFAULT now(),
  created_by             uuid DEFAULT app.current_membership_id(),
  updated_at             timestamptz NOT NULL DEFAULT now(),
  updated_by             uuid,
  row_version            integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, employee_no),
  UNIQUE (tenant_id, membership_id),
  CHECK (end_date IS NULL OR end_date >= start_date),
  CHECK (status <> 'terminated' OR end_date IS NOT NULL),
  CHECK ((national_id_ciphertext IS NULL) = (national_id_key_id IS NULL)),
  FOREIGN KEY (tenant_id, membership_id) REFERENCES app.memberships(tenant_id, id),
  FOREIGN KEY (tenant_id, location_id)   REFERENCES app.locations(tenant_id, id),
  FOREIGN KEY (tenant_id, photo_file_id) REFERENCES app.files(tenant_id, id)
);
COMMENT ON TABLE app.employees IS 'HR master record. Salaries are NOT here: see app.employee_compensation (restricted RLS) so Managers can manage attendance/leave without seeing pay.';

CREATE TYPE app.pay_method AS ENUM ('bank', 'mobile_money', 'cash');
CREATE TABLE app.employee_compensation (
  id                       uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id                uuid NOT NULL REFERENCES app.tenants(id),
  employee_id              uuid NOT NULL,
  effective_from           date NOT NULL,
  effective_to             date,
  basic_salary             app.money_nn NOT NULL,
  pay_method               app.pay_method NOT NULL,
  payout_account_ciphertext bytea,     -- bank account / mobile money number, envelope-encrypted
  payout_key_id            text,
  payout_account_last4     text CHECK (payout_account_last4 ~ '^[0-9]{4}$'),
  payout_provider          text CHECK (length(payout_provider) <= 60),
  reason                   text NOT NULL CHECK (length(btrim(reason)) >= 3),
  created_at               timestamptz NOT NULL DEFAULT now(),
  created_by               uuid DEFAULT app.current_membership_id(),
  UNIQUE (tenant_id, id),
  CHECK (effective_to IS NULL OR effective_to > effective_from),
  CHECK (pay_method = 'cash' OR payout_account_ciphertext IS NOT NULL),
  CHECK ((payout_account_ciphertext IS NULL) = (payout_key_id IS NULL)),
  FOREIGN KEY (tenant_id, employee_id) REFERENCES app.employees(tenant_id, id),
  CONSTRAINT employee_compensation_no_overlap EXCLUDE USING gist (
    tenant_id WITH =, employee_id WITH =,
    daterange(effective_from, effective_to, '[)') WITH &&)
);
COMMENT ON TABLE app.employee_compensation IS 'RESTRICTED. Salary history; new row per change (no in-place edits). Visible only with hr.payroll.view or to the employee themself.';

CREATE TABLE app.employee_allowances (
  id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id      uuid NOT NULL REFERENCES app.tenants(id),
  employee_id    uuid NOT NULL,
  name           app.short_text NOT NULL,
  amount         app.money_pos NOT NULL,
  effective_from date NOT NULL,
  effective_to   date,
  created_at     timestamptz NOT NULL DEFAULT now(),
  created_by     uuid DEFAULT app.current_membership_id(),
  updated_at     timestamptz NOT NULL DEFAULT now(),
  updated_by     uuid,
  row_version    integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id),
  CHECK (effective_to IS NULL OR effective_to > effective_from),
  FOREIGN KEY (tenant_id, employee_id) REFERENCES app.employees(tenant_id, id)
);

-- ---------- Attendance ----------------------------------------------
CREATE TYPE app.attendance_status AS ENUM ('present', 'late', 'absent', 'on_leave', 'holiday', 'half_day');
CREATE TYPE app.attendance_source AS ENUM ('self', 'manager', 'system');

CREATE TABLE app.attendance_records (
  id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id        uuid NOT NULL REFERENCES app.tenants(id),
  employee_id      uuid NOT NULL,
  work_date        date NOT NULL,
  clock_in_at      timestamptz,
  clock_out_at     timestamptz,
  status           app.attendance_status NOT NULL,
  source           app.attendance_source NOT NULL DEFAULT 'self',
  minutes_late     integer NOT NULL DEFAULT 0 CHECK (minutes_late >= 0),
  overtime_minutes integer NOT NULL DEFAULT 0 CHECK (overtime_minutes >= 0),
  clock_in_ip      inet,
  is_corrected     boolean NOT NULL DEFAULT false,
  created_at       timestamptz NOT NULL DEFAULT now(),
  created_by       uuid DEFAULT app.current_membership_id(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  updated_by       uuid,
  row_version      integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, employee_id, work_date),
  CHECK (clock_out_at IS NULL OR (clock_in_at IS NOT NULL AND clock_out_at > clock_in_at)),
  CHECK (status NOT IN ('present', 'late', 'half_day') OR clock_in_at IS NOT NULL),
  FOREIGN KEY (tenant_id, employee_id) REFERENCES app.employees(tenant_id, id)
);
CREATE INDEX attendance_date_idx ON app.attendance_records (tenant_id, work_date);

CREATE TABLE app.attendance_corrections (
  id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id        uuid NOT NULL REFERENCES app.tenants(id),
  attendance_id    uuid NOT NULL,
  old_clock_in_at  timestamptz,
  new_clock_in_at  timestamptz,
  old_clock_out_at timestamptz,
  new_clock_out_at timestamptz,
  old_status       app.attendance_status NOT NULL,
  new_status       app.attendance_status NOT NULL,
  reason           text NOT NULL CHECK (length(btrim(reason)) >= 5),
  created_at       timestamptz NOT NULL DEFAULT now(),
  created_by       uuid NOT NULL DEFAULT app.current_membership_id(),
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id, attendance_id) REFERENCES app.attendance_records(tenant_id, id)
);
COMMENT ON TABLE app.attendance_corrections IS 'Append-only. Spec §9: secretary corrects a record with a reason that is logged.';

-- ---------- Leave ---------------------------------------------------
CREATE TABLE app.leave_types (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES app.tenants(id),
  name        app.short_text NOT NULL,
  is_paid     boolean NOT NULL DEFAULT true,
  annual_days numeric(5,1) CHECK (annual_days >= 0),  -- entitlement: business decision (D-09)
  status      app.record_status NOT NULL DEFAULT 'active',
  created_at  timestamptz NOT NULL DEFAULT now(),
  created_by  uuid DEFAULT app.current_membership_id(),
  UNIQUE (tenant_id, name),
  UNIQUE (tenant_id, id)
);

CREATE TYPE app.leave_status AS ENUM ('requested', 'approved', 'rejected', 'cancelled');
CREATE TABLE app.leave_requests (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id     uuid NOT NULL REFERENCES app.tenants(id),
  employee_id   uuid NOT NULL,
  leave_type_id uuid NOT NULL,
  start_date    date NOT NULL,
  end_date      date NOT NULL,
  days          numeric(5,1) NOT NULL CHECK (days > 0),
  reason        text CHECK (length(reason) <= 1000),  -- free text; must not require medical detail
  status        app.leave_status NOT NULL DEFAULT 'requested',
  decided_by    uuid,
  decided_at    timestamptz,
  decision_note text CHECK (length(decision_note) <= 1000),
  created_at    timestamptz NOT NULL DEFAULT now(),
  created_by    uuid DEFAULT app.current_membership_id(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  updated_by    uuid,
  row_version   integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id),
  CHECK (end_date >= start_date),
  CHECK (status IN ('requested', 'cancelled') OR (decided_by IS NOT NULL AND decided_at IS NOT NULL)),
  FOREIGN KEY (tenant_id, employee_id)   REFERENCES app.employees(tenant_id, id),
  FOREIGN KEY (tenant_id, leave_type_id) REFERENCES app.leave_types(tenant_id, id),
  FOREIGN KEY (tenant_id, decided_by)    REFERENCES app.memberships(tenant_id, id),
  CONSTRAINT leave_no_overlap EXCLUDE USING gist (
    tenant_id WITH =, employee_id WITH =,
    daterange(start_date, end_date, '[]') WITH &&)
    WHERE (status IN ('requested', 'approved'))
);
