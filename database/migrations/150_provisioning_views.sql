-- =====================================================================
-- 150_provisioning_views.sql — tenant provisioning, default roles,
-- reporting views (security_invoker => RLS of the caller applies)
-- =====================================================================
SET search_path = app, public;

-- ---------- Tenant provisioning ----------------------------------------
CREATE FUNCTION app.provision_tenant(
  p_slug text, p_legal_name text, p_owner_user uuid,
  p_shop_name text DEFAULT 'Shop', p_services_name text DEFAULT 'Services',
  p_location_name text DEFAULT 'Main shop')
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE
  t uuid := gen_random_uuid(); bu_a uuid; bu_b uuid; loc uuid;
  r_admin uuid; r_mgr uuid; r_staff uuid; m uuid;
BEGIN
  PERFORM set_config('app.tenant_id', t::text, true);
  INSERT INTO app.tenants (id, slug, legal_name) VALUES (t, p_slug, p_legal_name);
  INSERT INTO app.tenant_settings (tenant_id, business_name) VALUES (t, p_legal_name);

  INSERT INTO app.business_units (tenant_id, code, name, kind) VALUES (t, 'A', p_shop_name, 'retail_shop') RETURNING id INTO bu_a;
  INSERT INTO app.business_units (tenant_id, code, name, kind) VALUES (t, 'B', p_services_name, 'professional_services') RETURNING id INTO bu_b;
  INSERT INTO app.locations (tenant_id, business_unit_id, name, kind, is_default)
  VALUES (t, bu_a, p_location_name, 'shop', true) RETURNING id INTO loc;

  INSERT INTO app.roles (tenant_id, code, name, is_system, rank) VALUES (t, 'admin', 'Admin', true, 100) RETURNING id INTO r_admin;
  INSERT INTO app.roles (tenant_id, code, name, is_system, rank) VALUES (t, 'manager', 'Manager', true, 60) RETURNING id INTO r_mgr;
  INSERT INTO app.roles (tenant_id, code, name, is_system, rank) VALUES (t, 'staff', 'Staff', true, 20) RETURNING id INTO r_staff;

  -- Admin: everything.
  INSERT INTO app.role_permissions (tenant_id, role_id, permission_code) SELECT t, r_admin, code FROM app.permissions;
  -- Manager: spec §2A defaults.
  INSERT INTO app.role_permissions (tenant_id, role_id, permission_code)
  SELECT t, r_mgr, code FROM app.permissions WHERE code IN (
    'dashboard.operations.view', 'shop.sale.create', 'shop.sale.view_all', 'shop.sale.request_void',
    'shop.discount.apply', 'shop.discount.approve', 'shop.return.create', 'shop.return.approve',
    'shop.product.view', 'shop.product.view_cost', 'shop.product.manage', 'shop.stock.view', 'shop.stock.manage',
    'shop.customer.view_all', 'shop.customer.manage', 'shop.supplier.manage', 'shop.purchase.manage',
    'services.client.view_all', 'services.client.manage', 'services.project.view_all', 'services.project.manage',
    'services.project.assign', 'services.catalog.manage', 'services.quotation.manage', 'services.invoice.manage',
    'services.document.manage', 'services.price.approve', 'services.renewal.manage', 'services.contractor.manage',
    'services.design.view_all', 'services.design.manage', 'services.task.view_all', 'services.task.manage',
    'finance.payment.receive', 'finance.payment.view_all', 'finance.payment.make', 'finance.expense.enter',
    'finance.expense.submit', 'finance.expense.approve', 'finance.expense.pay', 'finance.debt.view', 'finance.day.close',
    'hr.employee.view', 'hr.attendance.manage', 'hr.leave.approve', 'reports.operations.view', 'reports.export');
  -- Staff: sell, register clients/jobs, own work.
  INSERT INTO app.role_permissions (tenant_id, role_id, permission_code)
  SELECT t, r_staff, code FROM app.permissions WHERE code IN (
    'shop.sale.create', 'shop.discount.apply', 'shop.return.create', 'shop.product.view', 'shop.customer.manage',
    'services.client.manage', 'services.project.manage', 'services.quotation.draft', 'services.invoice.draft',
    'services.document.manage', 'services.design.manage', 'services.task.manage',
    'finance.payment.receive', 'finance.expense.submit');

  -- Safe provisional limits (A-09): until the Admin sets them, staff discounts
  -- always need approval and Manager approvals of money escalate to the Admin.
  INSERT INTO app.authority_limits (tenant_id, role_id, limit_code, limit_value) VALUES
    (t, r_staff, 'sale_discount_pct', 0),
    (t, r_mgr,   'sale_discount_pct', 0),
    (t, r_mgr,   'discount_approve_pct', 0),
    (t, r_mgr,   'refund_approve_amount', 0),
    (t, r_mgr,   'expense_approve_amount', 0),
    (t, r_mgr,   'price_change_approve_pct', 0);

  INSERT INTO app.memberships (tenant_id, user_id, status, default_location_id, joined_at)
  VALUES (t, p_owner_user, 'active', loc, now()) RETURNING id INTO m;
  INSERT INTO app.membership_roles (tenant_id, membership_id, role_id) VALUES (t, m, r_admin);

  INSERT INTO app.document_sequences (tenant_id, doc_type, prefix) VALUES
    (t, 'sale_receipt', 'R-'), (t, 'payment_receipt', 'RC-'), (t, 'sale_return', 'RT-'), (t, 'invoice', 'INV-'),
    (t, 'quotation', 'Q-'), (t, 'project', 'PRJ-'), (t, 'design_job', 'DJ-'), (t, 'purchase', 'PO-'),
    (t, 'goods_receipt', 'GRN-'), (t, 'stock_adjustment', 'ADJ-'), (t, 'stock_transfer', 'TRF-'),
    (t, 'expense', 'EXP-'), (t, 'payment_made', 'PM-'), (t, 'account_transfer', 'AT-'), (t, 'payslip', 'PS-'),
    (t, 'contractor_bill', 'CB-'), (t, 'employee', 'EMP-');

  INSERT INTO app.notification_rules (tenant_id, notification_type)
  SELECT t, nt FROM unnest(enum_range(NULL::app.notification_type)) nt;

  PERFORM audit.log_event('tenant.provision', 'tenants', t, 'success', NULL,
                          jsonb_build_object('slug', p_slug), 'system');
  RETURN t;
END $$;
REVOKE EXECUTE ON FUNCTION app.provision_tenant(text, text, uuid, text, text, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION app.provision_tenant(text, text, uuid, text, text, text) TO bms_worker;

-- ---------- Reporting / screen views ----------------------------------
-- Staff product catalogue: NO buying price / cost (spec §2A).
CREATE VIEW app.v_staff_catalog WITH (security_invoker = true) AS
SELECT p.tenant_id, p.id AS product_id, p.name, p.barcode, p.sku, p.category_id, c.name AS category_name,
       p.selling_price, p.photo_file_id, p.tracks_serial, p.warranty_months,
       b.location_id, coalesce(b.quantity_on_hand, 0) AS quantity_on_hand,
       coalesce(b.quantity_on_hand, 0) <= coalesce(p.min_stock_level, s.low_stock_default) AS is_low_stock
  FROM app.products p
  JOIN app.tenant_settings s ON s.tenant_id = p.tenant_id
  LEFT JOIN app.product_categories c ON c.tenant_id = p.tenant_id AND c.id = p.category_id
  LEFT JOIN app.stock_balances b ON b.tenant_id = p.tenant_id AND b.product_id = p.id
 WHERE p.status = 'active';

CREATE VIEW app.v_stock_valuation WITH (security_invoker = true) AS
SELECT b.tenant_id, b.location_id, p.id AS product_id, p.name, b.quantity_on_hand, p.average_cost,
       round(b.quantity_on_hand * p.average_cost, 2) AS stock_value,
       coalesce(p.min_stock_level, s.low_stock_default) AS min_level,
       b.quantity_on_hand <= coalesce(p.min_stock_level, s.low_stock_default) AS is_low_stock
  FROM app.stock_balances b
  JOIN app.products p ON p.tenant_id = b.tenant_id AND p.id = b.product_id
  JOIN app.tenant_settings s ON s.tenant_id = b.tenant_id;

-- Debts: owed to us (credit sales + invoices), aged 0-30 / 31-60 / >60.
CREATE VIEW app.v_receivables WITH (security_invoker = true) AS
SELECT s.tenant_id, 'sale'::text AS doc_kind, s.id AS doc_id, s.receipt_no AS doc_no, s.customer_id,
       s.business_unit_id, s.business_date AS doc_date, s.due_date, s.total, s.amount_paid, s.balance_due,
       current_date - coalesce(s.due_date, s.business_date) AS days_overdue,
       CASE WHEN current_date - s.business_date <= 30 THEN '0-30'
            WHEN current_date - s.business_date <= 60 THEN '31-60' ELSE '60+' END AS age_bucket
  FROM app.sales s WHERE s.status = 'completed' AND s.balance_due > 0
UNION ALL
SELECT i.tenant_id, 'invoice', i.id, i.invoice_no, i.customer_id, i.business_unit_id, i.issue_date, i.due_date,
       i.total, i.amount_paid, i.balance_due, current_date - i.due_date,
       CASE WHEN current_date - i.issue_date <= 30 THEN '0-30'
            WHEN current_date - i.issue_date <= 60 THEN '31-60' ELSE '60+' END
  FROM app.invoices i WHERE i.status IN ('issued', 'partially_paid') AND i.balance_due > 0;

CREATE VIEW app.v_payables WITH (security_invoker = true) AS
SELECT p.tenant_id, 'purchase'::text AS doc_kind, p.id AS doc_id, p.purchase_no AS doc_no,
       p.supplier_id, NULL::uuid AS contractor_id, p.order_date AS doc_date, p.due_date, p.total, p.amount_paid, p.balance_due,
       CASE WHEN current_date - p.order_date <= 30 THEN '0-30'
            WHEN current_date - p.order_date <= 60 THEN '31-60' ELSE '60+' END AS age_bucket
  FROM app.purchases p WHERE p.status <> 'cancelled' AND p.balance_due > 0
UNION ALL
SELECT b.tenant_id, 'contractor_bill', b.id, b.bill_no, NULL, b.contractor_id, b.bill_date, b.due_date,
       b.amount, b.amount_paid, b.balance_due,
       CASE WHEN current_date - b.bill_date <= 30 THEN '0-30'
            WHEN current_date - b.bill_date <= 60 THEN '31-60' ELSE '60+' END
  FROM app.contractor_bills b WHERE b.status IN ('open', 'partially_paid');

-- Project profit = payments received - costs (spec §7 Costs tab, §10 Project Profit).
CREATE VIEW app.v_project_financials WITH (security_invoker = true) AS
SELECT pr.tenant_id, pr.id AS project_id, pr.project_no, pr.title, pr.customer_id, pr.status, pr.total_price,
       coalesce(inv.invoiced, 0) AS invoiced,
       coalesce(inv.received, 0) AS received,
       coalesce(ex.costs, 0) + coalesce(cb.costs, 0) AS costs,
       coalesce(inv.received, 0) - coalesce(ex.costs, 0) - coalesce(cb.costs, 0) AS profit,
       pr.total_price - coalesce(inv.received, 0) AS balance_due
  FROM app.projects pr
  LEFT JOIN LATERAL (SELECT sum(i.total) invoiced, sum(i.amount_paid) received FROM app.invoices i
                      WHERE i.tenant_id = pr.tenant_id AND i.project_id = pr.id AND i.status <> 'void') inv ON true
  LEFT JOIN LATERAL (SELECT sum(e.amount) costs FROM app.expenses e
                      WHERE e.tenant_id = pr.tenant_id AND e.project_id = pr.id AND e.status IN ('approved', 'paid')) ex ON true
  LEFT JOIN LATERAL (SELECT sum(b.amount) costs FROM app.contractor_bills b
                      WHERE b.tenant_id = pr.tenant_id AND b.project_id = pr.id AND b.status <> 'void') cb ON true;

CREATE VIEW app.v_design_job_financials WITH (security_invoker = true) AS
SELECT j.tenant_id, j.id AS design_job_id, j.job_no, j.title, j.designer_id, j.status, j.price,
       coalesce(inv.received, 0) AS received,
       coalesce(ex.costs, 0) + coalesce(cb.costs, 0) AS costs,
       coalesce(inv.received, 0) - coalesce(ex.costs, 0) - coalesce(cb.costs, 0) AS profit,
       j.revisions_used, j.revisions_included, j.revisions_used > j.revisions_included AS over_revisions
  FROM app.design_jobs j
  LEFT JOIN LATERAL (SELECT sum(i.amount_paid) received FROM app.invoices i
                      WHERE i.tenant_id = j.tenant_id AND i.design_job_id = j.id AND i.status <> 'void') inv ON true
  LEFT JOIN LATERAL (SELECT sum(e.amount) costs FROM app.expenses e
                      WHERE e.tenant_id = j.tenant_id AND e.design_job_id = j.id AND e.status IN ('approved', 'paid')) ex ON true
  LEFT JOIN LATERAL (SELECT sum(b.amount) costs FROM app.contractor_bills b
                      WHERE b.tenant_id = j.tenant_id AND b.design_job_id = j.id AND b.status <> 'void') cb ON true;

-- Dashboard "Approvals" widget: one list across all request sources.
CREATE VIEW app.v_pending_approvals WITH (security_invoker = true) AS
SELECT tenant_id, 'approval_request'::text AS source, id, approval_type::text AS kind, requested_by,
       requested_at, requested_amount AS amount, requested_percent AS percent, reason
  FROM app.approval_requests WHERE status = 'pending'
UNION ALL
SELECT tenant_id, 'expense', id, 'expense', requested_by, created_at, amount, NULL, description
  FROM app.expenses WHERE status = 'submitted'
UNION ALL
SELECT tenant_id, 'sale_return', id, 'refund', requested_by, created_at, refund_amount, NULL, reason
  FROM app.sale_returns WHERE status = 'requested'
UNION ALL
SELECT tenant_id, 'leave_request', id, 'leave', created_by, created_at, NULL, NULL, reason
  FROM app.leave_requests WHERE status = 'requested';

-- End-of-day: expected balance of each account at a location as of the end of a business date.
CREATE FUNCTION app.expected_account_balances(p_location uuid, p_date date)
RETURNS TABLE (account_id uuid, account_name text, kind app.account_kind, expected numeric)
LANGUAGE sql STABLE AS $$
  SELECT a.id, a.name::text, a.kind,
         coalesce(sum(CASE t.direction WHEN 'in' THEN t.amount ELSE -t.amount END), 0)
    FROM app.financial_accounts a
    LEFT JOIN app.financial_transactions t ON t.tenant_id = a.tenant_id AND t.account_id = a.id AND t.business_date <= p_date
   WHERE a.tenant_id = app.current_tenant_id() AND a.status = 'active'
     AND (a.location_id = p_location OR a.location_id IS NULL)
   GROUP BY a.id, a.name, a.kind
$$;

-- Daily sales summary by method (End-of-Day screen, dashboard cards).
CREATE VIEW app.v_daily_takings WITH (security_invoker = true) AS
SELECT pr.tenant_id, pr.location_id, pr.business_date, pr.business_unit_id, pr.method,
       count(*) AS payments, sum(pr.amount) AS amount
  FROM app.payments_received pr WHERE pr.status = 'posted'
 GROUP BY pr.tenant_id, pr.location_id, pr.business_date, pr.business_unit_id, pr.method;

-- Management P&L by month and business unit (accrual basis for sales/invoices; A-12).
CREATE VIEW app.v_profit_and_loss_monthly WITH (security_invoker = true) AS
WITH lines AS (
  SELECT s.tenant_id, s.business_unit_id, date_trunc('month', s.business_date)::date AS month,
         'revenue'::text AS kind, s.total AS amount
    FROM app.sales s WHERE s.status = 'completed'
  UNION ALL
  SELECT s.tenant_id, s.business_unit_id, date_trunc('month', s.business_date)::date, 'cogs',
         round(sum(sl.quantity * coalesce(sl.unit_cost, 0)), 2)
    FROM app.sales s JOIN app.sale_lines sl ON sl.tenant_id = s.tenant_id AND sl.sale_id = s.id
   WHERE s.status = 'completed' GROUP BY s.tenant_id, s.business_unit_id, s.business_date
  UNION ALL
  SELECT r.tenant_id, s.business_unit_id, date_trunc('month', r.business_date)::date, 'returns',
         -round(sum(rl.quantity * sl.line_total / sl.quantity), 2)
    FROM app.sale_returns r JOIN app.sales s ON s.tenant_id = r.tenant_id AND s.id = r.sale_id
    JOIN app.sale_return_lines rl ON rl.tenant_id = r.tenant_id AND rl.sale_return_id = r.id
    JOIN app.sale_lines sl ON sl.tenant_id = rl.tenant_id AND sl.id = rl.sale_line_id
   WHERE r.status = 'completed' GROUP BY r.tenant_id, s.business_unit_id, r.business_date
  UNION ALL
  SELECT r.tenant_id, s.business_unit_id, date_trunc('month', r.business_date)::date, 'cogs',
         -round(sum(rl.quantity * coalesce(sl.unit_cost, 0)), 2)
    FROM app.sale_returns r JOIN app.sales s ON s.tenant_id = r.tenant_id AND s.id = r.sale_id
    JOIN app.sale_return_lines rl ON rl.tenant_id = r.tenant_id AND rl.sale_return_id = r.id AND rl.restock
    JOIN app.sale_lines sl ON sl.tenant_id = rl.tenant_id AND sl.id = rl.sale_line_id
   WHERE r.status = 'completed' GROUP BY r.tenant_id, s.business_unit_id, r.business_date
  UNION ALL
  SELECT i.tenant_id, i.business_unit_id, date_trunc('month', i.issue_date)::date, 'revenue', i.total
    FROM app.invoices i WHERE i.status IN ('issued', 'partially_paid', 'paid')
  UNION ALL
  SELECT e.tenant_id, e.business_unit_id, date_trunc('month', e.expense_date)::date, 'expenses', e.amount
    FROM app.expenses e WHERE e.status IN ('approved', 'paid')
  UNION ALL
  SELECT b.tenant_id, b.business_unit_id, date_trunc('month', b.bill_date)::date, 'expenses', b.amount
    FROM app.contractor_bills b WHERE b.status <> 'void'
  UNION ALL
  SELECT r.tenant_id, r.business_unit_id, make_date(r.period_year, r.period_month, 1), 'payroll', l.gross_pay
    FROM app.payroll_runs r JOIN app.payroll_lines l ON l.tenant_id = r.tenant_id AND l.payroll_run_id = r.id
   WHERE r.status IN ('approved', 'paid')
)
SELECT tenant_id, business_unit_id, month,
       sum(amount) FILTER (WHERE kind IN ('revenue', 'returns')) AS net_revenue,
       sum(amount) FILTER (WHERE kind = 'cogs')     AS cost_of_goods,
       sum(amount) FILTER (WHERE kind = 'expenses') AS operating_expenses,
       sum(amount) FILTER (WHERE kind = 'payroll')  AS payroll,
       coalesce(sum(amount) FILTER (WHERE kind IN ('revenue', 'returns')), 0)
         - coalesce(sum(amount) FILTER (WHERE kind IN ('cogs', 'expenses', 'payroll')), 0) AS net_profit
  FROM lines GROUP BY tenant_id, business_unit_id, month;

GRANT SELECT ON app.v_staff_catalog, app.v_stock_valuation, app.v_receivables, app.v_payables,
  app.v_project_financials, app.v_design_job_financials, app.v_pending_approvals, app.v_daily_takings,
  app.v_profit_and_loss_monthly TO bms_app, bms_readonly;
GRANT EXECUTE ON FUNCTION app.expected_account_balances(uuid, date) TO bms_app;
