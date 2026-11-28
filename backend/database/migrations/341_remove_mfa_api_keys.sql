-- 341_remove_mfa_api_keys.sql
-- Remove user-facing MFA and API-key features.  Provider credentials used by
-- the outbound sender remain in application configuration and are not here.

BEGIN;

DROP TRIGGER IF EXISTS zz_audit_rows ON iam.mfa_factors;
DROP TRIGGER IF EXISTS zz_audit_changes ON iam.mfa_factors;
DROP TABLE IF EXISTS iam.mfa_recovery_codes CASCADE;
DROP TABLE IF EXISTS iam.mfa_factors CASCADE;
DROP TABLE IF EXISTS iam.api_keys CASCADE;

ALTER TABLE iam.sessions DROP COLUMN IF EXISTS mfa_verified_at;
ALTER TABLE iam.sessions DROP COLUMN IF EXISTS reauthenticated_at;
ALTER TABLE iam.sessions DROP COLUMN IF EXISTS tenant_id;

ALTER TABLE iam.auth_events DROP COLUMN IF EXISTS tenant_id;
ALTER TABLE iam.auth_events_default DROP COLUMN IF EXISTS tenant_id;
ALTER TABLE iam.public_tokens DROP COLUMN IF EXISTS tenant_id;

ALTER TABLE app.business_settings DROP COLUMN IF EXISTS mfa_required_for_admin;

-- Remove MFA/API-key grants if migrations granted them to the API role.
DROP TYPE IF EXISTS iam.mfa_type CASCADE;

-- Regrant the surviving identity tables and the business settings tables.
GRANT SELECT, INSERT, UPDATE ON iam.users, iam.user_credentials, iam.sessions,
  iam.password_history, iam.password_reset_requests, iam.public_tokens,
  iam.session_refresh_tokens, iam.auth_events TO bms_app;
GRANT SELECT ON iam.users, iam.sessions, iam.auth_events TO bms_readonly;
GRANT SELECT, UPDATE ON iam.user_credentials TO bms_worker;

GRANT SELECT ON app.business_profile, app.business_settings, app.setting_values
  TO bms_app, bms_worker, bms_readonly;
GRANT INSERT, UPDATE ON app.business_settings, app.setting_values
  TO bms_app, bms_worker;

COMMIT;
