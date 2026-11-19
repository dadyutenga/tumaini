-- Unified End-of-Day Closing (SRS Section 8).
CREATE TABLE IF NOT EXISTS day_closings (
  id                uuid PRIMARY KEY,
  business_id       uuid NOT NULL REFERENCES businesses(id),
  branch_id         uuid NOT NULL REFERENCES branches(id),
  business_date     date NOT NULL,
  expected_cash     numeric(14,2) NOT NULL DEFAULT 0,
  counted_cash      numeric(14,2) NOT NULL DEFAULT 0,
  difference        numeric(14,2) NOT NULL DEFAULT 0,
  sales_by_method   jsonb NOT NULL DEFAULT '{}'::jsonb,
  payments_received numeric(14,2) NOT NULL DEFAULT 0,
  expenses_total    numeric(14,2) NOT NULL DEFAULT 0,
  note              text,
  status            text NOT NULL DEFAULT 'closed' CHECK (status IN ('closed','reopened')),
  closed_by         uuid REFERENCES users(id),
  closed_at         timestamptz NOT NULL DEFAULT now(),
  reopened_by       uuid REFERENCES users(id),
  reopened_at       timestamptz,
  reopen_reason     text,
  created_at        timestamptz NOT NULL DEFAULT now(),
  updated_at        timestamptz NOT NULL DEFAULT now(),
  UNIQUE (business_id, branch_id, business_date)
);

CREATE INDEX IF NOT EXISTS idx_day_closings_biz_date ON day_closings (business_id, business_date DESC);

ALTER TABLE day_closings ENABLE ROW LEVEL SECURITY;
ALTER TABLE day_closings FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON day_closings;
CREATE POLICY tenant_isolation ON day_closings
  USING (business_id = NULLIF(current_setting('app.business_id', true), '')::uuid)
  WITH CHECK (business_id = NULLIF(current_setting('app.business_id', true), '')::uuid);

DROP TRIGGER IF EXISTS set_updated_at ON day_closings;
CREATE TRIGGER set_updated_at BEFORE UPDATE ON day_closings FOR EACH ROW EXECUTE FUNCTION set_updated_at();

GRANT SELECT, INSERT, UPDATE, DELETE ON day_closings TO bms_app;
