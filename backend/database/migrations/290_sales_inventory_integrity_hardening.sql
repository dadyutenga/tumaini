-- =====================================================================
-- 290_sales_inventory_integrity_hardening.sql —
--   1. Enforce strict payment-status vs invoice-status consistency on
--      app.invoices so status and amount_paid can never contradict.
--   2. Make posted stock_adjustments / lines, non-draft stock_transfers /
--      lines, and goods_receipts / lines immutable against silent edits
--      or deletion.
--   3. Prevent hard DELETE on app.products that have historical sales,
--      purchases, or stock movements (must deactivate instead).
-- =====================================================================
SET search_path = app, public;

-- 1. Invoice status and payment totals can never contradict each other.
ALTER TABLE app.invoices
  DROP CONSTRAINT IF EXISTS invoices_paid_status_check,
  DROP CONSTRAINT IF EXISTS invoices_partial_status_check,
  DROP CONSTRAINT IF EXISTS invoices_draft_unpaid_check,
  DROP CONSTRAINT IF EXISTS invoices_issued_unpaid_check;

ALTER TABLE app.invoices
  ADD CONSTRAINT invoices_paid_status_check    CHECK (status <> 'paid'           OR (total > 0 AND amount_paid = total)),
  ADD CONSTRAINT invoices_partial_status_check CHECK (status <> 'partially_paid' OR (amount_paid > 0 AND amount_paid < total)),
  ADD CONSTRAINT invoices_draft_unpaid_check   CHECK (status <> 'draft'          OR amount_paid = 0),
  ADD CONSTRAINT invoices_issued_unpaid_check  CHECK (status <> 'issued'         OR amount_paid = 0);

-- 2. Immutable posted stock adjustments and lines.
CREATE OR REPLACE FUNCTION app.tg_stock_adjustment_guard() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    IF OLD.status = 'posted' THEN
      RAISE EXCEPTION 'posted stock adjustments cannot be deleted' USING ERRCODE = 'BM004';
    END IF;
    RETURN OLD;
  END IF;
  IF OLD.status = 'posted' THEN
    RAISE EXCEPTION 'posted stock adjustments are immutable' USING ERRCODE = 'BM004';
  END IF;
  IF OLD.status = 'cancelled' THEN
    RAISE EXCEPTION 'cancelled stock adjustments are immutable' USING ERRCODE = 'BM004';
  END IF;
  RETURN NEW;
END $$;

DROP TRIGGER IF EXISTS b_guard ON app.stock_adjustments;
CREATE TRIGGER b_guard
  BEFORE UPDATE OR DELETE ON app.stock_adjustments
  FOR EACH ROW EXECUTE FUNCTION app.tg_stock_adjustment_guard();

CREATE OR REPLACE FUNCTION app.tg_stock_adjustment_lines_guard() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE adj_id uuid; st app.doc_post_status;
BEGIN
  adj_id := CASE WHEN TG_OP = 'DELETE' THEN OLD.stock_adjustment_id ELSE NEW.stock_adjustment_id END;
  SELECT status INTO st FROM app.stock_adjustments WHERE id = adj_id;
  IF st IS DISTINCT FROM 'draft' THEN
    RAISE EXCEPTION 'lines of a % stock adjustment are immutable', st USING ERRCODE = 'BM004';
  END IF;
  RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
END $$;

DROP TRIGGER IF EXISTS b_guard ON app.stock_adjustment_lines;
CREATE TRIGGER b_guard
  BEFORE INSERT OR UPDATE OR DELETE ON app.stock_adjustment_lines
  FOR EACH ROW EXECUTE FUNCTION app.tg_stock_adjustment_lines_guard();

-- 3. Immutable dispatched/received stock transfers and lines.
CREATE OR REPLACE FUNCTION app.tg_stock_transfer_guard() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    IF OLD.status <> 'draft' THEN
      RAISE EXCEPTION 'non-draft stock transfers cannot be deleted' USING ERRCODE = 'BM004';
    END IF;
    RETURN OLD;
  END IF;
  IF OLD.status IN ('received', 'cancelled') THEN
    RAISE EXCEPTION 'stock transfer % is immutable', OLD.status USING ERRCODE = 'BM004';
  END IF;
  IF OLD.status = 'dispatched' THEN
    -- Only transitioning dispatched -> received (with receipt metadata) is permitted.
    IF NEW.status <> 'received'
       OR NEW.from_location_id IS DISTINCT FROM OLD.from_location_id
       OR NEW.to_location_id IS DISTINCT FROM OLD.to_location_id
       OR NEW.transfer_no IS DISTINCT FROM OLD.transfer_no
       OR NEW.dispatched_at IS DISTINCT FROM OLD.dispatched_at
       OR NEW.dispatched_by IS DISTINCT FROM OLD.dispatched_by THEN
      RAISE EXCEPTION 'dispatched stock transfers cannot be altered' USING ERRCODE = 'BM004';
    END IF;
  END IF;
  RETURN NEW;
END $$;

DROP TRIGGER IF EXISTS b_guard ON app.stock_transfers;
CREATE TRIGGER b_guard
  BEFORE UPDATE OR DELETE ON app.stock_transfers
  FOR EACH ROW EXECUTE FUNCTION app.tg_stock_transfer_guard();

CREATE OR REPLACE FUNCTION app.tg_stock_transfer_lines_guard() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE tr_id uuid; st app.transfer_status;
BEGIN
  tr_id := CASE WHEN TG_OP = 'DELETE' THEN OLD.stock_transfer_id ELSE NEW.stock_transfer_id END;
  SELECT status INTO st FROM app.stock_transfers WHERE id = tr_id;
  IF st IS DISTINCT FROM 'draft' THEN
    RAISE EXCEPTION 'lines of a % stock transfer are immutable', st USING ERRCODE = 'BM004';
  END IF;
  RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
END $$;

DROP TRIGGER IF EXISTS b_guard ON app.stock_transfer_lines;
CREATE TRIGGER b_guard
  BEFORE INSERT OR UPDATE OR DELETE ON app.stock_transfer_lines
  FOR EACH ROW EXECUTE FUNCTION app.tg_stock_transfer_lines_guard();

-- 4. Append-only goods receipts and lines (received stock history cannot be silently rewritten).
DROP TRIGGER IF EXISTS no_mutation ON app.goods_receipts;
CREATE TRIGGER no_mutation
  BEFORE UPDATE OR DELETE ON app.goods_receipts
  FOR EACH ROW EXECUTE FUNCTION app.tg_block_mutation();

DROP TRIGGER IF EXISTS no_mutation ON app.goods_receipt_lines;
CREATE TRIGGER no_mutation
  BEFORE UPDATE OR DELETE ON app.goods_receipt_lines
  FOR EACH ROW EXECUTE FUNCTION app.tg_block_mutation();

-- 5. Historical product traceability: block hard DELETE on products with transactions.
CREATE OR REPLACE FUNCTION app.tg_product_no_delete_with_history() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
BEGIN
  IF EXISTS (SELECT 1 FROM app.stock_movements WHERE tenant_id = OLD.tenant_id AND product_id = OLD.id)
     OR EXISTS (SELECT 1 FROM app.sale_lines WHERE tenant_id = OLD.tenant_id AND product_id = OLD.id)
     OR EXISTS (SELECT 1 FROM app.purchase_lines WHERE tenant_id = OLD.tenant_id AND product_id = OLD.id) THEN
    RAISE EXCEPTION 'products with historical transactions cannot be deleted; deactivate instead' USING ERRCODE = 'BM004';
  END IF;
  RETURN OLD;
END $$;

DROP TRIGGER IF EXISTS b_no_delete_with_history ON app.products;
CREATE TRIGGER b_no_delete_with_history
  BEFORE DELETE ON app.products
  FOR EACH ROW EXECUTE FUNCTION app.tg_product_no_delete_with_history();
