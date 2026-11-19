-- =====================================================================
-- 120_trigger_wiring.sql — attach triggers; actor FKs; state machines
-- =====================================================================
SET search_path = app, public;

-- ---------- Generic: actor FKs, touch, key freeze ---------------------
-- created_by / updated_by always reference a membership of the SAME tenant.
DO $$
DECLARE r record;
BEGIN
  FOR r IN
    SELECT c.table_name, c.column_name
      FROM information_schema.columns c
      JOIN information_schema.columns t ON t.table_schema = c.table_schema AND t.table_name = c.table_name
                                        AND t.column_name = 'tenant_id'
      JOIN information_schema.tables tb ON tb.table_schema = c.table_schema AND tb.table_name = c.table_name
                                        AND tb.table_type = 'BASE TABLE'
     WHERE c.table_schema = 'app' AND c.column_name IN ('created_by', 'updated_by')
  LOOP
    EXECUTE format('ALTER TABLE app.%I ADD CONSTRAINT %I FOREIGN KEY (tenant_id, %I) REFERENCES app.memberships(tenant_id, id)',
                   r.table_name, left(r.table_name || '_' || r.column_name || '_fk', 63), r.column_name);
  END LOOP;

  FOR r IN SELECT table_name FROM information_schema.columns
            WHERE table_schema = 'app' AND column_name = 'row_version' LOOP
    EXECUTE format('CREATE TRIGGER zz_touch BEFORE UPDATE ON app.%I FOR EACH ROW EXECUTE FUNCTION app.tg_touch_row()', r.table_name);
  END LOOP;

  FOR r IN SELECT c.table_name FROM information_schema.columns c
             JOIN information_schema.tables tb ON tb.table_schema = c.table_schema AND tb.table_name = c.table_name
                                               AND tb.table_type = 'BASE TABLE'
            WHERE c.table_schema = 'app' AND c.column_name = 'tenant_id' LOOP
    EXECUTE format('CREATE TRIGGER aa_freeze_keys BEFORE UPDATE OF tenant_id ON app.%I FOR EACH ROW EXECUTE FUNCTION app.tg_freeze_keys()', r.table_name);
  END LOOP;
END $$;

-- iam.users has no tenant: plain touch trigger.
CREATE TRIGGER zz_touch BEFORE UPDATE ON iam.users FOR EACH ROW EXECUTE FUNCTION app.tg_touch_row();

-- ---------- Append-only tables -----------------------------------------
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['app.stock_movements', 'app.financial_transactions', 'app.project_stage_history',
                           'app.attendance_corrections', 'app.product_price_changes', 'app.payment_allocations',
                           'app.payment_made_allocations', 'app.design_feedback', 'app.client_interactions',
                           'iam.auth_events', 'iam.password_history'] LOOP
    EXECUTE format('CREATE TRIGGER no_mutation BEFORE UPDATE OR DELETE ON %s FOR EACH ROW EXECUTE FUNCTION app.tg_block_mutation()', t);
  END LOOP;
END $$;

-- ---------- State machines ---------------------------------------------
DO $$
DECLARE r record;
BEGIN
  FOR r IN SELECT * FROM (VALUES
    ('sales', 'sale'), ('sale_returns', 'sale_return'), ('purchases', 'purchase'),
    ('stock_adjustments', 'stock_adjustment'), ('stock_transfers', 'stock_transfer'),
    ('stock_counts', 'stock_count'), ('quotations', 'quotation'), ('projects', 'project'),
    ('project_stages', 'project_stage'), ('client_documents', 'client_document'),
    ('invoices', 'invoice'), ('design_jobs', 'design_job'), ('design_versions', 'design_version'),
    ('print_orders', 'print_order'), ('tasks', 'task'), ('renewals', 'renewal'),
    ('expenses', 'expense'), ('payments_received', 'payment'), ('payments_made', 'payment'),
    ('contractor_bills', 'contractor_bill'), ('day_closings', 'day_closing'),
    ('payroll_runs', 'payroll_run'), ('approval_requests', 'approval_request'),
    ('leave_requests', 'leave_request'), ('memberships', 'membership'),
    ('employee_advances', 'employee_advance'), ('commission_entries', 'commission_entry'),
    ('outbound_messages', 'outbound_message')) AS x(tbl, ent)
  LOOP
    EXECUTE format('CREATE TRIGGER ab_state_machine BEFORE INSERT OR UPDATE OF status ON app.%I FOR EACH ROW EXECUTE FUNCTION app.tg_enforce_transition(%L)', r.tbl, r.ent);
  END LOOP;
END $$;

-- ---------- Day lock on insert-only money / stock documents ------------
CREATE TRIGGER ac_day_lock BEFORE INSERT ON app.payments_received FOR EACH ROW EXECUTE FUNCTION app.tg_day_lock_insert('business_date', 'location_id');
CREATE TRIGGER ac_day_lock BEFORE INSERT ON app.payments_made     FOR EACH ROW EXECUTE FUNCTION app.tg_day_lock_insert('business_date', 'location_id');
CREATE TRIGGER ac_day_lock BEFORE INSERT ON app.account_transfers FOR EACH ROW EXECUTE FUNCTION app.tg_day_lock_insert('business_date', 'location_id');
CREATE TRIGGER ac_day_lock BEFORE INSERT ON app.goods_receipts    FOR EACH ROW EXECUTE FUNCTION app.tg_day_lock_insert('business_date', 'location_id');
CREATE TRIGGER ac_day_lock BEFORE INSERT OR UPDATE OF status ON app.stock_adjustments FOR EACH ROW EXECUTE FUNCTION app.tg_day_lock_insert('business_date', 'location_id');
CREATE TRIGGER ac_day_lock_paid BEFORE UPDATE OF status ON app.expenses FOR EACH ROW
  WHEN (NEW.status = 'paid') EXECUTE FUNCTION app.tg_day_lock_insert('paid_business_date', 'location_id');

-- ---------- Payment method / account consistency ------------------------
CREATE TRIGGER ad_method_account BEFORE INSERT ON app.payments_received FOR EACH ROW EXECUTE FUNCTION app.tg_method_matches_account();
CREATE TRIGGER ad_method_account BEFORE INSERT ON app.payments_made     FOR EACH ROW EXECUTE FUNCTION app.tg_method_matches_account();

-- ---------- Ledgers ----------------------------------------------------
CREATE TRIGGER ae_guard BEFORE INSERT ON app.financial_transactions FOR EACH ROW EXECUTE FUNCTION app.tg_fin_txn_guard();
CREATE TRIGGER za_apply AFTER INSERT ON app.financial_transactions FOR EACH ROW EXECUTE FUNCTION app.tg_fin_txn_apply();
CREATE TRIGGER za_apply AFTER INSERT ON app.stock_movements        FOR EACH ROW EXECUTE FUNCTION app.tg_stock_movement_apply();

-- ---------- Sales --------------------------------------------------------
CREATE TRIGGER b_guard BEFORE INSERT OR UPDATE OR DELETE ON app.sales FOR EACH ROW EXECUTE FUNCTION app.tg_sales_guard();
CREATE TRIGGER zb_post AFTER UPDATE OF status ON app.sales FOR EACH ROW EXECUTE FUNCTION app.tg_sales_post();
CREATE CONSTRAINT TRIGGER zc_check_totals AFTER INSERT OR UPDATE ON app.sales
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION app.tg_sales_check_totals();
CREATE TRIGGER b_guard BEFORE INSERT OR UPDATE OR DELETE ON app.sale_lines FOR EACH ROW EXECUTE FUNCTION app.tg_sale_lines_guard();
CREATE TRIGGER b_guard BEFORE INSERT OR DELETE ON app.sale_line_units FOR EACH ROW EXECUTE FUNCTION app.tg_sale_line_units_guard();

-- ---------- Returns ------------------------------------------------------
CREATE TRIGGER b_guard BEFORE INSERT OR UPDATE ON app.sale_returns FOR EACH ROW EXECUTE FUNCTION app.tg_returns_guard();
CREATE TRIGGER zb_post AFTER UPDATE OF status ON app.sale_returns FOR EACH ROW EXECUTE FUNCTION app.tg_returns_post();

-- ---------- Purchasing / inventory --------------------------------------
CREATE TRIGGER zb_post AFTER INSERT ON app.goods_receipt_lines FOR EACH ROW EXECUTE FUNCTION app.tg_goods_receipt_line_post();
CREATE TRIGGER zb_post AFTER UPDATE OF status ON app.stock_adjustments FOR EACH ROW EXECUTE FUNCTION app.tg_adjustment_post();
CREATE TRIGGER zb_post AFTER UPDATE OF status ON app.stock_transfers FOR EACH ROW EXECUTE FUNCTION app.tg_transfer_post();
CREATE TRIGGER b_price_guard BEFORE UPDATE OF selling_price ON app.products FOR EACH ROW EXECUTE FUNCTION app.tg_product_price_guard();

-- ---------- Money in / out ----------------------------------------------
CREATE TRIGGER zb_post AFTER INSERT OR UPDATE ON app.payments_received FOR EACH ROW EXECUTE FUNCTION app.tg_payment_received_post();
CREATE TRIGGER zb_post AFTER INSERT ON app.payment_allocations FOR EACH ROW EXECUTE FUNCTION app.tg_payment_allocation_apply();
CREATE TRIGGER zb_post AFTER INSERT OR UPDATE ON app.payments_made FOR EACH ROW EXECUTE FUNCTION app.tg_payment_made_post();
CREATE TRIGGER zb_post AFTER INSERT ON app.payment_made_allocations FOR EACH ROW EXECUTE FUNCTION app.tg_payment_made_allocation_apply();
CREATE TRIGGER b_guard BEFORE UPDATE ON app.expenses FOR EACH ROW EXECUTE FUNCTION app.tg_expense_guard();
CREATE TRIGGER zb_post AFTER UPDATE OF status ON app.expenses FOR EACH ROW EXECUTE FUNCTION app.tg_expense_post();
CREATE TRIGGER zb_post AFTER INSERT ON app.account_transfers FOR EACH ROW EXECUTE FUNCTION app.tg_account_transfer_post();
CREATE TRIGGER no_mutation BEFORE UPDATE OR DELETE ON app.account_transfers FOR EACH ROW EXECUTE FUNCTION app.tg_block_mutation();

-- ---------- Payroll ------------------------------------------------------
CREATE TRIGGER b_guard BEFORE UPDATE ON app.payroll_runs FOR EACH ROW EXECUTE FUNCTION app.tg_payroll_guard();
CREATE TRIGGER zb_post AFTER UPDATE OF status ON app.payroll_runs FOR EACH ROW EXECUTE FUNCTION app.tg_payroll_post();
CREATE TRIGGER b_guard BEFORE INSERT OR UPDATE OR DELETE ON app.payroll_lines FOR EACH ROW EXECUTE FUNCTION app.tg_payroll_lines_guard();
CREATE TRIGGER b_guard BEFORE UPDATE OR DELETE ON app.employee_compensation FOR EACH ROW EXECUTE FUNCTION app.tg_compensation_guard();
CREATE TRIGGER b_decide BEFORE UPDATE OF status ON app.leave_requests FOR EACH ROW EXECUTE FUNCTION app.tg_leave_decide();

-- ---------- Approvals / day closing --------------------------------------
CREATE TRIGGER b_decide BEFORE UPDATE OF status ON app.approval_requests FOR EACH ROW EXECUTE FUNCTION app.tg_approval_decide();
CREATE TRIGGER b_guard BEFORE UPDATE OF status ON app.day_closings FOR EACH ROW EXECUTE FUNCTION app.tg_day_closing_guard();

-- ---------- Services / design --------------------------------------------
CREATE TRIGGER b_status BEFORE UPDATE OF status ON app.project_stages FOR EACH ROW EXECUTE FUNCTION app.tg_project_stage_status();
CREATE TRIGGER zb_history AFTER UPDATE OF status ON app.project_stages FOR EACH ROW
  WHEN (OLD.status IS DISTINCT FROM NEW.status) EXECUTE FUNCTION app.tg_project_stage_history();
CREATE TRIGGER b_guard BEFORE UPDATE ON app.projects FOR EACH ROW EXECUTE FUNCTION app.tg_project_guard();
CREATE TRIGGER zb_commission AFTER UPDATE OF status ON app.projects FOR EACH ROW EXECUTE FUNCTION app.tg_project_commission();
CREATE TRIGGER b_guard BEFORE INSERT OR UPDATE ON app.design_jobs FOR EACH ROW EXECUTE FUNCTION app.tg_design_job_guard();
CREATE TRIGGER zb_rollup AFTER UPDATE OF status ON app.design_jobs FOR EACH ROW EXECUTE FUNCTION app.tg_branding_stage_rollup();
CREATE TRIGGER b_stamp BEFORE UPDATE OF status ON app.design_versions FOR EACH ROW EXECUTE FUNCTION app.tg_design_version_stamp();
CREATE TRIGGER zb_post AFTER UPDATE OF status ON app.design_versions FOR EACH ROW EXECUTE FUNCTION app.tg_design_version_post();
CREATE TRIGGER b_decide BEFORE UPDATE ON app.design_approval_links FOR EACH ROW
  WHEN (OLD.decision IS NOT NULL OR NEW.decision IS NOT NULL) EXECUTE FUNCTION app.tg_design_link_decide();
CREATE TRIGGER b_guard BEFORE INSERT ON app.service_package_items FOR EACH ROW EXECUTE FUNCTION app.tg_service_package_guard();

-- ---------- IAM invariants ----------------------------------------------
CREATE CONSTRAINT TRIGGER zz_keep_admin AFTER UPDATE OR DELETE ON app.membership_roles
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION app.tg_keep_one_admin();
CREATE CONSTRAINT TRIGGER zz_keep_admin AFTER UPDATE OF status ON app.memberships
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION app.tg_keep_one_admin();

-- ---------- Audit capture ------------------------------------------------
-- Every business table except pure ledgers/queues (which are themselves
-- immutable records with actor + timestamp) and derived projections.
DO $$
DECLARE r record; redact text;
BEGIN
  FOR r IN SELECT t.table_name FROM information_schema.tables t
            WHERE t.table_schema = 'app' AND t.table_type = 'BASE TABLE'
              AND t.table_name NOT IN ('stock_movements', 'stock_balances', 'financial_transactions',
                    'project_stage_history', 'notifications', 'outbound_messages', 'idempotency_keys',
                    'state_transitions', 'permissions', 'retention_policies', 'backup_runs',
                    'design_time_logs', 'renewal_reminders')
  LOOP
    SELECT string_agg(quote_literal(column_name), ',') INTO redact
      FROM information_schema.columns
     WHERE table_schema = 'app' AND table_name = r.table_name
       AND column_name ~ '(ciphertext|_hmac|token_hash|key_hash|code_hash)$';
    EXECUTE format('CREATE TRIGGER zz_audit AFTER INSERT OR UPDATE OR DELETE ON app.%I FOR EACH ROW EXECUTE FUNCTION audit.tg_capture(%s)',
                   r.table_name, coalesce(redact, ''));
  END LOOP;
END $$;
