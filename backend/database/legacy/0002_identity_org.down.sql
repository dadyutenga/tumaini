DROP FUNCTION IF EXISTS auth_find_reset_user(text);
DROP FUNCTION IF EXISTS auth_failure_count(uuid, timestamptz);
DROP FUNCTION IF EXISTS auth_set_lock(uuid, timestamptz);
DROP FUNCTION IF EXISTS auth_record_login(uuid, uuid, uuid, text, boolean, text, text, text);
DROP FUNCTION IF EXISTS auth_find_user(text);
DROP TABLE IF EXISTS idempotency_keys, document_sequences, tax_rates, settings, password_resets,
  login_history, sessions, user_branches, users, role_permissions, permissions, roles, branches, businesses CASCADE;
