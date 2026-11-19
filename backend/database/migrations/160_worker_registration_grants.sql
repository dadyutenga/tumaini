-- =====================================================================
-- 160_worker_registration_grants.sql — let tenant registration be one transaction
--
-- app.provision_tenant is executable only by bms_worker (150). Registering a business also creates
-- its first user, and until now that needed a second connection with a different role, so a failure
-- in between could leave a user without a tenant. This grants the worker role exactly the identity
-- writes registration needs, so the API can create the user and provision the tenant atomically.
-- Additive: no existing object changes.
-- =====================================================================
GRANT SELECT, INSERT ON iam.users, iam.user_credentials TO bms_worker;
GRANT INSERT ON iam.password_history TO bms_worker;
