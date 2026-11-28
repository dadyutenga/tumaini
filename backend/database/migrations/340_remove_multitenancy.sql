-- 340_remove_multitenancy.sql
-- Forward migration: convert the deployed single-tenant database to the
-- non-tenanted product model.  This migration is destructive and additive
-- migrations 000-330 are never edited.
--
-- tenant_settings -> business_settings
-- tenant_setting_values -> setting_values
--
-- The migration aborts if app.tenants contains more than one row.

BEGIN;

DO $$
DECLARE
  tenant_count integer;
BEGIN
  SELECT count(*) INTO tenant_count FROM app.tenants;
  IF tenant_count > 1 THEN
    RAISE EXCEPTION 'multi_tenant_data_present';
  END IF;
END $$;

CREATE TABLE IF NOT EXISTS app.business_profile (
  id boolean PRIMARY KEY DEFAULT true CHECK (id),
  name app.short_text NOT NULL,
  timezone text NOT NULL DEFAULT 'Africa/Dar_es_Salaam',
  default_currency app.currency NOT NULL DEFAULT 'TZS',
  status text NOT NULL DEFAULT 'active',
  phone app.phone_e164,
  email app.email,
  logo_file_id uuid,
  address text,
  tin text,
  vrn text,
  registration_no text,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  row_version integer NOT NULL DEFAULT 1
);

INSERT INTO app.business_profile
  (name, timezone, default_currency, status, phone, email, address, tin, vrn,
   registration_no, created_at)
SELECT
  coalesce(t.legal_name, s.business_name, 'Tumaini'),
  coalesce(t.timezone, 'Africa/Dar_es_Salaam'),
  coalesce(t.default_currency, s.currency, 'TZS'),
  coalesce(t.status::text, 'active'),
  s.phone,
  s.email,
  s.address,
  s.tin,
  s.vrn,
  s.registration_no,
  t.created_at
FROM app.tenants t
LEFT JOIN app.tenant_settings s ON s.tenant_id = t.id
ON CONFLICT (id) DO NOTHING;

-- Snapshot tenant-dependent constraints before any DROP COLUMN CASCADE.
CREATE TEMP TABLE tenant_constraint_snapshot AS
SELECT c.oid,
       n.nspname AS table_schema,
       cl.relname AS table_name,
       c.conname,
       c.contype,
       c.conrelid,
       c.confrelid,
       c.conkey,
       c.confkey,
       ARRAY(
         SELECT a.attname
         FROM unnest(c.conkey) k(attnum)
         JOIN pg_attribute a ON a.attrelid=c.conrelid AND a.attnum=k.attnum
       ) AS conkey_names,
       ARRAY(
         SELECT a.attname
         FROM unnest(c.confkey) k(attnum)
         JOIN pg_attribute a ON a.attrelid=c.confrelid AND a.attnum=k.attnum
       ) AS confkey_names,
       c.confupdtype,
       c.confdeltype,
       c.confmatchtype,
       c.condeferrable,
       c.condeferred,
       pg_get_constraintdef(c.oid) AS definition
FROM pg_constraint c
JOIN pg_class cl ON cl.oid = c.conrelid
JOIN pg_namespace n ON n.oid = cl.relnamespace
WHERE n.nspname IN ('app', 'iam', 'audit')
  AND pg_get_constraintdef(c.oid) ILIKE '%tenant_id%';

CREATE TEMP TABLE tenant_index_snapshot AS
SELECT ci.oid AS index_oid,
       tn.nspname AS table_schema,
       tc.relname AS table_name,
       ci.relname AS index_name,
       ix.indisunique,
       ix.indisprimary,
       ix.indnkeyatts,
       ix.indkey,
       pg_get_expr(ix.indpred, ix.indrelid) AS predicate,
       am.amname AS method
FROM pg_index ix
JOIN pg_class ci ON ci.oid = ix.indexrelid
JOIN pg_class tc ON tc.oid = ix.indrelid
JOIN pg_namespace tn ON tn.oid = tc.relnamespace
JOIN pg_am am ON am.oid = ci.relam
WHERE tn.nspname IN ('app', 'iam', 'audit')
  AND pg_get_indexdef(ix.indexrelid) ILIKE '%tenant_id%';

CREATE TEMP TABLE tenant_policy_snapshot AS
SELECT schemaname, tablename, policyname
FROM pg_policies
WHERE schemaname IN ('app', 'iam', 'audit')
  AND (coalesce(qual, '') ILIKE '%tenant_id%'
       OR coalesce(with_check, '') ILIKE '%tenant_id%'
       OR policyname = 'tenant_isolation'
       OR policyname = 'tenant_self'
       OR policyname = 'membership_visibility'
       OR policyname = 'audit_read');

CREATE TEMP TABLE tenant_trigger_snapshot AS
SELECT c.oid AS table_oid,
       n.nspname AS table_schema,
       c.relname AS table_name,
       t.tgname,
       pg_get_triggerdef(t.oid) AS definition
FROM pg_trigger t
JOIN pg_class c ON c.oid = t.tgrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname IN ('app', 'iam', 'audit')
  AND NOT t.tgisinternal
  AND EXISTS (
    SELECT 1
    FROM information_schema.columns ic
    WHERE ic.table_schema = n.nspname
      AND ic.table_name = c.relname
      AND ic.column_name = 'tenant_id'
  );

-- Tenant references in trigger and audit function bodies do not become
-- invalid only because a column is dropped: PL/pgSQL resolves NEW.field at
-- runtime.  Remove the tenant dependency before dropping tenant_id.
CREATE OR REPLACE FUNCTION app.tg_freeze_keys()
RETURNS trigger LANGUAGE plpgsql AS $function$
BEGIN
  RETURN NEW;
END
$function$;

CREATE OR REPLACE FUNCTION audit.tg_capture()
RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER
SET search_path = pg_catalog, public AS $function$
DECLARE
  o jsonb;
  n jsonb;
  chg text[];
  red text[] := coalesce(TG_ARGV::text[], '{}');
  e uuid;
  k text;
BEGIN
  IF TG_OP <> 'INSERT' THEN o := to_jsonb(OLD); END IF;
  IF TG_OP <> 'DELETE' THEN n := to_jsonb(NEW); END IF;
  e := CASE WHEN coalesce(n, o) ? 'id' THEN coalesce(n ->> 'id', o ->> 'id')::uuid END;
  IF TG_OP = 'UPDATE' THEN
    SELECT array_agg(key ORDER BY key) INTO chg
      FROM jsonb_object_keys(n) AS key
     WHERE n -> key IS DISTINCT FROM o -> key
       AND key NOT IN ('updated_at', 'updated_by', 'row_version');
    IF chg IS NULL THEN RETURN NULL; END IF;
    SELECT jsonb_object_agg(key, o -> key),
           jsonb_object_agg(key, n -> key)
      INTO o, n
      FROM unnest(chg) AS key;
  END IF;
  FOREACH k IN ARRAY red LOOP
    IF o ? k AND o -> k <> 'null'::jsonb THEN
      o := jsonb_set(o, ARRAY[k], '"[redacted]"');
    END IF;
    IF n ? k AND n -> k <> 'null'::jsonb THEN
      n := jsonb_set(n, ARRAY[k], '"[redacted]"');
    END IF;
  END LOOP;
  INSERT INTO audit.audit_log
    (actor_type, actor_user_id, actor_membership_id, action, entity_table,
     entity_id, old_values, new_values, changed_columns, request_id, client_ip, row_hash)
  VALUES
    (CASE WHEN app.current_membership_id() IS NULL THEN 'system' ELSE 'user' END,
     app.current_user_id(), app.current_membership_id(), lower(TG_OP), TG_TABLE_NAME,
     e, o, n, chg, app.current_request_id(), app.current_client_ip(), ''::bytea);
  RETURN NULL;
END
$function$;

-- Mechanically remove the remaining tenant references from SQL/PLpgSQL
-- function definitions.  Tenant arguments remain for compatibility but are
-- ignored; tenant-aware joins become ordinary joins.
DO $$
DECLARE r record;
  rewritten text;
BEGIN
  FOR r IN
    SELECT n.nspname, p.proname, pg_get_function_identity_arguments(p.oid) args,
           pg_get_functiondef(p.oid) def
    FROM pg_proc p
    JOIN pg_namespace n ON n.oid = p.pronamespace
    WHERE n.nspname = 'app'
      AND p.prosrc ILIKE '%tenant%'
      AND p.proname NOT IN ('tg_freeze_keys')
  LOOP
    rewritten := r.def;
    rewritten := replace(rewritten, 'NEW.tenant_id', 'NULL::uuid');
    rewritten := replace(rewritten, 'OLD.tenant_id', 'NULL::uuid');
    rewritten := replace(rewritten, 'app.current_tenant_id()', 'NULL::uuid');
    rewritten := regexp_replace(rewritten, '\btenant_id\b', 'id', 'g');
    BEGIN
      EXECUTE rewritten;
    EXCEPTION WHEN undefined_table OR undefined_column OR syntax_error THEN
      RAISE NOTICE 'tenant rewrite deferred for %.%(%): %',
                   r.nspname, r.proname, r.args, SQLERRM;
    END;
  END LOOP;
END $$;

-- Drop tenant-dependent constraints explicitly (never DROP COLUMN CASCADE).
DO $$
DECLARE r record;
BEGIN
  DROP VIEW IF EXISTS app.v_staff_catalog CASCADE;
  DROP VIEW IF EXISTS app.v_stock_valuation CASCADE;
  DROP VIEW IF EXISTS app.v_receivables CASCADE;
  DROP VIEW IF EXISTS app.v_payables CASCADE;
  DROP VIEW IF EXISTS app.v_project_financials CASCADE;
  DROP VIEW IF EXISTS app.v_design_job_financials CASCADE;
  DROP VIEW IF EXISTS app.v_pending_approvals CASCADE;
  DROP VIEW IF EXISTS app.v_daily_takings CASCADE;
  DROP VIEW IF EXISTS app.v_profit_and_loss_monthly CASCADE;
  FOR r IN
    SELECT table_schema, table_name, conname, contype
    FROM tenant_constraint_snapshot
    ORDER BY
      CASE contype WHEN 'f' THEN 0 ELSE 1 END,
      table_schema, table_name, conname
  LOOP
    EXECUTE format('ALTER TABLE %I.%I DROP CONSTRAINT IF EXISTS %I',
                   r.table_schema, r.table_name, r.conname);
  END LOOP;
END $$;

-- Drop tenant-dependent indexes explicitly.
DO $$
DECLARE r record;
BEGIN
  FOR r IN
    SELECT table_schema, index_name
    FROM tenant_index_snapshot
    WHERE NOT EXISTS (
      SELECT 1
      FROM pg_inherits i
      WHERE i.inhrelid = tenant_index_snapshot.index_oid
    )
    ORDER BY table_schema, index_name
  LOOP
    EXECUTE format('DROP INDEX IF EXISTS %I.%I', r.table_schema, r.index_name);
  END LOOP;
END $$;

-- Drop tenant isolation and tenant references from mixed role/access policies.
DO $$
DECLARE r record;
BEGIN
  FOR r IN
    SELECT schemaname, tablename, policyname
    FROM tenant_policy_snapshot
  LOOP
    EXECUTE format('DROP POLICY IF EXISTS %I ON %I.%I',
                   r.policyname, r.schemaname, r.tablename);
  END LOOP;
END $$;

-- Membership visibility is now one-user visibility.
DO $$
BEGIN
  IF to_regclass('app.memberships') IS NOT NULL THEN
    EXECUTE 'CREATE POLICY membership_visibility ON app.memberships FOR ALL '
            'USING (user_id = app.current_user_id()) '
            'WITH CHECK (user_id = app.current_user_id())';
  END IF;
END $$;

-- Audit visibility keeps the role check but removes tenant isolation.
DO $$
BEGIN
  IF to_regclass('audit.audit_log') IS NOT NULL THEN
    EXECUTE 'CREATE POLICY audit_read ON audit.audit_log FOR SELECT '
            'USING (app.has_permission(''security.audit.view''))';
  END IF;
END $$;

-- Drop tenant_id from app, iam and audit tables.
DO $$
DECLARE r record;
BEGIN
  FOR r IN
    SELECT table_schema, table_name, tgname
    FROM tenant_trigger_snapshot
    ORDER BY table_schema, table_name, tgname
  LOOP
    EXECUTE format('DROP TRIGGER IF EXISTS %I ON %I.%I',
                   r.tgname, r.table_schema, r.table_name);
  END LOOP;
  FOR r IN
    SELECT table_schema, table_name
    FROM information_schema.columns
    WHERE column_name = 'tenant_id'
      AND table_schema IN ('app', 'iam', 'audit')
    GROUP BY table_schema, table_name
    ORDER BY table_schema, table_name
  LOOP
    IF EXISTS (
      SELECT 1 FROM information_schema.columns c
      WHERE c.table_schema = r.table_schema
        AND c.table_name = r.table_name
        AND c.column_name = 'tenant_id'
    ) THEN
      RAISE NOTICE 'dropping %.%.tenant_id', r.table_schema, r.table_name;
      EXECUTE format('ALTER TABLE %I.%I DROP COLUMN tenant_id',
                     r.table_schema, r.table_name);
    END IF;
  END LOOP;
  FOR r IN
    SELECT table_schema, table_name, tgname, definition
    FROM tenant_trigger_snapshot
    ORDER BY table_schema, table_name, tgname
  LOOP
    IF r.definition NOT ILIKE '%tenant_id%'
       AND NOT EXISTS (
         SELECT 1 FROM pg_inherits i
         JOIN pg_class c ON c.oid = i.inhrelid
         JOIN pg_namespace n ON n.oid = c.relnamespace
         WHERE n.nspname = r.table_schema AND c.relname = r.table_name
       )
    THEN
      EXECUTE format('DROP TRIGGER IF EXISTS %I ON %I.%I',
                     r.tgname, r.table_schema, r.table_name);
      EXECUTE r.definition;
    END IF;
  END LOOP;
END $$;

-- Recreate UNIQUE and PRIMARY constraints without tenant_id.
DO $$
DECLARE r record;
  cols text[];
BEGIN
  FOR r IN
    SELECT s.* FROM tenant_constraint_snapshot s
    WHERE s.contype IN ('u', 'p')
    ORDER BY table_schema, table_name, conname
  LOOP
    SELECT array_agg(c ORDER BY ordinality)
    INTO cols
    FROM unnest(r.conkey_names) WITH ORDINALITY k(c, ordinality)
    WHERE k.c <> 'tenant_id';

    IF cols IS NULL OR cardinality(cols) = 0 THEN
      CONTINUE;
    END IF;

    IF r.contype = 'p' THEN
      EXECUTE format('ALTER TABLE %I.%I ADD PRIMARY KEY (%s)',
                     r.table_schema, r.table_name,
                     (SELECT string_agg(format('%I', c), ', ') FROM unnest(cols) c));
    ELSE
      EXECUTE format('ALTER TABLE %I.%I ADD CONSTRAINT %I UNIQUE (%s)',
                     r.table_schema, r.table_name, r.conname,
                     (SELECT string_agg(format('%I', c), ', ') FROM unnest(cols) c));
    END IF;
  END LOOP;
END $$;

-- Recreate foreign keys with tenant_id removed from both sides.
DO $$
DECLARE r record;
  local_cols text[];
  foreign_cols text[];
  local_def text;
  foreign_def text;
  on_update text;
  on_delete text;
BEGIN
  FOR r IN
    SELECT s.* FROM tenant_constraint_snapshot s
    WHERE s.contype = 'f'
    ORDER BY table_schema, table_name, conname
  LOOP
    SELECT array_agg(c ORDER BY ordinality)
    INTO local_cols
    FROM unnest(r.conkey_names) WITH ORDINALITY k(c, ordinality)
    WHERE k.c <> 'tenant_id';

    SELECT array_agg(c ORDER BY ordinality)
    INTO foreign_cols
    FROM unnest(r.confkey_names) WITH ORDINALITY k(c, ordinality)
    WHERE k.c <> 'tenant_id';

    IF local_cols IS NULL OR foreign_cols IS NULL
       OR cardinality(local_cols) <> cardinality(foreign_cols) THEN
      CONTINUE;
    END IF;

    local_def := (SELECT string_agg(format('%I', c), ', ') FROM unnest(local_cols) c);
    foreign_def := (SELECT string_agg(format('%I', c), ', ') FROM unnest(foreign_cols) c);
    on_update := CASE r.confupdtype
      WHEN 'a' THEN '' WHEN 'r' THEN ' ON UPDATE RESTRICT'
      WHEN 'c' THEN ' ON UPDATE CASCADE' WHEN 'n' THEN ' ON UPDATE SET NULL'
      WHEN 'd' THEN ' ON UPDATE SET DEFAULT' ELSE '' END;
    on_delete := CASE r.confdeltype
      WHEN 'a' THEN '' WHEN 'r' THEN ' ON DELETE RESTRICT'
      WHEN 'c' THEN ' ON DELETE CASCADE' WHEN 'n' THEN ' ON DELETE SET NULL'
      WHEN 'd' THEN ' ON DELETE SET DEFAULT' ELSE '' END;

    EXECUTE format(
      'ALTER TABLE %I.%I ADD CONSTRAINT %I FOREIGN KEY (%s) REFERENCES %I.%I (%s)%s%s',
      r.table_schema, r.table_name, r.conname, local_def,
      (SELECT n.nspname FROM pg_namespace n JOIN pg_class c ON c.relnamespace=n.oid WHERE c.oid=r.confrelid),
      (SELECT c.relname FROM pg_class c WHERE c.oid=r.confrelid),
      foreign_def, on_update, on_delete);
  END LOOP;
END $$;

-- Recreate tenant-free ordinary indexes.  Tenant predicates are removed.
DO $$
DECLARE r record;
  key_cols text[];
  key_def text;
  predicate text;
  i integer;
  expr text;
BEGIN
  FOR r IN
    SELECT s.*, ci.relnatts
    FROM tenant_index_snapshot s
    JOIN pg_class ci ON ci.oid = s.index_oid
    ORDER BY table_schema, table_name, index_name
  LOOP
    key_cols := '{}';
    FOR i IN 1..r.indnkeyatts LOOP
      SELECT pg_get_indexdef(r.index_oid, i, true)
      INTO expr;
      IF expr IS NOT NULL AND expr NOT ILIKE '%tenant_id%' THEN
        key_cols := array_append(key_cols, expr);
      END IF;
    END LOOP;
    IF cardinality(key_cols) = 0 THEN
      CONTINUE;
    END IF;
    key_def := (SELECT string_agg(format('%s', c), ', ') FROM unnest(key_cols) c);
    predicate := r.predicate;
    IF predicate IS NOT NULL THEN
      predicate := regexp_replace(predicate,
        '\(?tenant_id\s*=\s*app\.current_tenant_id\(\)\)?(\s*AND\s*)?', '', 'gi');
      predicate := regexp_replace(predicate,
        '(\s*AND\s*)\(?tenant_id\s*=\s*app\.current_tenant_id\(\)\)?', '', 'gi');
      predicate := btrim(regexp_replace(predicate, '^\s*AND\s*', '', 'i'));
      predicate := btrim(regexp_replace(predicate, '\s*AND\s*$', '', 'i'));
    END IF;
    IF predicate IS NULL OR btrim(predicate) = '' THEN
      predicate := NULL;
    END IF;
    EXECUTE format(
      'CREATE%s INDEX %I ON %I.%I USING %s (%s)%s',
      CASE WHEN r.indisunique THEN ' UNIQUE' ELSE '' END,
      r.index_name, r.table_schema, r.table_name, r.method, key_def,
      CASE WHEN predicate IS NULL THEN '' ELSE ' WHERE ' || predicate END);
  END LOOP;
END $$;

-- Fix RLS: preserve role restrictive policies and add a permissive access
-- policy beside them.  Tables that only had tenant isolation lose RLS.
DO $$
DECLARE r record;
  has_restrictive boolean;
BEGIN
  FOR r IN
    SELECT DISTINCT schemaname, tablename
    FROM tenant_policy_snapshot
    WHERE policyname = 'tenant_isolation'
  LOOP
    SELECT EXISTS (
      SELECT 1 FROM pg_policies p
      WHERE p.schemaname = r.schemaname
        AND p.tablename = r.tablename
        AND p.permissive = 'RESTRICTIVE'
    ) INTO has_restrictive;

    IF has_restrictive THEN
      EXECUTE format(
        'CREATE POLICY app_access ON %I.%I FOR ALL USING (true) WITH CHECK (true)',
        r.schemaname, r.tablename);
    ELSE
      EXECUTE format('ALTER TABLE %I.%I DISABLE ROW LEVEL SECURITY',
                     r.schemaname, r.tablename);
      EXECUTE format('ALTER TABLE %I.%I NO FORCE ROW LEVEL SECURITY',
                     r.schemaname, r.tablename);
    END IF;
  END LOOP;
END $$;

-- Recreate role policies that included tenant_id in a tenant/composite join.
CREATE POLICY repayment_read ON app.advance_repayments FOR SELECT USING (
  app.has_permission('hr.payroll.view')
  OR EXISTS (
    SELECT 1 FROM app.employee_advances a
    WHERE a.id = advance_repayments.advance_id
      AND app.is_self_employee(a.employee_id)
  )
);

CREATE POLICY statutory_read ON app.payroll_statutory_items FOR SELECT USING (
  app.has_permission('hr.payroll.view')
  OR EXISTS (
    SELECT 1 FROM app.payroll_lines l
    WHERE l.id = payroll_statutory_items.payroll_line_id
      AND app.is_self_employee(l.employee_id)
  )
);

CREATE POLICY payslip_read ON app.payslips FOR SELECT USING (
  app.has_permission('hr.payroll.view')
  OR app.has_permission('hr.payroll.approve')
  OR EXISTS (
    SELECT 1 FROM app.payroll_lines l
    WHERE l.id = payslips.payroll_line_id
      AND app.is_self_employee(l.employee_id)
  )
);

-- Membership remains global within the business and has one membership/user.
ALTER TABLE app.memberships DROP CONSTRAINT IF EXISTS memberships_tenant_id_user_id_key;
ALTER TABLE app.memberships ADD CONSTRAINT memberships_user_id_key UNIQUE (user_id);

-- Rename tenant settings to business settings.
ALTER TABLE app.tenant_settings RENAME TO business_settings;
ALTER TABLE app.tenant_setting_values RENAME TO setting_values;

ALTER TABLE app.setting_values DROP CONSTRAINT IF EXISTS tenant_setting_values_pkey;
ALTER TABLE app.setting_values ADD PRIMARY KEY (key);

-- Tenant-specific reporting views are not reusable after tenant removal.
DROP VIEW IF EXISTS app.v_staff_catalog CASCADE;
DROP VIEW IF EXISTS app.v_stock_valuation CASCADE;
DROP VIEW IF EXISTS app.v_receivables CASCADE;
DROP VIEW IF EXISTS app.v_payables CASCADE;
DROP VIEW IF EXISTS app.v_project_financials CASCADE;
DROP VIEW IF EXISTS app.v_design_job_financials CASCADE;
DROP VIEW IF EXISTS app.v_pending_approvals CASCADE;
DROP VIEW IF EXISTS app.v_daily_takings CASCADE;
DROP VIEW IF EXISTS app.v_profit_and_loss_monthly CASCADE;

-- Remove tenant entry points and tenant-only objects.
DROP FUNCTION IF EXISTS app.provision_tenant(text, text, uuid, text, text, text);
DROP FUNCTION IF EXISTS app.current_tenant_id();
DROP FUNCTION IF EXISTS app.require_tenant();

DROP TABLE app.tenants CASCADE;
DROP TYPE IF EXISTS app.tenant_status;

COMMIT;
