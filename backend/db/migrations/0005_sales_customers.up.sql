CREATE TABLE customers (
  id            uuid PRIMARY KEY,
  business_id   uuid NOT NULL REFERENCES businesses(id),
  name          text NOT NULL,
  phone         text,
  email         citext,
  address       text,
  tax_number    text,
  credit_limit  numeric(18,2) NOT NULL DEFAULT 0 CHECK (credit_limit >= 0),
  payment_terms_days integer NOT NULL DEFAULT 0 CHECK (payment_terms_days >= 0),
  is_walk_in    boolean NOT NULL DEFAULT false,
  notes         text,
  is_active     boolean NOT NULL DEFAULT true,
  created_by    uuid REFERENCES users(id),
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX customers_phone_uq ON customers (business_id, phone) WHERE phone IS NOT NULL;
CREATE UNIQUE INDEX customers_one_walk_in ON customers (business_id) WHERE is_walk_in;
CREATE INDEX customers_name_trgm ON customers USING gin (name gin_trgm_ops);

CREATE TABLE till_sessions (
  id               uuid PRIMARY KEY,
  business_id      uuid NOT NULL REFERENCES businesses(id),
  branch_id        uuid NOT NULL REFERENCES branches(id),
  money_account_id uuid NOT NULL REFERENCES money_accounts(id),
  cashier_id       uuid NOT NULL REFERENCES users(id),
  status           text NOT NULL DEFAULT 'open' CHECK (status IN ('open','closed')),
  opening_float    numeric(18,2) NOT NULL DEFAULT 0 CHECK (opening_float >= 0),
  expected_cash    numeric(18,2),
  counted_cash     numeric(18,2),
  difference       numeric(18,2),
  opened_at        timestamptz NOT NULL DEFAULT now(),
  closed_at        timestamptz,
  closed_by        uuid REFERENCES users(id),
  note             text
);
CREATE UNIQUE INDEX till_one_open_per_cashier ON till_sessions (cashier_id) WHERE status = 'open';
CREATE INDEX till_sessions_branch_idx ON till_sessions (branch_id, opened_at DESC);

ALTER TABLE payments
  ADD CONSTRAINT payments_till_fk FOREIGN KEY (till_session_id) REFERENCES till_sessions(id);

CREATE TABLE sales (
  id               uuid PRIMARY KEY,
  business_id      uuid NOT NULL REFERENCES businesses(id),
  branch_id        uuid NOT NULL REFERENCES branches(id),
  number           text NOT NULL,
  kind             text NOT NULL DEFAULT 'sale' CHECK (kind IN ('sale','quotation')),
  status           text NOT NULL CHECK (status IN ('draft','held','pending_approval','completed','partially_refunded','refunded','voided','cancelled')),
  customer_id      uuid REFERENCES customers(id),
  cashier_id       uuid NOT NULL REFERENCES users(id),
  employee_id      uuid,
  till_session_id  uuid REFERENCES till_sessions(id),
  business_date    date NOT NULL,
  subtotal         numeric(18,2) NOT NULL,
  discount_total   numeric(18,2) NOT NULL DEFAULT 0,
  tax_total        numeric(18,2) NOT NULL DEFAULT 0,
  total            numeric(18,2) NOT NULL,
  cost_total       numeric(18,2) NOT NULL DEFAULT 0,
  paid_total       numeric(18,2) NOT NULL DEFAULT 0,
  balance          numeric(18,2) NOT NULL DEFAULT 0,
  change_due       numeric(18,2) NOT NULL DEFAULT 0,
  due_date         date,
  note             text,
  voided_by        uuid REFERENCES users(id),
  voided_at        timestamptz,
  void_reason      text,
  completed_at     timestamptz,
  valid_until      date,
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  UNIQUE (business_id, number),
  CHECK (balance = total - paid_total OR status IN ('draft','held','voided','pending_approval','cancelled'))
);
CREATE INDEX sales_list_idx ON sales (business_id, branch_id, business_date DESC);
CREATE INDEX sales_customer_idx ON sales (customer_id) WHERE customer_id IS NOT NULL;
CREATE INDEX sales_cashier_idx ON sales (cashier_id, business_date DESC);
CREATE INDEX sales_unpaid_idx ON sales (business_id, customer_id) WHERE balance > 0 AND status IN ('completed','partially_refunded');

CREATE TABLE sale_items (
  id            uuid PRIMARY KEY,
  business_id   uuid NOT NULL REFERENCES businesses(id),
  sale_id       uuid NOT NULL REFERENCES sales(id) ON DELETE CASCADE,
  product_id    uuid NOT NULL REFERENCES products(id),
  product_name  text NOT NULL,
  qty           numeric(18,3) NOT NULL CHECK (qty > 0),
  unit_price    numeric(18,2) NOT NULL,
  unit_cost     numeric(18,4) NOT NULL DEFAULT 0,
  discount      numeric(18,2) NOT NULL DEFAULT 0,
  tax_rate      numeric(7,4) NOT NULL DEFAULT 0,
  tax_amount    numeric(18,2) NOT NULL DEFAULT 0,
  line_total    numeric(18,2) NOT NULL,
  qty_returned  numeric(18,3) NOT NULL DEFAULT 0 CHECK (qty_returned >= 0 AND qty_returned <= qty)
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
  money_account_id uuid REFERENCES money_accounts(id),
  refund_total  numeric(18,2) NOT NULL,
  requested_by  uuid NOT NULL REFERENCES users(id),
  approved_by   uuid REFERENCES users(id),
  created_at    timestamptz NOT NULL DEFAULT now(),
  completed_at  timestamptz,
  UNIQUE (business_id, number)
);
CREATE INDEX sale_returns_sale_idx ON sale_returns (sale_id);

CREATE TABLE sale_return_items (
  id            uuid PRIMARY KEY,
  business_id   uuid NOT NULL REFERENCES businesses(id),
  return_id     uuid NOT NULL REFERENCES sale_returns(id) ON DELETE CASCADE,
  sale_item_id  uuid NOT NULL REFERENCES sale_items(id),
  qty           numeric(18,3) NOT NULL CHECK (qty > 0),
  restock       boolean NOT NULL DEFAULT true,
  amount        numeric(18,2) NOT NULL,
  unit_cost     numeric(18,4) NOT NULL DEFAULT 0
);
