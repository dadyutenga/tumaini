-- =====================================================================
-- 020_iam.sql — tenants, global identity, memberships, RBAC, sessions
-- =====================================================================
SET search_path = app, public;

-- ---------- Tenants (organizations) ----------------------------------
CREATE TYPE app.tenant_status AS ENUM ('active', 'suspended', 'closed');

CREATE TABLE app.tenants (
  id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  slug             citext NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9][a-z0-9-]{2,62}$'),
  legal_name       app.short_text NOT NULL,
  status           app.tenant_status NOT NULL DEFAULT 'active',
  default_currency app.currency NOT NULL DEFAULT 'TZS',
  timezone         text NOT NULL DEFAULT 'Africa/Dar_es_Salaam',
  default_locale   text NOT NULL DEFAULT 'sw' CHECK (default_locale IN ('sw', 'en')),
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  updated_by       uuid,
  row_version      integer NOT NULL DEFAULT 1
);
COMMENT ON TABLE app.tenants IS 'One row per customer organization (a business owner running Business A shop + Business B services).';

-- ---------- Global identity (schema iam, no tenant RLS) ---------------
-- Identity must be resolvable before a tenant is chosen (login, session
-- lookup, password reset), so these tables are global and reachable only
-- through the API role's narrow grants. They hold no business data.
CREATE TYPE iam.user_status AS ENUM ('active', 'disabled');

CREATE TABLE iam.users (
  id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  full_name        app.short_text NOT NULL,
  email            app.email UNIQUE,
  phone            app.phone_e164 UNIQUE,
  preferred_locale text NOT NULL DEFAULT 'sw' CHECK (preferred_locale IN ('sw', 'en')),
  avatar_storage_key text,
  status           iam.user_status NOT NULL DEFAULT 'active',
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  updated_by       uuid,
  row_version      integer NOT NULL DEFAULT 1,
  CONSTRAINT users_has_login_identifier CHECK (email IS NOT NULL OR phone IS NOT NULL)
);
COMMENT ON TABLE iam.users IS 'Global person identity. Login is by phone or email + password (spec §4).';

CREATE TABLE iam.user_credentials (
  user_id              uuid PRIMARY KEY REFERENCES iam.users(id) ON DELETE CASCADE,
  password_hash        text NOT NULL CHECK (password_hash LIKE '$argon2id$%'),
  password_changed_at  timestamptz NOT NULL DEFAULT now(),
  must_change_password boolean NOT NULL DEFAULT true,
  failed_attempts      integer NOT NULL DEFAULT 0 CHECK (failed_attempts >= 0),
  last_failed_at       timestamptz,
  locked_until         timestamptz,
  updated_at           timestamptz NOT NULL DEFAULT now()
);
COMMENT ON TABLE iam.user_credentials IS 'Argon2id hashes only. Kept apart from iam.users so profile queries never touch secrets.';
COMMENT ON COLUMN iam.user_credentials.must_change_password IS 'True for temporary passwords set by Admin (spec §4 First login).';

CREATE TABLE iam.password_history (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id       uuid NOT NULL REFERENCES iam.users(id) ON DELETE CASCADE,
  password_hash text NOT NULL CHECK (password_hash LIKE '$argon2id$%'),
  created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX password_history_user_idx ON iam.password_history (user_id, created_at DESC);

CREATE TYPE iam.mfa_type AS ENUM ('totp', 'sms_otp');
CREATE TABLE iam.mfa_factors (
  id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id           uuid NOT NULL REFERENCES iam.users(id) ON DELETE CASCADE,
  factor_type       iam.mfa_type NOT NULL,
  secret_ciphertext bytea,          -- TOTP seed, envelope-encrypted by the API (KMS DEK)
  key_id            text,           -- KMS key version used for the DEK
  phone             app.phone_e164, -- for sms_otp
  confirmed_at      timestamptz,
  last_used_at      timestamptz,
  disabled_at       timestamptz,
  created_at        timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT mfa_totp_secret CHECK (factor_type <> 'totp' OR (secret_ciphertext IS NOT NULL AND key_id IS NOT NULL)),
  CONSTRAINT mfa_sms_phone   CHECK (factor_type <> 'sms_otp' OR phone IS NOT NULL)
);
CREATE UNIQUE INDEX mfa_one_active_per_type ON iam.mfa_factors (user_id, factor_type) WHERE disabled_at IS NULL;

CREATE TABLE iam.mfa_recovery_codes (
  id        uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id   uuid NOT NULL REFERENCES iam.users(id) ON DELETE CASCADE,
  code_hash bytea NOT NULL CHECK (octet_length(code_hash) = 32),
  used_at   timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (user_id, code_hash)
);

CREATE TYPE iam.otp_channel AS ENUM ('sms', 'email');
CREATE TABLE iam.password_reset_requests (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id       uuid NOT NULL REFERENCES iam.users(id) ON DELETE CASCADE,
  channel       iam.otp_channel NOT NULL,
  code_hash     bytea NOT NULL CHECK (octet_length(code_hash) = 32), -- HMAC-SHA256(code, pepper)
  expires_at    timestamptz NOT NULL,
  attempts      smallint NOT NULL DEFAULT 0 CHECK (attempts BETWEEN 0 AND 5),
  consumed_at   timestamptz,
  superseded_at timestamptz,
  requested_ip  inet,
  created_at    timestamptz NOT NULL DEFAULT now(),
  CHECK (expires_at > created_at AND expires_at <= created_at + interval '30 minutes')
);
CREATE UNIQUE INDEX password_reset_one_live ON iam.password_reset_requests (user_id)
  WHERE consumed_at IS NULL AND superseded_at IS NULL;

-- ---------- Memberships (user <-> tenant) -----------------------------
CREATE TYPE app.membership_status AS ENUM ('invited', 'active', 'suspended', 'revoked');

CREATE TABLE app.memberships (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  user_id             uuid NOT NULL REFERENCES iam.users(id),
  status              app.membership_status NOT NULL DEFAULT 'invited',
  default_location_id uuid,
  joined_at           timestamptz,
  deactivated_at      timestamptz,
  deactivation_reason text,
  created_at          timestamptz NOT NULL DEFAULT now(),
  created_by          uuid DEFAULT app.current_membership_id(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  updated_by          uuid,
  row_version         integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, user_id),
  UNIQUE (tenant_id, id),
  CHECK (status NOT IN ('revoked', 'suspended') OR deactivated_at IS NOT NULL)
);
COMMENT ON TABLE app.memberships IS 'A user''s seat in a tenant. All actor columns (created_by, approved_by, sold_by...) reference memberships, never users, so actions are always tenant-attributable. Users are deactivated, never deleted.';

-- ---------- RBAC ----------------------------------------------------
CREATE TABLE app.permissions (
  code        text PRIMARY KEY CHECK (code ~ '^[a-z_]+(\.[a-z_]+){1,3}$'),
  module      text NOT NULL,
  description text NOT NULL,
  is_sensitive boolean NOT NULL DEFAULT false
);
COMMENT ON TABLE app.permissions IS 'Global catalogue of permission codes (module.resource.action). Seeded in 160.';

CREATE TYPE app.record_status AS ENUM ('active', 'inactive');

CREATE TABLE app.roles (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES app.tenants(id),
  code        citext NOT NULL CHECK (code ~ '^[a-z][a-z0-9_]{1,40}$'),
  name        app.short_text NOT NULL,
  description text,
  is_system   boolean NOT NULL DEFAULT false,   -- admin/manager/staff/designer templates
  rank        smallint NOT NULL CHECK (rank BETWEEN 1 AND 100), -- 100 = Admin
  status      app.record_status NOT NULL DEFAULT 'active',
  created_at  timestamptz NOT NULL DEFAULT now(),
  created_by  uuid DEFAULT app.current_membership_id(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  updated_by  uuid,
  row_version integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, code),
  UNIQUE (tenant_id, id)
);

CREATE TABLE app.role_permissions (
  tenant_id       uuid NOT NULL,
  role_id         uuid NOT NULL,
  permission_code text NOT NULL REFERENCES app.permissions(code),
  created_at      timestamptz NOT NULL DEFAULT now(),
  created_by      uuid DEFAULT app.current_membership_id(),
  PRIMARY KEY (tenant_id, role_id, permission_code),
  FOREIGN KEY (tenant_id, role_id) REFERENCES app.roles(tenant_id, id) ON DELETE CASCADE
);

CREATE TABLE app.membership_roles (
  tenant_id     uuid NOT NULL,
  membership_id uuid NOT NULL,
  role_id       uuid NOT NULL,
  created_at    timestamptz NOT NULL DEFAULT now(),
  created_by    uuid DEFAULT app.current_membership_id(),
  PRIMARY KEY (tenant_id, membership_id, role_id),
  -- Spec §2A: one role per user. Drop this constraint to allow multi-role later.
  CONSTRAINT membership_single_role UNIQUE (tenant_id, membership_id),
  FOREIGN KEY (tenant_id, membership_id) REFERENCES app.memberships(tenant_id, id),
  FOREIGN KEY (tenant_id, role_id)       REFERENCES app.roles(tenant_id, id)
);

CREATE TYPE app.permission_effect AS ENUM ('allow', 'deny');
CREATE TABLE app.membership_permission_overrides (
  tenant_id       uuid NOT NULL,
  membership_id   uuid NOT NULL,
  permission_code text NOT NULL REFERENCES app.permissions(code),
  effect          app.permission_effect NOT NULL,
  reason          text NOT NULL CHECK (length(btrim(reason)) >= 3),
  expires_at      timestamptz,
  created_at      timestamptz NOT NULL DEFAULT now(),
  created_by      uuid DEFAULT app.current_membership_id(),
  PRIMARY KEY (tenant_id, membership_id, permission_code),
  FOREIGN KEY (tenant_id, membership_id) REFERENCES app.memberships(tenant_id, id)
);
COMMENT ON TABLE app.membership_permission_overrides IS 'Per-user allow/deny on top of the role (spec §2A: Admin can change any permission for any individual user).';

-- Monetary / percentage authority (discount, refund, expense approval limits).
CREATE TYPE app.limit_code AS ENUM (
  'sale_discount_pct',        -- max discount % applied without approval
  'discount_approve_pct',     -- max discount % this person may approve
  'refund_approve_amount',    -- max refund this person may approve
  'expense_approve_amount',   -- max expense this person may approve
  'price_change_approve_pct'  -- max job price change % this person may approve
);
CREATE TABLE app.authority_limits (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id     uuid NOT NULL REFERENCES app.tenants(id),
  role_id       uuid,
  membership_id uuid,
  limit_code    app.limit_code NOT NULL,
  limit_value   numeric(18,2) NOT NULL CHECK (limit_value >= 0),
  created_at    timestamptz NOT NULL DEFAULT now(),
  created_by    uuid DEFAULT app.current_membership_id(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  updated_by    uuid,
  row_version   integer NOT NULL DEFAULT 1,
  CHECK (num_nonnulls(role_id, membership_id) = 1),
  CHECK (limit_code::text NOT LIKE '%pct' OR limit_value <= 100),
  UNIQUE NULLS NOT DISTINCT (tenant_id, role_id, membership_id, limit_code),
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id, role_id)       REFERENCES app.roles(tenant_id, id),
  FOREIGN KEY (tenant_id, membership_id) REFERENCES app.memberships(tenant_id, id)
);
COMMENT ON TABLE app.authority_limits IS 'Membership row overrides role row. No row for a permission holder = unlimited (Admin default).';

-- ---------- Sessions & auth events (global, iam) -----------------------
CREATE TABLE iam.sessions (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id             uuid NOT NULL REFERENCES iam.users(id),
  tenant_id           uuid REFERENCES app.tenants(id),  -- null until tenant chosen
  membership_id       uuid REFERENCES app.memberships(id),
  token_hash          bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32), -- SHA-256 of opaque token
  created_at          timestamptz NOT NULL DEFAULT now(),
  last_seen_at        timestamptz NOT NULL DEFAULT now(),
  idle_timeout        interval NOT NULL DEFAULT interval '30 minutes',
  absolute_expires_at timestamptz NOT NULL,
  remember_me         boolean NOT NULL DEFAULT false,
  mfa_verified_at     timestamptz,
  ip                  inet,
  user_agent          text CHECK (length(user_agent) <= 512),
  revoked_at          timestamptz,
  revoked_reason      text CHECK (revoked_reason IN ('logout','idle_timeout','admin_force_signout','password_changed','user_disabled','membership_revoked','expired')),
  CHECK (absolute_expires_at > created_at),
  CHECK ((revoked_at IS NULL) = (revoked_reason IS NULL)),
  CHECK ((tenant_id IS NULL) = (membership_id IS NULL))
);
CREATE INDEX sessions_user_live_idx   ON iam.sessions (user_id) WHERE revoked_at IS NULL;
CREATE INDEX sessions_tenant_live_idx ON iam.sessions (tenant_id, membership_id) WHERE revoked_at IS NULL;

CREATE TYPE iam.auth_event_type AS ENUM (
  'login_success', 'login_failure', 'account_locked', 'account_unlocked', 'logout',
  'session_timeout', 'forced_signout', 'password_changed', 'password_reset_requested',
  'password_reset_completed', 'mfa_enrolled', 'mfa_challenge_failed', 'mfa_disabled',
  'tenant_selected');

CREATE TABLE iam.auth_events (
  id               uuid NOT NULL DEFAULT gen_random_uuid(),
  occurred_at      timestamptz NOT NULL DEFAULT now(),
  event_type       iam.auth_event_type NOT NULL,
  user_id          uuid,          -- null when identifier did not match a user
  tenant_id        uuid,
  identifier_hmac  bytea,         -- HMAC of submitted phone/email (no plaintext in logs)
  session_id       uuid,
  ip               inet,
  user_agent       text CHECK (length(user_agent) <= 512),
  geo_country      text,
  geo_city         text,
  failure_reason   text CHECK (failure_reason IN ('bad_password','unknown_identifier','locked','disabled','mfa_failed','membership_inactive')),
  PRIMARY KEY (occurred_at, id)
) PARTITION BY RANGE (occurred_at);
COMMENT ON TABLE iam.auth_events IS 'Login History screen (spec §10). Append-only, monthly partitions. No FKs so history survives user lifecycle.';
CREATE INDEX auth_events_user_idx   ON iam.auth_events (user_id, occurred_at DESC);
CREATE INDEX auth_events_tenant_idx ON iam.auth_events (tenant_id, occurred_at DESC);
CREATE TABLE iam.auth_events_default PARTITION OF iam.auth_events DEFAULT;

-- Public, unauthenticated token resolution (design approval links etc.).
CREATE TYPE iam.public_token_purpose AS ENUM ('design_approval', 'document_download');
CREATE TABLE iam.public_tokens (
  token_hash  bytea PRIMARY KEY CHECK (octet_length(token_hash) = 32),
  tenant_id   uuid NOT NULL REFERENCES app.tenants(id),
  purpose     iam.public_token_purpose NOT NULL,
  target_id   uuid NOT NULL,
  expires_at  timestamptz NOT NULL,
  revoked_at  timestamptz,
  created_at  timestamptz NOT NULL DEFAULT now()
);
COMMENT ON TABLE iam.public_tokens IS 'Resolves a public link token to its tenant before tenant context exists; the API then sets app.tenant_id and loads the target under RLS.';

-- API keys for service accounts / POS devices.
CREATE TABLE iam.api_keys (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id     uuid NOT NULL REFERENCES app.tenants(id),
  name          app.short_text NOT NULL,
  key_prefix    text NOT NULL UNIQUE CHECK (key_prefix ~ '^bms_[a-z0-9]{8}$'),
  key_hash      bytea NOT NULL CHECK (octet_length(key_hash) = 32),
  scopes        text[] NOT NULL CHECK (cardinality(scopes) > 0),
  service_membership_id uuid REFERENCES app.memberships(id), -- acting identity
  expires_at    timestamptz,
  last_used_at  timestamptz,
  revoked_at    timestamptz,
  created_at    timestamptz NOT NULL DEFAULT now(),
  created_by    uuid REFERENCES app.memberships(id)
);
