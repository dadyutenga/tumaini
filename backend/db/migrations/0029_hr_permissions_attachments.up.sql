-- Module 6: Project-based commissions, per-user permission overrides, and polymorphic attachments.

ALTER TABLE commission_rules DROP CONSTRAINT IF EXISTS commission_rules_calc_check;
ALTER TABLE commission_rules
  ADD CONSTRAINT commission_rules_calc_check
  CHECK (calc IN ('percent_of_sales', 'fixed_per_sale', 'fixed_per_project'));

CREATE TABLE user_permissions (
  business_id    uuid NOT NULL REFERENCES businesses(id) ON DELETE CASCADE,
  user_id        uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  permission_key text NOT NULL,
  allowed        boolean NOT NULL DEFAULT true,
  scope          text NOT NULL DEFAULT 'all' CHECK (scope IN ('all', 'branch', 'own')),
  approval_limit numeric(18,2),
  PRIMARY KEY (user_id, permission_key)
);

CREATE INDEX user_permissions_biz_user_idx ON user_permissions (business_id, user_id);

ALTER TABLE user_permissions ENABLE ROW LEVEL SECURITY;
ALTER TABLE user_permissions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON user_permissions
  USING (business_id = NULLIF(current_setting('app.business_id', true), '')::uuid)
  WITH CHECK (business_id = NULLIF(current_setting('app.business_id', true), '')::uuid);
GRANT SELECT, INSERT, UPDATE, DELETE ON user_permissions TO bms_app;

CREATE TABLE attachments (
  id          uuid PRIMARY KEY,
  business_id uuid NOT NULL REFERENCES businesses(id) ON DELETE CASCADE,
  entity_type text NOT NULL,
  entity_id   uuid NOT NULL,
  file_id     uuid NOT NULL REFERENCES files(id) ON DELETE CASCADE,
  label       text,
  uploaded_by uuid REFERENCES users(id),
  created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX attachments_entity_idx ON attachments (business_id, entity_type, entity_id);

ALTER TABLE attachments ENABLE ROW LEVEL SECURITY;
ALTER TABLE attachments FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON attachments
  USING (business_id = NULLIF(current_setting('app.business_id', true), '')::uuid)
  WITH CHECK (business_id = NULLIF(current_setting('app.business_id', true), '')::uuid);
GRANT SELECT, INSERT, UPDATE, DELETE ON attachments TO bms_app;
