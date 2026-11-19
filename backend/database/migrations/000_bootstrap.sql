-- =====================================================================
-- 000_bootstrap.sql
-- Business Management System (BMS) — PostgreSQL 16
-- Run ONCE per database as a superuser (or cloud admin role).
-- Creates cluster roles, extensions and schemas. All later migrations
-- run as bms_owner (see migrate.sh).
-- =====================================================================

DO $$
BEGIN
  -- Owns every object. Never used by the running application.
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'bms_owner') THEN
    CREATE ROLE bms_owner NOLOGIN;
  END IF;
  -- Privilege set for the Go API. Not owner, no BYPASSRLS => RLS always applies.
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'bms_app') THEN
    CREATE ROLE bms_app NOLOGIN NOBYPASSRLS;
  END IF;
  -- Background workers (outbox sender, reminders, audit sealing, partitions).
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'bms_worker') THEN
    CREATE ROLE bms_worker NOLOGIN NOBYPASSRLS;
  END IF;
  -- Read-only reporting / support (still tenant-scoped through RLS).
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'bms_readonly') THEN
    CREATE ROLE bms_readonly NOLOGIN NOBYPASSRLS;
  END IF;
  -- Login roles. Passwords are set out-of-band from the secrets manager:
  --   ALTER ROLE bms_api LOGIN PASSWORD '<from vault>';
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'bms_api') THEN
    CREATE ROLE bms_api NOLOGIN IN ROLE bms_app;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'bms_jobs') THEN
    CREATE ROLE bms_jobs NOLOGIN IN ROLE bms_worker;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'bms_migrator') THEN
    CREATE ROLE bms_migrator NOLOGIN IN ROLE bms_owner;
  END IF;
END $$;

CREATE EXTENSION IF NOT EXISTS pgcrypto;    -- gen_random_uuid(), digest()
CREATE EXTENSION IF NOT EXISTS citext;      -- case-insensitive email / codes
CREATE EXTENSION IF NOT EXISTS btree_gist;  -- exclusion constraints (leave, pay history)
CREATE EXTENSION IF NOT EXISTS pg_trgm;     -- fuzzy search on names / products

CREATE SCHEMA IF NOT EXISTS iam   AUTHORIZATION bms_owner;  -- global identity (pre-tenant auth)
CREATE SCHEMA IF NOT EXISTS app   AUTHORIZATION bms_owner;  -- tenant business data (RLS)
CREATE SCHEMA IF NOT EXISTS audit AUTHORIZATION bms_owner;  -- append-only audit trail

REVOKE ALL ON SCHEMA public FROM PUBLIC;
GRANT USAGE ON SCHEMA public TO bms_owner, bms_app, bms_worker, bms_readonly;
GRANT CREATE ON SCHEMA public TO bms_owner;
