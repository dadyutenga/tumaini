CREATE TABLE approval_requests (
  id            uuid PRIMARY KEY,
  business_id   uuid NOT NULL REFERENCES businesses(id),
  branch_id     uuid REFERENCES branches(id),
  kind          text NOT NULL CHECK (kind IN (
                  'discount','refund','sale_void','expense','stock_adjustment','purchase_order',
                  'loan','leave','overtime','payroll_run','credit_override','payment_void')),
  subject_type  text NOT NULL,
  subject_id    uuid NOT NULL,
  amount        numeric(18,2),
  summary       text NOT NULL,
  payload       jsonb,
  status        text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected','cancelled')),
  requested_by  uuid NOT NULL REFERENCES users(id),
  decided_by    uuid REFERENCES users(id),
  decided_at    timestamptz,
  decision_note text,
  created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX approval_pending_idx ON approval_requests (business_id, branch_id) WHERE status = 'pending';
CREATE INDEX approval_subject_idx ON approval_requests (subject_type, subject_id);
CREATE INDEX approval_requester_idx ON approval_requests (requested_by, created_at DESC);

CREATE TABLE notifications (
  id            uuid PRIMARY KEY,
  business_id   uuid NOT NULL REFERENCES businesses(id),
  user_id       uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  type          text NOT NULL,
  title         text NOT NULL,
  body          text NOT NULL,
  link          text,
  entity_type   text,
  entity_id     uuid,
  read_at       timestamptz,
  created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX notifications_unread_idx ON notifications (user_id, created_at DESC) WHERE read_at IS NULL;
CREATE INDEX notifications_user_idx ON notifications (user_id, created_at DESC);

CREATE TABLE notification_rules (
  business_id   uuid NOT NULL REFERENCES businesses(id),
  type          text NOT NULL,
  in_app        boolean NOT NULL DEFAULT true,
  email         boolean NOT NULL DEFAULT false,
  sms           boolean NOT NULL DEFAULT false,
  days_before   integer,
  PRIMARY KEY (business_id, type)
);

CREATE TABLE audit_logs (
  id            uuid PRIMARY KEY,
  business_id   uuid NOT NULL REFERENCES businesses(id),
  branch_id     uuid,
  user_id       uuid,
  action        text NOT NULL,
  entity_type   text NOT NULL,
  entity_id     uuid,
  before        jsonb,
  after         jsonb,
  ip            inet,
  request_id    text,
  created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX audit_entity_idx ON audit_logs (entity_type, entity_id, created_at DESC);
CREATE INDEX audit_user_idx ON audit_logs (business_id, user_id, created_at DESC);
CREATE INDEX audit_list_idx ON audit_logs (business_id, created_at DESC);

CREATE TABLE files (
  id            uuid PRIMARY KEY,
  business_id   uuid NOT NULL REFERENCES businesses(id),
  path          text NOT NULL,
  original_name text NOT NULL,
  mime_type     text NOT NULL,
  size_bytes    bigint NOT NULL CHECK (size_bytes >= 0),
  uploaded_by   uuid NOT NULL REFERENCES users(id),
  created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE export_jobs (
  id            uuid PRIMARY KEY,
  business_id   uuid NOT NULL REFERENCES businesses(id),
  user_id       uuid NOT NULL REFERENCES users(id),
  kind          text NOT NULL,
  params        jsonb NOT NULL,
  status        text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','running','done','failed')),
  file_id       uuid REFERENCES files(id),
  error         text,
  created_at    timestamptz NOT NULL DEFAULT now(),
  finished_at   timestamptz
);
CREATE INDEX export_jobs_user_idx ON export_jobs (user_id, created_at DESC);
