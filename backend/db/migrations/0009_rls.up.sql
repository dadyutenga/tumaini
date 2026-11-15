DO $$
DECLARE r record;
BEGIN
  FOR r IN
    SELECT c.table_name
    FROM information_schema.columns c
    JOIN information_schema.tables t
      ON t.table_schema = c.table_schema AND t.table_name = c.table_name
    WHERE c.table_schema = 'public'
      AND c.column_name = 'business_id'
      AND t.table_type = 'BASE TABLE'
      AND c.table_name NOT IN ('schema_migrations')
  LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', r.table_name);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', r.table_name);
    EXECUTE format('DROP POLICY IF EXISTS tenant_isolation ON %I', r.table_name);
    EXECUTE format(
      'CREATE POLICY tenant_isolation ON %I USING (business_id = NULLIF(current_setting(''app.business_id'', true), '''')::uuid) WITH CHECK (business_id = NULLIF(current_setting(''app.business_id'', true), '''')::uuid)',
      r.table_name
    );
  END LOOP;
END $$;

ALTER TABLE businesses ENABLE ROW LEVEL SECURITY;
ALTER TABLE businesses FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON businesses;
CREATE POLICY tenant_isolation ON businesses
  USING (id = NULLIF(current_setting('app.business_id', true), '')::uuid)
  WITH CHECK (id = NULLIF(current_setting('app.business_id', true), '')::uuid);

DO $$
DECLARE r record;
BEGIN
  FOR r IN
    SELECT c.table_name
    FROM information_schema.columns c
    WHERE c.table_schema = 'public' AND c.column_name = 'updated_at'
  LOOP
    EXECUTE format('DROP TRIGGER IF EXISTS set_updated_at ON %I', r.table_name);
    EXECUTE format('CREATE TRIGGER set_updated_at BEFORE UPDATE ON %I FOR EACH ROW EXECUTE FUNCTION set_updated_at()', r.table_name);
  END LOOP;
END $$;

GRANT USAGE ON SCHEMA public TO bms_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO bms_app;
GRANT SELECT ON permissions TO bms_app;
REVOKE INSERT, UPDATE, DELETE ON permissions FROM bms_app;
REVOKE UPDATE, DELETE ON audit_logs FROM bms_app;
REVOKE UPDATE, DELETE ON journal_entries FROM bms_app;
REVOKE UPDATE, DELETE ON journal_lines FROM bms_app;
REVOKE UPDATE, DELETE ON stock_movements FROM bms_app;

ALTER DEFAULT PRIVILEGES FOR ROLE bms_owner IN SCHEMA public
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO bms_app;
