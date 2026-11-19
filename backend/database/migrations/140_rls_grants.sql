-- =====================================================================
-- 140_rls_grants.sql — Row-Level Security (tenant isolation + restricted
-- HR/payroll/audit data) and least-privilege grants
-- =====================================================================
SET search_path = app, public;

-- ---------- Tenant isolation on every tenant-owned table ----------------
DO $$
DECLARE r record;
BEGIN
  FOR r IN SELECT c.table_name FROM information_schema.columns c
             JOIN information_schema.tables t ON t.table_schema = c.table_schema AND t.table_name = c.table_name
                                              AND t.table_type = 'BASE TABLE'
            WHERE c.table_schema = 'app' AND c.column_name = 'tenant_id'
              AND c.table_name NOT IN ('memberships', 'backup_runs')
  LOOP
    EXECUTE format('ALTER TABLE app.%I ENABLE ROW LEVEL SECURITY', r.table_name);
    EXECUTE format('ALTER TABLE app.%I FORCE ROW LEVEL SECURITY', r.table_name);
    EXECUTE format($p$CREATE POLICY tenant_isolation ON app.%I
                      USING (tenant_id = app.current_tenant_id())
                      WITH CHECK (tenant_id = app.current_tenant_id())$p$, r.table_name);
  END LOOP;
END $$;

-- Tenants: own tenant, plus tenants the signed-in user belongs to (tenant picker).
ALTER TABLE app.tenants ENABLE ROW LEVEL SECURITY;
ALTER TABLE app.tenants FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_self ON app.tenants
  USING (id = app.current_tenant_id()
         OR id IN (SELECT m.tenant_id FROM app.memberships m
                    WHERE m.user_id = app.current_user_id() AND m.status IN ('active', 'invited')))
  WITH CHECK (id = app.current_tenant_id());

-- Memberships: tenant rows, plus the signed-in user's own memberships (pre-tenant selection).
ALTER TABLE app.memberships ENABLE ROW LEVEL SECURITY;
ALTER TABLE app.memberships FORCE ROW LEVEL SECURITY;
CREATE POLICY membership_visibility ON app.memberships
  USING (tenant_id = app.current_tenant_id() OR user_id = app.current_user_id())
  WITH CHECK (tenant_id = app.current_tenant_id());

-- Backup runs: tenant sees only its own exports; platform rows (tenant_id NULL) invisible to tenants.
ALTER TABLE app.backup_runs ENABLE ROW LEVEL SECURITY;
ALTER TABLE app.backup_runs FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON app.backup_runs
  USING (tenant_id = app.current_tenant_id()) WITH CHECK (tenant_id = app.current_tenant_id());

-- ---------- Restricted data (RESTRICTIVE = AND-ed with tenant policy) ---
-- Helper: is this employee row the current user?
CREATE FUNCTION app.is_self_employee(p_employee uuid) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog, public AS $$
  SELECT EXISTS (SELECT 1 FROM app.employees e
                  WHERE e.tenant_id = app.current_tenant_id() AND e.id = p_employee
                    AND e.membership_id = app.current_membership_id())
$$;

CREATE POLICY comp_read ON app.employee_compensation AS RESTRICTIVE FOR SELECT
  USING ((SELECT app.has_permission('hr.compensation.view')) OR app.is_self_employee(employee_id));
CREATE POLICY comp_write ON app.employee_compensation AS RESTRICTIVE FOR INSERT
  WITH CHECK ((SELECT app.has_permission('hr.compensation.manage')));
CREATE POLICY comp_update ON app.employee_compensation AS RESTRICTIVE FOR UPDATE
  USING ((SELECT app.has_permission('hr.compensation.manage')));

CREATE POLICY allow_read ON app.employee_allowances AS RESTRICTIVE FOR SELECT
  USING ((SELECT app.has_permission('hr.compensation.view')) OR app.is_self_employee(employee_id));
CREATE POLICY allow_write ON app.employee_allowances AS RESTRICTIVE FOR INSERT
  WITH CHECK ((SELECT app.has_permission('hr.compensation.manage')));
CREATE POLICY allow_update ON app.employee_allowances AS RESTRICTIVE FOR UPDATE
  USING ((SELECT app.has_permission('hr.compensation.manage')));

CREATE POLICY payroll_run_read ON app.payroll_runs AS RESTRICTIVE FOR SELECT
  USING ((SELECT app.has_permission('hr.payroll.view')));
CREATE POLICY payroll_run_write ON app.payroll_runs AS RESTRICTIVE FOR INSERT
  WITH CHECK ((SELECT app.has_permission('hr.payroll.manage')));
CREATE POLICY payroll_run_update ON app.payroll_runs AS RESTRICTIVE FOR UPDATE
  USING ((SELECT app.has_permission('hr.payroll.manage')) OR (SELECT app.has_permission('hr.payroll.approve')));

DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['payroll_lines', 'employee_advances', 'employee_pay_adjustments', 'commission_entries'] LOOP
    EXECUTE format($p$CREATE POLICY own_or_payroll_read ON app.%I AS RESTRICTIVE FOR SELECT
      USING ((SELECT app.has_permission('hr.payroll.view')) OR app.is_self_employee(employee_id)
             OR current_user = 'bms_owner')$p$, t);   -- bms_owner = internal definer posting functions only
    EXECUTE format($p$CREATE POLICY payroll_write ON app.%I AS RESTRICTIVE FOR INSERT
      WITH CHECK ((SELECT app.has_permission('hr.payroll.manage')) OR (SELECT app.has_permission('hr.compensation.manage'))
                  OR current_user = 'bms_owner')$p$, t);
    EXECUTE format($p$CREATE POLICY payroll_update ON app.%I AS RESTRICTIVE FOR UPDATE
      USING ((SELECT app.has_permission('hr.payroll.manage')) OR (SELECT app.has_permission('hr.payroll.approve'))
             OR (SELECT app.has_permission('hr.compensation.manage')) OR current_user = 'bms_owner')$p$, t);
  END LOOP;
END $$;
-- Commission rows are created only by app.accrue_* (definer, run from sale /
-- project / return triggers) or by payroll managers; staff cannot insert their own.

CREATE POLICY payslip_read ON app.payslips AS RESTRICTIVE FOR SELECT
  USING ((SELECT app.has_permission('hr.payroll.view'))
         OR EXISTS (SELECT 1 FROM app.payroll_lines l WHERE l.tenant_id = payslips.tenant_id
                     AND l.id = payslips.payroll_line_id AND app.is_self_employee(l.employee_id)));
CREATE POLICY statutory_read ON app.payroll_statutory_items AS RESTRICTIVE FOR SELECT
  USING ((SELECT app.has_permission('hr.payroll.view'))
         OR EXISTS (SELECT 1 FROM app.payroll_lines l WHERE l.tenant_id = payroll_statutory_items.tenant_id
                     AND l.id = payroll_statutory_items.payroll_line_id AND app.is_self_employee(l.employee_id)));
CREATE POLICY repayment_read ON app.advance_repayments AS RESTRICTIVE FOR SELECT
  USING ((SELECT app.has_permission('hr.payroll.view'))
         OR EXISTS (SELECT 1 FROM app.employee_advances a WHERE a.tenant_id = advance_repayments.tenant_id
                     AND a.id = advance_repayments.advance_id AND app.is_self_employee(a.employee_id)));

-- Notifications: each user sees only their own inbox.
CREATE POLICY own_inbox ON app.notifications AS RESTRICTIVE FOR SELECT
  USING (recipient_id = app.current_membership_id() OR current_user = 'bms_worker');
CREATE POLICY own_inbox_update ON app.notifications AS RESTRICTIVE FOR UPDATE
  USING (recipient_id = app.current_membership_id());

-- Audit log: tenant-scoped and only with security.audit.view. RLS enabled
-- (not forced) so SECURITY DEFINER writers owned by bms_owner can insert.
ALTER TABLE audit.audit_log ENABLE ROW LEVEL SECURITY;
CREATE POLICY audit_read ON audit.audit_log FOR SELECT
  USING (tenant_id = app.current_tenant_id() AND (SELECT app.has_permission('security.audit.view')));

-- ---------- Grants -------------------------------------------------------
REVOKE ALL ON ALL TABLES IN SCHEMA app, iam, audit FROM PUBLIC;
GRANT USAGE ON SCHEMA app, iam TO bms_app, bms_worker, bms_readonly;
GRANT USAGE ON SCHEMA audit TO bms_app, bms_worker, bms_readonly;

-- Reference data: read-only.
GRANT SELECT ON app.permissions, app.state_transitions, app.retention_policies TO bms_app, bms_worker, bms_readonly;

-- Tenant tables: SELECT + INSERT for the API; UPDATE on every column EXCEPT
-- trigger-maintained ones; DELETE only on draft-editable children.
DO $$
DECLARE r record; cols text;
  protected text[] := ARRAY[
    'financial_accounts.current_balance', 'products.average_cost',
    'sales.amount_paid', 'invoices.amount_paid', 'purchases.amount_paid', 'contractor_bills.amount_paid',
    'sale_lines.returned_quantity', 'sale_lines.unit_cost', 'purchase_lines.quantity_received',
    'employee_advances.amount_repaid', 'design_jobs.revisions_used', 'design_jobs.final_version_id',
    'payroll_lines.paid_at'];
  no_update text[] := ARRAY['stock_movements', 'stock_balances', 'financial_transactions', 'project_stage_history',
    'attendance_corrections', 'product_price_changes', 'payment_allocations', 'payment_made_allocations',
    'design_feedback', 'client_interactions', 'account_transfers', 'state_transitions', 'permissions',
    'retention_policies'];
  no_insert text[] := ARRAY['stock_movements', 'stock_balances', 'project_stage_history', 'product_price_changes',
    'state_transitions', 'permissions', 'retention_policies'];
BEGIN
  FOR r IN SELECT table_name FROM information_schema.tables
            WHERE table_schema = 'app' AND table_type = 'BASE TABLE' LOOP
    EXECUTE format('GRANT SELECT ON app.%I TO bms_app, bms_worker, bms_readonly', r.table_name);
    IF NOT r.table_name = ANY (no_insert) THEN
      EXECUTE format('GRANT INSERT ON app.%I TO bms_app, bms_worker', r.table_name);
    END IF;
    IF NOT r.table_name = ANY (no_update) THEN
      SELECT string_agg(quote_ident(column_name), ', ') INTO cols
        FROM information_schema.columns
       WHERE table_schema = 'app' AND table_name = r.table_name
         AND is_generated = 'NEVER'
         AND column_name NOT IN ('id', 'tenant_id', 'created_at', 'created_by')
         AND NOT (r.table_name || '.' || column_name) = ANY (protected);
      IF cols IS NOT NULL THEN
        EXECUTE format('GRANT UPDATE (%s) ON app.%I TO bms_app, bms_worker', cols, r.table_name);
      END IF;
    END IF;
  END LOOP;
END $$;
-- tenant_settings / tenants: key column is tenant_id / id, still not updatable.

GRANT DELETE ON app.sales, app.sale_lines, app.sale_line_units, app.quotation_lines, app.invoice_lines,
  app.invoice_installments, app.purchase_lines, app.stock_adjustment_lines, app.stock_transfer_lines,
  app.stock_count_lines, app.role_permissions, app.membership_roles, app.membership_permission_overrides,
  app.authority_limits, app.notification_rule_roles, app.service_package_items, app.service_stages,
  app.service_stage_checklist_items, app.service_stage_documents, app.payroll_lines, app.payroll_statutory_items,
  app.advance_repayments, app.idempotency_keys, app.day_closing_lines, app.attachments
  TO bms_app;
GRANT DELETE ON app.idempotency_keys, app.notifications TO bms_worker;

-- Identity schema (no business data; API is the only reader).
GRANT SELECT, INSERT, UPDATE ON iam.users, iam.user_credentials, iam.sessions, iam.mfa_factors,
  iam.mfa_recovery_codes, iam.password_reset_requests, iam.public_tokens, iam.api_keys TO bms_app;
GRANT SELECT, INSERT ON iam.auth_events, iam.password_history TO bms_app;
GRANT SELECT, DELETE ON iam.sessions, iam.password_reset_requests TO bms_worker;
GRANT SELECT ON iam.users TO bms_readonly;

-- Audit: read via RLS, write only through definer functions.
GRANT SELECT ON audit.audit_log TO bms_app, bms_readonly;
GRANT EXECUTE ON FUNCTION audit.log_event(text, text, uuid, text, text, jsonb, text, text) TO bms_app, bms_worker;
GRANT EXECUTE ON FUNCTION audit.seal_all(), audit.seal_tenant(uuid, interval), audit.ensure_partitions(integer),
  audit.verify_tenant(uuid) TO bms_worker;
REVOKE EXECUTE ON FUNCTION audit.seal_all(), audit.seal_tenant(uuid, interval), audit.ensure_partitions(integer),
  audit.verify_tenant(uuid) FROM PUBLIC;
GRANT SELECT ON audit.audit_seals TO bms_worker;
