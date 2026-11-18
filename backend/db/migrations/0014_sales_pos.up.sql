-- Sales and customers: till pay-outs, held carts, quotations, receipt reprints
-- and bad debt write-offs.

ALTER TABLE money_transfers ADD COLUMN IF NOT EXISTS till_session_id uuid REFERENCES till_sessions(id);
CREATE INDEX IF NOT EXISTS money_transfers_till_idx ON money_transfers (till_session_id) WHERE till_session_id IS NOT NULL;

-- The cart as the cashier entered it (discount types, customer, note), kept
-- for held carts and quotations so they can be resumed or converted exactly.
ALTER TABLE sales ADD COLUMN IF NOT EXISTS cart jsonb;
ALTER TABLE sales ADD COLUMN IF NOT EXISTS quotation_id uuid REFERENCES sales(id);
ALTER TABLE sales ADD COLUMN IF NOT EXISTS converted_sale_id uuid REFERENCES sales(id);
ALTER TABLE sales ADD COLUMN IF NOT EXISTS receipt_prints integer NOT NULL DEFAULT 0;
ALTER TABLE sales ADD COLUMN IF NOT EXISTS written_off numeric(18,2) NOT NULL DEFAULT 0;
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'sales'::regclass AND conname = 'sales_written_off_check') THEN
    ALTER TABLE sales ADD CONSTRAINT sales_written_off_check CHECK (written_off >= 0);
  END IF;
END $$;

-- A converted quotation keeps its own status.
ALTER TABLE sales DROP CONSTRAINT IF EXISTS sales_status_check;
ALTER TABLE sales ADD CONSTRAINT sales_status_check CHECK (status IN (
  'draft','held','pending_approval','completed','partially_refunded','refunded','voided','cancelled','converted'));

-- Balance stays total - paid - written off for every posted sale.
ALTER TABLE sales DROP CONSTRAINT IF EXISTS sales_check;
ALTER TABLE sales ADD CONSTRAINT sales_check CHECK (
  balance = total - paid_total - written_off
  OR status IN ('draft','held','voided','pending_approval','cancelled','converted'));

CREATE INDEX IF NOT EXISTS sales_held_idx ON sales (cashier_id) WHERE status = 'held';
CREATE INDEX IF NOT EXISTS sales_kind_idx ON sales (business_id, kind, created_at DESC);
CREATE INDEX IF NOT EXISTS sale_returns_list_idx ON sale_returns (business_id, branch_id, created_at DESC);

CREATE TABLE IF NOT EXISTS customer_write_offs (
  id            uuid PRIMARY KEY,
  business_id   uuid NOT NULL REFERENCES businesses(id),
  customer_id   uuid NOT NULL REFERENCES customers(id),
  amount        numeric(18,2) NOT NULL CHECK (amount > 0),
  reason        text NOT NULL,
  write_off_date date NOT NULL,
  created_by    uuid NOT NULL REFERENCES users(id),
  created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS customer_write_offs_customer_idx ON customer_write_offs (customer_id, write_off_date);

CREATE TABLE IF NOT EXISTS customer_write_off_items (
  id            uuid PRIMARY KEY,
  business_id   uuid NOT NULL REFERENCES businesses(id),
  write_off_id  uuid NOT NULL REFERENCES customer_write_offs(id) ON DELETE CASCADE,
  sale_id       uuid NOT NULL REFERENCES sales(id),
  amount        numeric(18,2) NOT NULL CHECK (amount > 0)
);

DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['customer_write_offs','customer_write_off_items'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('DROP POLICY IF EXISTS tenant_isolation ON %I', t);
    EXECUTE format('CREATE POLICY tenant_isolation ON %I USING (business_id = NULLIF(current_setting(''app.business_id'', true), '''')::uuid) WITH CHECK (business_id = NULLIF(current_setting(''app.business_id'', true), '''')::uuid)', t);
    EXECUTE format('GRANT SELECT, INSERT ON %I TO bms_app', t);
  END LOOP;
END $$;
