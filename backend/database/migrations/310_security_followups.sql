-- =====================================================================
-- 310_security_followups.sql
--   1. Rate-limit counters shared by every API process and kept across restarts.
--   2. A ledger correction must use its own approval, once.
--   3. Changes to sign-in identities are captured in the audit trail, so a
--      password or second factor changed behind the application is visible.
-- =====================================================================
SET search_path = app, public;

-- 1. bucket is a SHA-256 of limiter, client and route: no addresses are stored.
CREATE TABLE iam.rate_limits (
  bucket       bytea PRIMARY KEY CHECK (octet_length(bucket) = 32),
  window_start timestamptz NOT NULL,
  hits         integer NOT NULL CHECK (hits > 0)
);
CREATE INDEX rate_limits_window_idx ON iam.rate_limits (window_start);
GRANT SELECT, INSERT, UPDATE ON iam.rate_limits TO bms_app;
GRANT SELECT, DELETE ON iam.rate_limits TO bms_worker;

-- 2. approved_request_exists() accepted any approved correction request for the
-- target, however the row referred to it and however often it had been used.
CREATE UNIQUE INDEX fin_txn_correction_once ON app.financial_transactions (tenant_id, approval_request_id)
  WHERE txn_type = 'correction';

CREATE OR REPLACE FUNCTION app.tg_fin_txn_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF current_user <> 'bms_owner' AND NEW.txn_type NOT IN ('opening_balance', 'correction') THEN
    RAISE EXCEPTION 'transaction type % is posted automatically from its source document', NEW.txn_type
      USING ERRCODE = 'BM004';
  END IF;
  IF NEW.txn_type = 'correction' AND NOT EXISTS (
       SELECT 1 FROM app.approval_requests a
        WHERE a.tenant_id = NEW.tenant_id AND a.id = NEW.approval_request_id
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
      PERFORM 1 FROM app.financial_accounts a
        WHERE a.tenant_id = NEW.tenant_id AND a.id = NEW.account_id FOR UPDATE;
      IF NEW.direction <> 'in' OR EXISTS (SELECT 1 FROM app.financial_transactions t
            WHERE t.tenant_id = NEW.tenant_id AND t.account_id = NEW.account_id) THEN
        RAISE EXCEPTION 'an opening balance can only be the first entry of an account' USING ERRCODE = 'BM004';
      END IF;
    END IF;
  END IF;
  RETURN NEW;
END $$;

-- 3. Identity changes. Only the names of the changed columns are recorded,
-- never values: they are credentials or personal data. TG_ARGV[0] names the
-- column holding the user's id.
CREATE FUNCTION audit.tg_capture_identity() RETURNS trigger
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
  INSERT INTO audit.audit_log (tenant_id, actor_type, actor_user_id, actor_membership_id, action,
                               entity_table, entity_id, changed_columns, request_id, client_ip, row_hash)
  VALUES (app.current_tenant_id(), CASE WHEN app.current_user_id() IS NULL THEN 'system' ELSE 'user' END,
          app.current_user_id(), app.current_membership_id(), lower(TG_OP), TG_TABLE_NAME,
          (coalesce(n, o) ->> TG_ARGV[0])::uuid, chg, app.current_request_id(), app.current_client_ip(), ''::bytea);
  RETURN NULL;
END $$;

CREATE TRIGGER zz_audit_rows AFTER INSERT OR DELETE ON iam.users
  FOR EACH ROW EXECUTE FUNCTION audit.tg_capture_identity('id');
CREATE TRIGGER zz_audit_changes AFTER UPDATE ON iam.users
  FOR EACH ROW WHEN ((OLD.status, OLD.email, OLD.phone) IS DISTINCT FROM (NEW.status, NEW.email, NEW.phone))
  EXECUTE FUNCTION audit.tg_capture_identity('id');
CREATE TRIGGER zz_audit_changes AFTER UPDATE ON iam.user_credentials
  FOR EACH ROW WHEN (OLD.password_hash IS DISTINCT FROM NEW.password_hash)
  EXECUTE FUNCTION audit.tg_capture_identity('user_id');
CREATE TRIGGER zz_audit_rows AFTER INSERT OR DELETE ON iam.mfa_factors
  FOR EACH ROW EXECUTE FUNCTION audit.tg_capture_identity('user_id');
CREATE TRIGGER zz_audit_changes AFTER UPDATE ON iam.mfa_factors
  FOR EACH ROW WHEN ((OLD.secret_ciphertext, OLD.phone, OLD.confirmed_at, OLD.disabled_at)
                     IS DISTINCT FROM (NEW.secret_ciphertext, NEW.phone, NEW.confirmed_at, NEW.disabled_at))
  EXECUTE FUNCTION audit.tg_capture_identity('user_id');
