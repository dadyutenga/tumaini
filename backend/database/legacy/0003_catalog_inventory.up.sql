CREATE TABLE categories (
  id           uuid PRIMARY KEY,
  business_id  uuid NOT NULL REFERENCES businesses(id),
  parent_id    uuid REFERENCES categories(id),
  name         text NOT NULL,
  is_active    boolean NOT NULL DEFAULT true,
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX categories_name_uq ON categories (business_id, COALESCE(parent_id, '00000000-0000-0000-0000-000000000000'::uuid), name);

CREATE TABLE units (
  id           uuid PRIMARY KEY,
  business_id  uuid NOT NULL REFERENCES businesses(id),
  name         text NOT NULL,
  short_name   text NOT NULL,
  allow_decimal boolean NOT NULL DEFAULT false,
  created_at   timestamptz NOT NULL DEFAULT now(),
  UNIQUE (business_id, name)
);

CREATE TABLE products (
  id             uuid PRIMARY KEY,
  business_id    uuid NOT NULL REFERENCES businesses(id),
  category_id    uuid REFERENCES categories(id),
  unit_id        uuid NOT NULL REFERENCES units(id),
  tax_rate_id    uuid REFERENCES tax_rates(id),
  sku            text NOT NULL,
  barcode        text,
  name           text NOT NULL,
  description    text,
  kind           text NOT NULL DEFAULT 'stock' CHECK (kind IN ('stock','service')),
  sell_price     numeric(18,2) NOT NULL CHECK (sell_price >= 0),
  reorder_level  numeric(18,3) NOT NULL DEFAULT 0,
  image_file     text,
  is_active      boolean NOT NULL DEFAULT true,
  created_by     uuid REFERENCES users(id),
  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now(),
  UNIQUE (business_id, sku)
);
CREATE UNIQUE INDEX products_barcode_uq ON products (business_id, barcode) WHERE barcode IS NOT NULL;
CREATE INDEX products_name_trgm ON products USING gin (name gin_trgm_ops);
CREATE INDEX products_category_idx ON products (category_id);

CREATE TABLE product_branch_prices (
  product_id   uuid NOT NULL REFERENCES products(id),
  branch_id    uuid NOT NULL REFERENCES branches(id),
  business_id  uuid NOT NULL REFERENCES businesses(id),
  sell_price   numeric(18,2) NOT NULL CHECK (sell_price >= 0),
  PRIMARY KEY (product_id, branch_id)
);

CREATE TABLE price_history (
  id           uuid PRIMARY KEY,
  business_id  uuid NOT NULL REFERENCES businesses(id),
  product_id   uuid NOT NULL REFERENCES products(id),
  branch_id    uuid REFERENCES branches(id),
  old_price    numeric(18,2) NOT NULL,
  new_price    numeric(18,2) NOT NULL,
  changed_by   uuid NOT NULL REFERENCES users(id),
  changed_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX price_history_product_idx ON price_history (product_id, changed_at DESC);

CREATE TABLE stock_levels (
  business_id  uuid NOT NULL REFERENCES businesses(id),
  branch_id    uuid NOT NULL REFERENCES branches(id),
  product_id   uuid NOT NULL REFERENCES products(id),
  qty_on_hand  numeric(18,3) NOT NULL DEFAULT 0,
  avg_cost     numeric(18,4) NOT NULL DEFAULT 0,
  low_notified boolean NOT NULL DEFAULT false,
  updated_at   timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (branch_id, product_id)
);

CREATE TABLE stock_movements (
  id           uuid PRIMARY KEY,
  business_id  uuid NOT NULL REFERENCES businesses(id),
  branch_id    uuid NOT NULL REFERENCES branches(id),
  product_id   uuid NOT NULL REFERENCES products(id),
  type         text NOT NULL CHECK (type IN (
                 'opening','stock_in','stock_out','purchase','purchase_return',
                 'sale','sale_return','transfer_out','transfer_in','adjustment')),
  qty          numeric(18,3) NOT NULL CHECK (qty <> 0),
  unit_cost    numeric(18,4) NOT NULL,
  qty_after    numeric(18,3) NOT NULL,
  avg_cost_after numeric(18,4) NOT NULL,
  source_type  text NOT NULL,
  source_id    uuid NOT NULL,
  note         text,
  created_by   uuid REFERENCES users(id),
  created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX stock_movements_card_idx ON stock_movements (branch_id, product_id, created_at);
CREATE INDEX stock_movements_source_idx ON stock_movements (source_type, source_id);

CREATE TABLE stock_entries (
  id           uuid PRIMARY KEY,
  business_id  uuid NOT NULL REFERENCES businesses(id),
  branch_id    uuid NOT NULL REFERENCES branches(id),
  number       text NOT NULL,
  direction    text NOT NULL CHECK (direction IN ('in','out')),
  reason       text NOT NULL CHECK (reason IN ('opening','found','gift','damaged','expired','internal_use','theft','other')),
  note         text,
  total_value  numeric(18,2) NOT NULL DEFAULT 0,
  created_by   uuid NOT NULL REFERENCES users(id),
  created_at   timestamptz NOT NULL DEFAULT now(),
  UNIQUE (business_id, number)
);
CREATE INDEX stock_entries_list_idx ON stock_entries (business_id, branch_id, created_at DESC);

CREATE TABLE stock_entry_items (
  id           uuid PRIMARY KEY,
  business_id  uuid NOT NULL REFERENCES businesses(id),
  entry_id     uuid NOT NULL REFERENCES stock_entries(id) ON DELETE CASCADE,
  product_id   uuid NOT NULL REFERENCES products(id),
  qty          numeric(18,3) NOT NULL CHECK (qty > 0),
  unit_cost    numeric(18,4) NOT NULL
);

CREATE TABLE stock_adjustments (
  id            uuid PRIMARY KEY,
  business_id   uuid NOT NULL REFERENCES businesses(id),
  branch_id     uuid NOT NULL REFERENCES branches(id),
  number        text NOT NULL,
  reason        text NOT NULL,
  status        text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','pending_approval','posted','rejected','cancelled')),
  is_stock_take boolean NOT NULL DEFAULT false,
  total_value   numeric(18,2) NOT NULL DEFAULT 0,
  created_by    uuid NOT NULL REFERENCES users(id),
  posted_by     uuid REFERENCES users(id),
  posted_at     timestamptz,
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  UNIQUE (business_id, number)
);

CREATE TABLE stock_adjustment_items (
  id             uuid PRIMARY KEY,
  business_id    uuid NOT NULL REFERENCES businesses(id),
  adjustment_id  uuid NOT NULL REFERENCES stock_adjustments(id) ON DELETE CASCADE,
  product_id     uuid NOT NULL REFERENCES products(id),
  qty_system     numeric(18,3) NOT NULL,
  qty_counted    numeric(18,3) NOT NULL,
  unit_cost      numeric(18,4) NOT NULL,
  UNIQUE (adjustment_id, product_id)
);

CREATE TABLE stock_transfers (
  id              uuid PRIMARY KEY,
  business_id     uuid NOT NULL REFERENCES businesses(id),
  number          text NOT NULL,
  from_branch_id  uuid NOT NULL REFERENCES branches(id),
  to_branch_id    uuid NOT NULL REFERENCES branches(id),
  status          text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','dispatched','received','received_with_difference','cancelled')),
  note            text,
  created_by      uuid NOT NULL REFERENCES users(id),
  dispatched_by   uuid REFERENCES users(id),
  dispatched_at   timestamptz,
  received_by     uuid REFERENCES users(id),
  received_at     timestamptz,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  CHECK (from_branch_id <> to_branch_id),
  UNIQUE (business_id, number)
);
CREATE INDEX stock_transfers_from_idx ON stock_transfers (from_branch_id, created_at DESC);
CREATE INDEX stock_transfers_to_idx ON stock_transfers (to_branch_id, created_at DESC);

CREATE TABLE stock_transfer_items (
  id            uuid PRIMARY KEY,
  business_id   uuid NOT NULL REFERENCES businesses(id),
  transfer_id   uuid NOT NULL REFERENCES stock_transfers(id) ON DELETE CASCADE,
  product_id    uuid NOT NULL REFERENCES products(id),
  qty_sent      numeric(18,3) NOT NULL CHECK (qty_sent > 0),
  qty_received  numeric(18,3),
  unit_cost     numeric(18,4) NOT NULL DEFAULT 0,
  UNIQUE (transfer_id, product_id)
);
