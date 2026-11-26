-- =====================================================================
-- 260_staff_shared_visibility.sql — ensure Admin always holds all
-- permissions, and Staff can see shared Shop customers & Service clients
-- created by Admin/Manager (so data entered by Admin shows up for Staff).
-- =====================================================================
SET search_path = app, public;

-- 1. Backfill any missing permissions on existing system Admin roles.
INSERT INTO app.role_permissions (tenant_id, role_id, permission_code)
SELECT r.tenant_id, r.id, p.code
FROM app.roles r
CROSS JOIN app.permissions p
WHERE r.code = 'admin' AND r.is_system = true
ON CONFLICT DO NOTHING;

-- 2. Backfill shared customer & client visibility on existing system Staff roles.
INSERT INTO app.role_permissions (tenant_id, role_id, permission_code)
SELECT r.tenant_id, r.id, p.code
FROM app.roles r
JOIN app.permissions p ON p.code IN ('shop.customer.view_all', 'services.client.view_all')
WHERE r.code = 'staff' AND r.is_system = true
ON CONFLICT DO NOTHING;

-- 3. Update app.provision_tenant so new tenants also grant Staff shared customer & client visibility.
CREATE OR REPLACE FUNCTION app.provision_tenant(
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
  -- Staff: sell, view shared customers/clients, register jobs, own work.
  INSERT INTO app.role_permissions (tenant_id, role_id, permission_code)
  SELECT t, r_staff, code FROM app.permissions WHERE code IN (
    'shop.sale.create', 'shop.discount.apply', 'shop.return.create', 'shop.product.view',
    'shop.customer.view_all', 'shop.customer.manage',
    'services.client.view_all', 'services.client.manage', 'services.project.manage',
    'services.quotation.draft', 'services.invoice.draft',
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
