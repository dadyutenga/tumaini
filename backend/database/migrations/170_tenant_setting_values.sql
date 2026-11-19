-- =====================================================================
-- 170_tenant_setting_values.sql — settings that have no typed column
--
-- app.tenant_settings (030) holds the settings the specification names. The product also has a few
-- operational knobs it does not name (payroll pay day and overtime multiplier, receipt layout,
-- reminder lead times, the clock-in network allow-list, the payment-method to account mapping,
-- message templates, currency display). They are real features and must not be dropped, so they
-- live here as validated key/value rows, one per tenant and key. The API validates every key
-- against a fixed specification before writing; the database only guarantees tenant isolation,
-- attribution and the audit trail.
-- Additive: no existing object changes.
-- =====================================================================
SET search_path = app, public;

CREATE TABLE app.tenant_setting_values (
  tenant_id  uuid NOT NULL REFERENCES app.tenants(id),
  key        text NOT NULL CHECK (key ~ '^[a-z_]+\.[a-z0-9_]+$' AND length(key) <= 80),
  value      jsonb NOT NULL,
  updated_at timestamptz NOT NULL DEFAULT now(),
  updated_by uuid DEFAULT app.current_membership_id(),
  PRIMARY KEY (tenant_id, key)
);
COMMENT ON TABLE app.tenant_setting_values IS 'Operational settings without a typed column in tenant_settings. Keys are validated by the API (org settings specification).';

ALTER TABLE app.tenant_setting_values ENABLE ROW LEVEL SECURITY;
ALTER TABLE app.tenant_setting_values FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON app.tenant_setting_values
  USING (tenant_id = app.current_tenant_id()) WITH CHECK (tenant_id = app.current_tenant_id());

GRANT SELECT ON app.tenant_setting_values TO bms_app, bms_worker, bms_readonly;
GRANT INSERT ON app.tenant_setting_values TO bms_app, bms_worker;
GRANT UPDATE (value, updated_at, updated_by) ON app.tenant_setting_values TO bms_app, bms_worker;

CREATE TRIGGER zz_audit AFTER INSERT OR UPDATE OR DELETE ON app.tenant_setting_values
  FOR EACH ROW EXECUTE FUNCTION audit.tg_capture();
