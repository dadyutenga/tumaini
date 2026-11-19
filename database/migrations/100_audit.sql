-- =====================================================================
-- 100_audit.sql — append-only, tamper-evident audit trail
-- =====================================================================
SET search_path = audit, app, public;

CREATE SEQUENCE audit.audit_log_seq AS bigint;

CREATE TABLE audit.audit_log (
  seq                 bigint NOT NULL DEFAULT nextval('audit.audit_log_seq'),
  id                  uuid NOT NULL DEFAULT gen_random_uuid(),
  occurred_at         timestamptz NOT NULL DEFAULT clock_timestamp(),
  tenant_id           uuid,             -- null = platform event
  actor_type          text NOT NULL CHECK (actor_type IN ('user', 'system', 'client_link', 'device', 'service_account')),
  actor_user_id       uuid,
  actor_membership_id uuid,
  action              text NOT NULL CHECK (action ~ '^[a-z_]+(\.[a-z_]+)*$'),
  entity_table        text,
  entity_id           uuid,
  old_values          jsonb,
  new_values          jsonb,
  changed_columns     text[],
  outcome             text NOT NULL DEFAULT 'success' CHECK (outcome IN ('success', 'denied', 'failure')),
  reason              text CHECK (length(reason) <= 2000),
  request_id          text CHECK (length(request_id) <= 100),
  client_ip           inet,
  user_agent          text CHECK (length(user_agent) <= 512),
  row_hash            bytea NOT NULL,
  PRIMARY KEY (occurred_at, seq)
) PARTITION BY RANGE (occurred_at);
COMMENT ON TABLE audit.audit_log IS 'Immutable audit trail (spec §10 Audit Log; §12 History tab). Monthly partitions. row_hash = SHA-256 of the canonical row; audit_seals chains hashes per tenant so deletion or edits are detectable.';
CREATE TABLE audit.audit_log_default PARTITION OF audit.audit_log DEFAULT;
CREATE INDEX audit_entity_idx ON audit.audit_log (tenant_id, entity_table, entity_id, occurred_at);
CREATE INDEX audit_actor_idx  ON audit.audit_log (tenant_id, actor_membership_id, occurred_at DESC);
CREATE INDEX audit_tenant_time_idx ON audit.audit_log (tenant_id, occurred_at DESC);
CREATE INDEX audit_seq_idx ON audit.audit_log (tenant_id, seq);

CREATE FUNCTION audit.compute_row_hash(r audit.audit_log) RETURNS bytea
LANGUAGE sql IMMUTABLE AS $$
  SELECT digest(concat_ws('|', r.seq, r.id, r.occurred_at AT TIME ZONE 'UTC', r.tenant_id, r.actor_type,
                 r.actor_user_id, r.actor_membership_id, r.action, r.entity_table, r.entity_id,
                 r.old_values::text, r.new_values::text, array_to_string(r.changed_columns, ','),
                 r.outcome, r.reason, r.request_id, host(r.client_ip)), 'sha256')
$$;

CREATE FUNCTION audit.tg_hash_row() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  NEW.row_hash := audit.compute_row_hash(NEW);
  RETURN NEW;
END $$;
CREATE TRIGGER audit_log_hash BEFORE INSERT ON audit.audit_log
  FOR EACH ROW EXECUTE FUNCTION audit.tg_hash_row();

CREATE FUNCTION audit.tg_immutable() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'audit records are immutable' USING ERRCODE = 'BM004';
END $$;
CREATE TRIGGER audit_log_no_update BEFORE UPDATE OR DELETE ON audit.audit_log
  FOR EACH ROW EXECUTE FUNCTION audit.tg_immutable();
-- TRUNCATE is blocked by privileges (only bms_owner) and by statement trigger:
CREATE TRIGGER audit_log_no_truncate BEFORE TRUNCATE ON audit.audit_log
  FOR EACH STATEMENT EXECUTE FUNCTION audit.tg_immutable();

-- ---------- Hash-chain seals -------------------------------------------
CREATE TABLE audit.audit_seals (
  tenant_key      uuid NOT NULL,     -- tenant_id, or the nil UUID for platform events
  seal_no         bigint NOT NULL,
  from_seq        bigint NOT NULL,
  to_seq          bigint NOT NULL,
  row_count       integer NOT NULL CHECK (row_count > 0),
  prev_chain_hash bytea,
  chain_hash      bytea NOT NULL,
  sealed_at       timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_key, seal_no),
  CHECK (to_seq >= from_seq)
);
CREATE TRIGGER audit_seals_no_update BEFORE UPDATE OR DELETE ON audit.audit_seals
  FOR EACH ROW EXECUTE FUNCTION audit.tg_immutable();
COMMENT ON TABLE audit.audit_seals IS 'Run audit.seal_all() every 15 min (worker). Export seal hashes daily to WORM object storage so even a DB superuser cannot rewrite history unnoticed.';

CREATE FUNCTION audit.seal_tenant(p_tenant uuid, p_lag interval DEFAULT interval '5 minutes')
RETURNS integer LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE
  k uuid := coalesce(p_tenant, '00000000-0000-0000-0000-000000000000');
  last audit.audit_seals%ROWTYPE;
  h bytea; r record; n integer := 0; first_seq bigint; last_seq bigint;
BEGIN
  PERFORM pg_advisory_xact_lock(hashtextextended('audit_seal:' || k::text, 0));
  SELECT * INTO last FROM audit.audit_seals WHERE tenant_key = k ORDER BY seal_no DESC LIMIT 1;
  h := last.chain_hash;
  FOR r IN SELECT seq, row_hash FROM audit.audit_log
            WHERE tenant_id IS NOT DISTINCT FROM p_tenant
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

CREATE FUNCTION audit.seal_all() RETURNS integer
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE t uuid; total integer := 0;
BEGIN
  FOR t IN SELECT DISTINCT tenant_id FROM audit.audit_log WHERE occurred_at > now() - interval '40 days' LOOP
    total := total + audit.seal_tenant(t);
  END LOOP;
  RETURN total;
END $$;

-- Recomputes row hashes and the chain; returns the first broken seal (null = intact).
CREATE FUNCTION audit.verify_tenant(p_tenant uuid) RETURNS TABLE (seal_no bigint, problem text)
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE s record; h bytea; r audit.audit_log; cnt integer;
BEGIN
  FOR s IN SELECT * FROM audit.audit_seals
            WHERE tenant_key = coalesce(p_tenant, '00000000-0000-0000-0000-000000000000') ORDER BY seal_no LOOP
    h := s.prev_chain_hash; cnt := 0;
    FOR r IN SELECT a.* FROM audit.audit_log a
              WHERE a.tenant_id IS NOT DISTINCT FROM p_tenant AND a.seq BETWEEN s.from_seq AND s.to_seq
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

-- ---------- Writers -----------------------------------------------------
-- Explicit business / security events from the API (login, export, approval,
-- permission denied, backup download...). The only write path for bms_app.
CREATE FUNCTION audit.log_event(
  p_action text, p_entity_table text DEFAULT NULL, p_entity_id uuid DEFAULT NULL,
  p_outcome text DEFAULT 'success', p_reason text DEFAULT NULL,
  p_details jsonb DEFAULT NULL, p_actor_type text DEFAULT 'user', p_user_agent text DEFAULT NULL)
RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
BEGIN
  INSERT INTO audit.audit_log (tenant_id, actor_type, actor_user_id, actor_membership_id, action,
                               entity_table, entity_id, new_values, outcome, reason, request_id,
                               client_ip, user_agent, row_hash)
  VALUES (app.current_tenant_id(), p_actor_type, app.current_user_id(), app.current_membership_id(),
          p_action, p_entity_table, p_entity_id, p_details, p_outcome, p_reason,
          app.current_request_id(), app.current_client_ip(), p_user_agent, ''::bytea);
END $$;

-- Generic row-change capture. Trigger args = column names to redact.
CREATE FUNCTION audit.tg_capture() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE
  o jsonb; n jsonb; chg text[]; red text[] := coalesce(TG_ARGV::text[], '{}');
  t uuid; e uuid; k text;
BEGIN
  IF TG_OP <> 'INSERT' THEN o := to_jsonb(OLD); END IF;
  IF TG_OP <> 'DELETE' THEN n := to_jsonb(NEW); END IF;
  t := coalesce(n ->> 'tenant_id', o ->> 'tenant_id')::uuid;
  e := CASE WHEN coalesce(n, o) ? 'id' THEN coalesce(n ->> 'id', o ->> 'id')::uuid END;

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

  INSERT INTO audit.audit_log (tenant_id, actor_type, actor_user_id, actor_membership_id, action,
                               entity_table, entity_id, old_values, new_values, changed_columns,
                               request_id, client_ip, row_hash)
  VALUES (t, CASE WHEN app.current_membership_id() IS NULL THEN 'system' ELSE 'user' END,
          app.current_user_id(), app.current_membership_id(), lower(TG_OP),
          TG_TABLE_NAME, e, o, n, chg, app.current_request_id(), app.current_client_ip(), ''::bytea);
  RETURN NULL;
END $$;

-- ---------- Partition maintenance --------------------------------------
CREATE FUNCTION audit.ensure_partitions(p_months_ahead integer DEFAULT 3)
RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE m date; i integer; tbl text;
BEGIN
  FOR i IN 0..p_months_ahead LOOP
    m := (date_trunc('month', now()) + make_interval(months => i))::date;
    FOREACH tbl IN ARRAY ARRAY['audit.audit_log', 'iam.auth_events'] LOOP
      IF to_regclass(tbl || '_' || to_char(m, 'YYYYMM')) IS NULL THEN
        EXECUTE format('CREATE TABLE %s_%s PARTITION OF %s FOR VALUES FROM (%L) TO (%L)',
                       tbl, to_char(m, 'YYYYMM'), tbl, m, (m + interval '1 month')::date);
      END IF;
    END LOOP;
  END LOOP;
END $$;
DO $$ BEGIN PERFORM audit.ensure_partitions(3); END $$;
