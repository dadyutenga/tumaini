-- =====================================================================
-- 340_remove_multitenancy.sql — one business, with branches and business units.
--
-- Forward only: 000-330 are never edited. The file runs inside the migrator's transaction, so it
-- applies completely or not at all. It aborts if app.tenants holds more than one business.
--
--   app.tenants               -> app.business_profile (one row): legal name, time zone, currency,
--                               default language and status; then dropped
--   app.tenant_settings       -> app.business_settings (one row): the name customers see, contact
--                               details, tax numbers, logo and settings
--   app.tenant_setting_values -> app.setting_values
--   audit.audit_log.tenant_id -> audit.audit_log.legacy_scope. Row hashes written before this
--     migration cover that value, so it is renamed, not dropped; new rows leave it NULL.
--
-- Every constraint and index that included tenant_id is rebuilt without it, from its own
-- definition, so uniqueness, foreign keys and partial indexes survive. Every function that
-- mentioned a tenant is redefined below. A verification block at the end fails the migration if
-- anything was lost.
-- =====================================================================
SET search_path = app, public;

-- ---------- Let the migration see the data ------------------------------
-- Forced row security also restricts the table owner, which runs this file. Without this the guard
-- below would count no businesses and nothing would be copied, silently. With row_security off, any
-- statement row security would still filter fails instead. The security section further down sets
-- the final state of every table.
DO $$
DECLARE r record;
BEGIN
  FOR r IN SELECT n.nspname, c.relname FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
            WHERE n.nspname IN ('app', 'iam', 'audit') AND c.relforcerowsecurity LOOP
    EXECUTE format('ALTER TABLE %I.%I NO FORCE ROW LEVEL SECURITY', r.nspname, r.relname);
  END LOOP;
END $$;
SET LOCAL row_security = off;

DO $$
BEGIN
  IF (SELECT count(*) FROM app.tenants) > 1 THEN
    RAISE EXCEPTION 'multi_tenant_data_present';
  END IF;
END $$;

-- ---------- The one business --------------------------------------------
CREATE TABLE app.business_profile (
  id               boolean PRIMARY KEY DEFAULT true CHECK (id),
  name             app.short_text NOT NULL,
  timezone         text NOT NULL DEFAULT 'Africa/Dar_es_Salaam',
  default_currency app.currency NOT NULL DEFAULT 'TZS',
  default_locale   text NOT NULL DEFAULT 'sw' CHECK (default_locale IN ('sw', 'en')),
  status           text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'suspended', 'closed')),
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  updated_by       uuid,
  row_version      integer NOT NULL DEFAULT 1
);

INSERT INTO app.business_profile (name, timezone, default_currency, default_locale, status, created_at)
SELECT legal_name, timezone, default_currency, default_locale, status::text, created_at
  FROM app.tenants;

ALTER TABLE audit.audit_log RENAME COLUMN tenant_id TO legacy_scope;
COMMENT ON COLUMN audit.audit_log.legacy_scope IS
  'Value covered by the row hash of entries written before the single-business migration. NULL for new rows.';

-- ---------- Remember what depends on tenant_id --------------------------
CREATE TEMP TABLE tenant_constraints ON COMMIT DROP AS
SELECT n.nspname AS table_schema, cl.relname AS table_name, c.conname, c.contype,
       pg_get_constraintdef(c.oid) AS definition
  FROM pg_constraint c
  JOIN pg_class cl ON cl.oid = c.conrelid
  JOIN pg_namespace n ON n.oid = cl.relnamespace
 WHERE n.nspname IN ('app', 'iam', 'audit')
   AND c.conparentid = 0 AND c.coninhcount = 0
   AND pg_get_constraintdef(c.oid) ~ '\mtenant_id\M';

-- Indexes that stand on their own (constraint indexes come back with their constraint).
CREATE TEMP TABLE tenant_indexes ON COMMIT DROP AS
SELECT n.nspname AS table_schema, ci.relname AS index_name, ix.indisunique,
       pg_get_indexdef(ix.indexrelid) AS definition
  FROM pg_index ix
  JOIN pg_class ci ON ci.oid = ix.indexrelid
  JOIN pg_namespace n ON n.oid = ci.relnamespace
 WHERE n.nspname IN ('app', 'iam', 'audit')
   AND NOT EXISTS (SELECT 1 FROM pg_constraint k WHERE k.conindid = ix.indexrelid)
   AND NOT EXISTS (SELECT 1 FROM pg_inherits i WHERE i.inhrelid = ix.indexrelid)
   AND pg_get_indexdef(ix.indexrelid) ~ '\m(tenant_id|legacy_scope)\M';

-- ---------- Views, policies and triggers that name the column -----------
DROP VIEW IF EXISTS app.v_staff_catalog, app.v_stock_valuation, app.v_receivables, app.v_payables,
  app.v_project_financials, app.v_design_job_financials, app.v_pending_approvals,
  app.v_daily_takings, app.v_profit_and_loss_monthly;

DO $$
DECLARE r record;
BEGIN
  FOR r IN
    SELECT schemaname, tablename, policyname FROM pg_policies
     WHERE schemaname IN ('app', 'iam', 'audit')
       AND (policyname IN ('tenant_isolation', 'tenant_self', 'membership_visibility', 'audit_read', 'worker_all', 'worker_read')
            OR coalesce(qual, '') || coalesce(with_check, '') ~ '\mtenant_id\M|current_tenant_id')
  LOOP
    EXECUTE format('DROP POLICY %I ON %I.%I', r.policyname, r.schemaname, r.tablename);
  END LOOP;

  FOR r IN
    SELECT n.nspname, c.relname, t.tgname
      FROM pg_trigger t
      JOIN pg_class c ON c.oid = t.tgrelid
      JOIN pg_namespace n ON n.oid = c.relnamespace
     WHERE n.nspname IN ('app', 'iam', 'audit') AND NOT t.tgisinternal AND t.tgparentid = 0
       AND pg_get_triggerdef(t.oid) ~ '\mtenant_id\M'
  LOOP
    EXECUTE format('DROP TRIGGER %I ON %I.%I', r.tgname, r.nspname, r.relname);
  END LOOP;

  -- Foreign keys first: they lean on the unique constraints.
  FOR r IN SELECT * FROM tenant_constraints ORDER BY (contype <> 'f'), table_schema, table_name, conname LOOP
    IF r.contype NOT IN ('f', 'u', 'p', 'c', 'x') THEN
      RAISE EXCEPTION 'unexpected constraint type % on %.%', r.contype, r.table_schema, r.table_name;
    END IF;
    EXECUTE format('ALTER TABLE %I.%I DROP CONSTRAINT %I', r.table_schema, r.table_name, r.conname);
  END LOOP;

  FOR r IN SELECT * FROM tenant_indexes LOOP
    EXECUTE format('DROP INDEX IF EXISTS %I.%I', r.table_schema, r.index_name);
  END LOOP;

  FOR r IN
    SELECT n.nspname, c.relname
      FROM pg_attribute a
      JOIN pg_class c ON c.oid = a.attrelid
      JOIN pg_namespace n ON n.oid = c.relnamespace
     WHERE n.nspname IN ('app', 'iam', 'audit') AND a.attname = 'tenant_id' AND NOT a.attisdropped
       AND c.relkind IN ('r', 'p') AND NOT c.relispartition
     ORDER BY 1, 2
  LOOP
    EXECUTE format('ALTER TABLE %I.%I DROP COLUMN tenant_id', r.nspname, r.relname);
  END LOOP;
END $$;

-- ---------- Rebuild constraints and indexes without the column ----------
DO $$
DECLARE r record; def text;
BEGIN
  -- Primary keys and unique constraints (NULLS NOT DISTINCT and the rest are kept).
  FOR r IN SELECT * FROM tenant_constraints WHERE contype IN ('p', 'u') ORDER BY table_schema, table_name, conname LOOP
    CONTINUE WHEN r.definition ~ '\(tenant_id\)';  -- one row per business: handled below
    def := regexp_replace(r.definition, '\mtenant_id, |, tenant_id\M', '', 'g');
    EXECUTE format('ALTER TABLE %I.%I ADD CONSTRAINT %I %s', r.table_schema, r.table_name, r.conname, def);
  END LOOP;

  FOR r IN SELECT * FROM tenant_indexes ORDER BY table_schema, index_name LOOP
    def := replace(r.definition, ' ON ONLY ', ' ON ');
    IF def ~ '\((tenant_id|legacy_scope)\)' THEN
      CONTINUE WHEN NOT r.indisunique;
      -- "One per business", such as the single default location.
      def := regexp_replace(def, '\((tenant_id|legacy_scope)\)', '((true))');
    END IF;
    def := regexp_replace(def, '\m(tenant_id|legacy_scope), |, (tenant_id|legacy_scope)\M', '', 'g');
    EXECUTE def;
  END LOOP;

  -- Foreign keys last, once everything they reference is unique again.
  FOR r IN SELECT * FROM tenant_constraints WHERE contype = 'f' ORDER BY table_schema, table_name, conname LOOP
    CONTINUE WHEN r.definition ~ 'FOREIGN KEY \(tenant_id\)';  -- pointed at app.tenants
    def := regexp_replace(r.definition, '\mtenant_id, |, tenant_id\M', '', 'g');
    EXECUTE format('ALTER TABLE %I.%I ADD CONSTRAINT %I %s', r.table_schema, r.table_name, r.conname, def);
  END LOOP;
END $$;

ALTER TABLE app.employee_compensation ADD CONSTRAINT employee_compensation_no_overlap EXCLUDE USING gist (
  employee_id WITH =, daterange(effective_from, effective_to, '[)') WITH &&);
ALTER TABLE app.leave_requests ADD CONSTRAINT leave_no_overlap EXCLUDE USING gist (
  employee_id WITH =, daterange(start_date, end_date, '[]') WITH &&)
  WHERE (status IN ('requested', 'approved'));

ALTER TABLE app.tenant_settings RENAME TO business_settings;
ALTER TABLE app.tenant_setting_values RENAME TO setting_values;
CREATE UNIQUE INDEX business_settings_single_row ON app.business_settings ((true));

-- ---------- Row-level security ------------------------------------------
-- Role rules (RESTRICTIVE policies) stay. PostgreSQL shows nothing through restrictive policies
-- alone, so each of those tables gets a permissive policy beside them. Every other table only had
-- tenant isolation and needs no row security any more.
DO $$
DECLARE
  r record; p record;
  role_ruled text[] := ARRAY['employee_compensation', 'employee_allowances', 'payroll_runs', 'payroll_lines',
    'employee_advances', 'employee_pay_adjustments', 'commission_entries', 'payslips',
    'payroll_statutory_items', 'advance_repayments', 'notifications'];
BEGIN
  FOR r IN
    SELECT n.nspname, c.relname
      FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
     WHERE n.nspname IN ('app', 'iam', 'audit') AND c.relkind IN ('r', 'p') AND c.relrowsecurity
       AND NOT (n.nspname = 'audit' AND c.relname LIKE 'audit_log%')
  LOOP
    IF r.nspname = 'app' AND r.relname = ANY (role_ruled) THEN
      EXECUTE format('ALTER TABLE %I.%I ENABLE ROW LEVEL SECURITY', r.nspname, r.relname);
      EXECUTE format('ALTER TABLE %I.%I FORCE ROW LEVEL SECURITY', r.nspname, r.relname);
      EXECUTE format('CREATE POLICY app_access ON %I.%I USING (true) WITH CHECK (true)', r.nspname, r.relname);
    ELSE
      FOR p IN SELECT policyname FROM pg_policies WHERE schemaname = r.nspname AND tablename = r.relname LOOP
        EXECUTE format('DROP POLICY %I ON %I.%I', p.policyname, r.nspname, r.relname);
      END LOOP;
      EXECUTE format('ALTER TABLE %I.%I DISABLE ROW LEVEL SECURITY', r.nspname, r.relname);
      EXECUTE format('ALTER TABLE %I.%I NO FORCE ROW LEVEL SECURITY', r.nspname, r.relname);
    END IF;
  END LOOP;
END $$;

-- ---------- Functions ---------------------------------------------------
-- Signatures are unchanged, so ownership and EXECUTE privileges carry over. A p_tenant argument,
-- where one remains, is ignored.

CREATE OR REPLACE FUNCTION app.next_document_number(p_type app.doc_type) RETURNS text
LANGUAGE plpgsql AS $$
DECLARE v_prefix text; v_val bigint; v_pad smallint;
BEGIN
  UPDATE app.document_sequences
     SET next_value = next_value + 1, updated_at = now()
   WHERE doc_type = p_type
  RETURNING prefix, next_value - 1, pad_width INTO v_prefix, v_val, v_pad;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'no sequence configured for %', p_type USING ERRCODE = 'BM008';
  END IF;
  RETURN v_prefix || lpad(v_val::text, greatest(v_pad, length(v_val::text)), '0');
END $$;

CREATE OR REPLACE FUNCTION app.assert_day_open(p_tenant uuid, p_location uuid, p_date date) RETURNS void
LANGUAGE plpgsql STABLE AS $$
BEGIN
  IF EXISTS (SELECT 1 FROM app.day_closings
             WHERE location_id = p_location AND business_date = p_date AND status = 'closed') THEN
    RAISE EXCEPTION 'business day % is closed for this location', p_date USING ERRCODE = 'BM002';
  END IF;
END $$;

CREATE OR REPLACE FUNCTION audit.compute_row_hash(r audit.audit_log) RETURNS bytea
LANGUAGE sql IMMUTABLE AS $$
  SELECT digest(concat_ws('|', r.seq, r.id, r.occurred_at AT TIME ZONE 'UTC', r.legacy_scope, r.actor_type,
                 r.actor_user_id, r.actor_membership_id, r.action, r.entity_table, r.entity_id,
                 r.old_values::text, r.new_values::text, array_to_string(r.changed_columns, ','),
                 r.outcome, r.reason, r.request_id, host(r.client_ip)), 'sha256')
$$;

-- p_tenant names a seal chain: NULL (or the zero uuid) is the business's own chain; a value is the
-- chain of entries written before the single-business migration.
CREATE OR REPLACE FUNCTION audit.seal_tenant(p_tenant uuid, p_lag interval DEFAULT interval '5 minutes')
RETURNS integer LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE
  scope uuid := nullif(p_tenant, '00000000-0000-0000-0000-000000000000');
  k uuid := coalesce(p_tenant, '00000000-0000-0000-0000-000000000000');
  last audit.audit_seals%ROWTYPE;
  h bytea; r record; n integer := 0; first_seq bigint; last_seq bigint;
BEGIN
  PERFORM pg_advisory_xact_lock(hashtextextended('audit_seal:' || k::text, 0));
  SELECT * INTO last FROM audit.audit_seals WHERE tenant_key = k ORDER BY seal_no DESC LIMIT 1;
  h := last.chain_hash;
  FOR r IN SELECT seq, row_hash FROM audit.audit_log
            WHERE legacy_scope IS NOT DISTINCT FROM scope
              AND seq > coalesce(last.to_seq, 0)
              AND occurred_at < now() - p_lag
            ORDER BY seq LOOP
    h := digest(coalesce(h, ''::bytea) || r.row_hash, 'sha256');
    n := n + 1; first_seq := coalesce(first_seq, r.seq); last_seq := r.seq;
  END LOOP;
  IF n > 0 THEN
    INSERT INTO audit.audit_seals (tenant_key, seal_no, from_seq, to_seq, row_count, prev_chain_hash, chain_hash)
    VALUES (k, coalesce(last.seal_no, 0) + 1, first_seq, last_seq, n, last.chain_hash, h);
  END IF;
  RETURN n;
END $$;

CREATE OR REPLACE FUNCTION audit.seal_all() RETURNS integer
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE s uuid; total integer := 0;
BEGIN
  FOR s IN SELECT DISTINCT legacy_scope FROM audit.audit_log WHERE occurred_at > now() - interval '40 days' LOOP
    total := total + audit.seal_tenant(s);
  END LOOP;
  RETURN total;
END $$;

CREATE OR REPLACE FUNCTION audit.verify_tenant(p_tenant uuid) RETURNS TABLE (seal_no bigint, problem text)
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE s record; h bytea; r audit.audit_log; cnt integer;
  scope uuid := nullif(p_tenant, '00000000-0000-0000-0000-000000000000');
BEGIN
  FOR s IN SELECT * FROM audit.audit_seals a
            WHERE a.tenant_key = coalesce(p_tenant, '00000000-0000-0000-0000-000000000000') ORDER BY a.seal_no LOOP
    h := s.prev_chain_hash; cnt := 0;
    FOR r IN SELECT a.* FROM audit.audit_log a
              WHERE a.legacy_scope IS NOT DISTINCT FROM scope AND a.seq BETWEEN s.from_seq AND s.to_seq
              ORDER BY a.seq LOOP
      IF audit.compute_row_hash(r) <> r.row_hash THEN
        seal_no := s.seal_no; problem := 'row ' || r.seq || ' content altered'; RETURN NEXT; RETURN;
      END IF;
      h := digest(coalesce(h, ''::bytea) || r.row_hash, 'sha256'); cnt := cnt + 1;
    END LOOP;
    IF cnt <> s.row_count OR h <> s.chain_hash THEN
      seal_no := s.seal_no; problem := 'rows missing or chain mismatch'; RETURN NEXT; RETURN;
    END IF;
  END LOOP;
END $$;

CREATE OR REPLACE FUNCTION audit.log_event(
  p_action text, p_entity_table text DEFAULT NULL, p_entity_id uuid DEFAULT NULL,
  p_outcome text DEFAULT 'success', p_reason text DEFAULT NULL,
  p_details jsonb DEFAULT NULL, p_actor_type text DEFAULT 'user', p_user_agent text DEFAULT NULL)
RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
BEGIN
  INSERT INTO audit.audit_log (actor_type, actor_user_id, actor_membership_id, action,
                               entity_table, entity_id, new_values, outcome, reason, request_id,
                               client_ip, user_agent, row_hash)
  VALUES (p_actor_type, app.current_user_id(), app.current_membership_id(),
          p_action, p_entity_table, p_entity_id, p_details, p_outcome, p_reason,
          app.current_request_id(), app.current_client_ip(), p_user_agent, ''::bytea);
END $$;

CREATE OR REPLACE FUNCTION audit.tg_capture() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE
  o jsonb; n jsonb; chg text[]; red text[] := coalesce(TG_ARGV::text[], '{}');
  e uuid; k text; raw_id text;
BEGIN
  IF TG_OP <> 'INSERT' THEN o := to_jsonb(OLD); END IF;
  IF TG_OP <> 'DELETE' THEN n := to_jsonb(NEW); END IF;
  raw_id := coalesce(n ->> 'id', o ->> 'id');
  -- The single-row business tables have no uuid id.
  IF raw_id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' THEN e := raw_id::uuid; END IF;

  IF TG_OP = 'UPDATE' THEN
    SELECT array_agg(key ORDER BY key) INTO chg
      FROM jsonb_object_keys(n) AS key
     WHERE n -> key IS DISTINCT FROM o -> key
       AND key NOT IN ('updated_at', 'updated_by', 'row_version');
    IF chg IS NULL THEN RETURN NULL; END IF;
    SELECT jsonb_object_agg(key, o -> key), jsonb_object_agg(key, n -> key)
      INTO o, n FROM unnest(chg) AS key;
  END IF;

  FOREACH k IN ARRAY red LOOP
    IF o ? k AND o -> k <> 'null'::jsonb THEN o := jsonb_set(o, ARRAY[k], '"[redacted]"'); END IF;
    IF n ? k AND n -> k <> 'null'::jsonb THEN n := jsonb_set(n, ARRAY[k], '"[redacted]"'); END IF;
  END LOOP;

  INSERT INTO audit.audit_log (actor_type, actor_user_id, actor_membership_id, action,
                               entity_table, entity_id, old_values, new_values, changed_columns,
                               request_id, client_ip, row_hash)
  VALUES (CASE WHEN app.current_membership_id() IS NULL THEN 'system' ELSE 'user' END,
          app.current_user_id(), app.current_membership_id(), lower(TG_OP),
          TG_TABLE_NAME, e, o, n, chg, app.current_request_id(), app.current_client_ip(), ''::bytea);
  RETURN NULL;
END $$;

CREATE OR REPLACE FUNCTION audit.tg_capture_identity() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE o jsonb; n jsonb; chg text[];
BEGIN
  IF TG_OP <> 'INSERT' THEN o := to_jsonb(OLD); END IF;
  IF TG_OP <> 'DELETE' THEN n := to_jsonb(NEW); END IF;
  IF TG_OP = 'UPDATE' THEN
    SELECT array_agg(key ORDER BY key) INTO chg
      FROM jsonb_object_keys(n) AS key
     WHERE n -> key IS DISTINCT FROM o -> key
       AND key NOT IN ('updated_at', 'updated_by', 'row_version');
  END IF;
  INSERT INTO audit.audit_log (actor_type, actor_user_id, actor_membership_id, action,
                               entity_table, entity_id, changed_columns, request_id, client_ip, row_hash)
  VALUES (CASE WHEN app.current_user_id() IS NULL THEN 'system' ELSE 'user' END,
          app.current_user_id(), app.current_membership_id(), lower(TG_OP), TG_TABLE_NAME,
          (coalesce(n, o) ->> TG_ARGV[0])::uuid, chg, app.current_request_id(), app.current_client_ip(), ''::bytea);
  RETURN NULL;
END $$;

CREATE OR REPLACE FUNCTION app.has_permission(p_code text) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog, public AS $$
  WITH ctx AS (SELECT app.current_membership_id() AS m)
  SELECT EXISTS (SELECT 1 FROM app.memberships x, ctx
                  WHERE x.id = ctx.m AND x.status = 'active')
     AND coalesce(
           (SELECT o.effect = 'allow' FROM app.membership_permission_overrides o, ctx
             WHERE o.membership_id = ctx.m AND o.permission_code = p_code
               AND (o.expires_at IS NULL OR o.expires_at > now())),
           EXISTS (SELECT 1 FROM app.membership_roles mr
                     JOIN app.role_permissions rp ON rp.role_id = mr.role_id
                     JOIN app.roles r ON r.id = mr.role_id AND r.status = 'active', ctx
                    WHERE mr.membership_id = ctx.m AND rp.permission_code = p_code))
$$;

CREATE OR REPLACE FUNCTION app.authority_limit(p_membership uuid, p_code app.limit_code) RETURNS numeric
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog, public AS $$
  SELECT coalesce(
    (SELECT limit_value FROM app.authority_limits
      WHERE membership_id = p_membership AND limit_code = p_code),
    (SELECT min(al.limit_value) FROM app.authority_limits al
       JOIN app.membership_roles mr ON mr.role_id = al.role_id
      WHERE mr.membership_id = p_membership AND al.limit_code = p_code))
$$;

CREATE OR REPLACE FUNCTION app.approved_request_exists(p_type app.approval_type, p_target uuid) RETURNS boolean
LANGUAGE sql STABLE AS $$
  SELECT EXISTS (SELECT 1 FROM app.approval_requests
                  WHERE approval_type = p_type AND status = 'approved'
                    AND p_target IN (sale_id, project_id, design_job_id, financial_transaction_id,
                                     payment_received_id, payment_made_id, day_closing_id, product_id))
$$;

CREATE OR REPLACE FUNCTION app.tg_day_lock_insert() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE d date; l uuid;
BEGIN
  EXECUTE format('SELECT ($1).%I, ($1).%I', TG_ARGV[0], TG_ARGV[1]) INTO d, l USING NEW;
  PERFORM app.assert_day_open(NULL, l, d);
  RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION app.tg_method_matches_account() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE k app.account_kind;
BEGIN
  SELECT kind INTO k FROM app.financial_accounts
   WHERE id = NEW.account_id AND status = 'active';
  IF k IS NULL THEN
    RAISE EXCEPTION 'account is not active' USING ERRCODE = 'BM008';
  END IF;
  IF NOT ((NEW.method = 'cash' AND k IN ('cash', 'petty_cash'))
       OR (NEW.method = 'mobile_money' AND k = 'mobile_money')
       OR (NEW.method IN ('bank_transfer', 'card', 'cheque') AND k = 'bank')) THEN
    RAISE EXCEPTION 'payment method % cannot use a % account', NEW.method, k USING ERRCODE = 'BM008';
  END IF;
  RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION app.tg_keep_one_admin() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
BEGIN
  IF NOT EXISTS (
       SELECT 1 FROM app.membership_roles mr
         JOIN app.roles r ON r.id = mr.role_id AND r.code = 'admin'
         JOIN app.memberships m ON m.id = mr.membership_id AND m.status = 'active') THEN
    RAISE EXCEPTION 'the business must keep at least one active Admin' USING ERRCODE = 'BM007';
  END IF;
  RETURN NULL;
END $$;

CREATE OR REPLACE FUNCTION app.tg_stock_movement_apply() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE
  new_qty numeric; allow_neg boolean; total_before numeric; avg_before numeric;
  product_name text; location_name text;
BEGIN
  PERFORM app.assert_day_open(NULL, NEW.location_id, NEW.business_date);

  INSERT INTO app.stock_balances AS b (location_id, product_id, quantity_on_hand)
  VALUES (NEW.location_id, NEW.product_id, 0)
  ON CONFLICT (location_id, product_id) DO NOTHING;

  -- Moving weighted average cost on receipts and positive costed adjustments (across all locations).
  IF NEW.movement_type = 'purchase_receipt'
     OR (NEW.movement_type = 'adjustment' AND NEW.quantity_delta > 0 AND NEW.unit_cost IS NOT NULL) THEN
    SELECT coalesce(sum(quantity_on_hand), 0) INTO total_before
      FROM app.stock_balances WHERE product_id = NEW.product_id;
    SELECT average_cost INTO avg_before FROM app.products
     WHERE id = NEW.product_id FOR UPDATE;
    UPDATE app.products
       SET average_cost = CASE WHEN greatest(total_before, 0) + NEW.quantity_delta = 0 THEN NEW.unit_cost
                               ELSE round((greatest(total_before, 0) * avg_before + NEW.quantity_delta * NEW.unit_cost)
                                          / (greatest(total_before, 0) + NEW.quantity_delta), 4) END,
           buying_price = CASE WHEN NEW.movement_type = 'purchase_receipt' THEN NEW.unit_cost ELSE buying_price END
     WHERE id = NEW.product_id;
  END IF;

  -- Row lock on the balance serialises concurrent movements of one product/location.
  UPDATE app.stock_balances
     SET quantity_on_hand = quantity_on_hand + NEW.quantity_delta, updated_at = now()
   WHERE location_id = NEW.location_id AND product_id = NEW.product_id
  RETURNING quantity_on_hand INTO new_qty;

  IF new_qty < 0 THEN
    SELECT coalesce(p.allow_negative_stock, s.allow_negative_stock), p.name INTO allow_neg, product_name
      FROM app.products p CROSS JOIN app.business_settings s
     WHERE p.id = NEW.product_id;
    IF NOT coalesce(allow_neg, false) THEN
      SELECT name INTO location_name FROM app.locations WHERE id = NEW.location_id;
      RAISE EXCEPTION 'Not enough stock: % at % (short by %)',
        coalesce(product_name, 'this product'), coalesce(location_name, 'this location'), trim_scale(-new_qty)
        USING ERRCODE = 'BM003';
    END IF;
  END IF;
  RETURN NULL;
END $$;

CREATE OR REPLACE FUNCTION app.tg_fin_txn_apply() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
BEGIN
  PERFORM app.assert_day_open(NULL, NEW.location_id, NEW.business_date);
  UPDATE app.financial_accounts
     SET current_balance = current_balance + CASE NEW.direction WHEN 'in' THEN NEW.amount ELSE -NEW.amount END
   WHERE id = NEW.account_id;
  RETURN NULL;
END $$;

CREATE OR REPLACE FUNCTION app.tg_fin_txn_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF current_user <> 'bms_owner' AND NEW.txn_type NOT IN ('opening_balance', 'correction') THEN
    RAISE EXCEPTION 'transaction type % is posted automatically from its source document', NEW.txn_type
      USING ERRCODE = 'BM004';
  END IF;
  IF NEW.txn_type = 'correction' AND NOT EXISTS (
       SELECT 1 FROM app.approval_requests a
        WHERE a.id = NEW.approval_request_id
          AND a.approval_type = 'transaction_correction' AND a.status = 'approved'
          AND a.financial_transaction_id = NEW.reverses_transaction_id) THEN
    RAISE EXCEPTION 'correction requires its approved transaction_correction request' USING ERRCODE = 'BM007';
  END IF;
  IF NEW.txn_type = 'opening_balance' THEN
    IF NOT app.has_permission('finance.account.manage') THEN
      RAISE EXCEPTION 'permission finance.account.manage required' USING ERRCODE = 'BM007';
    END IF;
    IF current_user <> 'bms_owner' THEN
      -- Serialise on the account so two concurrent "first" entries cannot both pass.
      PERFORM 1 FROM app.financial_accounts a WHERE a.id = NEW.account_id FOR UPDATE;
      IF NEW.direction <> 'in' OR EXISTS (SELECT 1 FROM app.financial_transactions t
            WHERE t.account_id = NEW.account_id) THEN
        RAISE EXCEPTION 'an opening balance can only be the first entry of an account' USING ERRCODE = 'BM004';
      END IF;
    END IF;
  END IF;
  RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION app.reverse_ledger(p_tenant uuid, p_where text, p_id uuid, p_date date, p_reason text)
RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
BEGIN
  EXECUTE format($q$
    INSERT INTO app.financial_transactions (account_id, direction, amount, txn_type, business_unit_id,
           location_id, business_date, description, project_id, design_job_id, reverses_transaction_id)
    SELECT t.account_id, CASE t.direction WHEN 'in' THEN 'out'::app.txn_direction ELSE 'in' END,
           t.amount, 'reversal', t.business_unit_id, t.location_id, $2, left('Reversal: ' || $3, 500),
           t.project_id, t.design_job_id, t.id
      FROM app.financial_transactions t
     WHERE t.%I = $1 AND t.txn_type <> 'reversal'
       AND NOT EXISTS (SELECT 1 FROM app.financial_transactions r
                        WHERE r.reverses_transaction_id = t.id)$q$, p_where)
  USING p_id, p_date, p_reason;
END $$;

CREATE OR REPLACE FUNCTION app.accrue_sale_commissions(p_tenant uuid, p_sale uuid) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
BEGIN
  INSERT INTO app.commission_entries (employee_id, commission_rule_id, sale_id, amount, earned_on)
  SELECT e.id, r.id, s.id, round(sum(sl.line_total) * r.percent / 100, 2), s.business_date
    FROM app.sales s
    JOIN app.employees e ON e.membership_id = s.sold_by AND e.status = 'active'
    JOIN app.commission_rules r ON r.active AND r.basis = 'sale_percent'
         AND s.business_date >= r.effective_from AND (r.effective_to IS NULL OR s.business_date < r.effective_to)
         AND (r.employee_id IS NULL OR r.employee_id = e.id)
    JOIN app.sale_lines sl ON sl.sale_id = s.id
    JOIN app.products p ON p.id = sl.product_id
   WHERE s.id = p_sale
     AND (r.product_category_id IS NULL OR r.product_category_id = p.category_id)
   GROUP BY e.id, r.id, s.id, r.percent, s.business_date
  HAVING round(sum(sl.line_total) * r.percent / 100, 2) > 0
  ON CONFLICT DO NOTHING;
END $$;

CREATE OR REPLACE FUNCTION app.reverse_return_commissions(p_tenant uuid, p_return uuid) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
BEGIN
  INSERT INTO app.commission_entries (employee_id, commission_rule_id, sale_id, sale_return_id, amount, earned_on)
  SELECT c.employee_id, c.commission_rule_id, c.sale_id, r.id,
         -round(c.amount * rv.returned_value / nullif(s.subtotal, 0), 2), r.business_date
    FROM app.sale_returns r
    JOIN app.sales s ON s.id = r.sale_id
    JOIN app.commission_entries c ON c.sale_id = s.id
         AND c.amount > 0 AND c.status IN ('accrued', 'included')
    CROSS JOIN LATERAL (SELECT coalesce(sum(rl.quantity * sl.line_total / sl.quantity), 0) AS returned_value
                          FROM app.sale_return_lines rl
                          JOIN app.sale_lines sl ON sl.id = rl.sale_line_id
                         WHERE rl.sale_return_id = r.id) rv
   WHERE r.id = p_return
     AND round(c.amount * rv.returned_value / nullif(s.subtotal, 0), 2) > 0;
END $$;

CREATE OR REPLACE FUNCTION app.tg_project_commission() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
BEGIN
  IF NEW.status = 'completed' AND OLD.status <> 'completed' THEN
    INSERT INTO app.commission_entries (employee_id, commission_rule_id, project_id, amount, earned_on)
    SELECT e.id, r.id, NEW.id, r.fixed_amount, current_date
      FROM app.employees e
      JOIN app.commission_rules r ON r.active AND r.basis = 'project_fixed'
           AND current_date >= r.effective_from AND (r.effective_to IS NULL OR current_date < r.effective_to)
           AND (r.employee_id IS NULL OR r.employee_id = e.id)
           AND (r.service_id IS NULL OR r.service_id = NEW.service_id)
     WHERE e.membership_id = NEW.owner_id AND e.status = 'active'
    ON CONFLICT DO NOTHING;
  END IF;
  RETURN NULL;
END $$;

CREATE OR REPLACE FUNCTION app.tg_sales_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  line_disc numeric;
  total_disc numeric;
  pct numeric;
  lim numeric;
  has_approved boolean;
BEGIN
  IF TG_OP = 'DELETE' THEN
    IF OLD.status NOT IN ('draft', 'held', 'pending_approval') OR OLD.amount_paid > 0 THEN
      RAISE EXCEPTION 'only unpaid draft/held sales can be deleted' USING ERRCODE = 'BM004';
    END IF;
    RETURN OLD;
  END IF;

  IF TG_OP = 'INSERT' THEN
    PERFORM app.assert_day_open(NULL, NEW.location_id, NEW.business_date);
    RETURN NEW;
  END IF;

  -- UPDATE
  IF OLD.status IN ('completed', 'voided') THEN
    IF (NEW.subtotal, NEW.discount_amount, NEW.discount_value, NEW.tax_amount, NEW.total, NEW.customer_id,
        NEW.location_id, NEW.business_date, NEW.sold_by, NEW.receipt_no, NEW.due_date, NEW.business_unit_id)
       IS DISTINCT FROM
       (OLD.subtotal, OLD.discount_amount, OLD.discount_value, OLD.tax_amount, OLD.total, OLD.customer_id,
        OLD.location_id, OLD.business_date, OLD.sold_by, OLD.receipt_no, OLD.due_date, OLD.business_unit_id) THEN
      RAISE EXCEPTION 'completed sale % is read-only; use a return or void', OLD.receipt_no USING ERRCODE = 'BM004';
    END IF;
  ELSE
    PERFORM app.assert_day_open(NULL, NEW.location_id, NEW.business_date);
  END IF;

  IF NEW.status = 'held' AND OLD.status <> 'held' THEN NEW.held_at := coalesce(NEW.held_at, now()); END IF;

  IF NEW.status = 'completed' AND OLD.status <> 'completed' THEN
    NEW.completed_at := coalesce(NEW.completed_at, now());
    -- Credit sale (balance left at completion) requires a registered customer and a due date (spec §6).
    IF NEW.amount_paid < NEW.total AND (NEW.customer_id IS NULL OR NEW.due_date IS NULL) THEN
      RAISE EXCEPTION 'credit sale requires a registered customer and a due date' USING ERRCODE = 'BM008';
    END IF;
    -- Discount authority: covers both sale-level and line-level discounts.
    SELECT coalesce(sum(line_discount), 0) INTO line_disc
      FROM app.sale_lines WHERE sale_id = NEW.id;
    total_disc := NEW.discount_amount + line_disc;
    IF total_disc > 0 THEN
      pct := round(total_disc * 100 / nullif(NEW.subtotal + line_disc, 0), 2);
      lim := app.authority_limit(NEW.sold_by, 'sale_discount_pct');
      SELECT EXISTS (
        SELECT 1 FROM app.approval_requests a
         WHERE a.sale_id = NEW.id AND a.approval_type = 'sale_discount'
           AND a.status = 'approved' AND a.requested_percent >= pct
      ) INTO has_approved;
      IF app.current_membership_id() IS NOT NULL AND NOT has_approved AND NOT app.has_permission('shop.discount.apply') THEN
        RAISE EXCEPTION 'permission shop.discount.apply required for discounts' USING ERRCODE = 'BM007';
      END IF;
      IF lim IS NOT NULL AND pct > lim AND NOT has_approved THEN
        RAISE EXCEPTION 'discount %%% exceeds seller limit %%%; approval required', pct, lim USING ERRCODE = 'BM007';
      END IF;
    END IF;
  END IF;

  IF NEW.status = 'voided' AND OLD.status <> 'voided' THEN
    NEW.voided_at := coalesce(NEW.voided_at, now());
    NEW.voided_by := coalesce(NEW.voided_by, app.current_membership_id());
    IF NOT (app.has_permission('shop.sale.void') OR app.approved_request_exists('sale_void', NEW.id)) THEN
      RAISE EXCEPTION 'voiding a sale requires Admin approval' USING ERRCODE = 'BM007';
    END IF;
    IF NEW.amount_paid > 0 THEN
      RAISE EXCEPTION 'void or refund the sale''s payments first' USING ERRCODE = 'BM005';
    END IF;
    IF EXISTS (SELECT 1 FROM app.sale_returns WHERE sale_id = NEW.id AND status = 'completed') THEN
      RAISE EXCEPTION 'sale has completed returns and cannot be voided' USING ERRCODE = 'BM004';
    END IF;
    PERFORM app.assert_day_open(NULL, NEW.location_id, NEW.business_date);
  END IF;
  RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION app.tg_sales_post() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE expected integer; marked integer;
BEGIN
  IF NEW.status = 'completed' AND OLD.status <> 'completed' THEN
    UPDATE app.sale_lines sl SET unit_cost = p.average_cost
      FROM app.products p
     WHERE sl.sale_id = NEW.id AND p.id = sl.product_id;

    INSERT INTO app.stock_movements (location_id, product_id, movement_type, quantity_delta,
                                     unit_cost, business_date, sale_line_id)
    SELECT NEW.location_id, sl.product_id, 'sale', -sl.quantity, sl.unit_cost, NEW.business_date, sl.id
      FROM app.sale_lines sl WHERE sl.sale_id = NEW.id;

    SELECT count(*) INTO expected FROM app.sale_line_units slu
      JOIN app.sale_lines sl ON sl.id = slu.sale_line_id
     WHERE sl.sale_id = NEW.id;
    UPDATE app.product_units u
       SET status = 'sold', sold_at = now(),
           warranty_expires_on = CASE WHEN sl.warranty_months > 0
                                      THEN (current_date + make_interval(months => sl.warranty_months))::date END
      FROM app.sale_line_units slu
      JOIN app.sale_lines sl ON sl.id = slu.sale_line_id
     WHERE sl.sale_id = NEW.id AND u.id = slu.product_unit_id
       AND u.status = 'in_stock' AND u.location_id = NEW.location_id;
    GET DIAGNOSTICS marked = ROW_COUNT;
    IF marked <> expected THEN
      RAISE EXCEPTION 'a serial number on this sale is not in stock at this location' USING ERRCODE = 'BM003';
    END IF;
    PERFORM app.accrue_sale_commissions(NULL, NEW.id);

  ELSIF NEW.status = 'voided' AND OLD.status = 'completed' THEN
    INSERT INTO app.stock_movements (location_id, product_id, movement_type, quantity_delta,
                                     unit_cost, business_date, sale_line_id)
    SELECT NEW.location_id, sl.product_id, 'sale_void', sl.quantity, sl.unit_cost, NEW.business_date, sl.id
      FROM app.sale_lines sl WHERE sl.sale_id = NEW.id;
    UPDATE app.product_units u SET status = 'in_stock', sold_at = NULL, warranty_expires_on = NULL
      FROM app.sale_line_units slu JOIN app.sale_lines sl ON sl.id = slu.sale_line_id
     WHERE sl.sale_id = NEW.id AND u.id = slu.product_unit_id;
    UPDATE app.commission_entries SET status = 'reversed'
     WHERE sale_id = NEW.id AND status = 'accrued';
  END IF;
  RETURN NULL;
END $$;

CREATE OR REPLACE FUNCTION app.tg_sales_check_totals() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE s app.sales%ROWTYPE; lines_total numeric; n_lines integer; bad_serial integer;
BEGIN
  SELECT * INTO s FROM app.sales WHERE id = NEW.id;
  IF NOT FOUND OR s.status <> 'completed' THEN RETURN NULL; END IF;
  SELECT coalesce(sum(line_total), 0), count(*) INTO lines_total, n_lines
    FROM app.sale_lines WHERE sale_id = s.id;
  IF n_lines = 0 THEN
    RAISE EXCEPTION 'completed sale % has no lines', s.receipt_no USING ERRCODE = 'BM008';
  END IF;
  IF lines_total <> s.subtotal THEN
    RAISE EXCEPTION 'sale % subtotal % <> sum of lines %', s.receipt_no, s.subtotal, lines_total USING ERRCODE = 'BM008';
  END IF;
  SELECT count(*) INTO bad_serial FROM app.sale_lines sl
    JOIN app.products p ON p.id = sl.product_id AND p.tracks_serial
   WHERE sl.sale_id = s.id
     AND sl.quantity <> (SELECT count(*) FROM app.sale_line_units u WHERE u.sale_line_id = sl.id);
  IF bad_serial > 0 THEN
    RAISE EXCEPTION 'serial/IMEI required for each unit of serialised products' USING ERRCODE = 'BM008';
  END IF;
  RETURN NULL;
END $$;

CREATE OR REPLACE FUNCTION app.tg_sale_lines_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE st app.sale_status;
BEGIN
  SELECT status INTO st FROM app.sales WHERE id = coalesce(NEW.sale_id, OLD.sale_id);
  IF st IN ('completed', 'voided') THEN
    IF TG_OP <> 'UPDATE' OR current_user <> 'bms_owner'
       OR (NEW.product_id, NEW.quantity, NEW.unit_price, NEW.line_discount, NEW.line_total, NEW.sale_id)
          IS DISTINCT FROM (OLD.product_id, OLD.quantity, OLD.unit_price, OLD.line_discount, OLD.line_total, OLD.sale_id) THEN
      RAISE EXCEPTION 'lines of a completed sale are read-only' USING ERRCODE = 'BM004';
    END IF;
  END IF;
  RETURN coalesce(NEW, OLD);
END $$;

CREATE OR REPLACE FUNCTION app.tg_sale_line_units_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE st app.sale_status;
BEGIN
  SELECT s.status INTO st FROM app.sale_lines sl JOIN app.sales s ON s.id = sl.sale_id
   WHERE sl.id = coalesce(NEW.sale_line_id, OLD.sale_line_id);
  IF st IN ('completed', 'voided') THEN
    RAISE EXCEPTION 'serials of a completed sale are read-only' USING ERRCODE = 'BM004';
  END IF;
  RETURN coalesce(NEW, OLD);
END $$;

CREATE OR REPLACE FUNCTION app.tg_returns_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE sale_st app.sale_status; lines_refund numeric;
BEGIN
  IF TG_OP = 'INSERT' THEN
    SELECT status INTO sale_st FROM app.sales WHERE id = NEW.sale_id;
    IF sale_st <> 'completed' THEN
      RAISE EXCEPTION 'only completed sales can be returned' USING ERRCODE = 'BM001';
    END IF;
    PERFORM app.assert_day_open(NULL, NEW.location_id, NEW.business_date);
    RETURN NEW;
  END IF;
  IF NEW.status IN ('approved', 'rejected') AND OLD.status = 'requested' THEN
    NEW.decided_by := coalesce(NEW.decided_by, app.current_membership_id());
    NEW.decided_at := coalesce(NEW.decided_at, now());
    IF NEW.status = 'approved' THEN
      PERFORM app.assert_can_approve('refund', NEW.refund_amount);
    END IF;
  END IF;
  IF NEW.status = 'completed' AND OLD.status <> 'completed' THEN
    NEW.completed_at := coalesce(NEW.completed_at, now());
    SELECT coalesce(sum(refund_amount), 0) INTO lines_refund
      FROM app.sale_return_lines WHERE sale_return_id = NEW.id;
    IF lines_refund <> NEW.refund_amount THEN
      RAISE EXCEPTION 'refund amount % <> sum of lines %', NEW.refund_amount, lines_refund USING ERRCODE = 'BM008';
    END IF;
    PERFORM app.assert_day_open(NULL, NEW.location_id, NEW.business_date);
  END IF;
  RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION app.tg_returns_post() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE refund_cap numeric;
BEGIN
  IF NEW.status = 'completed' AND OLD.status <> 'completed' THEN
    -- Lock the parent sale row so concurrent return completions or payment voids serialize.
    PERFORM 1 FROM app.sales s WHERE s.id = NEW.sale_id FOR UPDATE;

    -- Cumulative returned quantity is capped by the CHECK on sale_lines.
    UPDATE app.sale_lines sl SET returned_quantity = sl.returned_quantity + rl.quantity
      FROM app.sale_return_lines rl
     WHERE rl.sale_return_id = NEW.id AND sl.id = rl.sale_line_id;

    -- Refunds can never exceed what was actually paid for the sale.
    SELECT s.amount_paid - coalesce((SELECT sum(r.refund_amount) FROM app.sale_returns r
                                      WHERE r.sale_id = s.id
                                        AND r.status = 'completed' AND r.id <> NEW.id), 0)
      INTO refund_cap FROM app.sales s WHERE s.id = NEW.sale_id;
    IF NEW.refund_amount > refund_cap THEN
      RAISE EXCEPTION 'refund % exceeds amount paid and not yet refunded %', NEW.refund_amount, refund_cap
        USING ERRCODE = 'BM005';
    END IF;

    INSERT INTO app.stock_movements (location_id, product_id, movement_type, quantity_delta,
                                     unit_cost, business_date, sale_return_line_id)
    SELECT NEW.location_id, sl.product_id, 'sale_return', rl.quantity, sl.unit_cost, NEW.business_date, rl.id
      FROM app.sale_return_lines rl
      JOIN app.sale_lines sl ON sl.id = rl.sale_line_id
     WHERE rl.sale_return_id = NEW.id AND rl.restock;

    UPDATE app.product_units u
       SET status = CASE WHEN rl.restock THEN 'in_stock'::app.unit_status ELSE 'defective' END,
           location_id = NEW.location_id, sold_at = CASE WHEN rl.restock THEN NULL ELSE u.sold_at END
      FROM app.sale_return_lines rl
     WHERE rl.sale_return_id = NEW.id AND u.id = rl.product_unit_id;

    PERFORM app.reverse_return_commissions(NULL, NEW.id);
  END IF;
  RETURN NULL;
END $$;

CREATE OR REPLACE FUNCTION app.tg_goods_receipt_line_post() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE gr app.goods_receipts%ROWTYPE; pl app.purchase_lines%ROWTYPE; p_status app.purchase_status;
        open_qty numeric;
BEGIN
  SELECT * INTO gr FROM app.goods_receipts WHERE id = NEW.goods_receipt_id;
  SELECT * INTO pl FROM app.purchase_lines WHERE id = NEW.purchase_line_id FOR UPDATE;
  IF pl.purchase_id <> gr.purchase_id THEN
    RAISE EXCEPTION 'purchase line belongs to another purchase' USING ERRCODE = 'BM008';
  END IF;
  SELECT status INTO p_status FROM app.purchases WHERE id = gr.purchase_id;
  IF p_status NOT IN ('ordered', 'partially_received') THEN
    RAISE EXCEPTION 'purchase is % and cannot receive goods', p_status USING ERRCODE = 'BM001';
  END IF;
  UPDATE app.purchase_lines SET quantity_received = quantity_received + NEW.quantity
   WHERE id = NEW.purchase_line_id;   -- CHECK caps at ordered
  INSERT INTO app.stock_movements (location_id, product_id, movement_type, quantity_delta,
                                   unit_cost, business_date, goods_receipt_line_id)
  VALUES (gr.location_id, pl.product_id, 'purchase_receipt', NEW.quantity, NEW.unit_cost,
          gr.business_date, NEW.id);
  SELECT coalesce(sum(quantity_ordered - quantity_received), 0) INTO open_qty
    FROM app.purchase_lines WHERE purchase_id = gr.purchase_id;
  UPDATE app.purchases SET status = CASE WHEN open_qty = 0 THEN 'received'::app.purchase_status ELSE 'partially_received' END
   WHERE id = gr.purchase_id
     AND status IS DISTINCT FROM CASE WHEN open_qty = 0 THEN 'received'::app.purchase_status ELSE 'partially_received' END;
  RETURN NULL;
END $$;

CREATE OR REPLACE FUNCTION app.tg_adjustment_post() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
BEGIN
  IF NEW.status = 'posted' AND OLD.status <> 'posted' THEN
    INSERT INTO app.stock_movements (location_id, product_id, movement_type, quantity_delta,
                                     unit_cost, business_date, stock_adjustment_line_id)
    SELECT NEW.location_id, l.product_id, 'adjustment', l.quantity_delta,
           coalesce(l.unit_cost, p.average_cost), NEW.business_date, l.id
      FROM app.stock_adjustment_lines l JOIN app.products p ON p.id = l.product_id
     WHERE l.stock_adjustment_id = NEW.id;
    IF NOT FOUND THEN
      RAISE EXCEPTION 'adjustment has no lines' USING ERRCODE = 'BM008';
    END IF;
  END IF;
  RETURN NULL;
END $$;

CREATE OR REPLACE FUNCTION app.tg_transfer_post() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE zone text := (SELECT timezone FROM app.business_profile);
BEGIN
  IF NEW.status = 'dispatched' AND OLD.status = 'draft' THEN
    INSERT INTO app.stock_movements (location_id, product_id, movement_type, quantity_delta,
                                     unit_cost, business_date, stock_transfer_line_id)
    SELECT NEW.from_location_id, l.product_id, 'transfer_out', -l.quantity, p.average_cost,
           (NEW.dispatched_at AT TIME ZONE zone)::date, l.id
      FROM app.stock_transfer_lines l JOIN app.products p ON p.id = l.product_id
     WHERE l.stock_transfer_id = NEW.id;
  ELSIF NEW.status = 'received' AND OLD.status = 'dispatched' THEN
    INSERT INTO app.stock_movements (location_id, product_id, movement_type, quantity_delta,
                                     unit_cost, business_date, stock_transfer_line_id)
    SELECT NEW.to_location_id, l.product_id, 'transfer_in', l.quantity, p.average_cost,
           (NEW.received_at AT TIME ZONE zone)::date, l.id
      FROM app.stock_transfer_lines l JOIN app.products p ON p.id = l.product_id
     WHERE l.stock_transfer_id = NEW.id;
  END IF;
  RETURN NULL;
END $$;

CREATE OR REPLACE FUNCTION app.tg_product_price_guard() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE req uuid;
BEGIN
  IF NEW.selling_price IS DISTINCT FROM OLD.selling_price THEN
    SELECT id INTO req FROM app.approval_requests
     WHERE product_id = NEW.id AND approval_type = 'product_price_change'
       AND status = 'approved' AND requested_amount = NEW.selling_price
     ORDER BY decided_at DESC LIMIT 1;
    IF req IS NULL AND NOT app.has_permission('shop.product.price_change') THEN
      RAISE EXCEPTION 'changing a selling price requires approval' USING ERRCODE = 'BM007';
    END IF;
    INSERT INTO app.product_price_changes (product_id, old_selling_price, new_selling_price,
                                           old_buying_price, new_buying_price, reason, approval_request_id)
    VALUES (NEW.id, OLD.selling_price, NEW.selling_price, OLD.buying_price, NEW.buying_price,
            coalesce(nullif(current_setting('app.change_note', true), ''), 'price update'), req);
  END IF;
  RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION app.tg_payment_received_post() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
BEGIN
  IF TG_OP = 'INSERT' THEN
    INSERT INTO app.financial_transactions (account_id, direction, amount, txn_type, business_unit_id,
           location_id, business_date, description, payment_received_id)
    VALUES (NEW.account_id, 'in', NEW.amount, 'customer_receipt', NEW.business_unit_id,
            NEW.location_id, NEW.business_date, 'Receipt ' || NEW.receipt_no, NEW.id);
  ELSIF NEW.status = 'voided' AND OLD.status = 'posted' THEN
    IF NOT (app.has_permission('finance.payment.void') OR app.approved_request_exists('payment_void', NEW.id)) THEN
      RAISE EXCEPTION 'voiding a payment requires Admin approval' USING ERRCODE = 'BM007';
    END IF;
    PERFORM app.assert_day_open(NULL, NEW.location_id, NEW.business_date);
    -- undo allocation effects
    UPDATE app.sales s SET amount_paid = s.amount_paid - a.amount
      FROM app.payment_allocations a
     WHERE a.payment_received_id = NEW.id AND s.id = a.sale_id;
    UPDATE app.invoices i SET amount_paid = i.amount_paid - a.amount,
           status = CASE WHEN i.amount_paid - a.amount = 0 THEN 'issued'::app.invoice_status ELSE 'partially_paid' END
      FROM app.payment_allocations a
     WHERE a.payment_received_id = NEW.id AND i.id = a.invoice_id;
    PERFORM app.reverse_ledger(NULL, 'payment_received_id', NEW.id, NEW.business_date,
                               'payment ' || NEW.receipt_no || ' voided');
  ELSIF (NEW.amount, NEW.account_id, NEW.method, NEW.customer_id, NEW.business_date, NEW.location_id)
        IS DISTINCT FROM (OLD.amount, OLD.account_id, OLD.method, OLD.customer_id, OLD.business_date, OLD.location_id) THEN
    RAISE EXCEPTION 'posted payments are immutable; void and re-enter' USING ERRCODE = 'BM004';
  END IF;
  RETURN NULL;
END $$;

CREATE OR REPLACE FUNCTION app.tg_payment_allocation_apply() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE pay app.payments_received%ROWTYPE; allocated numeric; tgt_customer uuid; tgt_status text;
BEGIN
  SELECT * INTO pay FROM app.payments_received WHERE id = NEW.payment_received_id FOR UPDATE;
  IF pay.status <> 'posted' THEN
    RAISE EXCEPTION 'cannot allocate a voided payment' USING ERRCODE = 'BM005';
  END IF;
  SELECT coalesce(sum(amount), 0) INTO allocated FROM app.payment_allocations
   WHERE payment_received_id = NEW.payment_received_id;
  IF allocated > pay.amount THEN
    RAISE EXCEPTION 'allocations % exceed payment amount %', allocated, pay.amount USING ERRCODE = 'BM005';
  END IF;

  IF NEW.sale_id IS NOT NULL THEN
    SELECT customer_id, status::text INTO tgt_customer, tgt_status FROM app.sales
     WHERE id = NEW.sale_id FOR UPDATE;
    IF tgt_status NOT IN ('draft', 'held', 'pending_approval', 'completed') THEN
      RAISE EXCEPTION 'sale is % and cannot take payments', tgt_status USING ERRCODE = 'BM001';
    END IF;
    UPDATE app.sales SET amount_paid = amount_paid + NEW.amount
     WHERE id = NEW.sale_id;      -- CHECK amount_paid <= total
  ELSE
    SELECT customer_id, status::text INTO tgt_customer, tgt_status FROM app.invoices
     WHERE id = NEW.invoice_id FOR UPDATE;
    IF tgt_status NOT IN ('issued', 'partially_paid') THEN
      RAISE EXCEPTION 'invoice is % and cannot take payments', tgt_status USING ERRCODE = 'BM001';
    END IF;
    UPDATE app.invoices
       SET amount_paid = amount_paid + NEW.amount,
           status = CASE WHEN amount_paid + NEW.amount = total THEN 'paid'::app.invoice_status ELSE 'partially_paid' END
     WHERE id = NEW.invoice_id;
  END IF;

  IF tgt_customer IS DISTINCT FROM pay.customer_id AND pay.customer_id IS NOT NULL THEN
    RAISE EXCEPTION 'payment customer does not match the document customer' USING ERRCODE = 'BM008';
  END IF;
  IF pay.customer_id IS NULL AND NEW.invoice_id IS NOT NULL THEN
    RAISE EXCEPTION 'invoice payments must name the customer' USING ERRCODE = 'BM008';
  END IF;
  RETURN NULL;
END $$;

CREATE OR REPLACE FUNCTION app.tg_payment_made_post() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE tt app.txn_type;
BEGIN
  IF TG_OP = 'INSERT' THEN
    tt := CASE WHEN NEW.supplier_id IS NOT NULL THEN 'supplier_payment'
               WHEN NEW.contractor_id IS NOT NULL THEN 'contractor_payment'
               WHEN NEW.customer_id IS NOT NULL THEN 'refund'
               ELSE 'other_payment' END;
    INSERT INTO app.financial_transactions (account_id, direction, amount, txn_type, business_unit_id,
           location_id, business_date, description, payment_made_id)
    VALUES (NEW.account_id, 'out', NEW.amount, tt, NEW.business_unit_id,
            NEW.location_id, NEW.business_date, 'Payment ' || NEW.payment_no, NEW.id);
  ELSIF NEW.status = 'voided' AND OLD.status = 'posted' THEN
    IF NOT (app.has_permission('finance.payment.void') OR app.approved_request_exists('payment_void', NEW.id)) THEN
      RAISE EXCEPTION 'voiding a payment requires Admin approval' USING ERRCODE = 'BM007';
    END IF;
    PERFORM app.assert_day_open(NULL, NEW.location_id, NEW.business_date);
    UPDATE app.purchases p SET amount_paid = p.amount_paid - a.amount
      FROM app.payment_made_allocations a
     WHERE a.payment_made_id = NEW.id AND p.id = a.purchase_id;
    UPDATE app.contractor_bills b SET amount_paid = b.amount_paid - a.amount,
           status = CASE WHEN b.amount_paid - a.amount = 0 THEN 'open'::app.bill_status ELSE 'partially_paid' END
      FROM app.payment_made_allocations a
     WHERE a.payment_made_id = NEW.id AND b.id = a.contractor_bill_id;
    PERFORM app.reverse_ledger(NULL, 'payment_made_id', NEW.id, NEW.business_date,
                               'payment ' || NEW.payment_no || ' voided');
  ELSIF (NEW.amount, NEW.account_id, NEW.method, NEW.business_date, NEW.location_id)
        IS DISTINCT FROM (OLD.amount, OLD.account_id, OLD.method, OLD.business_date, OLD.location_id) THEN
    RAISE EXCEPTION 'posted payments are immutable; void and re-enter' USING ERRCODE = 'BM004';
  END IF;
  RETURN NULL;
END $$;

CREATE OR REPLACE FUNCTION app.tg_payment_made_allocation_apply() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE
  pay app.payments_made%ROWTYPE;
  allocated numeric;
  refunded numeric;
  cap numeric;
  payee_ok boolean;
  tgt_status text;
BEGIN
  SELECT * INTO pay FROM app.payments_made WHERE id = NEW.payment_made_id FOR UPDATE;
  IF pay.status <> 'posted' THEN
    RAISE EXCEPTION 'cannot allocate a voided payment' USING ERRCODE = 'BM005';
  END IF;
  SELECT coalesce(sum(amount), 0) INTO allocated FROM app.payment_made_allocations
   WHERE payment_made_id = NEW.payment_made_id;
  IF allocated > pay.amount THEN
    RAISE EXCEPTION 'allocations % exceed payment amount %', allocated, pay.amount USING ERRCODE = 'BM005';
  END IF;

  IF NEW.purchase_id IS NOT NULL THEN
    SELECT supplier_id = pay.supplier_id, status::text
      INTO payee_ok, tgt_status
      FROM app.purchases
     WHERE id = NEW.purchase_id
       FOR UPDATE;
    IF NOT FOUND THEN
      RAISE EXCEPTION 'purchase not found' USING ERRCODE = 'BM008';
    END IF;
    IF tgt_status = 'cancelled' THEN
      RAISE EXCEPTION 'purchase is cancelled and cannot take payments' USING ERRCODE = 'BM001';
    END IF;
    UPDATE app.purchases SET amount_paid = amount_paid + NEW.amount
     WHERE id = NEW.purchase_id;
  ELSIF NEW.contractor_bill_id IS NOT NULL THEN
    SELECT contractor_id = pay.contractor_id, status::text
      INTO payee_ok, tgt_status
      FROM app.contractor_bills
     WHERE id = NEW.contractor_bill_id
       FOR UPDATE;
    IF NOT FOUND THEN
      RAISE EXCEPTION 'contractor bill not found' USING ERRCODE = 'BM008';
    END IF;
    IF tgt_status NOT IN ('open', 'partially_paid') THEN
      RAISE EXCEPTION 'contractor bill is % and cannot take payments', tgt_status USING ERRCODE = 'BM001';
    END IF;
    UPDATE app.contractor_bills
       SET amount_paid = amount_paid + NEW.amount,
           status = CASE WHEN amount_paid + NEW.amount = amount THEN 'paid'::app.bill_status ELSE 'partially_paid' END
     WHERE id = NEW.contractor_bill_id;
  ELSE
    SELECT r.status = 'completed' AND s.customer_id IS NOT DISTINCT FROM pay.customer_id, r.refund_amount
      INTO payee_ok, cap
      FROM app.sale_returns r
      JOIN app.sales s ON s.id = r.sale_id
     WHERE r.id = NEW.sale_return_id
       FOR UPDATE OF r;
    IF NOT FOUND THEN
      RAISE EXCEPTION 'sale return not found' USING ERRCODE = 'BM008';
    END IF;
    SELECT coalesce(sum(a.amount), 0) INTO refunded
      FROM app.payment_made_allocations a
      JOIN app.payments_made m ON m.id = a.payment_made_id AND m.status = 'posted'
     WHERE a.sale_return_id = NEW.sale_return_id;
    IF refunded > cap THEN
      RAISE EXCEPTION 'refund payments % exceed approved refund %', refunded, cap USING ERRCODE = 'BM005';
    END IF;
  END IF;

  IF NOT coalesce(payee_ok, false) THEN
    RAISE EXCEPTION 'payee does not match the document' USING ERRCODE = 'BM008';
  END IF;
  RETURN NULL;
END $$;

CREATE OR REPLACE FUNCTION app.tg_expense_post() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
BEGIN
  IF NEW.status = 'paid' AND OLD.status <> 'paid' THEN
    INSERT INTO app.financial_transactions (account_id, direction, amount, txn_type, business_unit_id,
           location_id, business_date, description, project_id, design_job_id, expense_id)
    VALUES (NEW.account_id, 'out', NEW.amount, 'expense', NEW.business_unit_id, NEW.location_id,
            NEW.paid_business_date, left('Expense ' || NEW.expense_no || ': ' || NEW.description, 500),
            NEW.project_id, NEW.design_job_id, NEW.id);
  END IF;
  RETURN NULL;
END $$;

CREATE OR REPLACE FUNCTION app.tg_account_transfer_post() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE bu uuid;
BEGIN
  SELECT business_unit_id INTO bu FROM app.locations WHERE id = NEW.location_id;
  INSERT INTO app.financial_transactions (account_id, direction, amount, txn_type, business_unit_id,
         location_id, business_date, description, account_transfer_id)
  VALUES (NEW.from_account_id, 'out', NEW.amount, 'transfer', bu, NEW.location_id, NEW.business_date,
          'Transfer ' || NEW.transfer_no, NEW.id),
         (NEW.to_account_id,   'in',  NEW.amount, 'transfer', bu, NEW.location_id, NEW.business_date,
          'Transfer ' || NEW.transfer_no, NEW.id);
  RETURN NULL;
END $$;

CREATE OR REPLACE FUNCTION app.tg_payroll_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE sod boolean;
BEGIN
  IF NEW.status = 'approved' AND OLD.status = 'draft' THEN
    PERFORM app.assert_can_approve('payroll');
    NEW.approved_by := coalesce(NEW.approved_by, app.current_membership_id());
    NEW.approved_at := coalesce(NEW.approved_at, now());
    SELECT require_sod_payroll INTO sod FROM app.business_settings;
    IF sod AND NEW.approved_by = NEW.generated_by THEN
      RAISE EXCEPTION 'payroll must be approved by someone other than who generated it' USING ERRCODE = 'BM007';
    END IF;
  END IF;
  IF NEW.status = 'paid' AND OLD.status = 'approved' THEN
    PERFORM app.assert_can_approve('payroll');
    NEW.paid_by := coalesce(NEW.paid_by, app.current_membership_id());
    NEW.paid_at := coalesce(NEW.paid_at, now());
  END IF;
  RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION app.tg_payroll_post() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
BEGIN
  IF NEW.status = 'paid' AND OLD.status = 'approved' THEN
    INSERT INTO app.financial_transactions (account_id, direction, amount, txn_type, business_unit_id,
           location_id, business_date, description, payroll_line_id)
    SELECT NEW.account_id, 'out', l.net_pay, 'salary', NEW.business_unit_id, NEW.location_id,
           NEW.paid_business_date, format('Salary %s-%s', NEW.period_year, lpad(NEW.period_month::text, 2, '0')), l.id
      FROM app.payroll_lines l
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

CREATE OR REPLACE FUNCTION app.tg_payroll_lines_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE st app.payroll_status;
BEGIN
  IF current_user = 'bms_owner' AND TG_OP <> 'INSERT' THEN RETURN coalesce(NEW, OLD); END IF;
  SELECT status INTO st FROM app.payroll_runs
   WHERE id = coalesce(NEW.payroll_run_id, OLD.payroll_run_id);
  IF st IS DISTINCT FROM 'draft' THEN
    RAISE EXCEPTION 'payroll lines are locked once the run leaves draft' USING ERRCODE = 'BM004';
  END IF;
  RETURN coalesce(NEW, OLD);
END $$;

CREATE OR REPLACE FUNCTION app.tg_project_stage_history() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
BEGIN
  INSERT INTO app.project_stage_history (project_stage_id, from_status, to_status, note)
  VALUES (NEW.id, OLD.status, NEW.status, nullif(current_setting('app.change_note', true), ''));
  -- First stage started => project in progress.
  UPDATE app.projects SET status = 'in_progress'
   WHERE id = NEW.project_id AND status = 'not_started' AND NEW.status = 'in_progress';
  RETURN NULL;
END $$;

CREATE OR REPLACE FUNCTION app.tg_project_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.status = 'completed' AND OLD.status <> 'completed' THEN
    IF EXISTS (SELECT 1 FROM app.project_stages WHERE project_id = NEW.id AND status <> 'done') THEN
      RAISE EXCEPTION 'all stages must be done before the project is completed' USING ERRCODE = 'BM001';
    END IF;
    NEW.completed_at := coalesce(NEW.completed_at, now());
  END IF;
  IF NEW.status = 'cancelled' AND OLD.status <> 'cancelled' THEN NEW.cancelled_at := coalesce(NEW.cancelled_at, now()); END IF;
  IF NEW.total_price <> OLD.total_price
     AND NOT (app.has_permission('services.price.approve') OR app.approved_request_exists('job_price_change', NEW.id)) THEN
    RAISE EXCEPTION 'changing a job price requires Manager approval' USING ERRCODE = 'BM007';
  END IF;
  RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION app.tg_design_version_post() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
BEGIN
  IF NEW.status = 'sent_to_client' AND OLD.status <> 'sent_to_client' THEN
    UPDATE app.design_jobs SET status = 'with_client'
     WHERE id = NEW.design_job_id AND status IN ('in_design', 'internal_review');
  ELSIF NEW.status = 'approved' AND OLD.status <> 'approved' THEN
    UPDATE app.design_versions SET status = 'superseded'
     WHERE design_job_id = NEW.design_job_id AND id <> NEW.id
       AND status IN ('draft', 'sent_to_client');
    UPDATE app.design_jobs SET status = 'approved', final_version_id = NEW.id, approved_at = now()
     WHERE id = NEW.design_job_id;
  ELSIF NEW.status = 'changes_requested' AND OLD.status <> 'changes_requested' THEN
    UPDATE app.design_jobs SET revisions_used = revisions_used + 1, status = 'in_design'
     WHERE id = NEW.design_job_id;
  END IF;
  RETURN NULL;
END $$;

CREATE OR REPLACE FUNCTION app.tg_design_link_decide() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
BEGIN
  IF OLD.decision IS NOT NULL AND NEW.decision IS DISTINCT FROM OLD.decision THEN
    RAISE EXCEPTION 'this approval link has already been used' USING ERRCODE = 'BM004';
  END IF;
  IF NEW.decision IS NOT NULL AND OLD.decision IS NULL THEN
    IF OLD.revoked_at IS NOT NULL OR OLD.expires_at <= now() THEN
      RAISE EXCEPTION 'approval link expired or revoked' USING ERRCODE = 'BM001';
    END IF;
    NEW.decided_at := now();
    UPDATE app.design_versions SET status = NEW.decision::text::app.design_version_status
     WHERE id = NEW.design_version_id AND status = 'sent_to_client';
    IF NOT FOUND THEN
      RAISE EXCEPTION 'version is no longer awaiting the client' USING ERRCODE = 'BM001';
    END IF;
  END IF;
  RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION app.tg_branding_stage_rollup() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
BEGIN
  IF NEW.project_stage_id IS NOT NULL AND NEW.status IN ('approved', 'printing', 'delivered')
     AND NOT EXISTS (SELECT 1 FROM app.design_jobs j
                      WHERE j.project_stage_id = NEW.project_stage_id
                        AND j.status NOT IN ('approved', 'printing', 'delivered', 'cancelled')) THEN
    UPDATE app.project_stages SET status = 'done'
     WHERE id = NEW.project_stage_id AND is_branding_stage AND status <> 'done';
  END IF;
  RETURN NULL;
END $$;

CREATE OR REPLACE FUNCTION app.tg_service_package_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM app.services WHERE id = NEW.package_service_id AND is_package) THEN
    RAISE EXCEPTION 'parent service is not a package' USING ERRCODE = 'BM008';
  END IF;
  IF EXISTS (SELECT 1 FROM app.services WHERE id = NEW.component_service_id AND is_package) THEN
    RAISE EXCEPTION 'packages cannot contain packages' USING ERRCODE = 'BM008';
  END IF;
  RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION app.tg_leave_decide() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.status IN ('approved', 'rejected') AND OLD.status = 'requested' THEN
    PERFORM app.assert_can_approve('leave');
    NEW.decided_by := coalesce(NEW.decided_by, app.current_membership_id());
    NEW.decided_at := coalesce(NEW.decided_at, now());
    IF EXISTS (SELECT 1 FROM app.employees e WHERE e.id = NEW.employee_id
                AND e.membership_id = NEW.decided_by) THEN
      RAISE EXCEPTION 'you cannot decide your own leave' USING ERRCODE = 'BM007';
    END IF;
  END IF;
  RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION app.is_self_employee(p_employee uuid) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog, public AS $$
  SELECT EXISTS (SELECT 1 FROM app.employees e
                  WHERE e.id = p_employee AND e.membership_id = app.current_membership_id())
$$;

CREATE OR REPLACE FUNCTION app.expected_account_balances(p_location uuid, p_date date)
RETURNS TABLE (account_id uuid, account_name text, kind app.account_kind, expected numeric)
LANGUAGE sql STABLE AS $$
  SELECT a.id, a.name::text, a.kind,
         coalesce(sum(CASE t.direction WHEN 'in' THEN t.amount ELSE -t.amount END), 0)
    FROM app.financial_accounts a
    LEFT JOIN app.financial_transactions t ON t.account_id = a.id AND t.business_date <= p_date
   WHERE a.status = 'active'
     AND (a.location_id = p_location OR a.location_id IS NULL)
   GROUP BY a.id, a.name, a.kind
$$;

CREATE OR REPLACE FUNCTION app.apply_advance_cash_repayment(
  p_advance uuid, p_amount numeric, p_account uuid, p_location uuid, p_date date, p_description text
) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE adv record; bu uuid;
BEGIN
  SELECT * INTO adv FROM app.employee_advances WHERE id = p_advance FOR UPDATE;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'advance not found' USING ERRCODE = 'BM008';
  END IF;
  IF adv.status <> 'active' THEN
    RAISE EXCEPTION 'only an active advance can be repaid' USING ERRCODE = 'BM004';
  END IF;
  IF p_amount <= 0 OR p_amount > (adv.principal - adv.amount_repaid) THEN
    RAISE EXCEPTION 'repayment exceeds the remaining balance' USING ERRCODE = 'BM005';
  END IF;
  SELECT business_unit_id INTO bu FROM app.locations WHERE id = p_location;
  INSERT INTO app.financial_transactions
    (account_id, direction, amount, txn_type, business_unit_id, location_id, business_date, description)
  VALUES (p_account, 'in', p_amount, 'other_payment', bu, p_location, p_date, left(p_description, 500));
  UPDATE app.employee_advances
     SET amount_repaid = amount_repaid + p_amount,
         status = CASE WHEN amount_repaid + p_amount = principal THEN 'settled'::app.advance_status ELSE status END
   WHERE id = adv.id;
END $$;

CREATE OR REPLACE FUNCTION app.tg_invoice_lines_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE st app.invoice_status;
BEGIN
  SELECT status INTO st FROM app.invoices WHERE id = coalesce(NEW.invoice_id, OLD.invoice_id);
  IF st IN ('issued', 'partially_paid', 'paid', 'void') THEN
    RAISE EXCEPTION 'lines of an issued invoice are read-only' USING ERRCODE = 'BM004';
  END IF;
  RETURN coalesce(NEW, OLD);
END $$;

CREATE OR REPLACE FUNCTION app.tg_invoices_check_totals() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE inv app.invoices%ROWTYPE; lines_total numeric; n_lines integer;
BEGIN
  SELECT * INTO inv FROM app.invoices WHERE id = NEW.id;
  IF NOT FOUND OR inv.status NOT IN ('issued', 'partially_paid', 'paid') THEN RETURN NULL; END IF;
  SELECT coalesce(sum(line_total), 0), count(*) INTO lines_total, n_lines
    FROM app.invoice_lines WHERE invoice_id = inv.id;
  IF n_lines = 0 THEN
    RAISE EXCEPTION 'issued invoice % has no lines', inv.invoice_no USING ERRCODE = 'BM008';
  END IF;
  IF lines_total <> inv.subtotal THEN
    RAISE EXCEPTION 'invoice % subtotal % <> sum of lines %', inv.invoice_no, inv.subtotal, lines_total USING ERRCODE = 'BM008';
  END IF;
  RETURN NULL;
END $$;

CREATE OR REPLACE FUNCTION app.tg_purchase_lines_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE st app.purchase_status;
BEGIN
  SELECT status INTO st FROM app.purchases WHERE id = coalesce(NEW.purchase_id, OLD.purchase_id);
  IF st IN ('partially_received', 'received', 'cancelled') THEN
    IF TG_OP <> 'UPDATE' OR current_user <> 'bms_owner'
       OR (NEW.product_id, NEW.quantity_ordered, NEW.unit_cost, NEW.line_total, NEW.purchase_id)
          IS DISTINCT FROM (OLD.product_id, OLD.quantity_ordered, OLD.unit_cost, OLD.line_total, OLD.purchase_id) THEN
      RAISE EXCEPTION 'lines of a received or cancelled purchase are read-only' USING ERRCODE = 'BM004';
    END IF;
  END IF;
  RETURN coalesce(NEW, OLD);
END $$;

CREATE OR REPLACE FUNCTION app.tg_product_no_delete_with_history() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
BEGIN
  IF EXISTS (SELECT 1 FROM app.stock_movements WHERE product_id = OLD.id)
     OR EXISTS (SELECT 1 FROM app.sale_lines WHERE product_id = OLD.id)
     OR EXISTS (SELECT 1 FROM app.purchase_lines WHERE product_id = OLD.id) THEN
    RAISE EXCEPTION 'products with historical transactions cannot be deleted; deactivate instead' USING ERRCODE = 'BM004';
  END IF;
  RETURN OLD;
END $$;

CREATE OR REPLACE FUNCTION app.tg_advance_repayments_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE st app.payroll_status;
BEGIN
  IF current_user = 'bms_owner' THEN RETURN coalesce(NEW, OLD); END IF;
  SELECT r.status INTO st
    FROM app.payroll_lines l
    JOIN app.payroll_runs r ON r.id = l.payroll_run_id
   WHERE l.id = coalesce(NEW.payroll_line_id, OLD.payroll_line_id);
  IF st IS DISTINCT FROM 'draft' THEN
    RAISE EXCEPTION 'loan repayments are locked once the payroll run leaves draft' USING ERRCODE = 'BM004';
  END IF;
  IF TG_OP = 'UPDATE' AND NEW.payroll_line_id IS DISTINCT FROM OLD.payroll_line_id THEN
    RAISE EXCEPTION 'a loan repayment cannot be moved to another payroll line' USING ERRCODE = 'BM004';
  END IF;
  RETURN coalesce(NEW, OLD);
END $$;

CREATE OR REPLACE FUNCTION app.tg_day_closing_lines_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE st app.day_status;
BEGIN
  IF current_user = 'bms_owner' THEN RETURN coalesce(NEW, OLD); END IF;
  SELECT status INTO st FROM app.day_closings
   WHERE id = coalesce(NEW.day_closing_id, OLD.day_closing_id);
  IF st IS NULL OR st = 'closed' THEN
    RAISE EXCEPTION 'lines of a closed day are read-only; reopen the day first' USING ERRCODE = 'BM004';
  END IF;
  IF TG_OP = 'UPDATE' AND NEW.day_closing_id IS DISTINCT FROM OLD.day_closing_id THEN
    RAISE EXCEPTION 'a closing line cannot be moved to another day' USING ERRCODE = 'BM004';
  END IF;
  RETURN coalesce(NEW, OLD);
END $$;

CREATE OR REPLACE FUNCTION app.tg_sale_return_lines_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE st app.return_status;
BEGIN
  IF current_user = 'bms_owner' THEN RETURN NEW; END IF;
  IF TG_OP = 'UPDATE' THEN
    RAISE EXCEPTION 'return lines are read-only; cancel the return and raise a new one' USING ERRCODE = 'BM004';
  END IF;
  SELECT status INTO st FROM app.sale_returns WHERE id = NEW.sale_return_id;
  IF st IS DISTINCT FROM 'requested' THEN
    RAISE EXCEPTION 'lines can only be added to a return that is still requested' USING ERRCODE = 'BM004';
  END IF;
  RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION app.tg_customer_archive_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF (NEW.archived_at IS NOT NULL AND OLD.archived_at IS NULL)
     OR (NEW.client_status = 'inactive' AND OLD.client_status IS DISTINCT FROM 'inactive') THEN
    IF EXISTS (SELECT 1 FROM app.sales s WHERE s.customer_id = NEW.id
                  AND s.status = 'completed' AND s.balance_due > 0)
       OR EXISTS (SELECT 1 FROM app.invoices i WHERE i.customer_id = NEW.id
                  AND i.status IN ('issued', 'partially_paid') AND i.balance_due > 0) THEN
      RAISE EXCEPTION 'this customer still owes money. Collect or void the unpaid documents first'
        USING ERRCODE = 'BM001';
    END IF;
  END IF;
  RETURN NEW;
END $$;

-- ---------- Role rules that named the tenant in a join ------------------
CREATE POLICY payslip_read ON app.payslips AS RESTRICTIVE FOR SELECT
  USING ((SELECT app.has_permission('hr.payroll.view'))
      OR (SELECT app.has_permission('hr.payroll.approve'))
      OR EXISTS (SELECT 1 FROM app.payroll_lines l
                  WHERE l.id = payslips.payroll_line_id AND app.is_self_employee(l.employee_id)));
CREATE POLICY statutory_read ON app.payroll_statutory_items AS RESTRICTIVE FOR SELECT
  USING ((SELECT app.has_permission('hr.payroll.view'))
         OR EXISTS (SELECT 1 FROM app.payroll_lines l
                     WHERE l.id = payroll_statutory_items.payroll_line_id AND app.is_self_employee(l.employee_id)));
CREATE POLICY repayment_read ON app.advance_repayments AS RESTRICTIVE FOR SELECT
  USING ((SELECT app.has_permission('hr.payroll.view'))
         OR EXISTS (SELECT 1 FROM app.employee_advances a
                     WHERE a.id = advance_repayments.advance_id AND app.is_self_employee(a.employee_id)));
CREATE POLICY audit_read ON audit.audit_log FOR SELECT
  USING ((SELECT app.has_permission('security.audit.view')));

-- ---------- Reporting / screen views ------------------------------------
-- Staff product catalogue: NO buying price / cost (spec §2A).
CREATE VIEW app.v_staff_catalog WITH (security_invoker = true) AS
SELECT p.id AS product_id, p.name, p.barcode, p.sku, p.category_id, c.name AS category_name,
       p.selling_price, p.photo_file_id, p.tracks_serial, p.warranty_months,
       b.location_id, coalesce(b.quantity_on_hand, 0) AS quantity_on_hand,
       coalesce(b.quantity_on_hand, 0) <= coalesce(p.min_stock_level, s.low_stock_default) AS is_low_stock
  FROM app.products p
 CROSS JOIN app.business_settings s
  LEFT JOIN app.product_categories c ON c.id = p.category_id
  LEFT JOIN app.stock_balances b ON b.product_id = p.id
 WHERE p.status = 'active';

CREATE VIEW app.v_stock_valuation WITH (security_invoker = true) AS
SELECT b.location_id, p.id AS product_id, p.name, b.quantity_on_hand, p.average_cost,
       round(b.quantity_on_hand * p.average_cost, 2) AS stock_value,
       coalesce(p.min_stock_level, s.low_stock_default) AS min_level,
       b.quantity_on_hand <= coalesce(p.min_stock_level, s.low_stock_default) AS is_low_stock
  FROM app.stock_balances b
  JOIN app.products p ON p.id = b.product_id
 CROSS JOIN app.business_settings s;

-- Debts: owed to us (credit sales + invoices), aged 0-30 / 31-60 / >60.
CREATE VIEW app.v_receivables WITH (security_invoker = true) AS
SELECT 'sale'::text AS doc_kind, s.id AS doc_id, s.receipt_no AS doc_no, s.customer_id,
       s.business_unit_id, s.business_date AS doc_date, s.due_date, s.total, s.amount_paid, s.balance_due,
       current_date - coalesce(s.due_date, s.business_date) AS days_overdue,
       CASE WHEN current_date - s.business_date <= 30 THEN '0-30'
            WHEN current_date - s.business_date <= 60 THEN '31-60' ELSE '60+' END AS age_bucket
  FROM app.sales s WHERE s.status = 'completed' AND s.balance_due > 0
UNION ALL
SELECT 'invoice', i.id, i.invoice_no, i.customer_id, i.business_unit_id, i.issue_date, i.due_date,
       i.total, i.amount_paid, i.balance_due, current_date - i.due_date,
       CASE WHEN current_date - i.issue_date <= 30 THEN '0-30'
            WHEN current_date - i.issue_date <= 60 THEN '31-60' ELSE '60+' END
  FROM app.invoices i WHERE i.status IN ('issued', 'partially_paid') AND i.balance_due > 0;

CREATE VIEW app.v_payables WITH (security_invoker = true) AS
SELECT 'purchase'::text AS doc_kind, p.id AS doc_id, p.purchase_no AS doc_no,
       p.supplier_id, NULL::uuid AS contractor_id, p.order_date AS doc_date, p.due_date, p.total, p.amount_paid, p.balance_due,
       CASE WHEN current_date - p.order_date <= 30 THEN '0-30'
            WHEN current_date - p.order_date <= 60 THEN '31-60' ELSE '60+' END AS age_bucket
  FROM app.purchases p WHERE p.status <> 'cancelled' AND p.balance_due > 0
UNION ALL
SELECT 'contractor_bill', b.id, b.bill_no, NULL, b.contractor_id, b.bill_date, b.due_date,
       b.amount, b.amount_paid, b.balance_due,
       CASE WHEN current_date - b.bill_date <= 30 THEN '0-30'
            WHEN current_date - b.bill_date <= 60 THEN '31-60' ELSE '60+' END
  FROM app.contractor_bills b WHERE b.status IN ('open', 'partially_paid');

-- Project profit = payments received - costs (spec §7 Costs tab, §10 Project Profit).
CREATE VIEW app.v_project_financials WITH (security_invoker = true) AS
SELECT pr.id AS project_id, pr.project_no, pr.title, pr.customer_id, pr.status, pr.total_price,
       coalesce(inv.invoiced, 0) AS invoiced,
       coalesce(inv.received, 0) AS received,
       coalesce(ex.costs, 0) + coalesce(cb.costs, 0) AS costs,
       coalesce(inv.received, 0) - coalesce(ex.costs, 0) - coalesce(cb.costs, 0) AS profit,
       pr.total_price - coalesce(inv.received, 0) AS balance_due
  FROM app.projects pr
  LEFT JOIN LATERAL (SELECT sum(i.total) invoiced, sum(i.amount_paid) received FROM app.invoices i
                      WHERE i.project_id = pr.id AND i.status <> 'void') inv ON true
  LEFT JOIN LATERAL (SELECT sum(e.amount) costs FROM app.expenses e
                      WHERE e.project_id = pr.id AND e.status IN ('approved', 'paid')) ex ON true
  LEFT JOIN LATERAL (SELECT sum(b.amount) costs FROM app.contractor_bills b
                      WHERE b.project_id = pr.id AND b.status <> 'void') cb ON true;

CREATE VIEW app.v_design_job_financials WITH (security_invoker = true) AS
SELECT j.id AS design_job_id, j.job_no, j.title, j.designer_id, j.status, j.price,
       coalesce(inv.received, 0) AS received,
       coalesce(ex.costs, 0) + coalesce(cb.costs, 0) AS costs,
       coalesce(inv.received, 0) - coalesce(ex.costs, 0) - coalesce(cb.costs, 0) AS profit,
       j.revisions_used, j.revisions_included, j.revisions_used > j.revisions_included AS over_revisions
  FROM app.design_jobs j
  LEFT JOIN LATERAL (SELECT sum(i.amount_paid) received FROM app.invoices i
                      WHERE i.design_job_id = j.id AND i.status <> 'void') inv ON true
  LEFT JOIN LATERAL (SELECT sum(e.amount) costs FROM app.expenses e
                      WHERE e.design_job_id = j.id AND e.status IN ('approved', 'paid')) ex ON true
  LEFT JOIN LATERAL (SELECT sum(b.amount) costs FROM app.contractor_bills b
                      WHERE b.design_job_id = j.id AND b.status <> 'void') cb ON true;

-- Dashboard "Approvals" widget: one list across all request sources.
CREATE VIEW app.v_pending_approvals WITH (security_invoker = true) AS
SELECT 'approval_request'::text AS source, id, approval_type::text AS kind, requested_by,
       requested_at, requested_amount AS amount, requested_percent AS percent, reason
  FROM app.approval_requests WHERE status = 'pending'
UNION ALL
SELECT 'expense', id, 'expense', requested_by, created_at, amount, NULL, description
  FROM app.expenses WHERE status = 'submitted'
UNION ALL
SELECT 'sale_return', id, 'refund', requested_by, created_at, refund_amount, NULL, reason
  FROM app.sale_returns WHERE status = 'requested'
UNION ALL
SELECT 'leave_request', id, 'leave', created_by, created_at, NULL, NULL, reason
  FROM app.leave_requests WHERE status = 'requested';

-- Daily sales summary by method (End-of-Day screen, dashboard cards).
CREATE VIEW app.v_daily_takings WITH (security_invoker = true) AS
SELECT pr.location_id, pr.business_date, pr.business_unit_id, pr.method,
       count(*) AS payments, sum(pr.amount) AS amount
  FROM app.payments_received pr WHERE pr.status = 'posted'
 GROUP BY pr.location_id, pr.business_date, pr.business_unit_id, pr.method;

-- Management P&L by month and business unit (accrual basis for sales/invoices; A-12).
CREATE VIEW app.v_profit_and_loss_monthly WITH (security_invoker = true) AS
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
  SELECT r.business_unit_id, make_date(r.period_year, r.period_month, 1), 'payroll', l.gross_pay
    FROM app.payroll_runs r JOIN app.payroll_lines l ON l.payroll_run_id = r.id
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

GRANT SELECT ON app.v_staff_catalog, app.v_stock_valuation, app.v_receivables, app.v_payables,
  app.v_project_financials, app.v_design_job_financials, app.v_pending_approvals,
  app.v_daily_takings, app.v_profit_and_loss_monthly TO bms_app, bms_readonly;
GRANT SELECT ON app.v_pending_approvals TO bms_worker;

-- ---------- Grants and audit for the new table --------------------------
GRANT SELECT ON app.business_profile TO bms_app, bms_worker, bms_readonly;
GRANT UPDATE (name, timezone, default_currency, default_locale, updated_at, updated_by, row_version)
  ON app.business_profile TO bms_app;
CREATE TRIGGER zz_audit AFTER INSERT OR UPDATE OR DELETE ON app.business_profile
  FOR EACH ROW EXECUTE FUNCTION audit.tg_capture();

-- ---------- Remove the tenant machinery ---------------------------------
DROP FUNCTION app.provision_tenant(text, text, uuid, text, text, text);
DROP FUNCTION app.tg_freeze_keys();
DROP TABLE app.tenants;
DROP FUNCTION IF EXISTS app.require_tenant();
DROP FUNCTION app.current_tenant_id();
DROP TYPE app.tenant_status;

-- ---------- First-run setup ---------------------------------------------
-- Creates the business, its units, main shop, roles and numbering, and makes the given user its
-- Admin. The worker role calls it once; a second call fails on the single-row key.
CREATE FUNCTION app.provision_business(
  p_name text, p_owner_user uuid, p_phone text, p_email text, p_currency text, p_timezone text)
RETURNS TABLE (membership_id uuid, location_id uuid)
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
#variable_conflict use_column
DECLARE
  bu_a uuid; loc uuid; r_admin uuid; r_mgr uuid; r_staff uuid; m uuid;
BEGIN
  -- First-run setup creates an administrator without signing in, so it must never run on a business
  -- that already has people.
  IF EXISTS (SELECT 1 FROM app.memberships) THEN
    RAISE EXCEPTION 'setup has already been completed' USING ERRCODE = 'unique_violation';
  END IF;
  INSERT INTO app.business_profile (name, default_currency, timezone)
  VALUES (p_name, coalesce(nullif(p_currency, ''), 'TZS')::app.currency,
          coalesce(nullif(p_timezone, ''), 'Africa/Dar_es_Salaam'));
  INSERT INTO app.business_settings (business_name, phone, email, currency)
  VALUES (p_name, nullif(p_phone, '')::app.phone_e164, nullif(p_email, '')::app.email,
          coalesce(nullif(p_currency, ''), 'TZS')::app.currency);

  INSERT INTO app.business_units (code, name, kind) VALUES ('A', 'Shop', 'retail_shop') RETURNING id INTO bu_a;
  INSERT INTO app.business_units (code, name, kind) VALUES ('B', 'Services', 'professional_services');
  INSERT INTO app.locations (business_unit_id, name, kind, is_default)
  VALUES (bu_a, 'Main shop', 'shop', true) RETURNING id INTO loc;

  INSERT INTO app.roles (code, name, is_system, rank) VALUES ('admin', 'Admin', true, 100) RETURNING id INTO r_admin;
  INSERT INTO app.roles (code, name, is_system, rank) VALUES ('manager', 'Manager', true, 60) RETURNING id INTO r_mgr;
  INSERT INTO app.roles (code, name, is_system, rank) VALUES ('staff', 'Staff', true, 20) RETURNING id INTO r_staff;

  -- Admin: everything.
  INSERT INTO app.role_permissions (role_id, permission_code) SELECT r_admin, code FROM app.permissions;
  -- Manager: spec §2A defaults.
  INSERT INTO app.role_permissions (role_id, permission_code)
  SELECT r_mgr, code FROM app.permissions WHERE code IN (
    'dashboard.operations.view', 'shop.sale.create', 'shop.sale.view_all', 'shop.sale.request_void',
    'shop.discount.apply', 'shop.discount.approve', 'shop.return.create', 'shop.return.approve',
    'shop.product.view', 'shop.product.view_cost', 'shop.product.manage', 'shop.stock.view', 'shop.stock.manage',
    'shop.customer.view_all', 'shop.customer.manage', 'shop.supplier.manage', 'shop.purchase.manage',
    'services.client.view_all', 'services.client.manage', 'services.project.view_all', 'services.project.manage',
    'services.project.assign', 'services.catalog.manage', 'services.quotation.manage', 'services.invoice.manage',
    'services.document.manage', 'services.price.approve', 'services.renewal.manage', 'services.contractor.manage',
    'services.design.view_all', 'services.design.manage', 'services.task.view_all', 'services.task.manage',
    'finance.payment.receive', 'finance.payment.view_all', 'finance.payment.make', 'finance.expense.enter',
    'finance.expense.submit', 'finance.expense.approve', 'finance.expense.pay', 'finance.debt.view', 'finance.day.close',
    'hr.employee.view', 'hr.attendance.manage', 'hr.leave.approve', 'reports.operations.view', 'reports.export');
  -- Staff: sell, view shared customers/clients, register jobs, own work.
  INSERT INTO app.role_permissions (role_id, permission_code)
  SELECT r_staff, code FROM app.permissions WHERE code IN (
    'shop.sale.create', 'shop.discount.apply', 'shop.return.create', 'shop.product.view',
    'shop.customer.view_all', 'shop.customer.manage',
    'services.client.view_all', 'services.client.manage', 'services.project.manage',
    'services.quotation.draft', 'services.invoice.draft',
    'services.document.manage', 'services.design.manage', 'services.task.manage',
    'finance.payment.receive', 'finance.expense.submit');

  -- Safe provisional limits (A-09): until the Admin sets them, staff discounts
  -- always need approval and Manager approvals of money escalate to the Admin.
  INSERT INTO app.authority_limits (role_id, limit_code, limit_value) VALUES
    (r_staff, 'sale_discount_pct', 0),
    (r_mgr,   'sale_discount_pct', 0),
    (r_mgr,   'discount_approve_pct', 0),
    (r_mgr,   'refund_approve_amount', 0),
    (r_mgr,   'expense_approve_amount', 0),
    (r_mgr,   'price_change_approve_pct', 0);

  INSERT INTO app.memberships (user_id, status, default_location_id, joined_at)
  VALUES (p_owner_user, 'active', loc, now()) RETURNING id INTO m;
  INSERT INTO app.membership_roles (membership_id, role_id) VALUES (m, r_admin);

  INSERT INTO app.document_sequences (doc_type, prefix) VALUES
    ('sale_receipt', 'R-'), ('payment_receipt', 'RC-'), ('sale_return', 'RT-'), ('invoice', 'INV-'),
    ('quotation', 'Q-'), ('project', 'PRJ-'), ('design_job', 'DJ-'), ('purchase', 'PO-'),
    ('goods_receipt', 'GRN-'), ('stock_adjustment', 'ADJ-'), ('stock_transfer', 'TRF-'),
    ('expense', 'EXP-'), ('payment_made', 'PM-'), ('account_transfer', 'AT-'), ('payslip', 'PS-'),
    ('contractor_bill', 'CB-'), ('employee', 'EMP-');

  INSERT INTO app.notification_rules (notification_type)
  SELECT nt FROM unnest(enum_range(NULL::app.notification_type)) nt;

  PERFORM audit.log_event('business.setup', 'business_profile', NULL, 'success', NULL,
                          jsonb_build_object('name', p_name), 'system');
  RETURN QUERY SELECT m, loc;
END $$;
REVOKE EXECUTE ON FUNCTION app.provision_business(text, uuid, text, text, text, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION app.provision_business(text, uuid, text, text, text, text) TO bms_worker;

-- ---------- Close the chain of entries written before this migration -----
-- Nothing is added to that chain again, so its last entries are sealed now or never.
SELECT audit.seal_tenant(k.legacy_scope, interval '0 seconds')
  FROM (SELECT DISTINCT legacy_scope FROM audit.audit_log WHERE legacy_scope IS NOT NULL) k;

-- ---------- Nothing may be left behind or lost --------------------------
DO $$
DECLARE missing text; expected_role_rules integer := 26;
BEGIN
  IF EXISTS (SELECT 1 FROM app.memberships) AND NOT EXISTS (SELECT 1 FROM app.business_profile) THEN
    RAISE EXCEPTION 'the business record was not copied';
  END IF;

  SELECT string_agg(table_schema || '.' || table_name, ', ') INTO missing
    FROM information_schema.columns WHERE table_schema IN ('app', 'iam', 'audit') AND column_name = 'tenant_id';
  IF missing IS NOT NULL THEN RAISE EXCEPTION 'tenant_id remains on: %', missing; END IF;

  SELECT string_agg(n.nspname || '.' || p.proname, ', ') INTO missing
    FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
   WHERE n.nspname IN ('app', 'iam', 'audit')
     AND p.prosrc ~ '\mtenant_id\M|current_tenant_id|require_tenant|app\.tenants|tenant_settings|tenant_setting_values';
  IF missing IS NOT NULL THEN RAISE EXCEPTION 'functions still refer to tenants: %', missing; END IF;

  SELECT string_agg(t.conname, ', ') INTO missing
    FROM tenant_constraints t
   WHERE t.contype IN ('p', 'u', 'f', 'x')
     AND t.definition !~ '\(tenant_id\)'
     AND NOT EXISTS (SELECT 1 FROM pg_constraint c JOIN pg_namespace n ON n.oid = c.connamespace
                      WHERE c.conname = t.conname AND n.nspname = t.table_schema);
  IF missing IS NOT NULL THEN RAISE EXCEPTION 'constraints were not rebuilt: %', missing; END IF;

  SELECT string_agg(t.index_name, ', ') INTO missing
    FROM tenant_indexes t
   WHERE (t.indisunique OR t.definition !~ '\((tenant_id|legacy_scope)\)')
     AND to_regclass(format('%I.%I', t.table_schema, t.index_name)) IS NULL;
  IF missing IS NOT NULL THEN RAISE EXCEPTION 'indexes were not rebuilt: %', missing; END IF;

  IF (SELECT count(*) FROM pg_policies WHERE schemaname = 'app' AND permissive = 'RESTRICTIVE') <> expected_role_rules THEN
    RAISE EXCEPTION 'expected % role rules, found %', expected_role_rules,
      (SELECT count(*) FROM pg_policies WHERE schemaname = 'app' AND permissive = 'RESTRICTIVE');
  END IF;
  -- A table with role rules but no permissive policy would show nothing at all.
  SELECT string_agg(DISTINCT r.tablename, ', ') INTO missing
    FROM pg_policies r JOIN pg_class c ON c.relname = r.tablename AND c.relnamespace = 'app'::regnamespace
   WHERE r.schemaname = 'app' AND r.permissive = 'RESTRICTIVE'
     AND (NOT c.relrowsecurity OR NOT c.relforcerowsecurity
          OR NOT EXISTS (SELECT 1 FROM pg_policies p WHERE p.schemaname = 'app' AND p.tablename = r.tablename
                          AND p.permissive = 'PERMISSIVE'));
  IF missing IS NOT NULL THEN RAISE EXCEPTION 'role rules are not enforced on: %', missing; END IF;
END $$;
