-- In-process PostgreSQL queue. The worker reads across tenants, so these
-- tables are infrastructure and are not tenant-isolated. Payloads hold ids only.
CREATE TABLE jobs (
  id            uuid PRIMARY KEY,
  business_id   uuid REFERENCES businesses(id),
  kind          text NOT NULL,
  payload       jsonb NOT NULL DEFAULT '{}'::jsonb,
  run_at        timestamptz NOT NULL DEFAULT now(),
  attempts      integer NOT NULL DEFAULT 0,
  max_attempts  integer NOT NULL DEFAULT 5,
  status        text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','running','done','failed')),
  last_error    text,
  locked_at     timestamptz,
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX jobs_ready_idx ON jobs (run_at) WHERE status = 'queued';

CREATE TABLE scheduled_job_marks (
  business_id  uuid NOT NULL REFERENCES businesses(id),
  kind         text NOT NULL,
  run_date     date NOT NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (business_id, kind, run_date)
);

CREATE TRIGGER set_updated_at BEFORE UPDATE ON jobs
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

GRANT SELECT, INSERT, UPDATE, DELETE ON jobs, scheduled_job_marks TO bms_app;
