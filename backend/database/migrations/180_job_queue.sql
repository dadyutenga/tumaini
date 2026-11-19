-- =====================================================================
-- 180_job_queue.sql — background job queue and daily schedule marks
--
-- The specification lists background workers (reminders, recurring expenses, day-end checks, exports)
-- but defines no queue for them. This adds one, tenant-scoped like every other table. The API role
-- enqueues from inside a request transaction (so a job exists if and only if the business change that
-- caused it committed); the worker role claims and finishes jobs across tenants through explicit
-- worker policies, the same pattern 140 uses for notifications. Payloads hold ids only.
-- Additive: no existing object changes.
-- =====================================================================
SET search_path = app, public;

CREATE TABLE app.job_queue (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id    uuid NOT NULL REFERENCES app.tenants(id),
  kind         text NOT NULL CHECK (kind ~ '^[a-z_]{3,60}$'),
  payload      jsonb NOT NULL DEFAULT '{}'::jsonb,
  run_at       timestamptz NOT NULL DEFAULT now(),
  attempts     integer NOT NULL DEFAULT 0,
  max_attempts integer NOT NULL DEFAULT 5,
  status       text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'running', 'done', 'failed')),
  last_error   text CHECK (length(last_error) <= 2000),
  locked_at    timestamptz,
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX job_queue_ready_idx ON app.job_queue (run_at) WHERE status = 'queued';
CREATE INDEX job_queue_kind_status_idx ON app.job_queue (tenant_id, kind, status);

CREATE TABLE app.scheduled_job_marks (
  tenant_id  uuid NOT NULL REFERENCES app.tenants(id),
  kind       text NOT NULL,
  run_date   date NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, kind, run_date)
);
COMMENT ON TABLE app.scheduled_job_marks IS 'One row per tenant, job kind and local date: makes the daily scheduler safe to run from several processes.';

ALTER TABLE app.job_queue ENABLE ROW LEVEL SECURITY;
ALTER TABLE app.job_queue FORCE ROW LEVEL SECURITY;
ALTER TABLE app.scheduled_job_marks ENABLE ROW LEVEL SECURITY;
ALTER TABLE app.scheduled_job_marks FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON app.job_queue
  USING (tenant_id = app.current_tenant_id()) WITH CHECK (tenant_id = app.current_tenant_id());
CREATE POLICY tenant_isolation ON app.scheduled_job_marks
  USING (tenant_id = app.current_tenant_id()) WITH CHECK (tenant_id = app.current_tenant_id());

-- The worker is a platform role: it works across tenants. These policies are the only place that is
-- granted, and only to the queue, the marks, and read access to the two tables the scheduler needs.
CREATE POLICY worker_all ON app.job_queue TO bms_worker USING (true) WITH CHECK (true);
CREATE POLICY worker_all ON app.scheduled_job_marks TO bms_worker USING (true) WITH CHECK (true);
CREATE POLICY worker_read ON app.tenants FOR SELECT TO bms_worker USING (true);
CREATE POLICY worker_read ON app.tenant_setting_values FOR SELECT TO bms_worker USING (true);

GRANT SELECT, INSERT ON app.job_queue TO bms_app;
GRANT SELECT, INSERT ON app.job_queue TO bms_worker;
GRANT UPDATE (run_at, attempts, status, last_error, locked_at, updated_at) ON app.job_queue TO bms_worker;
GRANT DELETE ON app.job_queue TO bms_worker;
GRANT SELECT, INSERT ON app.scheduled_job_marks TO bms_worker;
GRANT SELECT ON app.job_queue, app.scheduled_job_marks TO bms_readonly;
