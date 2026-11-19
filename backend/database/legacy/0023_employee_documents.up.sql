-- FR-EMP-19: contracts, ID copies and certificates attached to an employee.
CREATE TABLE IF NOT EXISTS employee_documents (
  id           uuid PRIMARY KEY,
  business_id  uuid NOT NULL REFERENCES businesses(id),
  employee_id  uuid NOT NULL REFERENCES employees(id),
  file_id      uuid NOT NULL REFERENCES files(id),
  kind         text NOT NULL CHECK (kind IN ('contract','id_copy','certificate','other')),
  name         text NOT NULL,
  uploaded_by  uuid NOT NULL REFERENCES users(id),
  created_at   timestamptz NOT NULL DEFAULT now(),
  UNIQUE (file_id)
);
CREATE INDEX IF NOT EXISTS employee_documents_employee_idx ON employee_documents (employee_id, created_at DESC);

ALTER TABLE employee_documents ENABLE ROW LEVEL SECURITY;
ALTER TABLE employee_documents FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON employee_documents;
CREATE POLICY tenant_isolation ON employee_documents
  USING (business_id = NULLIF(current_setting('app.business_id', true), '')::uuid)
  WITH CHECK (business_id = NULLIF(current_setting('app.business_id', true), '')::uuid);
GRANT SELECT, INSERT, UPDATE, DELETE ON employee_documents TO bms_app;
