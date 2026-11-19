-- =====================================================================
-- 010_foundation.sql — domains, request context, generic triggers
-- =====================================================================
SET search_path = app, public;

-- ---------- Domains ---------------------------------------------------
-- TZS has no circulating minor unit, but 2 dp keeps tax/percent maths exact
-- and supports other currencies later.
CREATE DOMAIN app.money      AS numeric(18,2);
CREATE DOMAIN app.money_nn   AS numeric(18,2) CHECK (VALUE >= 0);
CREATE DOMAIN app.money_pos  AS numeric(18,2) CHECK (VALUE > 0);
CREATE DOMAIN app.qty        AS numeric(14,3);
CREATE DOMAIN app.qty_pos    AS numeric(14,3) CHECK (VALUE > 0);
CREATE DOMAIN app.pct        AS numeric(5,2)  CHECK (VALUE >= 0 AND VALUE <= 100);
CREATE DOMAIN app.phone_e164 AS text CHECK (VALUE ~ '^\+[1-9][0-9]{7,14}$');
CREATE DOMAIN app.email      AS citext CHECK (VALUE ~ '^[^@\s]+@[^@\s]+\.[^@\s]+$' AND length(VALUE) <= 254);
CREATE DOMAIN app.currency   AS char(3) CHECK (VALUE ~ '^[A-Z]{3}$');
CREATE DOMAIN app.short_text AS text CHECK (length(btrim(VALUE)) BETWEEN 1 AND 200);
CREATE DOMAIN app.long_text  AS text CHECK (length(VALUE) <= 10000);
CREATE DOMAIN app.https_url  AS text CHECK (VALUE ~ '^https://' AND length(VALUE) <= 2000);

-- ---------- Request context ------------------------------------------
-- The Go API sets these with SET LOCAL (transaction-scoped) at the start of
-- every transaction, after authenticating the session:
--   SELECT set_config('app.tenant_id',     $1, true),
--          set_config('app.user_id',       $2, true),
--          set_config('app.membership_id', $3, true),
--          set_config('app.request_id',    $4, true),
--          set_config('app.client_ip',     $5, true);
-- Transaction scope (is_local = true) is mandatory with PgBouncer
-- transaction pooling so context never leaks between requests.
CREATE FUNCTION app.current_tenant_id() RETURNS uuid
LANGUAGE sql STABLE PARALLEL SAFE AS
$$ SELECT nullif(current_setting('app.tenant_id', true), '')::uuid $$;

CREATE FUNCTION app.current_user_id() RETURNS uuid
LANGUAGE sql STABLE PARALLEL SAFE AS
$$ SELECT nullif(current_setting('app.user_id', true), '')::uuid $$;

CREATE FUNCTION app.current_membership_id() RETURNS uuid
LANGUAGE sql STABLE PARALLEL SAFE AS
$$ SELECT nullif(current_setting('app.membership_id', true), '')::uuid $$;

CREATE FUNCTION app.current_request_id() RETURNS text
LANGUAGE sql STABLE PARALLEL SAFE AS
$$ SELECT nullif(current_setting('app.request_id', true), '') $$;

CREATE FUNCTION app.current_client_ip() RETURNS inet
LANGUAGE sql STABLE PARALLEL SAFE AS
$$ SELECT nullif(current_setting('app.client_ip', true), '')::inet $$;

CREATE FUNCTION app.require_tenant() RETURNS uuid
LANGUAGE plpgsql STABLE AS $$
DECLARE t uuid := app.current_tenant_id();
BEGIN
  IF t IS NULL THEN
    RAISE EXCEPTION 'tenant context not set' USING ERRCODE = 'BM006';
  END IF;
  RETURN t;
END $$;

-- ---------- Error codes (SQLSTATE) ------------------------------------
-- BM001 invalid state transition      BM002 business day closed
-- BM003 insufficient stock            BM004 immutable record
-- BM005 over-allocation / amount      BM006 tenant context / mismatch
-- BM007 approval / permission         BM008 document integrity
COMMENT ON SCHEMA app IS 'BMS tenant data. SQLSTATE BM001..BM008 are business-rule violations; map to HTTP 409/422 in the API.';

-- ---------- Row maintenance trigger ----------------------------------
CREATE FUNCTION app.tg_touch_row() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  NEW.updated_at  := now();
  NEW.updated_by  := coalesce(app.current_membership_id(), NEW.updated_by);
  NEW.row_version := OLD.row_version + 1;
  RETURN NEW;
END $$;

-- Tenant id and primary key are immutable on every table.
CREATE FUNCTION app.tg_freeze_keys() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.tenant_id IS DISTINCT FROM OLD.tenant_id THEN
    RAISE EXCEPTION 'tenant_id is immutable on %', TG_TABLE_NAME USING ERRCODE = 'BM006';
  END IF;
  RETURN NEW;
END $$;

-- Append-only tables (ledgers, histories).
CREATE FUNCTION app.tg_block_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION '% is append-only; % not allowed', TG_TABLE_NAME, TG_OP
    USING ERRCODE = 'BM004';
END $$;

-- ---------- State machine enforcement --------------------------------
-- Allowed transitions live in data (app.state_transitions) so they can be
-- reviewed, tested and documented. from_status '*new*' = allowed initial state.
CREATE TABLE app.state_transitions (
  entity      text NOT NULL,
  from_status text NOT NULL,
  to_status   text NOT NULL,
  PRIMARY KEY (entity, from_status, to_status)
);
COMMENT ON TABLE app.state_transitions IS 'State machine definitions enforced by app.tg_enforce_transition. Global reference data.';

CREATE FUNCTION app.tg_enforce_transition() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  ent text := TG_ARGV[0];
  col text := coalesce(TG_ARGV[1], 'status');
  o text; n text;
BEGIN
  IF TG_OP = 'INSERT' THEN
    EXECUTE format('SELECT ($1).%I::text', col) INTO n USING NEW;
    o := '*new*';
  ELSE
    EXECUTE format('SELECT ($1).%I::text, ($2).%I::text', col, col) INTO o, n USING OLD, NEW;
    IF o IS NOT DISTINCT FROM n THEN RETURN NEW; END IF;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM app.state_transitions
                 WHERE entity = ent AND from_status = o AND to_status = n) THEN
    RAISE EXCEPTION 'invalid % transition: % -> %', ent, o, n
      USING ERRCODE = 'BM001';
  END IF;
  RETURN NEW;
END $$;
