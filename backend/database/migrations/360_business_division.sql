-- =====================================================================
-- 360_business_division.sql — Business A (shop) and Business B (services) per person.
--
--   app.membership_business_units  which businesses a person works for, one of them primary
--   app.membership_locations       optional branches they are limited to (none = no limit)
--   app.business_units             what each business prints on its receipts and invoices
--   app.employees.business_unit_id the business an employee's pay is posted to
--
-- Row security limits business-tagged records to the businesses of the request
-- (setting app.business_units). A transaction that does not set it is the system's own work
-- (sign-in, setup, background jobs) and is not limited, and neither are statements run by
-- triggers: a rule such as "a customer who owes cannot be archived" must see every debt.
--
-- Three tagged tables stay readable by everyone: app.locations (a branch is a place that records
-- point at; the application limits which branches a person may work in), app.financial_accounts (a
-- till or bank account is shared; the money moved through it is tagged) and app.payroll_runs (one
-- run pays people of both businesses; each person's pay is limited through their employee record
-- and posted to their business).
--
-- Nobody loses access: every existing person gets both businesses, A primary.
-- =====================================================================
SET search_path = app, public;
-- This file reads and writes business data; fail rather than silently skip rows.
SET LOCAL row_security = off;

-- ---------- Who works where ----------------------------------------------
CREATE TABLE app.membership_business_units (
  membership_id    uuid NOT NULL REFERENCES app.memberships(id) ON DELETE CASCADE,
  business_unit_id uuid NOT NULL REFERENCES app.business_units(id),
  is_primary       boolean NOT NULL DEFAULT false,
  created_at       timestamptz NOT NULL DEFAULT now(),
  created_by       uuid DEFAULT app.current_membership_id() REFERENCES app.memberships(id),
  PRIMARY KEY (membership_id, business_unit_id)
);
CREATE UNIQUE INDEX membership_business_units_one_primary
  ON app.membership_business_units (membership_id) WHERE is_primary;
CREATE INDEX membership_business_units_unit_idx ON app.membership_business_units (business_unit_id);
COMMENT ON TABLE app.membership_business_units IS
  'The businesses a person may work in, on top of role permissions. Admins always have all of them.';

CREATE TABLE app.membership_locations (
  membership_id uuid NOT NULL REFERENCES app.memberships(id) ON DELETE CASCADE,
  location_id   uuid NOT NULL REFERENCES app.locations(id),
  created_at    timestamptz NOT NULL DEFAULT now(),
  created_by    uuid DEFAULT app.current_membership_id() REFERENCES app.memberships(id),
  PRIMARY KEY (membership_id, location_id)
);
CREATE INDEX membership_locations_location_idx ON app.membership_locations (location_id);
COMMENT ON TABLE app.membership_locations IS
  'Branches a person is limited to. No rows means no limit beyond the branches of their businesses.';

-- Backfill: both businesses, the shop primary.
INSERT INTO app.membership_business_units (membership_id, business_unit_id, is_primary)
SELECT m.id, u.id, u.id = (SELECT p.id FROM app.business_units p ORDER BY (p.code = 'A') DESC, p.code LIMIT 1)
  FROM app.memberships m CROSS JOIN app.business_units u;

-- A new person starts with every business; the Admin narrows it from Team.
CREATE FUNCTION app.tg_membership_default_units() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
BEGIN
  INSERT INTO app.membership_business_units (membership_id, business_unit_id, is_primary)
  SELECT NEW.id, u.id, u.id = (SELECT p.id FROM app.business_units p ORDER BY (p.code = 'A') DESC, p.code LIMIT 1)
    FROM app.business_units u
  ON CONFLICT DO NOTHING;
  RETURN NULL;
END $$;
CREATE TRIGGER za_default_units AFTER INSERT ON app.memberships
  FOR EACH ROW EXECUTE FUNCTION app.tg_membership_default_units();

-- Checked at commit, so a change may replace the rows in any order.
CREATE FUNCTION app.tg_membership_scope_check() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE
  m uuid;
  ids uuid[];
BEGIN
  IF TG_TABLE_NAME = 'memberships' THEN
    ids := ARRAY[NEW.id];
  ELSIF TG_OP = 'DELETE' THEN
    ids := ARRAY[OLD.membership_id];
  ELSE
    ids := ARRAY[NEW.membership_id];
  END IF;
  FOREACH m IN ARRAY ids LOOP
    IF NOT EXISTS (SELECT 1 FROM app.memberships x WHERE x.id = m) THEN
      CONTINUE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM app.membership_business_units b WHERE b.membership_id = m AND b.is_primary) THEN
      RAISE EXCEPTION 'a person needs at least one business, one of them primary' USING ERRCODE = 'BM001';
    END IF;
    IF EXISTS (SELECT 1 FROM app.membership_roles mr JOIN app.roles r ON r.id = mr.role_id AND r.code = 'admin'
                WHERE mr.membership_id = m)
       AND EXISTS (SELECT 1 FROM app.business_units u
                    WHERE NOT EXISTS (SELECT 1 FROM app.membership_business_units b
                                       WHERE b.membership_id = m AND b.business_unit_id = u.id)) THEN
      RAISE EXCEPTION 'an Admin always has every business' USING ERRCODE = 'BM001';
    END IF;
    IF EXISTS (SELECT 1 FROM app.membership_locations ml JOIN app.locations l ON l.id = ml.location_id
                WHERE ml.membership_id = m
                  AND NOT EXISTS (SELECT 1 FROM app.membership_business_units b
                                   WHERE b.membership_id = m AND b.business_unit_id = l.business_unit_id)) THEN
      RAISE EXCEPTION 'a branch belongs to a business this person does not have' USING ERRCODE = 'BM001';
    END IF;
    IF EXISTS (SELECT 1 FROM app.membership_locations ml WHERE ml.membership_id = m)
       AND EXISTS (SELECT 1 FROM app.memberships x WHERE x.id = m AND x.default_location_id IS NOT NULL
                    AND NOT EXISTS (SELECT 1 FROM app.membership_locations ml
                                     WHERE ml.membership_id = m AND ml.location_id = x.default_location_id)) THEN
      RAISE EXCEPTION 'the default branch must be one of the allowed branches' USING ERRCODE = 'BM001';
    END IF;
  END LOOP;
  RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER zz_scope AFTER INSERT OR UPDATE OR DELETE ON app.membership_business_units
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION app.tg_membership_scope_check();
CREATE CONSTRAINT TRIGGER zz_scope AFTER INSERT OR UPDATE ON app.membership_locations
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION app.tg_membership_scope_check();
CREATE CONSTRAINT TRIGGER zz_scope AFTER UPDATE OF default_location_id ON app.memberships
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION app.tg_membership_scope_check();
CREATE CONSTRAINT TRIGGER zz_scope AFTER INSERT OR UPDATE ON app.membership_roles
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION app.tg_membership_scope_check();

-- Becoming an Admin brings every business with it.
CREATE FUNCTION app.tg_admin_units() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
BEGIN
  IF EXISTS (SELECT 1 FROM app.roles r WHERE r.id = NEW.role_id AND r.code = 'admin') THEN
    INSERT INTO app.membership_business_units (membership_id, business_unit_id)
    SELECT NEW.membership_id, u.id FROM app.business_units u
    ON CONFLICT DO NOTHING;
    DELETE FROM app.membership_locations WHERE membership_id = NEW.membership_id;
  END IF;
  RETURN NULL;
END $$;
CREATE TRIGGER za_admin_units AFTER INSERT OR UPDATE ON app.membership_roles
  FOR EACH ROW EXECUTE FUNCTION app.tg_admin_units();

CREATE TRIGGER zz_audit AFTER INSERT OR UPDATE OR DELETE ON app.membership_business_units
  FOR EACH ROW EXECUTE FUNCTION audit.tg_capture();
CREATE TRIGGER zz_audit AFTER INSERT OR UPDATE OR DELETE ON app.membership_locations
  FOR EACH ROW EXECUTE FUNCTION audit.tg_capture();

GRANT SELECT, INSERT, UPDATE, DELETE ON app.membership_business_units, app.membership_locations TO bms_app;
GRANT SELECT ON app.membership_business_units, app.membership_locations TO bms_worker, bms_readonly;

-- ---------- What each business prints ------------------------------------
-- Empty fields fall back to the business profile and settings.
ALTER TABLE app.business_units
  ADD COLUMN display_name   app.short_text,
  ADD COLUMN logo_file_id   uuid REFERENCES app.files(id),
  ADD COLUMN phone          app.phone_e164,
  ADD COLUMN address        text CHECK (length(address) <= 500),
  ADD COLUMN tin            text CHECK (length(tin) <= 40),
  ADD COLUMN receipt_header text CHECK (length(receipt_header) <= 500),
  ADD COLUMN receipt_footer text CHECK (length(receipt_footer) <= 500);
GRANT UPDATE (display_name, logo_file_id, phone, address, tin, receipt_header, receipt_footer, updated_at, updated_by, row_version)
  ON app.business_units TO bms_app;

-- ---------- Employees ----------------------------------------------------
ALTER TABLE app.employees ADD COLUMN business_unit_id uuid REFERENCES app.business_units(id);
-- Filling the new column is not an edit of the person's record: leave when and by whom it was changed.
ALTER TABLE app.employees DISABLE TRIGGER zz_touch;
UPDATE app.employees e
   SET business_unit_id = coalesce(
         (SELECT b.business_unit_id FROM app.membership_business_units b
           WHERE b.membership_id = e.membership_id AND b.is_primary),
         (SELECT u.id FROM app.business_units u ORDER BY (u.code = 'A') DESC, u.code LIMIT 1));
ALTER TABLE app.employees ENABLE TRIGGER zz_touch;
ALTER TABLE app.employees ALTER COLUMN business_unit_id SET NOT NULL;
CREATE INDEX employees_business_unit_idx ON app.employees (business_unit_id);
GRANT UPDATE (business_unit_id) ON app.employees TO bms_app;

-- An employee created without a business takes their login's primary one, otherwise the creator's.
CREATE FUNCTION app.tg_employee_default_unit() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
BEGIN
  IF NEW.business_unit_id IS NULL THEN
    NEW.business_unit_id := coalesce(
      (SELECT b.business_unit_id FROM app.membership_business_units b
        WHERE b.membership_id = coalesce(NEW.membership_id, app.current_membership_id()) AND b.is_primary),
      (SELECT u.id FROM app.business_units u ORDER BY (u.code = 'A') DESC, u.code LIMIT 1));
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER a_default_unit BEFORE INSERT ON app.employees
  FOR EACH ROW EXECUTE FUNCTION app.tg_employee_default_unit();

-- ---------- Pay goes to each employee's business --------------------------
CREATE OR REPLACE FUNCTION app.tg_payroll_post() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
BEGIN
  IF NEW.status = 'paid' AND OLD.status = 'approved' THEN
    INSERT INTO app.financial_transactions (account_id, direction, amount, txn_type, business_unit_id,
           location_id, business_date, description, payroll_line_id)
    SELECT NEW.account_id, 'out', l.net_pay, 'salary', e.business_unit_id, NEW.location_id,
           NEW.paid_business_date, format('Salary %s-%s', NEW.period_year, lpad(NEW.period_month::text, 2, '0')), l.id
      FROM app.payroll_lines l JOIN app.employees e ON e.id = l.employee_id
     WHERE l.payroll_run_id = NEW.id AND l.net_pay > 0;
    UPDATE app.payroll_lines SET paid_at = NEW.paid_at WHERE payroll_run_id = NEW.id;
    UPDATE app.employee_advances a SET amount_repaid = a.amount_repaid + r.total,
           status = CASE WHEN a.amount_repaid + r.total = a.principal THEN 'settled'::app.advance_status ELSE a.status END
      FROM (SELECT ar.advance_id, sum(ar.amount) total FROM app.advance_repayments ar
              JOIN app.payroll_lines l ON l.id = ar.payroll_line_id
             WHERE l.payroll_run_id = NEW.id GROUP BY ar.advance_id) r
     WHERE a.id = r.advance_id;
  END IF;
  RETURN NULL;
END $$;

CREATE OR REPLACE VIEW app.v_profit_and_loss_monthly WITH (security_invoker = true) AS
WITH lines AS (
  SELECT s.business_unit_id, date_trunc('month', s.business_date)::date AS month,
         'revenue'::text AS kind, s.total AS amount
    FROM app.sales s WHERE s.status = 'completed'
  UNION ALL
  SELECT s.business_unit_id, date_trunc('month', s.business_date)::date, 'cogs',
         round(sum(sl.quantity * coalesce(sl.unit_cost, 0)), 2)
    FROM app.sales s JOIN app.sale_lines sl ON sl.sale_id = s.id
   WHERE s.status = 'completed' GROUP BY s.business_unit_id, s.business_date
  UNION ALL
  SELECT s.business_unit_id, date_trunc('month', r.business_date)::date, 'returns',
         -round(sum(rl.quantity * sl.line_total / sl.quantity), 2)
    FROM app.sale_returns r JOIN app.sales s ON s.id = r.sale_id
    JOIN app.sale_return_lines rl ON rl.sale_return_id = r.id
    JOIN app.sale_lines sl ON sl.id = rl.sale_line_id
   WHERE r.status = 'completed' GROUP BY s.business_unit_id, r.business_date
  UNION ALL
  SELECT s.business_unit_id, date_trunc('month', r.business_date)::date, 'cogs',
         -round(sum(rl.quantity * coalesce(sl.unit_cost, 0)), 2)
    FROM app.sale_returns r JOIN app.sales s ON s.id = r.sale_id
    JOIN app.sale_return_lines rl ON rl.sale_return_id = r.id AND rl.restock
    JOIN app.sale_lines sl ON sl.id = rl.sale_line_id
   WHERE r.status = 'completed' GROUP BY s.business_unit_id, r.business_date
  UNION ALL
  SELECT i.business_unit_id, date_trunc('month', i.issue_date)::date, 'revenue', i.total
    FROM app.invoices i WHERE i.status IN ('issued', 'partially_paid', 'paid')
  UNION ALL
  SELECT e.business_unit_id, date_trunc('month', e.expense_date)::date, 'expenses', e.amount
    FROM app.expenses e WHERE e.status IN ('approved', 'paid')
  UNION ALL
  SELECT b.business_unit_id, date_trunc('month', b.bill_date)::date, 'expenses', b.amount
    FROM app.contractor_bills b WHERE b.status <> 'void'
  UNION ALL
  SELECT e.business_unit_id, make_date(r.period_year, r.period_month, 1), 'payroll', l.gross_pay
    FROM app.payroll_runs r JOIN app.payroll_lines l ON l.payroll_run_id = r.id
    JOIN app.employees e ON e.id = l.employee_id
   WHERE r.status IN ('approved', 'paid')
)
SELECT business_unit_id, month,
       sum(amount) FILTER (WHERE kind IN ('revenue', 'returns')) AS net_revenue,
       sum(amount) FILTER (WHERE kind = 'cogs')     AS cost_of_goods,
       sum(amount) FILTER (WHERE kind = 'expenses') AS operating_expenses,
       sum(amount) FILTER (WHERE kind = 'payroll')  AS payroll,
       coalesce(sum(amount) FILTER (WHERE kind IN ('revenue', 'returns')), 0)
         - coalesce(sum(amount) FILTER (WHERE kind IN ('cogs', 'expenses', 'payroll')), 0) AS net_profit
  FROM lines GROUP BY business_unit_id, month;

-- ---------- Row security -------------------------------------------------
-- NULL means the transaction did not limit itself: system work.
CREATE FUNCTION app.current_business_units() RETURNS uuid[]
LANGUAGE sql STABLE AS $$
  SELECT nullif(current_setting('app.business_units', true), '')::uuid[]
$$;

CREATE FUNCTION app.can_access_business(p_unit uuid) RETURNS boolean
LANGUAGE sql STABLE AS $$
  SELECT p_unit IS NULL
      OR current_user IN ('bms_owner', 'bms_worker')
      OR app.current_business_units() IS NULL
      OR p_unit = ANY (app.current_business_units())
      OR pg_trigger_depth() > 0
$$;
GRANT EXECUTE ON FUNCTION app.current_business_units(), app.can_access_business(uuid) TO bms_app, bms_worker, bms_readonly;

DO $$
DECLARE t text;
BEGIN
  FOR t IN
    SELECT c.table_name FROM information_schema.columns c
      JOIN information_schema.tables x USING (table_schema, table_name)
     WHERE c.table_schema = 'app' AND c.column_name = 'business_unit_id' AND x.table_type = 'BASE TABLE'
       AND c.table_name NOT IN ('locations', 'financial_accounts', 'payroll_runs', 'membership_business_units')
     ORDER BY c.table_name
  LOOP
    -- A restrictive policy shows nothing by itself.
    IF NOT EXISTS (SELECT 1 FROM pg_policies WHERE schemaname = 'app' AND tablename = t AND permissive = 'PERMISSIVE') THEN
      EXECUTE format('CREATE POLICY app_access ON app.%I USING (true) WITH CHECK (true)', t);
    END IF;
    EXECUTE format('ALTER TABLE app.%I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY business_scope ON app.%I AS RESTRICTIVE
                      USING (app.can_access_business(business_unit_id))
                      WITH CHECK (app.can_access_business(business_unit_id))', t);
  END LOOP;
END $$;

-- ---------- Nothing may be left behind -----------------------------------
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM app.memberships m WHERE (SELECT count(*) FROM app.membership_business_units b WHERE b.membership_id = m.id)
                <> (SELECT count(*) FROM app.business_units)) THEN
    RAISE EXCEPTION 'every existing person must keep access to every business';
  END IF;
  IF EXISTS (SELECT 1 FROM information_schema.columns c
              JOIN information_schema.tables x USING (table_schema, table_name)
             WHERE c.table_schema = 'app' AND c.column_name = 'business_unit_id' AND x.table_type = 'BASE TABLE'
               AND c.table_name NOT IN ('locations', 'financial_accounts', 'payroll_runs', 'membership_business_units')
               AND NOT EXISTS (SELECT 1 FROM pg_policies p WHERE p.schemaname = 'app' AND p.tablename = c.table_name
                                AND p.policyname = 'business_scope')) THEN
    RAISE EXCEPTION 'a business-tagged table has no business rule';
  END IF;
END $$;
