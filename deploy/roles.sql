-- Cluster roles the database dump refers to. pg_dump does not include roles, so they must exist before
-- a restore onto a new server. Mirrors the role block of backend/database/migrations/000_bootstrap.sql;
-- safe to run repeatedly. Login passwords are set afterwards from .env.
DO $$
DECLARE r record;
BEGIN
  FOR r IN SELECT * FROM (VALUES
      (1, 'bms_owner',    ''),
      (2, 'bms_app',      'NOBYPASSRLS'),
      (3, 'bms_worker',   'NOBYPASSRLS'),
      (4, 'bms_readonly', 'NOBYPASSRLS'),
      (5, 'bms_api',      'IN ROLE bms_app'),
      (6, 'bms_jobs',     'IN ROLE bms_worker'),
      (7, 'bms_migrator', 'IN ROLE bms_owner')
    ) AS v(ord, name, opts) ORDER BY ord
  LOOP
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = r.name) THEN
      EXECUTE format('CREATE ROLE %I NOLOGIN %s', r.name, r.opts);
    END IF;
  END LOOP;
END $$;
