-- =====================================================================
-- 270_auth_security_hardening.sql — Refresh token reuse detection,
-- session re-authentication timestamp, and TOTP replay protection
-- =====================================================================
SET search_path = app, public;

-- Track rotated refresh token hashes so presenting a previously rotated
-- token is detected as token reuse / theft and revokes the entire session.
CREATE TABLE IF NOT EXISTS iam.session_refresh_tokens (
  token_hash  bytea PRIMARY KEY CHECK (octet_length(token_hash) = 32),
  session_id  uuid NOT NULL REFERENCES iam.sessions(id) ON DELETE CASCADE,
  user_id     uuid NOT NULL REFERENCES iam.users(id) ON DELETE CASCADE,
  replaced_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS session_refresh_tokens_session_idx
  ON iam.session_refresh_tokens (session_id);

-- Allow 'token_reuse' as a revocation reason on iam.sessions.
ALTER TABLE iam.sessions DROP CONSTRAINT IF EXISTS sessions_revoked_reason_check;
ALTER TABLE iam.sessions
  ADD CONSTRAINT sessions_revoked_reason_check
  CHECK (revoked_reason IN (
    'logout', 'idle_timeout', 'admin_force_signout', 'password_changed',
    'user_disabled', 'membership_revoked', 'expired', 'token_reuse'
  ));

-- Track when the session last verified primary credentials (password / MFA)
-- so sensitive account changes can enforce fresh re-authentication.
ALTER TABLE iam.sessions
  ADD COLUMN IF NOT EXISTS reauthenticated_at timestamptz NOT NULL DEFAULT now();

-- Track the last accepted 30-second TOTP time step to prevent code replay.
ALTER TABLE iam.mfa_factors
  ADD COLUMN IF NOT EXISTS last_used_step bigint;

GRANT SELECT, INSERT, UPDATE ON iam.session_refresh_tokens TO bms_app;
GRANT SELECT, DELETE ON iam.session_refresh_tokens TO bms_worker;
