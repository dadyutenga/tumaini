-- =====================================================================
-- 320_retention_decisions.sql — retention periods decided on 2026-11-27:
-- audit log and login history are kept for 5 years, matching the five-year
-- tax record-keeping period. This records the decision only; no job deletes
-- anything.
-- =====================================================================
SET search_path = app, public;

UPDATE app.retention_policies
   SET retention_period = interval '5 years',
       legal_basis      = 'Business decision, aligned with the five-year tax record-keeping period',
       approved_by      = 'Dadi Utenga',
       approved_at      = timestamptz '2026-11-27 12:00:00+03'
 WHERE data_category IN ('audit_log', 'auth_events');
