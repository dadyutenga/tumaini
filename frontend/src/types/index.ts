export type Theme = "light" | "dark" | "system";
export type Locale = "en" | "sw";

/** Decimal values (money, quantity) travel as strings, never floats. */
export type Decimal = string;

export interface Grant {
  scope?: "all" | "branch" | "own";
  limit?: Decimal | null;
}

export interface SessionUser {
  id: string;
  membership_id?: string;
  name: string;
  phone: string | null;
  email: string | null;
  role_id: string | null;
  role_name: string;
  role_slug: string;
  employee_id: string | null;
  language: string;
  is_active: boolean;
  branch_ids: string[];
  must_change_password: boolean;
}

export interface Me {
  user: SessionUser;
  permissions: Record<string, Grant>;
  limits?: Record<string, Decimal>;
  branches: string[];
  is_owner: boolean;
  tenant_id?: string;
  business_id?: string;
  membership_id?: string;
}

export interface Branch {
  id: string;
  name: string;
  code?: string | null;
  kind?: "shop" | "office" | "warehouse" | string;
  address: string | null;
  phone: string | null;
  manager_id?: string | null;
  is_main: boolean;
  is_active: boolean;
}

export interface Product {
  id: string;
  name: string;
  sku: string;
  barcode: string | null;
  category_id: string | null;
  unit_id?: string;
  unit: string;
  tax_rate_id: string | null;
  kind?: "stock" | "service";
  sell_price: Decimal;
  buy_price?: Decimal;
  reorder_level: Decimal;
  is_active: boolean;
  qty_on_hand?: Decimal;
  avg_cost?: Decimal;
  description?: string | null;
  warranty_months?: number;
  track_serial?: boolean;
  photo_file?: string | null;
}

export interface Category {
  id: string;
  parent_id: string | null;
  name: string;
  is_active: boolean;
}

export interface Unit {
  id: string;
  name: string;
  short_name: string;
  allow_decimal: boolean;
}

export interface Customer {
  id: string;
  name: string;
  phone: string | null;
  email: string | null;
  address: string | null;
  tax_number: string | null;
  credit_limit: Decimal;
  payment_terms_days?: number;
  is_walk_in: boolean;
  is_active: boolean;
  balance: Decimal;
  notes: string | null;
}

export interface MoneyAccount {
  id: string;
  name: string;
  type: string;
  branch_id: string | null;
  provider: string | null;
  account_number: string | null;
  allow_overdraft: boolean;
  is_active: boolean;
  balance?: Decimal;
}

export interface SaleRow {
  id: string;
  number: string;
  status: string;
  total: Decimal;
  paid_total: Decimal;
  balance: Decimal;
  business_date: string;
  cashier_id: string;
  customer_name?: string | null;
  cashier_name?: string | null;
}

export interface SaleItem {
  id?: string;
  product_id: string;
  product_name: string;
  qty: Decimal;
  unit_price: Decimal;
  serial_number?: string | null;
  warranty_months?: number;
  discount: Decimal;
  tax_amount: Decimal;
  line_total: Decimal;
  qty_returned?: Decimal;
}

export interface Sale {
  id?: string;
  number?: string;
  status: string;
  customer_id?: string | null;
  customer_name?: string | null;
  customer_phone?: string | null;
  subtotal: Decimal;
  discount_total: Decimal;
  tax_total: Decimal;
  total: Decimal;
  paid_total: Decimal;
  balance: Decimal;
  change_due?: Decimal;
  business_date?: string;
  receipt_footer?: string;
  profit?: Decimal;
  items: SaleItem[];
}

export interface Approval {
  id: string;
  kind: string;
  subject_type: string;
  subject_id: string;
  amount: { Decimal: Decimal; Valid: boolean } | Decimal | null;
  summary: string;
  status: string;
  requested_by: string;
  created_at: string;
}
