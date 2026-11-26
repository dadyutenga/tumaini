-- =====================================================================
-- 280_financial_integrity_hardening.sql — financial integrity hardening:
--   1. Row-level locking in tg_returns_post & tg_payment_made_allocation_apply
--      so concurrent refunds/payments can never exceed paid/open balances.
--   2. Reject cancelled/paid/void allocation targets in
--      tg_payment_made_allocation_apply instead of silent 0-row updates.
--   3. DB-level total discount (sale + line) permission & limit check in
--      tg_sales_guard.
--   4. DB-level immutability & total-verification triggers on app.invoices,
--      app.invoice_lines, app.purchases, app.purchase_lines.
--   5. DB-level BEFORE DELETE protection on core financial tables.
--   6. Unique partial index on non-cash app.payments_made references.
-- =====================================================================
SET search_path = app, public;

-- 1. Serialize concurrent returns completing on the same sale by locking app.sales.
CREATE OR REPLACE FUNCTION app.tg_returns_post() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE refund_cap numeric;
BEGIN
  IF NEW.status = 'completed' AND OLD.status <> 'completed' THEN
    -- Lock the parent sale row so concurrent return completions or payment voids serialize.
    PERFORM 1 FROM app.sales s WHERE s.tenant_id = NEW.tenant_id AND s.id = NEW.sale_id FOR UPDATE;

    -- Cumulative returned quantity is capped by the CHECK on sale_lines.
    UPDATE app.sale_lines sl SET returned_quantity = sl.returned_quantity + rl.quantity
      FROM app.sale_return_lines rl
     WHERE rl.tenant_id = NEW.tenant_id AND rl.sale_return_id = NEW.id
       AND sl.tenant_id = rl.tenant_id AND sl.id = rl.sale_line_id;

    -- Refunds can never exceed what was actually paid for the sale.
    SELECT s.amount_paid - coalesce((SELECT sum(r.refund_amount) FROM app.sale_returns r
                                      WHERE r.tenant_id = s.tenant_id AND r.sale_id = s.id
                                        AND r.status = 'completed' AND r.id <> NEW.id), 0)
      INTO refund_cap FROM app.sales s WHERE s.tenant_id = NEW.tenant_id AND s.id = NEW.sale_id;
    IF NEW.refund_amount > refund_cap THEN
      RAISE EXCEPTION 'refund % exceeds amount paid and not yet refunded %', NEW.refund_amount, refund_cap
        USING ERRCODE = 'BM005';
    END IF;

    INSERT INTO app.stock_movements (tenant_id, location_id, product_id, movement_type, quantity_delta,
                                     unit_cost, business_date, sale_return_line_id)
    SELECT rl.tenant_id, NEW.location_id, sl.product_id, 'sale_return', rl.quantity, sl.unit_cost, NEW.business_date, rl.id
      FROM app.sale_return_lines rl
      JOIN app.sale_lines sl ON sl.tenant_id = rl.tenant_id AND sl.id = rl.sale_line_id
     WHERE rl.tenant_id = NEW.tenant_id AND rl.sale_return_id = NEW.id AND rl.restock;

    UPDATE app.product_units u
       SET status = CASE WHEN rl.restock THEN 'in_stock'::app.unit_status ELSE 'defective' END,
           location_id = NEW.location_id, sold_at = CASE WHEN rl.restock THEN NULL ELSE u.sold_at END
      FROM app.sale_return_lines rl
     WHERE rl.tenant_id = NEW.tenant_id AND rl.sale_return_id = NEW.id
       AND u.tenant_id = rl.tenant_id AND u.id = rl.product_unit_id;

    PERFORM app.reverse_return_commissions(NEW.tenant_id, NEW.id);
  END IF;
  RETURN NULL;
END $$;

-- 2. Lock allocation targets FOR UPDATE and reject invalid target statuses explicitly.
CREATE OR REPLACE FUNCTION app.tg_payment_made_allocation_apply() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE
  pay app.payments_made%ROWTYPE;
  allocated numeric;
  refunded numeric;
  cap numeric;
  payee_ok boolean;
  tgt_status text;
BEGIN
  SELECT * INTO pay FROM app.payments_made WHERE tenant_id = NEW.tenant_id AND id = NEW.payment_made_id FOR UPDATE;
  IF pay.status <> 'posted' THEN
    RAISE EXCEPTION 'cannot allocate a voided payment' USING ERRCODE = 'BM005';
  END IF;
  SELECT coalesce(sum(amount), 0) INTO allocated FROM app.payment_made_allocations
   WHERE tenant_id = NEW.tenant_id AND payment_made_id = NEW.payment_made_id;
  IF allocated > pay.amount THEN
    RAISE EXCEPTION 'allocations % exceed payment amount %', allocated, pay.amount USING ERRCODE = 'BM005';
  END IF;

  IF NEW.purchase_id IS NOT NULL THEN
    SELECT supplier_id = pay.supplier_id, status::text
      INTO payee_ok, tgt_status
      FROM app.purchases
     WHERE tenant_id = NEW.tenant_id AND id = NEW.purchase_id
       FOR UPDATE;
    IF NOT FOUND THEN
      RAISE EXCEPTION 'purchase not found' USING ERRCODE = 'BM008';
    END IF;
    IF tgt_status = 'cancelled' THEN
      RAISE EXCEPTION 'purchase is cancelled and cannot take payments' USING ERRCODE = 'BM001';
    END IF;
    UPDATE app.purchases SET amount_paid = amount_paid + NEW.amount
     WHERE tenant_id = NEW.tenant_id AND id = NEW.purchase_id;
  ELSIF NEW.contractor_bill_id IS NOT NULL THEN
    SELECT contractor_id = pay.contractor_id, status::text
      INTO payee_ok, tgt_status
      FROM app.contractor_bills
     WHERE tenant_id = NEW.tenant_id AND id = NEW.contractor_bill_id
       FOR UPDATE;
    IF NOT FOUND THEN
      RAISE EXCEPTION 'contractor bill not found' USING ERRCODE = 'BM008';
    END IF;
    IF tgt_status NOT IN ('open', 'partially_paid') THEN
      RAISE EXCEPTION 'contractor bill is % and cannot take payments', tgt_status USING ERRCODE = 'BM001';
    END IF;
    UPDATE app.contractor_bills
       SET amount_paid = amount_paid + NEW.amount,
           status = CASE WHEN amount_paid + NEW.amount = amount THEN 'paid'::app.bill_status ELSE 'partially_paid' END
     WHERE tenant_id = NEW.tenant_id AND id = NEW.contractor_bill_id;
  ELSE
    SELECT r.status = 'completed' AND s.customer_id IS NOT DISTINCT FROM pay.customer_id, r.refund_amount
      INTO payee_ok, cap
      FROM app.sale_returns r
      JOIN app.sales s ON s.tenant_id = r.tenant_id AND s.id = r.sale_id
     WHERE r.tenant_id = NEW.tenant_id AND r.id = NEW.sale_return_id
       FOR UPDATE OF r;
    IF NOT FOUND THEN
      RAISE EXCEPTION 'sale return not found' USING ERRCODE = 'BM008';
    END IF;
    SELECT coalesce(sum(a.amount), 0) INTO refunded
      FROM app.payment_made_allocations a
      JOIN app.payments_made m ON m.tenant_id = a.tenant_id AND m.id = a.payment_made_id AND m.status = 'posted'
     WHERE a.tenant_id = NEW.tenant_id AND a.sale_return_id = NEW.sale_return_id;
    IF refunded > cap THEN
      RAISE EXCEPTION 'refund payments % exceed approved refund %', refunded, cap USING ERRCODE = 'BM005';
    END IF;
  END IF;

  IF NOT coalesce(payee_ok, false) THEN
    RAISE EXCEPTION 'payee does not match the document' USING ERRCODE = 'BM008';
  END IF;
  RETURN NULL;
END $$;

-- 3. Enforce total discount (sale-level + line-level) permission and limit in tg_sales_guard.
CREATE OR REPLACE FUNCTION app.tg_sales_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  line_disc numeric;
  total_disc numeric;
  pct numeric;
  lim numeric;
  has_approved boolean;
BEGIN
  IF TG_OP = 'DELETE' THEN
    IF OLD.status NOT IN ('draft', 'held', 'pending_approval') OR OLD.amount_paid > 0 THEN
      RAISE EXCEPTION 'only unpaid draft/held sales can be deleted' USING ERRCODE = 'BM004';
    END IF;
    RETURN OLD;
  END IF;

  IF TG_OP = 'INSERT' THEN
    PERFORM app.assert_day_open(NEW.tenant_id, NEW.location_id, NEW.business_date);
    RETURN NEW;
  END IF;

  -- UPDATE
  IF OLD.status IN ('completed', 'voided') THEN
    IF (NEW.subtotal, NEW.discount_amount, NEW.discount_value, NEW.tax_amount, NEW.total, NEW.customer_id,
        NEW.location_id, NEW.business_date, NEW.sold_by, NEW.receipt_no, NEW.due_date, NEW.business_unit_id)
       IS DISTINCT FROM
       (OLD.subtotal, OLD.discount_amount, OLD.discount_value, OLD.tax_amount, OLD.total, OLD.customer_id,
        OLD.location_id, OLD.business_date, OLD.sold_by, OLD.receipt_no, OLD.due_date, OLD.business_unit_id) THEN
      RAISE EXCEPTION 'completed sale % is read-only; use a return or void', OLD.receipt_no USING ERRCODE = 'BM004';
    END IF;
  ELSE
    PERFORM app.assert_day_open(NEW.tenant_id, NEW.location_id, NEW.business_date);
  END IF;

  IF NEW.status = 'held' AND OLD.status <> 'held' THEN NEW.held_at := coalesce(NEW.held_at, now()); END IF;

  IF NEW.status = 'completed' AND OLD.status <> 'completed' THEN
    NEW.completed_at := coalesce(NEW.completed_at, now());
    -- Credit sale (balance left at completion) requires a registered customer and a due date (spec §6).
    IF NEW.amount_paid < NEW.total AND (NEW.customer_id IS NULL OR NEW.due_date IS NULL) THEN
      RAISE EXCEPTION 'credit sale requires a registered customer and a due date' USING ERRCODE = 'BM008';
    END IF;
    -- Discount authority: covers both sale-level and line-level discounts.
    SELECT coalesce(sum(line_discount), 0) INTO line_disc
      FROM app.sale_lines WHERE tenant_id = NEW.tenant_id AND sale_id = NEW.id;
    total_disc := NEW.discount_amount + line_disc;
    IF total_disc > 0 THEN
      pct := round(total_disc * 100 / nullif(NEW.subtotal + line_disc, 0), 2);
      lim := app.authority_limit(NEW.sold_by, 'sale_discount_pct');
      SELECT EXISTS (
        SELECT 1 FROM app.approval_requests a
         WHERE a.tenant_id = NEW.tenant_id AND a.sale_id = NEW.id AND a.approval_type = 'sale_discount'
           AND a.status = 'approved' AND a.requested_percent >= pct
      ) INTO has_approved;
      IF app.current_membership_id() IS NOT NULL AND NOT has_approved AND NOT app.has_permission('shop.discount.apply') THEN
        RAISE EXCEPTION 'permission shop.discount.apply required for discounts' USING ERRCODE = 'BM007';
      END IF;
      IF lim IS NOT NULL AND pct > lim AND NOT has_approved THEN
        RAISE EXCEPTION 'discount %%% exceeds seller limit %%%; approval required', pct, lim USING ERRCODE = 'BM007';
      END IF;
    END IF;
  END IF;

  IF NEW.status = 'voided' AND OLD.status <> 'voided' THEN
    NEW.voided_at := coalesce(NEW.voided_at, now());
    NEW.voided_by := coalesce(NEW.voided_by, app.current_membership_id());
    IF NOT (app.has_permission('shop.sale.void') OR app.approved_request_exists('sale_void', NEW.id)) THEN
      RAISE EXCEPTION 'voiding a sale requires Admin approval' USING ERRCODE = 'BM007';
    END IF;
    IF NEW.amount_paid > 0 THEN
      RAISE EXCEPTION 'void or refund the sale''s payments first' USING ERRCODE = 'BM005';
    END IF;
    IF EXISTS (SELECT 1 FROM app.sale_returns WHERE tenant_id = NEW.tenant_id AND sale_id = NEW.id AND status = 'completed') THEN
      RAISE EXCEPTION 'sale has completed returns and cannot be voided' USING ERRCODE = 'BM004';
    END IF;
    PERFORM app.assert_day_open(NEW.tenant_id, NEW.location_id, NEW.business_date);
  END IF;
  RETURN NEW;
END $$;

-- 4. DB-level immutability & total-verification triggers for invoices & invoice_lines.
CREATE OR REPLACE FUNCTION app.tg_invoices_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    IF OLD.status <> 'draft' OR OLD.amount_paid > 0 THEN
      RAISE EXCEPTION 'only unpaid draft invoices can be deleted' USING ERRCODE = 'BM004';
    END IF;
    RETURN OLD;
  END IF;

  IF OLD.status IN ('issued', 'partially_paid', 'paid', 'void') THEN
    IF (NEW.subtotal, NEW.discount_amount, NEW.tax_amount, NEW.total, NEW.customer_id,
        NEW.business_unit_id, NEW.issue_date, NEW.invoice_no)
       IS DISTINCT FROM
       (OLD.subtotal, OLD.discount_amount, OLD.tax_amount, OLD.total, OLD.customer_id,
        OLD.business_unit_id, OLD.issue_date, OLD.invoice_no) THEN
      RAISE EXCEPTION 'issued invoice % is read-only; void and re-issue', OLD.invoice_no USING ERRCODE = 'BM004';
    END IF;
  END IF;
  RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION app.tg_invoice_lines_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE st app.invoice_status;
BEGIN
  SELECT status INTO st FROM app.invoices
   WHERE tenant_id = coalesce(NEW.tenant_id, OLD.tenant_id)
     AND id = coalesce(NEW.invoice_id, OLD.invoice_id);
  IF st IN ('issued', 'partially_paid', 'paid', 'void') THEN
    RAISE EXCEPTION 'lines of an issued invoice are read-only' USING ERRCODE = 'BM004';
  END IF;
  RETURN coalesce(NEW, OLD);
END $$;

CREATE OR REPLACE FUNCTION app.tg_invoices_check_totals() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE inv app.invoices%ROWTYPE; lines_total numeric; n_lines integer;
BEGIN
  SELECT * INTO inv FROM app.invoices WHERE tenant_id = NEW.tenant_id AND id = NEW.id;
  IF NOT FOUND OR inv.status NOT IN ('issued', 'partially_paid', 'paid') THEN RETURN NULL; END IF;
  SELECT coalesce(sum(line_total), 0), count(*) INTO lines_total, n_lines
    FROM app.invoice_lines WHERE tenant_id = inv.tenant_id AND invoice_id = inv.id;
  IF n_lines = 0 THEN
    RAISE EXCEPTION 'issued invoice % has no lines', inv.invoice_no USING ERRCODE = 'BM008';
  END IF;
  IF lines_total <> inv.subtotal THEN
    RAISE EXCEPTION 'invoice % subtotal % <> sum of lines %', inv.invoice_no, inv.subtotal, lines_total USING ERRCODE = 'BM008';
  END IF;
  RETURN NULL;
END $$;

DROP TRIGGER IF EXISTS b_guard ON app.invoices;
CREATE TRIGGER b_guard BEFORE UPDATE OR DELETE ON app.invoices
  FOR EACH ROW EXECUTE FUNCTION app.tg_invoices_guard();

DROP TRIGGER IF EXISTS zc_check_totals ON app.invoices;
CREATE CONSTRAINT TRIGGER zc_check_totals AFTER INSERT OR UPDATE ON app.invoices
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION app.tg_invoices_check_totals();

DROP TRIGGER IF EXISTS b_guard ON app.invoice_lines;
CREATE TRIGGER b_guard BEFORE INSERT OR UPDATE OR DELETE ON app.invoice_lines
  FOR EACH ROW EXECUTE FUNCTION app.tg_invoice_lines_guard();

-- 5. DB-level immutability guards for purchases & purchase_lines.
CREATE OR REPLACE FUNCTION app.tg_purchases_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    IF OLD.status <> 'draft' OR OLD.amount_paid > 0 THEN
      RAISE EXCEPTION 'only unpaid draft purchases can be deleted' USING ERRCODE = 'BM004';
    END IF;
    RETURN OLD;
  END IF;

  IF OLD.status IN ('partially_received', 'received', 'cancelled') THEN
    IF (NEW.subtotal, NEW.discount_amount, NEW.tax_amount, NEW.total, NEW.supplier_id,
        NEW.business_unit_id, NEW.location_id, NEW.order_date, NEW.purchase_no)
       IS DISTINCT FROM
       (OLD.subtotal, OLD.discount_amount, OLD.tax_amount, OLD.total, OLD.supplier_id,
        OLD.business_unit_id, OLD.location_id, OLD.order_date, OLD.purchase_no) THEN
      RAISE EXCEPTION 'finalized purchase % is read-only', OLD.purchase_no USING ERRCODE = 'BM004';
    END IF;
  END IF;
  RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION app.tg_purchase_lines_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE st app.purchase_status;
BEGIN
  SELECT status INTO st FROM app.purchases
   WHERE tenant_id = coalesce(NEW.tenant_id, OLD.tenant_id)
     AND id = coalesce(NEW.purchase_id, OLD.purchase_id);
  IF st IN ('partially_received', 'received', 'cancelled') THEN
    IF TG_OP <> 'UPDATE' OR current_user <> 'bms_owner'
       OR (NEW.product_id, NEW.quantity_ordered, NEW.unit_cost, NEW.line_total, NEW.purchase_id)
          IS DISTINCT FROM (OLD.product_id, OLD.quantity_ordered, OLD.unit_cost, OLD.line_total, OLD.purchase_id) THEN
      RAISE EXCEPTION 'lines of a received or cancelled purchase are read-only' USING ERRCODE = 'BM004';
    END IF;
  END IF;
  RETURN coalesce(NEW, OLD);
END $$;

DROP TRIGGER IF EXISTS b_guard ON app.purchases;
CREATE TRIGGER b_guard BEFORE UPDATE OR DELETE ON app.purchases
  FOR EACH ROW EXECUTE FUNCTION app.tg_purchases_guard();

DROP TRIGGER IF EXISTS b_guard ON app.purchase_lines;
CREATE TRIGGER b_guard BEFORE INSERT OR UPDATE OR DELETE ON app.purchase_lines
  FOR EACH ROW EXECUTE FUNCTION app.tg_purchase_lines_guard();

-- 6. Block silent deletion of core financial records.
CREATE OR REPLACE FUNCTION app.tg_block_financial_delete() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF TG_TABLE_NAME IN ('payments_received', 'payments_made') THEN
    RAISE EXCEPTION '% cannot be deleted; void the payment instead', TG_TABLE_NAME USING ERRCODE = 'BM004';
  ELSIF TG_TABLE_NAME = 'expenses' AND OLD.status IN ('approved', 'paid') THEN
    RAISE EXCEPTION 'approved or paid expenses cannot be deleted' USING ERRCODE = 'BM004';
  ELSIF TG_TABLE_NAME = 'sale_returns' AND OLD.status = 'completed' THEN
    RAISE EXCEPTION 'completed returns cannot be deleted' USING ERRCODE = 'BM004';
  ELSIF TG_TABLE_NAME = 'day_closings' AND OLD.status IN ('closed', 'reopened') THEN
    RAISE EXCEPTION 'day closing records cannot be deleted' USING ERRCODE = 'BM004';
  ELSIF TG_TABLE_NAME = 'contractor_bills' AND (OLD.amount_paid > 0 OR OLD.status IN ('partially_paid', 'paid', 'void')) THEN
    RAISE EXCEPTION 'paid or voided contractor bills cannot be deleted' USING ERRCODE = 'BM004';
  END IF;
  RETURN OLD;
END $$;

DROP TRIGGER IF EXISTS no_delete ON app.payments_received;
CREATE TRIGGER no_delete BEFORE DELETE ON app.payments_received
  FOR EACH ROW EXECUTE FUNCTION app.tg_block_financial_delete();

DROP TRIGGER IF EXISTS no_delete ON app.payments_made;
CREATE TRIGGER no_delete BEFORE DELETE ON app.payments_made
  FOR EACH ROW EXECUTE FUNCTION app.tg_block_financial_delete();

DROP TRIGGER IF EXISTS no_delete ON app.expenses;
CREATE TRIGGER no_delete BEFORE DELETE ON app.expenses
  FOR EACH ROW EXECUTE FUNCTION app.tg_block_financial_delete();

DROP TRIGGER IF EXISTS no_delete ON app.sale_returns;
CREATE TRIGGER no_delete BEFORE DELETE ON app.sale_returns
  FOR EACH ROW EXECUTE FUNCTION app.tg_block_financial_delete();

DROP TRIGGER IF EXISTS no_delete ON app.day_closings;
CREATE TRIGGER no_delete BEFORE DELETE ON app.day_closings
  FOR EACH ROW EXECUTE FUNCTION app.tg_block_financial_delete();

DROP TRIGGER IF EXISTS no_delete ON app.contractor_bills;
CREATE TRIGGER no_delete BEFORE DELETE ON app.contractor_bills
  FOR EACH ROW EXECUTE FUNCTION app.tg_block_financial_delete();

-- 7. Prevent duplicate non-cash payment_made references per tenant.
CREATE UNIQUE INDEX IF NOT EXISTS payments_made_ref_uq
  ON app.payments_made (tenant_id, method, reference)
  WHERE reference IS NOT NULL AND status = 'posted';
