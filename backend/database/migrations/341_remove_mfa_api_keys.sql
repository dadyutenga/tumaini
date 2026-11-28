-- 341_remove_mfa_api_keys.sql
-- Remove two-factor authentication and user-facing API keys. Provider credentials used by the
-- outbound sender are application configuration and are not here. Password re-confirmation
-- (iam.sessions.reauthenticated_at) is not two-factor authentication and stays.

DROP TRIGGER IF EXISTS zz_audit_rows ON iam.mfa_factors;
DROP TRIGGER IF EXISTS zz_audit_changes ON iam.mfa_factors;
DROP TABLE iam.mfa_recovery_codes;
DROP TABLE iam.mfa_factors;
DROP TABLE iam.api_keys;

ALTER TABLE iam.sessions DROP COLUMN IF EXISTS mfa_verified_at;
ALTER TABLE app.business_settings DROP COLUMN IF EXISTS mfa_required_for_admin;
DROP TYPE IF EXISTS iam.mfa_type;
