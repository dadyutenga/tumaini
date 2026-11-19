-- =====================================================================
-- 050_sales_purchases_inventory.sql — POS, returns, purchasing,
-- stock ledger, adjustments, transfers, counts
-- =====================================================================
SET search_path = app, public;

-- ---------- POS devices (offline sync identity) ------------------------
CREATE TYPE app.device_type AS ENUM ('pos_terminal', 'tablet', 'browser');
CREATE TABLE app.devices (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id     uuid NOT NULL REFERENCES app.tenants(id),
  location_id   uuid NOT NULL,
  name          app.short_text NOT NULL,
  device_type   app.device_type NOT NULL,
  status        app.record_status NOT NULL DEFAULT 'active',
  last_sync_at  timestamptz,
  created_at    timestamptz NOT NULL DEFAULT now(),
  created_by    uuid DEFAULT app.current_membership_id(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  updated_by    uuid,
  row_version   integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id, location_id) REFERENCES app.locations(tenant_id, id)
);

-- ---------- Sales ---------------------------------------------------
CREATE TYPE app.sale_status AS ENUM ('draft', 'held', 'pending_approval', 'completed', 'voided');
CREATE TYPE app.discount_type AS ENUM ('amount', 'percent');

CREATE TABLE app.sales (
  id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id        uuid NOT NULL REFERENCES app.tenants(id),
  business_unit_id uuid NOT NULL,
  location_id      uuid NOT NULL,
  receipt_no       text,                         -- assigned at completion
  status           app.sale_status NOT NULL DEFAULT 'draft',
  customer_id      uuid,                         -- null = Walk-in
  sold_by          uuid NOT NULL DEFAULT app.current_membership_id(),
  device_id        uuid,
  client_ref       uuid,                         -- idempotency key from POS (offline sync / double click)
  business_date    date NOT NULL,
  subtotal         app.money_nn NOT NULL DEFAULT 0,
  discount_type    app.discount_type,
  discount_value   numeric(18,2) NOT NULL DEFAULT 0 CHECK (discount_value >= 0),
  discount_amount  app.money_nn NOT NULL DEFAULT 0,
  tax_amount       app.money_nn NOT NULL DEFAULT 0, -- informational when prices include tax
  total            app.money_nn NOT NULL DEFAULT 0,
  amount_paid      app.money_nn NOT NULL DEFAULT 0, -- maintained by payment allocation trigger
  balance_due      numeric(18,2) GENERATED ALWAYS AS (total - amount_paid) STORED,
  due_date         date,                         -- credit sales
  held_label       text CHECK (length(held_label) <= 60),
  held_at          timestamptz,
  completed_at     timestamptz,
  voided_at        timestamptz,
  voided_by        uuid,
  void_reason      text CHECK (length(void_reason) <= 1000),
  offline_created_at timestamptz,
  synced_at        timestamptz,
  notes            text CHECK (length(notes) <= 1000),
  created_at       timestamptz NOT NULL DEFAULT now(),
  created_by       uuid DEFAULT app.current_membership_id(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  updated_by       uuid,
  row_version      integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, receipt_no),
  UNIQUE (tenant_id, client_ref),
  CHECK (discount_type IS NOT NULL OR discount_value = 0),
  CHECK (discount_type <> 'percent' OR discount_value <= 100),
  CHECK (discount_amount <= subtotal),
  CHECK (total = subtotal - discount_amount),
  CHECK (amount_paid <= total),
  CHECK (status NOT IN ('completed', 'voided') OR (receipt_no IS NOT NULL AND completed_at IS NOT NULL)),
  CHECK (status <> 'voided' OR (voided_at IS NOT NULL AND voided_by IS NOT NULL AND void_reason IS NOT NULL)),
  CHECK (status <> 'held' OR held_at IS NOT NULL),
  FOREIGN KEY (tenant_id, business_unit_id) REFERENCES app.business_units(tenant_id, id),
  FOREIGN KEY (tenant_id, location_id)      REFERENCES app.locations(tenant_id, id),
  FOREIGN KEY (tenant_id, customer_id)      REFERENCES app.customers(tenant_id, id),
  FOREIGN KEY (tenant_id, sold_by)          REFERENCES app.memberships(tenant_id, id),
  FOREIGN KEY (tenant_id, voided_by)        REFERENCES app.memberships(tenant_id, id),
  FOREIGN KEY (tenant_id, device_id)        REFERENCES app.devices(tenant_id, id)
);
COMMENT ON COLUMN app.sales.business_date IS 'Trading day in the tenant timezone; drives end-of-day lock (app.day_closings).';
CREATE INDEX sales_history_idx  ON app.sales (tenant_id, business_date DESC, location_id) WHERE status IN ('completed', 'voided');
CREATE INDEX sales_seller_idx   ON app.sales (tenant_id, sold_by, business_date DESC);       -- "My sales"
CREATE INDEX sales_customer_idx ON app.sales (tenant_id, customer_id) WHERE customer_id IS NOT NULL;
CREATE INDEX sales_open_credit_idx ON app.sales (tenant_id, due_date) WHERE status = 'completed' AND balance_due > 0;
CREATE INDEX sales_held_idx     ON app.sales (tenant_id, location_id) WHERE status IN ('held', 'draft', 'pending_approval');

CREATE TABLE app.sale_lines (
  id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id         uuid NOT NULL REFERENCES app.tenants(id),
  sale_id           uuid NOT NULL,
  line_no           smallint NOT NULL CHECK (line_no > 0),
  product_id        uuid NOT NULL,
  product_name      app.short_text NOT NULL,      -- snapshot for receipts
  quantity          app.qty_pos NOT NULL,
  unit_price        app.money_nn NOT NULL,        -- snapshot of selling price (staff cannot change)
  line_discount     app.money_nn NOT NULL DEFAULT 0,
  tax_rate_pct      app.pct NOT NULL DEFAULT 0,
  tax_amount        app.money_nn NOT NULL DEFAULT 0,
  line_total        app.money_nn NOT NULL,
  unit_cost         numeric(18,4) CHECK (unit_cost >= 0),  -- COGS snapshot at completion
  returned_quantity app.qty NOT NULL DEFAULT 0,
  warranty_months   smallint NOT NULL DEFAULT 0,
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, sale_id, line_no),
  CHECK (line_total = round(quantity * unit_price, 2) - line_discount),
  CHECK (line_discount <= round(quantity * unit_price, 2)),
  CHECK (returned_quantity >= 0 AND returned_quantity <= quantity),
  FOREIGN KEY (tenant_id, sale_id)    REFERENCES app.sales(tenant_id, id) ON DELETE CASCADE,
  FOREIGN KEY (tenant_id, product_id) REFERENCES app.products(tenant_id, id)
);
CREATE INDEX sale_lines_product_idx ON app.sale_lines (tenant_id, product_id);

CREATE TABLE app.sale_line_units (
  tenant_id       uuid NOT NULL,
  sale_line_id    uuid NOT NULL,
  product_unit_id uuid NOT NULL,
  PRIMARY KEY (tenant_id, sale_line_id, product_unit_id),
  FOREIGN KEY (tenant_id, sale_line_id)    REFERENCES app.sale_lines(tenant_id, id) ON DELETE CASCADE,
  FOREIGN KEY (tenant_id, product_unit_id) REFERENCES app.product_units(tenant_id, id)
);
CREATE INDEX sale_line_units_unit_idx ON app.sale_line_units (tenant_id, product_unit_id);

-- ---------- Returns ------------------------------------------------
CREATE TYPE app.return_status AS ENUM ('requested', 'approved', 'rejected', 'completed', 'cancelled');
CREATE TYPE app.return_resolution AS ENUM ('refund', 'exchange');
CREATE TYPE app.item_condition AS ENUM ('resellable', 'damaged');

CREATE TABLE app.sale_returns (
  id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id      uuid NOT NULL REFERENCES app.tenants(id),
  return_no      text NOT NULL,
  sale_id        uuid NOT NULL,
  location_id    uuid NOT NULL,
  status         app.return_status NOT NULL DEFAULT 'requested',
  resolution     app.return_resolution NOT NULL,
  reason         text NOT NULL CHECK (length(btrim(reason)) >= 3),
  refund_amount  app.money_nn NOT NULL DEFAULT 0,
  exchange_sale_id uuid,
  requested_by   uuid NOT NULL DEFAULT app.current_membership_id(),
  decided_by     uuid,
  decided_at     timestamptz,
  completed_at   timestamptz,
  business_date  date NOT NULL,
  created_at     timestamptz NOT NULL DEFAULT now(),
  created_by     uuid DEFAULT app.current_membership_id(),
  updated_at     timestamptz NOT NULL DEFAULT now(),
  updated_by     uuid,
  row_version    integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, return_no),
  CHECK (status NOT IN ('approved', 'rejected', 'completed') OR (decided_by IS NOT NULL AND decided_at IS NOT NULL)),
  CHECK (decided_by IS NULL OR decided_by <> requested_by),
  CHECK (status <> 'completed' OR completed_at IS NOT NULL),
  CHECK (resolution <> 'refund' OR refund_amount > 0 OR status IN ('requested','rejected','cancelled')),
  FOREIGN KEY (tenant_id, sale_id)          REFERENCES app.sales(tenant_id, id),
  FOREIGN KEY (tenant_id, exchange_sale_id) REFERENCES app.sales(tenant_id, id),
  FOREIGN KEY (tenant_id, location_id)      REFERENCES app.locations(tenant_id, id),
  FOREIGN KEY (tenant_id, requested_by)     REFERENCES app.memberships(tenant_id, id),
  FOREIGN KEY (tenant_id, decided_by)       REFERENCES app.memberships(tenant_id, id)
);
CREATE INDEX sale_returns_sale_idx ON app.sale_returns (tenant_id, sale_id);
CREATE INDEX sale_returns_pending_idx ON app.sale_returns (tenant_id) WHERE status = 'requested';

CREATE TABLE app.sale_return_lines (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id       uuid NOT NULL REFERENCES app.tenants(id),
  sale_return_id  uuid NOT NULL,
  sale_line_id    uuid NOT NULL,
  quantity        app.qty_pos NOT NULL,
  condition       app.item_condition NOT NULL,
  restock         boolean NOT NULL,
  refund_amount   app.money_nn NOT NULL DEFAULT 0,
  product_unit_id uuid,
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, sale_return_id, sale_line_id),
  CHECK (NOT restock OR condition = 'resellable'),
  FOREIGN KEY (tenant_id, sale_return_id)  REFERENCES app.sale_returns(tenant_id, id) ON DELETE CASCADE,
  FOREIGN KEY (tenant_id, sale_line_id)    REFERENCES app.sale_lines(tenant_id, id),
  FOREIGN KEY (tenant_id, product_unit_id) REFERENCES app.product_units(tenant_id, id)
);

-- ---------- Purchasing -------------------------------------------------
CREATE TYPE app.purchase_status AS ENUM ('draft', 'ordered', 'partially_received', 'received', 'cancelled');

CREATE TABLE app.purchases (
  id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id        uuid NOT NULL REFERENCES app.tenants(id),
  purchase_no      text NOT NULL,
  supplier_id      uuid NOT NULL,
  business_unit_id uuid NOT NULL,
  location_id      uuid NOT NULL,
  status           app.purchase_status NOT NULL DEFAULT 'draft',
  order_date       date NOT NULL,
  expected_date    date,
  due_date         date,
  subtotal         app.money_nn NOT NULL DEFAULT 0,
  discount_amount  app.money_nn NOT NULL DEFAULT 0,
  tax_amount       app.money_nn NOT NULL DEFAULT 0,
  total            app.money_nn NOT NULL DEFAULT 0,
  amount_paid      app.money_nn NOT NULL DEFAULT 0,
  balance_due      numeric(18,2) GENERATED ALWAYS AS (total - amount_paid) STORED,
  supplier_invoice_ref text CHECK (length(supplier_invoice_ref) <= 60),
  notes            text CHECK (length(notes) <= 2000),
  created_at       timestamptz NOT NULL DEFAULT now(),
  created_by       uuid DEFAULT app.current_membership_id(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  updated_by       uuid,
  row_version      integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, purchase_no),
  CHECK (total = subtotal - discount_amount + tax_amount),
  CHECK (amount_paid <= total),
  FOREIGN KEY (tenant_id, supplier_id)      REFERENCES app.suppliers(tenant_id, id),
  FOREIGN KEY (tenant_id, business_unit_id) REFERENCES app.business_units(tenant_id, id),
  FOREIGN KEY (tenant_id, location_id)      REFERENCES app.locations(tenant_id, id)
);
CREATE INDEX purchases_supplier_idx ON app.purchases (tenant_id, supplier_id, order_date DESC);
CREATE INDEX purchases_open_idx ON app.purchases (tenant_id, due_date) WHERE status <> 'cancelled' AND balance_due > 0;

CREATE TABLE app.purchase_lines (
  id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id         uuid NOT NULL REFERENCES app.tenants(id),
  purchase_id       uuid NOT NULL,
  line_no           smallint NOT NULL CHECK (line_no > 0),
  product_id        uuid NOT NULL,
  quantity_ordered  app.qty_pos NOT NULL,
  quantity_received app.qty NOT NULL DEFAULT 0,
  unit_cost         app.money_nn NOT NULL,
  line_total        app.money_nn NOT NULL,
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, purchase_id, line_no),
  CHECK (quantity_received >= 0 AND quantity_received <= quantity_ordered),
  CHECK (line_total = round(quantity_ordered * unit_cost, 2)),
  FOREIGN KEY (tenant_id, purchase_id) REFERENCES app.purchases(tenant_id, id) ON DELETE CASCADE,
  FOREIGN KEY (tenant_id, product_id)  REFERENCES app.products(tenant_id, id)
);

CREATE TABLE app.goods_receipts (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id     uuid NOT NULL REFERENCES app.tenants(id),
  receipt_no    text NOT NULL,
  purchase_id   uuid NOT NULL,
  location_id   uuid NOT NULL,
  received_at   timestamptz NOT NULL DEFAULT now(),
  business_date date NOT NULL,
  notes         text CHECK (length(notes) <= 1000),
  created_at    timestamptz NOT NULL DEFAULT now(),
  created_by    uuid DEFAULT app.current_membership_id(),
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, receipt_no),
  FOREIGN KEY (tenant_id, purchase_id) REFERENCES app.purchases(tenant_id, id),
  FOREIGN KEY (tenant_id, location_id) REFERENCES app.locations(tenant_id, id)
);

CREATE TABLE app.goods_receipt_lines (
  id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id        uuid NOT NULL REFERENCES app.tenants(id),
  goods_receipt_id uuid NOT NULL,
  purchase_line_id uuid NOT NULL,
  quantity         app.qty_pos NOT NULL,
  unit_cost        app.money_nn NOT NULL,
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, goods_receipt_id, purchase_line_id),
  FOREIGN KEY (tenant_id, goods_receipt_id) REFERENCES app.goods_receipts(tenant_id, id),
  FOREIGN KEY (tenant_id, purchase_line_id) REFERENCES app.purchase_lines(tenant_id, id)
);

CREATE TABLE app.goods_receipt_line_units (
  tenant_id             uuid NOT NULL,
  goods_receipt_line_id uuid NOT NULL,
  product_unit_id       uuid NOT NULL,
  PRIMARY KEY (tenant_id, goods_receipt_line_id, product_unit_id),
  UNIQUE (tenant_id, product_unit_id),
  FOREIGN KEY (tenant_id, goods_receipt_line_id) REFERENCES app.goods_receipt_lines(tenant_id, id),
  FOREIGN KEY (tenant_id, product_unit_id)       REFERENCES app.product_units(tenant_id, id)
);

-- ---------- Stock adjustments, transfers, counts -----------------------
CREATE TYPE app.adjustment_reason AS ENUM ('damaged', 'lost', 'found', 'count_correction', 'expired', 'theft', 'opening', 'other');
CREATE TYPE app.doc_post_status AS ENUM ('draft', 'posted', 'cancelled');

CREATE TABLE app.stock_adjustments (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id     uuid NOT NULL REFERENCES app.tenants(id),
  adjustment_no text NOT NULL,
  location_id   uuid NOT NULL,
  reason        app.adjustment_reason NOT NULL,
  note          text CHECK (length(note) <= 1000),
  status        app.doc_post_status NOT NULL DEFAULT 'draft',
  posted_at     timestamptz,
  business_date date NOT NULL,
  created_at    timestamptz NOT NULL DEFAULT now(),
  created_by    uuid DEFAULT app.current_membership_id(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  updated_by    uuid,
  row_version   integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, adjustment_no),
  CHECK (status <> 'posted' OR posted_at IS NOT NULL),
  CHECK (reason <> 'other' OR note IS NOT NULL),
  FOREIGN KEY (tenant_id, location_id) REFERENCES app.locations(tenant_id, id)
);

CREATE TABLE app.stock_adjustment_lines (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  stock_adjustment_id uuid NOT NULL,
  product_id          uuid NOT NULL,
  quantity_delta      app.qty NOT NULL CHECK (quantity_delta <> 0),
  unit_cost           numeric(18,4) CHECK (unit_cost >= 0),
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, stock_adjustment_id, product_id),
  FOREIGN KEY (tenant_id, stock_adjustment_id) REFERENCES app.stock_adjustments(tenant_id, id) ON DELETE CASCADE,
  FOREIGN KEY (tenant_id, product_id)          REFERENCES app.products(tenant_id, id)
);

CREATE TYPE app.transfer_status AS ENUM ('draft', 'dispatched', 'received', 'cancelled');
CREATE TABLE app.stock_transfers (
  id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id        uuid NOT NULL REFERENCES app.tenants(id),
  transfer_no      text NOT NULL,
  from_location_id uuid NOT NULL,
  to_location_id   uuid NOT NULL,
  status           app.transfer_status NOT NULL DEFAULT 'draft',
  dispatched_at    timestamptz,
  dispatched_by    uuid,
  received_at      timestamptz,
  received_by      uuid,
  note             text CHECK (length(note) <= 1000),
  created_at       timestamptz NOT NULL DEFAULT now(),
  created_by       uuid DEFAULT app.current_membership_id(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  updated_by       uuid,
  row_version      integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, transfer_no),
  CHECK (from_location_id <> to_location_id),
  CHECK (status NOT IN ('dispatched', 'received') OR dispatched_at IS NOT NULL),
  CHECK (status <> 'received' OR received_at IS NOT NULL),
  FOREIGN KEY (tenant_id, from_location_id) REFERENCES app.locations(tenant_id, id),
  FOREIGN KEY (tenant_id, to_location_id)   REFERENCES app.locations(tenant_id, id),
  FOREIGN KEY (tenant_id, dispatched_by)    REFERENCES app.memberships(tenant_id, id),
  FOREIGN KEY (tenant_id, received_by)      REFERENCES app.memberships(tenant_id, id)
);

CREATE TABLE app.stock_transfer_lines (
  id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id         uuid NOT NULL REFERENCES app.tenants(id),
  stock_transfer_id uuid NOT NULL,
  product_id        uuid NOT NULL,
  quantity          app.qty_pos NOT NULL,
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, stock_transfer_id, product_id),
  FOREIGN KEY (tenant_id, stock_transfer_id) REFERENCES app.stock_transfers(tenant_id, id) ON DELETE CASCADE,
  FOREIGN KEY (tenant_id, product_id)        REFERENCES app.products(tenant_id, id)
);

CREATE TYPE app.count_status AS ENUM ('in_progress', 'completed', 'cancelled');
CREATE TABLE app.stock_counts (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  location_id         uuid NOT NULL,
  status              app.count_status NOT NULL DEFAULT 'in_progress',
  stock_adjustment_id uuid,            -- variance posting
  completed_at        timestamptz,
  created_at          timestamptz NOT NULL DEFAULT now(),
  created_by          uuid DEFAULT app.current_membership_id(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  updated_by          uuid,
  row_version         integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id, location_id)         REFERENCES app.locations(tenant_id, id),
  FOREIGN KEY (tenant_id, stock_adjustment_id) REFERENCES app.stock_adjustments(tenant_id, id)
);
CREATE UNIQUE INDEX stock_counts_one_open ON app.stock_counts (tenant_id, location_id) WHERE status = 'in_progress';

CREATE TABLE app.stock_count_lines (
  tenant_id      uuid NOT NULL,
  stock_count_id uuid NOT NULL,
  product_id     uuid NOT NULL,
  system_qty     app.qty NOT NULL,
  counted_qty    app.qty CHECK (counted_qty >= 0),
  counted_by     uuid,
  counted_at     timestamptz,
  PRIMARY KEY (tenant_id, stock_count_id, product_id),
  FOREIGN KEY (tenant_id, stock_count_id) REFERENCES app.stock_counts(tenant_id, id) ON DELETE CASCADE,
  FOREIGN KEY (tenant_id, product_id)     REFERENCES app.products(tenant_id, id),
  FOREIGN KEY (tenant_id, counted_by)     REFERENCES app.memberships(tenant_id, id)
);

-- ---------- Stock ledger (single source of truth) ----------------------
CREATE TYPE app.stock_movement_type AS ENUM (
  'purchase_receipt', 'sale', 'sale_void', 'sale_return', 'adjustment', 'transfer_out', 'transfer_in');

CREATE TABLE app.stock_movements (
  id                       uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id                uuid NOT NULL REFERENCES app.tenants(id),
  location_id              uuid NOT NULL,
  product_id               uuid NOT NULL,
  movement_type            app.stock_movement_type NOT NULL,
  quantity_delta           app.qty NOT NULL CHECK (quantity_delta <> 0),
  unit_cost                numeric(18,4) CHECK (unit_cost >= 0),
  business_date            date NOT NULL,
  occurred_at              timestamptz NOT NULL DEFAULT now(),
  sale_line_id             uuid,
  sale_return_line_id      uuid,
  goods_receipt_line_id    uuid,
  stock_adjustment_line_id uuid,
  stock_transfer_line_id   uuid,
  created_at               timestamptz NOT NULL DEFAULT now(),
  created_by               uuid DEFAULT app.current_membership_id(),
  UNIQUE (tenant_id, id),
  CHECK (num_nonnulls(sale_line_id, sale_return_line_id, goods_receipt_line_id,
                      stock_adjustment_line_id, stock_transfer_line_id) = 1),
  CHECK (CASE movement_type
           WHEN 'purchase_receipt' THEN goods_receipt_line_id IS NOT NULL AND quantity_delta > 0
           WHEN 'sale'             THEN sale_line_id IS NOT NULL AND quantity_delta < 0
           WHEN 'sale_void'        THEN sale_line_id IS NOT NULL AND quantity_delta > 0
           WHEN 'sale_return'      THEN sale_return_line_id IS NOT NULL AND quantity_delta > 0
           WHEN 'adjustment'       THEN stock_adjustment_line_id IS NOT NULL
           WHEN 'transfer_out'     THEN stock_transfer_line_id IS NOT NULL AND quantity_delta < 0
           WHEN 'transfer_in'      THEN stock_transfer_line_id IS NOT NULL AND quantity_delta > 0
         END),
  FOREIGN KEY (tenant_id, location_id)              REFERENCES app.locations(tenant_id, id),
  FOREIGN KEY (tenant_id, product_id)               REFERENCES app.products(tenant_id, id),
  FOREIGN KEY (tenant_id, sale_line_id)             REFERENCES app.sale_lines(tenant_id, id),
  FOREIGN KEY (tenant_id, sale_return_line_id)      REFERENCES app.sale_return_lines(tenant_id, id),
  FOREIGN KEY (tenant_id, goods_receipt_line_id)    REFERENCES app.goods_receipt_lines(tenant_id, id),
  FOREIGN KEY (tenant_id, stock_adjustment_line_id) REFERENCES app.stock_adjustment_lines(tenant_id, id),
  FOREIGN KEY (tenant_id, stock_transfer_line_id)   REFERENCES app.stock_transfer_lines(tenant_id, id)
);
COMMENT ON TABLE app.stock_movements IS 'Append-only stock ledger. Every stock change is one row; stock_balances is a trigger-maintained projection. "Every movement shows who did it" (spec §6) = created_by.';
CREATE INDEX stock_movements_product_idx ON app.stock_movements (tenant_id, product_id, occurred_at DESC);
CREATE INDEX stock_movements_location_date_idx ON app.stock_movements (tenant_id, location_id, business_date);
-- Prevent the same source line from being posted twice with the same type (idempotent posting).
CREATE UNIQUE INDEX stock_movements_once ON app.stock_movements
  (tenant_id, movement_type, coalesce(sale_line_id, sale_return_line_id, goods_receipt_line_id,
                                      stock_adjustment_line_id, stock_transfer_line_id));

CREATE TABLE app.stock_balances (
  tenant_id        uuid NOT NULL,
  location_id      uuid NOT NULL,
  product_id       uuid NOT NULL,
  quantity_on_hand app.qty NOT NULL DEFAULT 0,
  updated_at       timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, location_id, product_id),
  FOREIGN KEY (tenant_id, location_id) REFERENCES app.locations(tenant_id, id),
  FOREIGN KEY (tenant_id, product_id)  REFERENCES app.products(tenant_id, id)
);
COMMENT ON TABLE app.stock_balances IS 'Derived. Written ONLY by app.tg_stock_movement_apply. Row lock serialises concurrent sales of the same product.';
CREATE INDEX stock_balances_product_idx ON app.stock_balances (tenant_id, product_id);
