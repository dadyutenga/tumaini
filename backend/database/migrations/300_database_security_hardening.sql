-- =====================================================================
-- 300_database_security_hardening.sql — what the API role may still change
-- after a record is final. Answers "if the application server is
-- compromised, can the attacker rewrite financial history?"
--   1. Internal posting functions are no longer callable by the API role.
--   2. Direct ledger inserts are limited to a first opening balance and
--      approved corrections.
--   3. Final financial records keep their amounts, parties and attribution.
--   4. Loans, loan repayments, closed days, bills and returns cannot be
--      quietly rewritten.
--   5. A customer who still owes money cannot be archived by any path.
--
-- bms_owner is exempt everywhere below: it is the identity of the
-- SECURITY DEFINER posting functions and of referential cascades, and the
-- running application cannot log in as it or SET ROLE to it.
-- =====================================================================
SET search_path = app, public;

-- 1. SECURITY DEFINER helpers that post money. PostgreSQL grants EXECUTE to
-- PUBLIC by default, so the API role could call reverse_ledger() directly
-- and cancel ledger entries without voiding the payment behind them. Only the
-- definer triggers (running as bms_owner) call these.
REVOKE EXECUTE ON FUNCTION app.reverse_ledger(uuid, text, uuid, date, text) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION app.accrue_sale_commissions(uuid, uuid) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION app.reverse_return_commissions(uuid, uuid) FROM PUBLIC;

-- 2. Direct ledger inserts.
CREATE OR REPLACE FUNCTION app.tg_fin_txn_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF current_user <> 'bms_owner' AND NEW.txn_type NOT IN ('opening_balance', 'correction') THEN
    RAISE EXCEPTION 'transaction type % is posted automatically from its source document', NEW.txn_type
      USING ERRCODE = 'BM004';
  END IF;
  IF NEW.txn_type = 'correction' AND NOT app.approved_request_exists('transaction_correction', NEW.reverses_transaction_id) THEN
    RAISE EXCEPTION 'correction requires an approved transaction_correction request' USING ERRCODE = 'BM007';
  END IF;
  IF NEW.txn_type = 'opening_balance' THEN
    IF NOT app.has_permission('finance.account.manage') THEN
      RAISE EXCEPTION 'permission finance.account.manage required' USING ERRCODE = 'BM007';
    END IF;
    IF current_user <> 'bms_owner' THEN
      -- Serialise on the account so two concurrent "first" entries cannot both pass.
      PERFORM 1 FROM app.financial_accounts a
        WHERE a.tenant_id = NEW.tenant_id AND a.id = NEW.account_id FOR UPDATE;
      IF NEW.direction <> 'in' OR EXISTS (SELECT 1 FROM app.financial_transactions t
            WHERE t.tenant_id = NEW.tenant_id AND t.account_id = NEW.account_id) THEN
        RAISE EXCEPTION 'an opening balance can only be the first entry of an account' USING ERRCODE = 'BM004';
      END IF;
    END IF;
  END IF;
  RETURN NEW;
END $$;

-- 3. Column freeze. Arguments: a comma-separated list of statuses in which the
-- freeze applies ('*' = always), then a comma-separated list of columns.
CREATE OR REPLACE FUNCTION app.tg_freeze_columns() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE o jsonb; n jsonb; col text;
BEGIN
  IF current_user = 'bms_owner' THEN RETURN NEW; END IF;
  o := to_jsonb(OLD);
  IF TG_ARGV[0] <> '*' AND NOT coalesce(o ->> 'status' = ANY (string_to_array(TG_ARGV[0], ',')), false) THEN
    RETURN NEW;
  END IF;
  n := to_jsonb(NEW);
  FOREACH col IN ARRAY string_to_array(TG_ARGV[1], ',') LOOP
    IF NOT o ? col THEN
      RAISE EXCEPTION 'freeze trigger on % names unknown column %', TG_TABLE_NAME, col;
    END IF;
    IF n -> col IS DISTINCT FROM o -> col THEN
      RAISE EXCEPTION '% of this % record can no longer be changed', col, TG_TABLE_NAME
        USING ERRCODE = 'BM004';
    END IF;
  END LOOP;
  RETURN NEW;
END $$;

-- Triggers named c_* run after the b_guard triggers, so they see their final NEW row.
CREATE TRIGGER c_freeze BEFORE UPDATE ON app.payments_received FOR EACH ROW
  EXECUTE FUNCTION app.tg_freeze_columns('*', 'receipt_no,reference,purpose,business_unit_id,received_at,received_by,client_ref,notes');
CREATE TRIGGER c_freeze_void BEFORE UPDATE ON app.payments_received FOR EACH ROW
  EXECUTE FUNCTION app.tg_freeze_columns('voided', 'voided_at,voided_by,void_reason');

CREATE TRIGGER c_freeze BEFORE UPDATE ON app.payments_made FOR EACH ROW
  EXECUTE FUNCTION app.tg_freeze_columns('*', 'payment_no,reference,business_unit_id,paid_at,supplier_id,contractor_id,employee_id,customer_id,other_payee_name,client_ref,notes');
CREATE TRIGGER c_freeze_void BEFORE UPDATE ON app.payments_made FOR EACH ROW
  EXECUTE FUNCTION app.tg_freeze_columns('voided', 'voided_at,voided_by,void_reason');

CREATE TRIGGER c_freeze_final BEFORE UPDATE ON app.sales FOR EACH ROW
  EXECUTE FUNCTION app.tg_freeze_columns('completed,voided', 'completed_at');
CREATE TRIGGER c_freeze_void BEFORE UPDATE ON app.sales FOR EACH ROW
  EXECUTE FUNCTION app.tg_freeze_columns('voided', 'voided_at,voided_by,void_reason');

CREATE TRIGGER c_freeze_final BEFORE UPDATE ON app.invoices FOR EACH ROW
  EXECUTE FUNCTION app.tg_freeze_columns('issued,partially_paid,paid,void', 'due_date');
CREATE TRIGGER c_freeze_void BEFORE UPDATE ON app.invoices FOR EACH ROW
  EXECUTE FUNCTION app.tg_freeze_columns('void', 'voided_at,void_reason');

CREATE TRIGGER c_freeze BEFORE UPDATE ON app.expenses FOR EACH ROW
  EXECUTE FUNCTION app.tg_freeze_columns('*', 'expense_no,requested_by');
CREATE TRIGGER c_freeze_final BEFORE UPDATE ON app.expenses FOR EACH ROW
  EXECUTE FUNCTION app.tg_freeze_columns('approved,paid,rejected,cancelled', 'description,receipt_file_id,location_id,decided_by,decided_at,rejection_reason');
CREATE TRIGGER c_freeze_paid BEFORE UPDATE ON app.expenses FOR EACH ROW
  EXECUTE FUNCTION app.tg_freeze_columns('paid', 'paid_at,paid_business_date');

CREATE TRIGGER c_freeze BEFORE UPDATE ON app.sale_returns FOR EACH ROW
  EXECUTE FUNCTION app.tg_freeze_columns('*', 'return_no,sale_id,location_id,resolution,reason,refund_amount,requested_by,business_date');
CREATE TRIGGER c_freeze_decided BEFORE UPDATE ON app.sale_returns FOR EACH ROW
  EXECUTE FUNCTION app.tg_freeze_columns('approved,completed,rejected,cancelled', 'decided_by,decided_at');
CREATE TRIGGER c_freeze_completed BEFORE UPDATE ON app.sale_returns FOR EACH ROW
  EXECUTE FUNCTION app.tg_freeze_columns('completed', 'completed_at,exchange_sale_id');

CREATE TRIGGER c_freeze BEFORE UPDATE ON app.commission_entries FOR EACH ROW
  EXECUTE FUNCTION app.tg_freeze_columns('*', 'employee_id,commission_rule_id,sale_id,project_id,sale_return_id,amount,earned_on');

CREATE TRIGGER c_freeze BEFORE UPDATE ON app.day_closings FOR EACH ROW
  EXECUTE FUNCTION app.tg_freeze_columns('*', 'location_id,business_date');
CREATE TRIGGER c_freeze_closed BEFORE UPDATE ON app.day_closings FOR EACH ROW
  EXECUTE FUNCTION app.tg_freeze_columns('closed', 'expected_cash,counted_cash,note,closed_by,closed_at');

CREATE TRIGGER c_freeze BEFORE UPDATE ON app.approval_requests FOR EACH ROW
  EXECUTE FUNCTION app.tg_freeze_columns('*', 'approval_type,reason,requested_amount,requested_percent,current_amount,sale_id,project_id,design_job_id,financial_transaction_id,payment_received_id,payment_made_id,day_closing_id,product_id,requested_by,requested_at');
CREATE TRIGGER c_freeze_decided BEFORE UPDATE ON app.approval_requests FOR EACH ROW
  EXECUTE FUNCTION app.tg_freeze_columns('approved,rejected,cancelled,expired', 'decided_by,decided_at,decision_note');

CREATE TRIGGER c_freeze BEFORE UPDATE ON app.payroll_runs FOR EACH ROW
  EXECUTE FUNCTION app.tg_freeze_columns('approved,paid,cancelled', 'period_year,period_month,location_id,business_unit_id,generated_by,generated_at');
CREATE TRIGGER c_freeze_final BEFORE UPDATE ON app.payroll_runs FOR EACH ROW
  EXECUTE FUNCTION app.tg_freeze_columns('paid,cancelled', 'account_id,approved_by,approved_at,paid_by,paid_at,paid_business_date');

CREATE TRIGGER c_freeze BEFORE UPDATE ON app.contractor_bills FOR EACH ROW
  EXECUTE FUNCTION app.tg_freeze_columns('*', 'bill_no,contractor_id,business_unit_id,project_id,design_job_id,print_order_id,description,amount,bill_date');

CREATE TRIGGER c_freeze_final BEFORE UPDATE ON app.purchases FOR EACH ROW
  EXECUTE FUNCTION app.tg_freeze_columns('partially_received,received,cancelled', 'due_date,supplier_invoice_ref');

CREATE TRIGGER c_freeze BEFORE UPDATE ON app.petty_cash_reconciliations FOR EACH ROW
  EXECUTE FUNCTION app.tg_freeze_columns('*', 'account_id,period_start,period_end,opening_balance,expected_closing,counted_closing,note');

CREATE TRIGGER c_freeze BEFORE UPDATE ON app.employee_advances FOR EACH ROW
  EXECUTE FUNCTION app.tg_freeze_columns('*', 'employee_id,kind,principal,issued_on,approved_by');

-- 4a. A bill becomes paid only by allocating a payment (definer trigger). The
-- application may only void it, and only while nothing has been paid.
CREATE OR REPLACE FUNCTION app.tg_contractor_bill_status_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF current_user <> 'bms_owner' AND NEW.status IS DISTINCT FROM OLD.status
     AND NOT (NEW.status = 'void' AND OLD.amount_paid = 0) THEN
    RAISE EXCEPTION 'a contractor bill is settled by payments; it can only be voided while unpaid'
      USING ERRCODE = 'BM004';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER c_status BEFORE UPDATE ON app.contractor_bills FOR EACH ROW
  EXECUTE FUNCTION app.tg_contractor_bill_status_guard();

-- 4b. Loans: the debt is reduced only by repayments. No application workflow
-- writes a loan off, so the API role cannot do it either; adding one needs a
-- migration that defines who may approve it. The disbursing payment is set once.
CREATE OR REPLACE FUNCTION app.tg_employee_advance_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF current_user = 'bms_owner' THEN RETURN NEW; END IF;
  IF NEW.status IS DISTINCT FROM OLD.status THEN
    RAISE EXCEPTION 'a loan is closed by its repayments and cannot be written off here' USING ERRCODE = 'BM004';
  END IF;
  IF OLD.payment_made_id IS NOT NULL AND NEW.payment_made_id IS DISTINCT FROM OLD.payment_made_id THEN
    RAISE EXCEPTION 'the payment that disbursed a loan cannot be replaced' USING ERRCODE = 'BM004';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER c_guard BEFORE UPDATE ON app.employee_advances FOR EACH ROW
  EXECUTE FUNCTION app.tg_employee_advance_guard();

-- 4c. Repayment rows follow the payroll run: editable while it is a draft. An
-- unknown status (row hidden by row-level security) is treated as locked.
CREATE OR REPLACE FUNCTION app.tg_advance_repayments_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE st app.payroll_status;
BEGIN
  IF current_user = 'bms_owner' THEN RETURN coalesce(NEW, OLD); END IF;
  SELECT r.status INTO st
    FROM app.payroll_lines l
    JOIN app.payroll_runs r ON r.tenant_id = l.tenant_id AND r.id = l.payroll_run_id
   WHERE l.tenant_id = coalesce(NEW.tenant_id, OLD.tenant_id)
     AND l.id = coalesce(NEW.payroll_line_id, OLD.payroll_line_id);
  IF st IS DISTINCT FROM 'draft' THEN
    RAISE EXCEPTION 'loan repayments are locked once the payroll run leaves draft' USING ERRCODE = 'BM004';
  END IF;
  IF TG_OP = 'UPDATE' AND NEW.payroll_line_id IS DISTINCT FROM OLD.payroll_line_id THEN
    RAISE EXCEPTION 'a loan repayment cannot be moved to another payroll line' USING ERRCODE = 'BM004';
  END IF;
  RETURN coalesce(NEW, OLD);
END $$;
CREATE TRIGGER b_guard BEFORE INSERT OR UPDATE OR DELETE ON app.advance_repayments FOR EACH ROW
  EXECUTE FUNCTION app.tg_advance_repayments_guard();

-- The payroll line guard let a change through when the run was hidden from the
-- caller (NULL <> 'draft' is not true). Fail closed instead.
CREATE OR REPLACE FUNCTION app.tg_payroll_lines_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE st app.payroll_status;
BEGIN
  IF current_user = 'bms_owner' AND TG_OP <> 'INSERT' THEN RETURN coalesce(NEW, OLD); END IF;
  SELECT status INTO st FROM app.payroll_runs
   WHERE tenant_id = coalesce(NEW.tenant_id, OLD.tenant_id) AND id = coalesce(NEW.payroll_run_id, OLD.payroll_run_id);
  IF st IS DISTINCT FROM 'draft' THEN
    RAISE EXCEPTION 'payroll lines are locked once the run leaves draft' USING ERRCODE = 'BM004';
  END IF;
  RETURN coalesce(NEW, OLD);
END $$;

-- 4d. The counted lines of a closed day are part of the closing.
CREATE OR REPLACE FUNCTION app.tg_day_closing_lines_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE st app.day_status;
BEGIN
  IF current_user = 'bms_owner' THEN RETURN coalesce(NEW, OLD); END IF;
  SELECT status INTO st FROM app.day_closings
   WHERE tenant_id = coalesce(NEW.tenant_id, OLD.tenant_id)
     AND id = coalesce(NEW.day_closing_id, OLD.day_closing_id);
  IF st IS NULL OR st = 'closed' THEN
    RAISE EXCEPTION 'lines of a closed day are read-only; reopen the day first' USING ERRCODE = 'BM004';
  END IF;
  IF TG_OP = 'UPDATE' AND NEW.day_closing_id IS DISTINCT FROM OLD.day_closing_id THEN
    RAISE EXCEPTION 'a closing line cannot be moved to another day' USING ERRCODE = 'BM004';
  END IF;
  RETURN coalesce(NEW, OLD);
END $$;
CREATE TRIGGER b_guard BEFORE INSERT OR UPDATE OR DELETE ON app.day_closing_lines FOR EACH ROW
  EXECUTE FUNCTION app.tg_day_closing_lines_guard();

-- 4e. Return lines are written with the request and never change afterwards.
CREATE OR REPLACE FUNCTION app.tg_sale_return_lines_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE st app.return_status;
BEGIN
  IF current_user = 'bms_owner' THEN RETURN NEW; END IF;
  IF TG_OP = 'UPDATE' THEN
    RAISE EXCEPTION 'return lines are read-only; cancel the return and raise a new one' USING ERRCODE = 'BM004';
  END IF;
  SELECT status INTO st FROM app.sale_returns WHERE tenant_id = NEW.tenant_id AND id = NEW.sale_return_id;
  IF st IS DISTINCT FROM 'requested' THEN
    RAISE EXCEPTION 'lines can only be added to a return that is still requested' USING ERRCODE = 'BM004';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER b_guard BEFORE INSERT OR UPDATE ON app.sale_return_lines FOR EACH ROW
  EXECUTE FUNCTION app.tg_sale_return_lines_guard();

-- The one reference to a money account that had no foreign key.
ALTER TABLE app.day_closing_lines ADD CONSTRAINT day_closing_lines_account_fk
  FOREIGN KEY (tenant_id, account_id) REFERENCES app.financial_accounts (tenant_id, id);

-- 5. Archiving or deactivating must not hide money a customer still owes. The
-- customer and client APIs each checked only their own kind of document, and
-- the client status route checked neither.
CREATE OR REPLACE FUNCTION app.tg_customer_archive_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF (NEW.archived_at IS NOT NULL AND OLD.archived_at IS NULL)
     OR (NEW.client_status = 'inactive' AND OLD.client_status IS DISTINCT FROM 'inactive') THEN
    IF EXISTS (SELECT 1 FROM app.sales s WHERE s.tenant_id = NEW.tenant_id AND s.customer_id = NEW.id
                  AND s.status = 'completed' AND s.balance_due > 0)
       OR EXISTS (SELECT 1 FROM app.invoices i WHERE i.tenant_id = NEW.tenant_id AND i.customer_id = NEW.id
                  AND i.status IN ('issued', 'partially_paid') AND i.balance_due > 0) THEN
      RAISE EXCEPTION 'this customer still owes money. Collect or void the unpaid documents first'
        USING ERRCODE = 'BM001';
    END IF;
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER b_archive_guard BEFORE UPDATE ON app.customers FOR EACH ROW
  EXECUTE FUNCTION app.tg_customer_archive_guard();
