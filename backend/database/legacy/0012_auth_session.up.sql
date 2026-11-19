CREATE OR REPLACE FUNCTION auth_find_session(p_hash bytea)
RETURNS TABLE (
  id uuid,
  business_id uuid,
  user_id uuid,
  role_id uuid,
  refresh_hash bytea,
  prev_hash bytea,
  expires_at timestamptz,
  last_used_at timestamptz,
  perm_version integer,
  user_active boolean
)
LANGUAGE sql
SECURITY DEFINER
SET search_path = public
AS $$
  SELECT s.id, s.business_id, s.user_id, u.role_id, s.refresh_hash, s.prev_hash,
         s.expires_at, s.last_used_at, r.perm_version, u.is_active AND b.is_active
  FROM sessions s
  JOIN users u ON u.id = s.user_id
  JOIN roles r ON r.id = u.role_id
  JOIN businesses b ON b.id = u.business_id
  WHERE s.refresh_hash = p_hash OR s.prev_hash = p_hash
  LIMIT 1;
$$;

REVOKE ALL ON FUNCTION auth_find_session(bytea) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION auth_find_session(bytea) TO bms_app;
