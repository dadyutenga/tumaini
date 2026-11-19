-- The job scheduler runs outside any tenant, so it reads the list of active
-- businesses and their local clock settings through a definer function.
-- It returns ids and settings only, never business data.
CREATE OR REPLACE FUNCTION job_businesses()
RETURNS TABLE (id uuid, timezone text, day_start text)
LANGUAGE sql
SECURITY DEFINER
SET search_path = public
AS $$
  SELECT b.id,
         COALESCE((SELECT s.value #>> '{}' FROM settings s WHERE s.business_id = b.id AND s.key = 'system.timezone'), 'Africa/Dar_es_Salaam'),
         COALESCE((SELECT s.value #>> '{}' FROM settings s WHERE s.business_id = b.id AND s.key = 'system.business_day_start'), '00:00')
  FROM businesses b
  WHERE b.is_active;
$$;

REVOKE ALL ON FUNCTION job_businesses() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION job_businesses() TO bms_app;

CREATE INDEX IF NOT EXISTS jobs_kind_status_idx ON jobs (kind, status);
CREATE INDEX IF NOT EXISTS idempotency_keys_created_idx ON idempotency_keys (created_at);
