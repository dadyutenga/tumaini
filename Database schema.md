# Database schema: PostgreSQL

The full table definitions, ready to split into migration files.

## 1. Conventions

- **Keys.** `id uuid PRIMARY KEY`, UUID version 7 generated in Go. Foreign keys always declared, `ON DELETE RESTRICT` unless noted.
- **Tenancy.** Every table has `business_id uuid NOT NULL REFERENCES businesses(id)`. Row-level security is enabled on every table with the policy shown below.
- **Branch.** Operational tables have `branch_id uuid NOT NULL REFERENCES branches(id)`.
- **Money.** `numeric(18,2)`. Quantities `numeric(18,3)`. Unit costs `numeric(18,4)` to keep average cost precise. Percentages `numeric(7,4)`.
- **Status and type columns.** `text` with a `CHECK` constraint, so adding a value is a simple migration.
- **Timestamps.** `created_at timestamptz NOT NULL DEFAULT now()`, `updated_at timestamptz NOT NULL DEFAULT now()` maintained by trigger. `created_by uuid REFERENCES users(id)`.
- **Soft delete.** Master data uses `is_active boolean NOT NULL DEFAULT true`. Documents use status.
- **Indexes.** Every foreign key is indexed. Lists are indexed by `(business_id, branch_id, created_at DESC)`. Uniqueness is always scoped by `business_id`.
- **Search.** `pg_trgm` GIN index on product and customer names.
- **Naming.** Tables plural snake\_case, columns snake\_case, constraints `<table>_<column>_<kind>`.

```sql
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE EXTENSION IF NOT EXISTS citext;

CREATE OR REPLACE FUNCTION set_updated_at() RETURNS trigger AS $$
BEGIN NEW.updated_at = now(); RETURN NEW; END $$ LANGUAGE plpgsql;

-- applied to every tenant table (generate with a loop in the migration)
ALTER TABLE products ENABLE ROW LEVEL SECURITY;
ALTER TABLE products FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON products
  USING (business_id = current_setting('app.business_id', true)::uuid)
  WITH CHECK (business_id = current_setting('app.business_id', true)::uuid);
```

The application connects as a role that is not the table owner and has no `BYPASSRLS`. Migrations run as the owner.

## 2. Identity, organization, settings

```sql
CREATE TABLE businesses (
  id            uuid PRIMARY KEY,
  name          text NOT NULL,
  legal_name    text,
  tax_number    text,
  reg_number    text,
  phone         text,
  email         citext,
  address       text,
  logo_file     text,
  currency      char(3) NOT NULL DEFAULT 'TZS',
  timezone      text NOT NULL DEFAULT 'Africa/Dar_es_Salaam',
  is_active     boolean NOT NULL DEFAULT true,
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE branches (
  id            uuid PRIMARY KEY,
  business_id   uuid NOT NULL REFERENCES businesses(id),
  name          text NOT NULL,
  code          text NOT NULL,              -- short, used in document numbers
  address       text,
  phone         text,
  manager_id    uuid,                        -- FK to employees added later
  is_main       boolean NOT NULL DEFAULT false,
  is_active     boolean NOT NULL DEFAULT true,
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  UNIQUE (business_id, code),
  UNIQUE (business_id, name)
);

CREATE TABLE roles (
  id            uuid PRIMARY KEY,
  business_id   uuid NOT NULL REFERENCES businesses(id),
  name          text NOT NULL,
  slug          text NOT NULL,              -- owner | manager | staff | custom-...
  is_system     boolean NOT NULL DEFAULT false,
  perm_version  integer NOT NULL DEFAULT 1,
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  UNIQUE (business_id, slug)
);

CREATE TABLE permissions (               -- global catalogue, seeded from code
  key           text PRIMARY KEY,
  module        text NOT NULL,
  description   text NOT NULL,
  is_sensitive  boolean NOT NULL DEFAULT false,
  has_limit     boolean NOT NULL DEFAULT false
);

CREATE TABLE role_permissions (
  role_id        uuid NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
  business_id    uuid NOT NULL REFERENCES businesses(id),
  permission_key text NOT NULL REFERENCES permissions(key),
  scope          text NOT NULL CHECK (scope IN ('all','branch','own')),
  approval_limit numeric(18,2),             -- NULL = no limit
  PRIMARY KEY (role_id, permission_key)
);

CREATE TABLE users (
  id              uuid PRIMARY KEY,
  business_id     uuid NOT NULL REFERENCES businesses(id),
  role_id         uuid NOT NULL REFERENCES roles(id),
  employee_id     uuid,                      -- FK to employees added later
  name            text NOT NULL,
  phone           text,
  email           citext,
  password_hash   text NOT NULL,
  pin_hash        text,
  language        text NOT NULL DEFAULT 'en',
  is_active       boolean NOT NULL DEFAULT true,
  locked_until    timestamptz,
  last_login_at   timestamptz,
  created_by      uuid REFERENCES users(id),
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX users_phone_uq ON users (phone) WHERE phone IS NOT NULL;
CREATE UNIQUE INDEX users_email_uq ON users (email) WHERE email IS NOT NULL;

CREATE TABLE user_branches (
  user_id      uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  branch_id    uuid NOT NULL REFERENCES branches(id),
  business_id  uuid NOT NULL REFERENCES businesses(id),
  is_default   boolean NOT NULL DEFAULT false,
  PRIMARY KEY (user_id, branch_id)
);

CREATE TABLE sessions (
  id            uuid PRIMARY KEY,
  business_id   uuid NOT NULL REFERENCES businesses(id),
  user_id       uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  refresh_hash  bytea NOT NULL UNIQUE,
  prev_hash     bytea,                       -- detects reuse of a rotated token
  user_agent    text,
  ip            inet,
  expires_at    timestamptz NOT NULL,
  created_at    timestamptz NOT NULL DEFAULT now(),
  last_used_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE login_history (
  id           uuid PRIMARY KEY,
  business_id  uuid REFERENCES businesses(id),
  user_id      uuid REFERENCES users(id),
  identifier   text NOT NULL,
  success      boolean NOT NULL,
  reason       text,
  ip           inet,
  user_agent   text,
  created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX login_history_user_idx ON login_history (user_id, created_at DESC);

CREATE TABLE settings (
  business_id  uuid NOT NULL REFERENCES businesses(id),
  key          text NOT NULL,               -- tax.prices_include_tax, receipt.footer, payroll.pay_day ...
  value        jsonb NOT NULL,
  updated_by   uuid REFERENCES users(id),
  updated_at   timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (business_id, key)
);

CREATE TABLE tax_rates (
  id           uuid PRIMARY KEY,
  business_id  uuid NOT NULL REFERENCES businesses(id),
  name         text NOT NULL,
  rate         numeric(7,4) NOT NULL CHECK (rate >= 0),
  is_default   boolean NOT NULL DEFAULT false,
  is_active    boolean NOT NULL DEFAULT true,
  UNIQUE (business_id, name)
);

CREATE TABLE document_sequences (
  business_id  uuid NOT NULL REFERENCES businesses(id),
  branch_id    uuid NOT NULL REFERENCES branches(id),
  doc_type     text NOT NULL,
  prefix       text NOT NULL,
  next_number  bigint NOT NULL DEFAULT 1,
  PRIMARY KEY (branch_id, doc_type)
);

CREATE TABLE idempotency_keys (
  business_id  uuid NOT NULL REFERENCES businesses(id),
  user_id      uuid NOT NULL REFERENCES users(id),
  key          text NOT NULL,
  request_hash bytea NOT NULL,
  status_code  integer,
  response     jsonb,
  created_at   timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, key)
);
```

## 3. Products and inventory

```sql
CREATE TABLE categories (
  id           uuid PRIMARY KEY,
  business_id  uuid NOT NULL REFERENCES businesses(id),
  parent_id    uuid REFERENCES categories(id),
  name         text NOT NULL,
  is_active    boolean NOT NULL DEFAULT true,
  created_at   timestamptz NOT NULL DEFAULT now(),
  UNIQUE (business_id, parent_id, name)
);

CREATE TABLE units (
  id           uuid PRIMARY KEY,
  business_id  uuid NOT NULL REFERENCES businesses(id),
  name         text NOT NULL,               -- piece, kg, litre
  short_name   text NOT NULL,
  allow_decimal boolean NOT NULL DEFAULT false,
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

CREATE TABLE stock_levels (                 -- cache of stock_movements
  business_id  uuid NOT NULL REFERENCES businesses(id),
  branch_id    uuid NOT NULL REFERENCES branches(id),
  product_id   uuid NOT NULL REFERENCES products(id),
  qty_on_hand  numeric(18,3) NOT NULL DEFAULT 0,
  avg_cost     numeric(18,4) NOT NULL DEFAULT 0,
  low_notified boolean NOT NULL DEFAULT false,
  updated_at   timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (branch_id, product_id)
);

CREATE TABLE stock_movements (              -- append-only
  id           uuid PRIMARY KEY,
  business_id  uuid NOT NULL REFERENCES businesses(id),
  branch_id    uuid NOT NULL REFERENCES branches(id),
  product_id   uuid NOT NULL REFERENCES products(id),
  type         text NOT NULL CHECK (type IN (
                 'opening','stock_in','stock_out','purchase','purchase_return',
                 'sale','sale_return','transfer_out','transfer_in','adjustment')),
  qty          numeric(18,3) NOT NULL CHECK (qty <> 0),   -- signed
  unit_cost    numeric(18,4) NOT NULL,
  qty_after    numeric(18,3) NOT NULL,
  avg_cost_after numeric(18,4) NOT NULL,
  source_type  text NOT NULL,               -- sale, grn, transfer, adjustment, stock_entry ...
  source_id    uuid NOT NULL,
  note         text,
  created_by   uuid REFERENCES users(id),
  created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX stock_movements_card_idx ON stock_movements (branch_id, product_id, created_at);
CREATE INDEX stock_movements_source_idx ON stock_movements (source_type, source_id);

CREATE TABLE stock_entries (                -- Stock In and Stock Out without a purchase or sale
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
  total_value   numeric(18,2) NOT NULL DEFAULT 0,   -- signed net value
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

CREATE TABLE stock_transfer_items (
  id            uuid PRIMARY KEY,
  business_id   uuid NOT NULL REFERENCES businesses(id),
  transfer_id   uuid NOT NULL REFERENCES stock_transfers(id) ON DELETE CASCADE,
  product_id    uuid NOT NULL REFERENCES products(id),
  qty_sent      numeric(18,3) NOT NULL CHECK (qty_sent > 0),
  qty_received  numeric(18,3),
  unit_cost     numeric(18,4) NOT NULL DEFAULT 0,   -- set at dispatch
  UNIQUE (transfer_id, product_id)
);
```

## 4. Sales and customers

```sql
CREATE TABLE customers (
  id            uuid PRIMARY KEY,
  business_id   uuid NOT NULL REFERENCES businesses(id),
  name          text NOT NULL,
  phone         text,
  email         citext,
  address       text,
  tax_number    text,
  credit_limit  numeric(18,2) NOT NULL DEFAULT 0,
  payment_terms_days integer NOT NULL DEFAULT 0,
  is_walk_in    boolean NOT NULL DEFAULT false,
  notes         text,
  is_active     boolean NOT NULL DEFAULT true,
  created_by    uuid REFERENCES users(id),
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX customers_phone_uq ON customers (business_id, phone) WHERE phone IS NOT NULL;
CREATE INDEX customers_name_trgm ON customers USING gin (name gin_trgm_ops);

CREATE TABLE till_sessions (
  id               uuid PRIMARY KEY,
  business_id      uuid NOT NULL REFERENCES businesses(id),
  branch_id        uuid NOT NULL REFERENCES branches(id),
  money_account_id uuid NOT NULL,           -- FK to money_accounts (cash till)
  cashier_id       uuid NOT NULL REFERENCES users(id),
  status           text NOT NULL DEFAULT 'open' CHECK (status IN ('open','closed')),
  opening_float    numeric(18,2) NOT NULL DEFAULT 0,
  expected_cash    numeric(18,2),
  counted_cash     numeric(18,2),
  difference       numeric(18,2),
  opened_at        timestamptz NOT NULL DEFAULT now(),
  closed_at        timestamptz,
  closed_by        uuid REFERENCES users(id),
  note             text
);
CREATE UNIQUE INDEX till_one_open_per_cashier ON till_sessions (cashier_id) WHERE status = 'open';

CREATE TABLE sales (
  id               uuid PRIMARY KEY,
  business_id      uuid NOT NULL REFERENCES businesses(id),
  branch_id        uuid NOT NULL REFERENCES branches(id),
  number           text NOT NULL,
  kind             text NOT NULL DEFAULT 'sale' CHECK (kind IN ('sale','quotation')),
  status           text NOT NULL CHECK (status IN ('draft','held','pending_approval','completed','partially_refunded','refunded','voided')),
  customer_id      uuid REFERENCES customers(id),
  cashier_id       uuid NOT NULL REFERENCES users(id),
  employee_id      uuid,                    -- seller, for commission
  till_session_id  uuid REFERENCES till_sessions(id),
  business_date    date NOT NULL,
  subtotal         numeric(18,2) NOT NULL,
  discount_total   numeric(18,2) NOT NULL DEFAULT 0,
  tax_total        numeric(18,2) NOT NULL DEFAULT 0,
  total            numeric(18,2) NOT NULL,
  cost_total       numeric(18,2) NOT NULL DEFAULT 0,
  paid_total       numeric(18,2) NOT NULL DEFAULT 0,
  balance          numeric(18,2) NOT NULL DEFAULT 0,
  due_date         date,
  note             text,
  voided_by        uuid REFERENCES users(id),
  voided_at        timestamptz,
  void_reason      text,
  completed_at     timestamptz,
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  UNIQUE (business_id, number),
  CHECK (balance = total - paid_total OR status IN ('draft','held','voided'))
);
CREATE INDEX sales_list_idx ON sales (business_id, branch_id, business_date DESC);
CREATE INDEX sales_customer_idx ON sales (customer_id) WHERE customer_id IS NOT NULL;
CREATE INDEX sales_cashier_idx ON sales (cashier_id, business_date DESC);
CREATE INDEX sales_unpaid_idx ON sales (business_id, customer_id) WHERE balance > 0 AND status = 'completed';

CREATE TABLE sale_items (
  id            uuid PRIMARY KEY,
  business_id   uuid NOT NULL REFERENCES businesses(id),
  sale_id       uuid NOT NULL REFERENCES sales(id) ON DELETE CASCADE,
  product_id    uuid NOT NULL REFERENCES products(id),
  product_name  text NOT NULL,              -- snapshot
  qty           numeric(18,3) NOT NULL CHECK (qty > 0),
  unit_price    numeric(18,2) NOT NULL,
  unit_cost     numeric(18,4) NOT NULL DEFAULT 0,
  discount      numeric(18,2) NOT NULL DEFAULT 0,
  tax_rate      numeric(7,4) NOT NULL DEFAULT 0,
  tax_amount    numeric(18,2) NOT NULL DEFAULT 0,
  line_total    numeric(18,2) NOT NULL,
  qty_returned  numeric(18,3) NOT NULL DEFAULT 0 CHECK (qty_returned <= qty)
);
CREATE INDEX sale_items_sale_idx ON sale_items (sale_id);
CREATE INDEX sale_items_product_idx ON sale_items (product_id);

CREATE TABLE sale_returns (
  id            uuid PRIMARY KEY,
  business_id   uuid NOT NULL REFERENCES businesses(id),
  branch_id     uuid NOT NULL REFERENCES branches(id),
  number        text NOT NULL,
  sale_id       uuid NOT NULL REFERENCES sales(id),
  status        text NOT NULL CHECK (status IN ('pending_approval','completed','rejected')),
  reason        text NOT NULL,
  refund_method text NOT NULL CHECK (refund_method IN ('cash','mobile_money','bank','reduce_debt','customer_credit')),
  refund_total  numeric(18,2) NOT NULL,
  requested_by  uuid NOT NULL REFERENCES users(id),
  approved_by   uuid REFERENCES users(id),
  created_at    timestamptz NOT NULL DEFAULT now(),
  completed_at  timestamptz,
  UNIQUE (business_id, number)
);

CREATE TABLE sale_return_items (
  id            uuid PRIMARY KEY,
  business_id   uuid NOT NULL REFERENCES businesses(id),
  return_id     uuid NOT NULL REFERENCES sale_returns(id) ON DELETE CASCADE,
  sale_item_id  uuid NOT NULL REFERENCES sale_items(id),
  qty           numeric(18,3) NOT NULL CHECK (qty > 0),
  restock       boolean NOT NULL DEFAULT true,
  amount        numeric(18,2) NOT NULL
);
```

## 5. Suppliers, purchasing, expenses

```sql
CREATE TABLE suppliers (
  id             uuid PRIMARY KEY,
  business_id    uuid NOT NULL REFERENCES businesses(id),
  name           text NOT NULL,
  contact_person text,
  phone          text,
  email          citext,
  address        text,
  tax_number     text,
  payment_terms_days integer NOT NULL DEFAULT 0,
  is_active      boolean NOT NULL DEFAULT true,
  created_by     uuid REFERENCES users(id),
  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now(),
  UNIQUE (business_id, name)
);

CREATE TABLE purchase_orders (
  id            uuid PRIMARY KEY,
  business_id   uuid NOT NULL REFERENCES businesses(id),
  branch_id     uuid NOT NULL REFERENCES branches(id),
  number        text NOT NULL,
  supplier_id   uuid NOT NULL REFERENCES suppliers(id),
  status        text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','pending_approval','approved','partially_received','received','closed','cancelled')),
  order_date    date NOT NULL,
  expected_date date,
  subtotal      numeric(18,2) NOT NULL DEFAULT 0,
  tax_total     numeric(18,2) NOT NULL DEFAULT 0,
  total         numeric(18,2) NOT NULL DEFAULT 0,
  note          text,
  created_by    uuid NOT NULL REFERENCES users(id),
  approved_by   uuid REFERENCES users(id),
  approved_at   timestamptz,
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  UNIQUE (business_id, number)
);

CREATE TABLE purchase_order_items (
  id            uuid PRIMARY KEY,
  business_id   uuid NOT NULL REFERENCES businesses(id),
  po_id         uuid NOT NULL REFERENCES purchase_orders(id) ON DELETE CASCADE,
  product_id    uuid NOT NULL REFERENCES products(id),
  qty_ordered   numeric(18,3) NOT NULL CHECK (qty_ordered > 0),
  qty_received  numeric(18,3) NOT NULL DEFAULT 0,
  unit_cost     numeric(18,4) NOT NULL,
  tax_rate      numeric(7,4) NOT NULL DEFAULT 0,
  line_total    numeric(18,2) NOT NULL,
  UNIQUE (po_id, product_id)
);

CREATE TABLE goods_receipts (
  id            uuid PRIMARY KEY,
  business_id   uuid NOT NULL REFERENCES businesses(id),
  branch_id     uuid NOT NULL REFERENCES branches(id),
  number        text NOT NULL,
  po_id         uuid REFERENCES purchase_orders(id),
  supplier_id   uuid NOT NULL REFERENCES suppliers(id),
  received_date date NOT NULL,
  delivery_note text,
  extra_costs   numeric(18,2) NOT NULL DEFAULT 0,
  total_value   numeric(18,2) NOT NULL,
  invoice_id    uuid,                        -- set when invoiced
  received_by   uuid NOT NULL REFERENCES users(id),
  created_at    timestamptz NOT NULL DEFAULT now(),
  UNIQUE (business_id, number)
);

CREATE TABLE goods_receipt_items (
  id            uuid PRIMARY KEY,
  business_id   uuid NOT NULL REFERENCES businesses(id),
  receipt_id    uuid NOT NULL REFERENCES goods_receipts(id) ON DELETE CASCADE,
  po_item_id    uuid REFERENCES purchase_order_items(id),
  product_id    uuid NOT NULL REFERENCES products(id),
  qty_received  numeric(18,3) NOT NULL CHECK (qty_received > 0),
  unit_cost     numeric(18,4) NOT NULL,     -- includes share of extra costs
  qty_returned  numeric(18,3) NOT NULL DEFAULT 0
);

CREATE TABLE purchase_invoices (
  id              uuid PRIMARY KEY,
  business_id     uuid NOT NULL REFERENCES businesses(id),
  branch_id       uuid NOT NULL REFERENCES branches(id),
  number          text NOT NULL,             -- our number
  supplier_ref    text,                      -- supplier's invoice number
  supplier_id     uuid NOT NULL REFERENCES suppliers(id),
  status          text NOT NULL DEFAULT 'posted' CHECK (status IN ('posted','voided')),
  invoice_date    date NOT NULL,
  due_date        date,
  subtotal        numeric(18,2) NOT NULL,
  tax_total       numeric(18,2) NOT NULL DEFAULT 0,
  total           numeric(18,2) NOT NULL,
  paid_total      numeric(18,2) NOT NULL DEFAULT 0,
  balance         numeric(18,2) NOT NULL,
  created_by      uuid NOT NULL REFERENCES users(id),
  created_at      timestamptz NOT NULL DEFAULT now(),
  UNIQUE (business_id, number),
  UNIQUE (business_id, supplier_id, supplier_ref)
);
CREATE INDEX purchase_invoices_unpaid_idx ON purchase_invoices (business_id, supplier_id) WHERE balance > 0;

CREATE TABLE purchase_returns (
  id            uuid PRIMARY KEY,
  business_id   uuid NOT NULL REFERENCES businesses(id),
  branch_id     uuid NOT NULL REFERENCES branches(id),
  number        text NOT NULL,
  supplier_id   uuid NOT NULL REFERENCES suppliers(id),
  receipt_id    uuid REFERENCES goods_receipts(id),
  reason        text NOT NULL,
  total         numeric(18,2) NOT NULL,
  settlement    text NOT NULL CHECK (settlement IN ('reduce_debt','refund')),
  created_by    uuid NOT NULL REFERENCES users(id),
  created_at    timestamptz NOT NULL DEFAULT now(),
  UNIQUE (business_id, number)
);

CREATE TABLE purchase_return_items (
  id               uuid PRIMARY KEY,
  business_id      uuid NOT NULL REFERENCES businesses(id),
  return_id        uuid NOT NULL REFERENCES purchase_returns(id) ON DELETE CASCADE,
  receipt_item_id  uuid REFERENCES goods_receipt_items(id),
  product_id       uuid NOT NULL REFERENCES products(id),
  qty              numeric(18,3) NOT NULL CHECK (qty > 0),
  unit_cost        numeric(18,4) NOT NULL
);

CREATE TABLE expense_categories (
  id                uuid PRIMARY KEY,
  business_id       uuid NOT NULL REFERENCES businesses(id),
  name              text NOT NULL,
  ledger_account_id uuid NOT NULL,          -- FK to ledger_accounts
  is_active         boolean NOT NULL DEFAULT true,
  UNIQUE (business_id, name)
);

CREATE TABLE expenses (
  id               uuid PRIMARY KEY,
  business_id      uuid NOT NULL REFERENCES businesses(id),
  branch_id        uuid NOT NULL REFERENCES branches(id),
  number           text NOT NULL,
  category_id      uuid NOT NULL REFERENCES expense_categories(id),
  expense_date     date NOT NULL,
  amount           numeric(18,2) NOT NULL CHECK (amount > 0),
  description      text NOT NULL,
  payee            text,
  status           text NOT NULL DEFAULT 'submitted' CHECK (status IN ('submitted','approved','rejected','paid','reversed')),
  money_account_id uuid,                    -- set when paid
  payment_id       uuid,
  receipt_file     text,
  recurring_id     uuid,
  submitted_by     uuid NOT NULL REFERENCES users(id),
  approved_by      uuid REFERENCES users(id),
  approved_at      timestamptz,
  paid_at          timestamptz,
  reject_reason    text,
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  UNIQUE (business_id, number)
);
CREATE INDEX expenses_list_idx ON expenses (business_id, branch_id, expense_date DESC);

CREATE TABLE recurring_expenses (
  id            uuid PRIMARY KEY,
  business_id   uuid NOT NULL REFERENCES businesses(id),
  branch_id     uuid NOT NULL REFERENCES branches(id),
  category_id   uuid NOT NULL REFERENCES expense_categories(id),
  amount        numeric(18,2) NOT NULL CHECK (amount > 0),
  description   text NOT NULL,
  payee         text,
  frequency     text NOT NULL CHECK (frequency IN ('weekly','monthly','yearly')),
  start_date    date NOT NULL,
  end_date      date,
  next_run      date NOT NULL,
  is_active     boolean NOT NULL DEFAULT true,
  created_by    uuid NOT NULL REFERENCES users(id),
  created_at    timestamptz NOT NULL DEFAULT now()
);
```

## 6. Finance and ledger

```sql
CREATE TABLE ledger_accounts (
  id           uuid PRIMARY KEY,
  business_id  uuid NOT NULL REFERENCES businesses(id),
  code         text NOT NULL,
  name         text NOT NULL,
  type         text NOT NULL CHECK (type IN ('asset','liability','equity','income','expense')),
  is_system    boolean NOT NULL DEFAULT false,
  is_active    boolean NOT NULL DEFAULT true,
  UNIQUE (business_id, code)
);

CREATE TABLE money_accounts (
  id                uuid PRIMARY KEY,
  business_id       uuid NOT NULL REFERENCES businesses(id),
  branch_id         uuid REFERENCES branches(id),     -- NULL = business-wide (bank)
  ledger_account_id uuid NOT NULL REFERENCES ledger_accounts(id),
  name              text NOT NULL,
  type              text NOT NULL CHECK (type IN ('cash','bank','mobile_money','petty_cash')),
  provider          text,                    -- bank name or mobile money operator
  account_number    text,
  custodian_id      uuid REFERENCES users(id),
  allow_overdraft   boolean NOT NULL DEFAULT false,
  is_active         boolean NOT NULL DEFAULT true,
  created_at        timestamptz NOT NULL DEFAULT now(),
  UNIQUE (business_id, name)
);

CREATE TABLE payments (
  id               uuid PRIMARY KEY,
  business_id      uuid NOT NULL REFERENCES businesses(id),
  branch_id        uuid NOT NULL REFERENCES branches(id),
  number           text NOT NULL,
  direction        text NOT NULL CHECK (direction IN ('in','out')),
  kind             text NOT NULL CHECK (kind IN (
                     'sale','customer_payment','sale_refund','supplier_payment','supplier_refund',
                     'expense','salary','loan_disbursement','loan_repayment','income','drawing','capital','statutory')),
  party_type       text CHECK (party_type IN ('customer','supplier','employee')),
  party_id         uuid,
  money_account_id uuid NOT NULL REFERENCES money_accounts(id),
  method           text NOT NULL CHECK (method IN ('cash','mobile_money','bank')),
  amount           numeric(18,2) NOT NULL CHECK (amount > 0),
  reference        text,
  payment_date     date NOT NULL,
  till_session_id  uuid REFERENCES till_sessions(id),
  status           text NOT NULL DEFAULT 'posted' CHECK (status IN ('posted','voided')),
  note             text,
  voided_by        uuid REFERENCES users(id),
  voided_at        timestamptz,
  created_by       uuid NOT NULL REFERENCES users(id),
  created_at       timestamptz NOT NULL DEFAULT now(),
  UNIQUE (business_id, number)
);
CREATE INDEX payments_list_idx ON payments (business_id, branch_id, payment_date DESC);
CREATE INDEX payments_party_idx ON payments (party_type, party_id);
CREATE INDEX payments_account_idx ON payments (money_account_id, payment_date DESC);

CREATE TABLE payment_allocations (
  id             uuid PRIMARY KEY,
  business_id    uuid NOT NULL REFERENCES businesses(id),
  payment_id     uuid NOT NULL REFERENCES payments(id),
  document_type  text NOT NULL CHECK (document_type IN ('sale','purchase_invoice','payslip','expense','loan')),
  document_id    uuid NOT NULL,
  amount         numeric(18,2) NOT NULL CHECK (amount > 0),
  created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX payment_allocations_doc_idx ON payment_allocations (document_type, document_id);

CREATE TABLE money_transfers (
  id               uuid PRIMARY KEY,
  business_id      uuid NOT NULL REFERENCES businesses(id),
  number           text NOT NULL,
  from_account_id  uuid NOT NULL REFERENCES money_accounts(id),
  to_account_id    uuid NOT NULL REFERENCES money_accounts(id),
  amount           numeric(18,2) NOT NULL CHECK (amount > 0),
  fee              numeric(18,2) NOT NULL DEFAULT 0,
  transfer_date    date NOT NULL,
  reference        text,
  note             text,
  created_by       uuid NOT NULL REFERENCES users(id),
  created_at       timestamptz NOT NULL DEFAULT now(),
  CHECK (from_account_id <> to_account_id),
  UNIQUE (business_id, number)
);

CREATE TABLE income_categories (
  id                uuid PRIMARY KEY,
  business_id       uuid NOT NULL REFERENCES businesses(id),
  name              text NOT NULL,
  ledger_account_id uuid NOT NULL REFERENCES ledger_accounts(id),
  UNIQUE (business_id, name)
);

CREATE TABLE journal_entries (              -- append-only
  id            uuid PRIMARY KEY,
  business_id   uuid NOT NULL REFERENCES businesses(id),
  entry_date    date NOT NULL,
  source_type   text NOT NULL,              -- sale, sale_return, grn, purchase_invoice, payment, expense, payroll_run ...
  source_id     uuid NOT NULL,
  description   text NOT NULL,
  reverses_id   uuid REFERENCES journal_entries(id),
  created_by    uuid REFERENCES users(id),
  created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX journal_entries_source_idx ON journal_entries (source_type, source_id);
CREATE INDEX journal_entries_date_idx ON journal_entries (business_id, entry_date);

CREATE TABLE journal_lines (
  id                uuid PRIMARY KEY,
  business_id       uuid NOT NULL REFERENCES businesses(id),
  entry_id          uuid NOT NULL REFERENCES journal_entries(id),
  branch_id         uuid REFERENCES branches(id),
  ledger_account_id uuid NOT NULL REFERENCES ledger_accounts(id),
  money_account_id  uuid REFERENCES money_accounts(id),
  party_type        text CHECK (party_type IN ('customer','supplier','employee')),
  party_id          uuid,
  debit             numeric(18,2) NOT NULL DEFAULT 0 CHECK (debit >= 0),
  credit            numeric(18,2) NOT NULL DEFAULT 0 CHECK (credit >= 0),
  entry_date        date NOT NULL,           -- copied for fast range sums
  CHECK ((debit = 0) <> (credit = 0))
);
CREATE INDEX journal_lines_account_idx ON journal_lines (business_id, ledger_account_id, entry_date);
CREATE INDEX journal_lines_money_idx ON journal_lines (money_account_id, entry_date) WHERE money_account_id IS NOT NULL;
CREATE INDEX journal_lines_party_idx ON journal_lines (party_type, party_id, entry_date) WHERE party_id IS NOT NULL;

-- balance check: deferred so all lines of an entry are inserted first
CREATE OR REPLACE FUNCTION check_entry_balanced() RETURNS trigger AS $$
BEGIN
  IF (SELECT COALESCE(SUM(debit),0) - COALESCE(SUM(credit),0)
        FROM journal_lines WHERE entry_id = NEW.entry_id) <> 0 THEN
    RAISE EXCEPTION 'journal entry % is not balanced', NEW.entry_id;
  END IF;
  RETURN NULL;
END $$ LANGUAGE plpgsql;

CREATE CONSTRAINT TRIGGER journal_lines_balanced
  AFTER INSERT ON journal_lines DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW EXECUTE FUNCTION check_entry_balanced();

CREATE TABLE period_locks (
  business_id   uuid NOT NULL REFERENCES businesses(id),
  locked_until  date NOT NULL,               -- no posting on or before this date
  locked_by     uuid NOT NULL REFERENCES users(id),
  locked_at     timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (business_id)
);

CREATE TABLE account_reconciliations (
  id               uuid PRIMARY KEY,
  business_id      uuid NOT NULL REFERENCES businesses(id),
  money_account_id uuid NOT NULL REFERENCES money_accounts(id),
  as_of_date       date NOT NULL,
  system_balance   numeric(18,2) NOT NULL,
  actual_balance   numeric(18,2) NOT NULL,
  difference       numeric(18,2) NOT NULL,
  note             text,
  created_by       uuid NOT NULL REFERENCES users(id),
  created_at       timestamptz NOT NULL DEFAULT now()
);
```

**Chart of accounts seeded for every new business**

| Code | Name | Type |
| --- | --- | --- |
| 1000 | Cash on hand (one sub-account per cash money account: 1001, 1002 ...) | asset |
| 1010 | Bank accounts (sub-accounts 1011 ...) | asset |
| 1020 | Mobile money (sub-accounts 1021 ...) | asset |
| 1030 | Petty cash (sub-accounts 1031 ...) | asset |
| 1100 | Accounts receivable | asset |
| 1200 | Inventory | asset |
| 1210 | Goods in transit | asset |
| 1300 | Staff loans and advances | asset |
| 1400 | Supplier advances | asset |
| 1500 | Input tax recoverable | asset |
| 2000 | Accounts payable | liability |
| 2050 | Goods received not invoiced | liability |
| 2100 | Output tax payable | liability |
| 2200 | Salaries payable | liability |
| 2210 | Statutory deductions payable | liability |
| 2300 | Customer credits | liability |
| 3000 | Owner equity | equity |
| 3100 | Opening balance equity | equity |
| 3200 | Owner drawings | equity |
| 4000 | Sales revenue | income |
| 4010 | Sales returns | income (contra) |
| 4100 | Other income | income |
| 4200 | Stock gain | income |
| 5000 | Cost of goods sold | expense |
| 5100 | Stock loss and adjustments | expense |
| 5110 | Transfer loss | expense |
| 6000 to 6090 | One account per expense category: rent, utilities, transport, repairs, marketing, other | expense |
| 6100 | Salaries and wages | expense |
| 6110 | Employer contributions | expense |
| 6200 | Bad debts | expense |
| 6300 | Cash over or short | expense |
| 6400 | Bank and transfer charges | expense |

**Derived balances**

```sql
-- money account balance
SELECT COALESCE(SUM(debit - credit), 0) FROM journal_lines WHERE money_account_id = $1 AND entry_date <= $2;

-- customer balance (what they owe)
SELECT COALESCE(SUM(l.debit - l.credit), 0)
FROM journal_lines l JOIN ledger_accounts a ON a.id = l.ledger_account_id
WHERE a.code IN ('1100','2300') AND l.party_type = 'customer' AND l.party_id = $1;

-- profit and loss for a period
SELECT a.type, a.code, a.name, SUM(l.credit - l.debit) AS amount
FROM journal_lines l JOIN ledger_accounts a ON a.id = l.ledger_account_id
WHERE l.business_id = $1 AND l.entry_date BETWEEN $2 AND $3 AND a.type IN ('income','expense')
GROUP BY a.type, a.code, a.name ORDER BY a.code;
```

## 7. HR, attendance, payroll

```sql
CREATE TABLE departments (
  id           uuid PRIMARY KEY,
  business_id  uuid NOT NULL REFERENCES businesses(id),
  name         text NOT NULL,
  head_id      uuid,
  UNIQUE (business_id, name)
);

CREATE TABLE positions (
  id            uuid PRIMARY KEY,
  business_id   uuid NOT NULL REFERENCES businesses(id),
  name          text NOT NULL,
  default_pay   numeric(18,2),
  UNIQUE (business_id, name)
);

CREATE TABLE employees (
  id               uuid PRIMARY KEY,
  business_id      uuid NOT NULL REFERENCES businesses(id),
  branch_id        uuid NOT NULL REFERENCES branches(id),
  department_id    uuid REFERENCES departments(id),
  position_id      uuid REFERENCES positions(id),
  employee_no      text NOT NULL,
  name             text NOT NULL,
  phone            text,
  email            citext,
  address          text,
  id_reference     text,
  photo_file       text,
  hire_date        date NOT NULL,
  employment_type  text NOT NULL CHECK (employment_type IN ('permanent','contract','casual')),
  pay_type         text NOT NULL CHECK (pay_type IN ('monthly','daily','hourly')),
  base_pay         numeric(18,2) NOT NULL DEFAULT 0,
  pay_method       text CHECK (pay_method IN ('cash','bank','mobile_money')),
  pay_account      text,
  emergency_contact text,
  status           text NOT NULL DEFAULT 'active' CHECK (status IN ('active','suspended','terminated')),
  terminated_on    date,
  termination_reason text,
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  UNIQUE (business_id, employee_no)
);
ALTER TABLE users    ADD CONSTRAINT users_employee_fk    FOREIGN KEY (employee_id) REFERENCES employees(id);
ALTER TABLE branches ADD CONSTRAINT branches_manager_fk  FOREIGN KEY (manager_id)  REFERENCES employees(id);

CREATE TABLE leave_types (
  id            uuid PRIMARY KEY,
  business_id   uuid NOT NULL REFERENCES businesses(id),
  name          text NOT NULL,
  days_per_year numeric(5,1) NOT NULL DEFAULT 0,
  is_paid       boolean NOT NULL DEFAULT true,
  UNIQUE (business_id, name)
);

CREATE TABLE leave_requests (
  id            uuid PRIMARY KEY,
  business_id   uuid NOT NULL REFERENCES businesses(id),
  employee_id   uuid NOT NULL REFERENCES employees(id),
  leave_type_id uuid NOT NULL REFERENCES leave_types(id),
  start_date    date NOT NULL,
  end_date      date NOT NULL,
  days          numeric(5,1) NOT NULL,
  reason        text,
  status        text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected','cancelled')),
  decided_by    uuid REFERENCES users(id),
  decided_at    timestamptz,
  created_at    timestamptz NOT NULL DEFAULT now(),
  CHECK (end_date >= start_date)
);

CREATE TABLE employee_loans (
  id                uuid PRIMARY KEY,
  business_id       uuid NOT NULL REFERENCES businesses(id),
  number            text NOT NULL,
  employee_id       uuid NOT NULL REFERENCES employees(id),
  kind              text NOT NULL CHECK (kind IN ('loan','advance')),
  principal         numeric(18,2) NOT NULL CHECK (principal > 0),
  monthly_deduction numeric(18,2) NOT NULL CHECK (monthly_deduction > 0),
  balance           numeric(18,2) NOT NULL,
  start_period      date NOT NULL,           -- first payroll month to deduct
  status            text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','disbursed','settled','rejected')),
  payment_id        uuid REFERENCES payments(id),
  approved_by       uuid REFERENCES users(id),
  created_by        uuid NOT NULL REFERENCES users(id),
  created_at        timestamptz NOT NULL DEFAULT now(),
  UNIQUE (business_id, number)
);

CREATE TABLE loan_repayments (
  id             uuid PRIMARY KEY,
  business_id    uuid NOT NULL REFERENCES businesses(id),
  loan_id        uuid NOT NULL REFERENCES employee_loans(id),
  amount         numeric(18,2) NOT NULL CHECK (amount > 0),
  source         text NOT NULL CHECK (source IN ('payroll','cash')),
  payslip_id     uuid,
  payment_id     uuid REFERENCES payments(id),
  created_at     timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE commission_rules (
  id            uuid PRIMARY KEY,
  business_id   uuid NOT NULL REFERENCES businesses(id),
  employee_id   uuid REFERENCES employees(id),
  position_id   uuid REFERENCES positions(id),
  calc          text NOT NULL CHECK (calc IN ('percent_of_sales','fixed_per_sale')),
  value         numeric(18,4) NOT NULL,
  min_target    numeric(18,2) NOT NULL DEFAULT 0,
  is_active     boolean NOT NULL DEFAULT true,
  CHECK (employee_id IS NOT NULL OR position_id IS NOT NULL)
);

CREATE TABLE pay_adjustments (              -- one-off bonuses and deductions
  id            uuid PRIMARY KEY,
  business_id   uuid NOT NULL REFERENCES businesses(id),
  employee_id   uuid NOT NULL REFERENCES employees(id),
  period        date NOT NULL,              -- first day of the payroll month
  kind          text NOT NULL CHECK (kind IN ('bonus','deduction')),
  amount        numeric(18,2) NOT NULL CHECK (amount > 0),
  reason        text NOT NULL,
  payslip_id    uuid,
  created_by    uuid NOT NULL REFERENCES users(id),
  created_at    timestamptz NOT NULL DEFAULT now()
);

-- attendance
CREATE TABLE work_schedules (
  id            uuid PRIMARY KEY,
  business_id   uuid NOT NULL REFERENCES businesses(id),
  branch_id     uuid REFERENCES branches(id),
  employee_id   uuid REFERENCES employees(id),   -- NULL = branch default
  weekday       smallint NOT NULL CHECK (weekday BETWEEN 0 AND 6),
  is_working    boolean NOT NULL DEFAULT true,
  start_time    time,
  end_time      time,
  break_minutes integer NOT NULL DEFAULT 0
);

CREATE TABLE holidays (
  id           uuid PRIMARY KEY,
  business_id  uuid NOT NULL REFERENCES businesses(id),
  day          date NOT NULL,
  name         text NOT NULL,
  UNIQUE (business_id, day)
);

CREATE TABLE attendance_events (
  id            uuid PRIMARY KEY,
  business_id   uuid NOT NULL REFERENCES businesses(id),
  branch_id     uuid NOT NULL REFERENCES branches(id),
  employee_id   uuid NOT NULL REFERENCES employees(id),
  type          text NOT NULL CHECK (type IN ('in','out')),
  at            timestamptz NOT NULL,
  source        text NOT NULL CHECK (source IN ('self','manager','manual')),
  note          text,
  ip            inet,
  created_by    uuid NOT NULL REFERENCES users(id),
  created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX attendance_events_emp_idx ON attendance_events (employee_id, at);

CREATE TABLE attendance_days (
  id               uuid PRIMARY KEY,
  business_id      uuid NOT NULL REFERENCES businesses(id),
  branch_id        uuid NOT NULL REFERENCES branches(id),
  employee_id      uuid NOT NULL REFERENCES employees(id),
  day              date NOT NULL,
  status           text NOT NULL CHECK (status IN ('present','late','absent','half_day','leave','holiday','day_off')),
  first_in         timestamptz,
  last_out         timestamptz,
  worked_minutes   integer NOT NULL DEFAULT 0,
  late_minutes     integer NOT NULL DEFAULT 0,
  overtime_minutes integer NOT NULL DEFAULT 0,
  overtime_status  text NOT NULL DEFAULT 'none' CHECK (overtime_status IN ('none','pending','approved','rejected')),
  needs_review     boolean NOT NULL DEFAULT false,
  is_locked        boolean NOT NULL DEFAULT false,
  UNIQUE (employee_id, day)
);

-- payroll
CREATE TABLE salary_components (
  id            uuid PRIMARY KEY,
  business_id   uuid NOT NULL REFERENCES businesses(id),
  name          text NOT NULL,
  kind          text NOT NULL CHECK (kind IN ('allowance','deduction','employer_contribution')),
  calc          text NOT NULL CHECK (calc IN ('fixed','percent_of_base','percent_of_gross','bracket')),
  value         numeric(18,4) NOT NULL DEFAULT 0,
  brackets      jsonb,                       -- [{"from":0,"to":270000,"rate":0,"base":0}, ...]
  is_taxable    boolean NOT NULL DEFAULT true,
  is_statutory  boolean NOT NULL DEFAULT false,
  applies_to_all boolean NOT NULL DEFAULT false,
  sort_order    integer NOT NULL DEFAULT 0,
  is_active     boolean NOT NULL DEFAULT true,
  UNIQUE (business_id, name)
);

CREATE TABLE employee_components (
  employee_id   uuid NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
  component_id  uuid NOT NULL REFERENCES salary_components(id),
  business_id   uuid NOT NULL REFERENCES businesses(id),
  value         numeric(18,4),               -- overrides the component default
  PRIMARY KEY (employee_id, component_id)
);

CREATE TABLE payroll_runs (
  id               uuid PRIMARY KEY,
  business_id      uuid NOT NULL REFERENCES businesses(id),
  branch_id        uuid REFERENCES branches(id),   -- NULL = whole business
  number           text NOT NULL,
  period_start     date NOT NULL,
  period_end       date NOT NULL,
  status           text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','calculated','pending_approval','approved','paid','locked','reversed')),
  gross_total      numeric(18,2) NOT NULL DEFAULT 0,
  deduction_total  numeric(18,2) NOT NULL DEFAULT 0,
  net_total        numeric(18,2) NOT NULL DEFAULT 0,
  employer_total   numeric(18,2) NOT NULL DEFAULT 0,
  created_by       uuid NOT NULL REFERENCES users(id),
  approved_by      uuid REFERENCES users(id),
  approved_at      timestamptz,
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  UNIQUE (business_id, number)
);
CREATE UNIQUE INDEX payroll_runs_period_uq ON payroll_runs (business_id, COALESCE(branch_id, '00000000-0000-0000-0000-000000000000'::uuid), period_start) WHERE status <> 'reversed';

CREATE TABLE payslips (
  id               uuid PRIMARY KEY,
  business_id      uuid NOT NULL REFERENCES businesses(id),
  run_id           uuid NOT NULL REFERENCES payroll_runs(id) ON DELETE CASCADE,
  employee_id      uuid NOT NULL REFERENCES employees(id),
  number           text NOT NULL,
  days_worked      numeric(5,1) NOT NULL DEFAULT 0,
  days_absent      numeric(5,1) NOT NULL DEFAULT 0,
  unpaid_leave_days numeric(5,1) NOT NULL DEFAULT 0,
  overtime_hours   numeric(7,2) NOT NULL DEFAULT 0,
  base_pay         numeric(18,2) NOT NULL,
  gross            numeric(18,2) NOT NULL,
  total_deductions numeric(18,2) NOT NULL,
  net              numeric(18,2) NOT NULL CHECK (net >= 0),
  paid_total       numeric(18,2) NOT NULL DEFAULT 0,
  status           text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','approved','partially_paid','paid')),
  UNIQUE (run_id, employee_id)
);

CREATE TABLE payslip_lines (
  id            uuid PRIMARY KEY,
  business_id   uuid NOT NULL REFERENCES businesses(id),
  payslip_id    uuid NOT NULL REFERENCES payslips(id) ON DELETE CASCADE,
  kind          text NOT NULL CHECK (kind IN ('base','allowance','overtime','commission','bonus','deduction','statutory','loan','employer_contribution','absence')),
  component_id  uuid REFERENCES salary_components(id),
  loan_id       uuid REFERENCES employee_loans(id),
  label         text NOT NULL,
  amount        numeric(18,2) NOT NULL,      -- positive; kind decides the sign
  is_manual     boolean NOT NULL DEFAULT false,
  note          text
);
```

## 8. Approvals, notifications, audit, files

```sql
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
  payload       jsonb,                       -- data needed to complete the action on approval
  status        text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected','cancelled')),
  requested_by  uuid NOT NULL REFERENCES users(id),
  decided_by    uuid REFERENCES users(id),
  decided_at    timestamptz,
  decision_note text,
  created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX approval_pending_idx ON approval_requests (business_id, branch_id) WHERE status = 'pending';
CREATE INDEX approval_subject_idx ON approval_requests (subject_type, subject_id);

CREATE TABLE notifications (
  id            uuid PRIMARY KEY,
  business_id   uuid NOT NULL REFERENCES businesses(id),
  user_id       uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  type          text NOT NULL,              -- low_stock, approval_request, salary_due ...
  title         text NOT NULL,
  body          text NOT NULL,
  link          text,                        -- frontend route
  entity_type   text,
  entity_id     uuid,
  read_at       timestamptz,
  created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX notifications_unread_idx ON notifications (user_id, created_at DESC) WHERE read_at IS NULL;

CREATE TABLE notification_rules (
  business_id   uuid NOT NULL REFERENCES businesses(id),
  type          text NOT NULL,
  in_app        boolean NOT NULL DEFAULT true,
  email         boolean NOT NULL DEFAULT false,
  sms           boolean NOT NULL DEFAULT false,
  days_before   integer,
  PRIMARY KEY (business_id, type)
);

CREATE TABLE audit_logs (                   -- INSERT and SELECT only for the app role
  id            uuid PRIMARY KEY,
  business_id   uuid NOT NULL REFERENCES businesses(id),
  branch_id     uuid,
  user_id       uuid,
  action        text NOT NULL,              -- sale.completed, product.updated, role.permission_changed
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
REVOKE UPDATE, DELETE ON audit_logs FROM bms_app;
REVOKE UPDATE, DELETE ON journal_entries, journal_lines, stock_movements FROM bms_app;

CREATE TABLE files (
  id            uuid PRIMARY KEY,
  business_id   uuid NOT NULL REFERENCES businesses(id),
  path          text NOT NULL,
  original_name text NOT NULL,
  mime_type     text NOT NULL,
  size_bytes    bigint NOT NULL,
  uploaded_by   uuid NOT NULL REFERENCES users(id),
  created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE export_jobs (
  id            uuid PRIMARY KEY,
  business_id   uuid NOT NULL REFERENCES businesses(id),
  user_id       uuid NOT NULL REFERENCES users(id),
  kind          text NOT NULL,              -- report key or 'business_backup'
  params        jsonb NOT NULL,
  status        text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','running','done','failed')),
  file_id       uuid REFERENCES files(id),
  error         text,
  created_at    timestamptz NOT NULL DEFAULT now(),
  finished_at   timestamptz
);
```

**Migration order**

| File | Contents |
| --- | --- |
| 0001\_extensions\_and\_functions | Extensions, `set_updated_at`, application role `bms_app` |
| 0002\_identity\_org | Section 2 tables, permission seed |
| 0003\_catalog\_inventory | Section 3 |
| 0004\_finance\_ledger | Section 6 (before sales, because sales reference money accounts) |
| 0005\_sales\_customers | Section 4 |
| 0006\_purchasing\_expenses | Section 5 |
| 0007\_hr\_attendance\_payroll | Section 7 |
| 0008\_kernel | Section 8 |
| 0009\_rls | Enable row-level security and policies on every tenant table, revoke update and delete on append-only tables |
| 0010\_job\_queue | The job queue's own tables, from its migration tool |

**Two places that run before a tenant is known**

- Login looks up a user by phone or email before any business is set. Do this through one `SECURITY DEFINER` function `auth_find_user(identifier text)` owned by the migration role, which returns only id, business\_id, password hash and lock state.
- Registration creates the business row first. Set `app.business_id` to the new ID at the start of that transaction, then insert everything else.

**Table count:** 77 tables: 13 identity and organization, 13 inventory, 6 sales and customers, 11 purchasing and expenses, 10 finance, 18 HR, attendance and payroll, 6 kernel.
