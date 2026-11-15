-- Superuser bootstrap for a fresh PostgreSQL cluster.
-- The application connects as bms_app (no BYPASSRLS, not the table owner).
-- Migrations and SECURITY DEFINER functions run as bms_owner, which has
-- BYPASSRLS so login lookups work while FORCE ROW LEVEL SECURITY is on.

DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'bms_owner') THEN
    CREATE ROLE bms_owner LOGIN PASSWORD 'bms_owner_dev' BYPASSRLS CREATEDB NOSUPERUSER;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'bms_app') THEN
    CREATE ROLE bms_app LOGIN PASSWORD 'bms_app_dev' NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE;
  END IF;
END $$;

SELECT 'CREATE DATABASE bms OWNER bms_owner'
WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = 'bms')\gexec

\connect bms

ALTER SCHEMA public OWNER TO bms_owner;
GRANT CONNECT ON DATABASE bms TO bms_app;
GRANT USAGE ON SCHEMA public TO bms_app;
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
