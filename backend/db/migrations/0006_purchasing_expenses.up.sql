CREATE TABLE suppliers (
  id             uuid PRIMARY KEY,
  business_id    uuid NOT NULL REFERENCES businesses(id),
  name           text NOT NULL,
  contact_person text,
  phone          text,
  email          citext,
  address        text,
  tax_number     text,
  payment_terms_days integer NOT NULL DEFAULT 0 CHECK (payment_terms_days >= 0),
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
CREATE INDEX purchase_orders_list_idx ON purchase_orders (business_id, branch_id, order_date DESC);
CREATE INDEX purchase_orders_supplier_idx ON purchase_orders (supplier_id);

CREATE TABLE purchase_order_items (
  id            uuid PRIMARY KEY,
  business_id   uuid NOT NULL REFERENCES businesses(id),
  po_id         uuid NOT NULL REFERENCES purchase_orders(id) ON DELETE CASCADE,
  product_id    uuid NOT NULL REFERENCES products(id),
  qty_ordered   numeric(18,3) NOT NULL CHECK (qty_ordered > 0),
  qty_received  numeric(18,3) NOT NULL DEFAULT 0 CHECK (qty_received >= 0),
  unit_cost     numeric(18,4) NOT NULL,
  tax_rate      numeric(7,4) NOT NULL DEFAULT 0,
  line_total    numeric(18,2) NOT NULL,
  UNIQUE (po_id, product_id)
);

CREATE TABLE purchase_invoices (
  id              uuid PRIMARY KEY,
  business_id     uuid NOT NULL REFERENCES businesses(id),
  branch_id       uuid NOT NULL REFERENCES branches(id),
  number          text NOT NULL,
  supplier_ref    text,
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
  UNIQUE (business_id, number)
);
CREATE UNIQUE INDEX purchase_invoices_supplier_ref_uq ON purchase_invoices (business_id, supplier_id, supplier_ref) WHERE supplier_ref IS NOT NULL;
CREATE INDEX purchase_invoices_unpaid_idx ON purchase_invoices (business_id, supplier_id) WHERE balance > 0 AND status = 'posted';

CREATE TABLE goods_receipts (
  id            uuid PRIMARY KEY,
  business_id   uuid NOT NULL REFERENCES businesses(id),
  branch_id     uuid NOT NULL REFERENCES branches(id),
  number        text NOT NULL,
  po_id         uuid REFERENCES purchase_orders(id),
  supplier_id   uuid NOT NULL REFERENCES suppliers(id),
  received_date date NOT NULL,
  delivery_note text,
  extra_costs   numeric(18,2) NOT NULL DEFAULT 0 CHECK (extra_costs >= 0),
  total_value   numeric(18,2) NOT NULL,
  invoice_id    uuid REFERENCES purchase_invoices(id),
  received_by   uuid NOT NULL REFERENCES users(id),
  created_at    timestamptz NOT NULL DEFAULT now(),
  UNIQUE (business_id, number)
);
CREATE INDEX goods_receipts_po_idx ON goods_receipts (po_id);
CREATE INDEX goods_receipts_supplier_idx ON goods_receipts (supplier_id);

CREATE TABLE goods_receipt_items (
  id            uuid PRIMARY KEY,
  business_id   uuid NOT NULL REFERENCES businesses(id),
  receipt_id    uuid NOT NULL REFERENCES goods_receipts(id) ON DELETE CASCADE,
  po_item_id    uuid REFERENCES purchase_order_items(id),
  product_id    uuid NOT NULL REFERENCES products(id),
  qty_received  numeric(18,3) NOT NULL CHECK (qty_received > 0),
  unit_cost     numeric(18,4) NOT NULL,
  qty_returned  numeric(18,3) NOT NULL DEFAULT 0 CHECK (qty_returned >= 0)
);

CREATE TABLE purchase_returns (
  id            uuid PRIMARY KEY,
  business_id   uuid NOT NULL REFERENCES businesses(id),
  branch_id     uuid NOT NULL REFERENCES branches(id),
  number        text NOT NULL,
  supplier_id   uuid NOT NULL REFERENCES suppliers(id),
  receipt_id    uuid REFERENCES goods_receipts(id),
  invoice_id    uuid REFERENCES purchase_invoices(id),
  reason        text NOT NULL,
  total         numeric(18,2) NOT NULL,
  settlement    text NOT NULL CHECK (settlement IN ('reduce_debt','refund')),
  money_account_id uuid REFERENCES money_accounts(id),
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
  ledger_account_id uuid NOT NULL REFERENCES ledger_accounts(id),
  is_active         boolean NOT NULL DEFAULT true,
  UNIQUE (business_id, name)
);

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
  money_account_id uuid REFERENCES money_accounts(id),
  payment_id       uuid REFERENCES payments(id),
  receipt_file     text,
  recurring_id     uuid REFERENCES recurring_expenses(id),
  supplier_id      uuid REFERENCES suppliers(id),
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
CREATE INDEX expenses_submitter_idx ON expenses (submitted_by, expense_date DESC);
