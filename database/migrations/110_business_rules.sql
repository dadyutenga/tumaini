-- =====================================================================
-- 110_business_rules.sql — authorization helpers and the triggers that
-- enforce invariants no API bug can bypass: state machines, day lock,
-- stock ledger, money ledger, allocations, approvals, immutability.
-- Posting functions are SECURITY DEFINER (owner bms_owner) so derived
-- columns can be write-protected from bms_app; FORCE RLS still applies.
-- =====================================================================
SET search_path = app, public;

-- =====================================================================
-- 1. Authorization helpers
-- =====================================================================
CREATE FUNCTION app.has_permission(p_code text) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog, public AS $$
  WITH ctx AS (SELECT app.current_tenant_id() AS t, app.current_membership_id() AS m)
  SELECT EXISTS (SELECT 1 FROM app.memberships x, ctx
                  WHERE x.tenant_id = ctx.t AND x.id = ctx.m AND x.status = 'active')
     AND coalesce(
           (SELECT o.effect = 'allow' FROM app.membership_permission_overrides o, ctx
             WHERE o.tenant_id = ctx.t AND o.membership_id = ctx.m AND o.permission_code = p_code
               AND (o.expires_at IS NULL OR o.expires_at > now())),
           EXISTS (SELECT 1 FROM app.membership_roles mr
                     JOIN app.role_permissions rp ON rp.tenant_id = mr.tenant_id AND rp.role_id = mr.role_id
                     JOIN app.roles r ON r.tenant_id = mr.tenant_id AND r.id = mr.role_id AND r.status = 'active', ctx
                    WHERE mr.tenant_id = ctx.t AND mr.membership_id = ctx.m AND rp.permission_code = p_code))
$$;
COMMENT ON FUNCTION app.has_permission(text) IS 'Effective permission of the current membership: user override (allow/deny) wins over role grant.';

CREATE FUNCTION app.authority_limit(p_membership uuid, p_code app.limit_code) RETURNS numeric
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog, public AS $$
  SELECT coalesce(
    (SELECT limit_value FROM app.authority_limits
      WHERE tenant_id = app.current_tenant_id() AND membership_id = p_membership AND limit_code = p_code),
    (SELECT min(al.limit_value) FROM app.authority_limits al
       JOIN app.membership_roles mr ON mr.tenant_id = al.tenant_id AND mr.role_id = al.role_id
      WHERE al.tenant_id = app.current_tenant_id() AND mr.membership_id = p_membership AND al.limit_code = p_code))
$$;
COMMENT ON FUNCTION app.authority_limit(uuid, app.limit_code) IS 'NULL = no limit configured = unlimited (for holders of the related permission).';

-- Raises BM007 unless the current membership may approve `p_kind` for the amount / percent.
CREATE FUNCTION app.assert_can_approve(p_kind text, p_amount numeric DEFAULT NULL, p_pct numeric DEFAULT NULL)
RETURNS void LANGUAGE plpgsql STABLE AS $$
DECLARE
  perm text; lim_code app.limit_code; lim numeric; val numeric;
BEGIN
  SELECT x.perm, x.lim INTO perm, lim_code FROM (VALUES
    ('discount',      'shop.discount.approve',       'discount_approve_pct'::app.limit_code),
    ('refund',        'shop.return.approve',         'refund_approve_amount'),
    ('expense',       'finance.expense.approve',     'expense_approve_amount'),
    ('price_change',  'services.price.approve',      'price_change_approve_pct'),
    ('sale_void',     'shop.sale.void',              NULL),
    ('txn_correction','finance.transaction.correct', NULL),
    ('payment_void',  'finance.payment.void',        NULL),
    ('day_reopen',    'finance.day.reopen',          NULL),
    ('product_price', 'shop.product.price_approve',  NULL),
    ('leave',         'hr.leave.approve',            NULL),
    ('payroll',       'hr.payroll.approve',          NULL)
  ) AS x(kind, perm, lim) WHERE x.kind = p_kind;
  IF perm IS NULL THEN
    RAISE EXCEPTION 'unknown approval kind %', p_kind USING ERRCODE = 'BM007';
  END IF;
  IF NOT app.has_permission(perm) THEN
    RAISE EXCEPTION 'permission % required', perm USING ERRCODE = 'BM007';
  END IF;
  IF lim_code IS NOT NULL THEN
    lim := app.authority_limit(app.current_membership_id(), lim_code);
    val := CASE WHEN lim_code::text LIKE '%pct' THEN p_pct ELSE p_amount END;
    IF lim IS NOT NULL AND val IS NOT NULL AND val > lim THEN
      RAISE EXCEPTION 'value % exceeds your approval limit % (%); escalate', val, lim, lim_code
        USING ERRCODE = 'BM007';
    END IF;
  END IF;
END $$;

CREATE FUNCTION app.approved_request_exists(p_type app.approval_type, p_target uuid) RETURNS boolean
LANGUAGE sql STABLE AS $$
  SELECT EXISTS (SELECT 1 FROM app.approval_requests
                  WHERE tenant_id = app.current_tenant_id() AND approval_type = p_type AND status = 'approved'
                    AND p_target IN (sale_id, project_id, design_job_id, financial_transaction_id,
                                     payment_received_id, payment_made_id, day_closing_id, product_id))
$$;

-- =====================================================================
-- 2. Generic guards
-- =====================================================================
CREATE FUNCTION app.tg_day_lock_insert() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE d date; l uuid;
BEGIN
  EXECUTE format('SELECT ($1).%I, ($1).%I', TG_ARGV[0], TG_ARGV[1]) INTO d, l USING NEW;
  PERFORM app.assert_day_open(NEW.tenant_id, l, d);
  RETURN NEW;
END $$;

-- Payment method must match the receiving / paying account kind.
CREATE FUNCTION app.tg_method_matches_account() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE k app.account_kind;
BEGIN
  SELECT kind INTO k FROM app.financial_accounts
   WHERE tenant_id = NEW.tenant_id AND id = NEW.account_id AND status = 'active';
  IF k IS NULL THEN
    RAISE EXCEPTION 'account is not active' USING ERRCODE = 'BM008';
  END IF;
  IF NOT ((NEW.method = 'cash' AND k IN ('cash', 'petty_cash'))
       OR (NEW.method = 'mobile_money' AND k = 'mobile_money')
       OR (NEW.method IN ('bank_transfer', 'card', 'cheque') AND k = 'bank')) THEN
    RAISE EXCEPTION 'payment method % cannot use a % account', NEW.method, k USING ERRCODE = 'BM008';
  END IF;
  RETURN NEW;
END $$;

-- At least one active Admin must always remain (prevents tenant lock-out).
CREATE FUNCTION app.tg_keep_one_admin() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE t uuid := coalesce(NEW.tenant_id, OLD.tenant_id);
BEGIN
  IF NOT EXISTS (
       SELECT 1 FROM app.membership_roles mr
         JOIN app.roles r ON r.tenant_id = mr.tenant_id AND r.id = mr.role_id AND r.code = 'admin'
         JOIN app.memberships m ON m.tenant_id = mr.tenant_id AND m.id = mr.membership_id AND m.status = 'active'
        WHERE mr.tenant_id = t) THEN
    RAISE EXCEPTION 'tenant must keep at least one active Admin' USING ERRCODE = 'BM007';
  END IF;
  RETURN NULL;
END $$;

-- =====================================================================
-- 3. Stock ledger
-- =====================================================================
CREATE FUNCTION app.tg_stock_movement_apply() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE
  new_qty numeric; allow_neg boolean; total_before numeric; avg_before numeric;
BEGIN
  PERFORM app.assert_day_open(NEW.tenant_id, NEW.location_id, NEW.business_date);

  INSERT INTO app.stock_balances AS b (tenant_id, location_id, product_id, quantity_on_hand)
  VALUES (NEW.tenant_id, NEW.location_id, NEW.product_id, 0)
  ON CONFLICT (tenant_id, location_id, product_id) DO NOTHING;

  -- Moving weighted average cost on receipts (across all locations).
  IF NEW.movement_type = 'purchase_receipt' THEN
    SELECT coalesce(sum(quantity_on_hand), 0) INTO total_before
      FROM app.stock_balances WHERE tenant_id = NEW.tenant_id AND product_id = NEW.product_id;
    SELECT average_cost INTO avg_before FROM app.products
     WHERE tenant_id = NEW.tenant_id AND id = NEW.product_id FOR UPDATE;
    UPDATE app.products
       SET average_cost = CASE WHEN greatest(total_before, 0) + NEW.quantity_delta = 0 THEN NEW.unit_cost
                               ELSE round((greatest(total_before, 0) * avg_before + NEW.quantity_delta * NEW.unit_cost)
                                          / (greatest(total_before, 0) + NEW.quantity_delta), 4) END,
           buying_price = NEW.unit_cost
     WHERE tenant_id = NEW.tenant_id AND id = NEW.product_id;
  END IF;

  -- Row lock on the balance serialises concurrent movements of one product/location.
  UPDATE app.stock_balances
     SET quantity_on_hand = quantity_on_hand + NEW.quantity_delta, updated_at = now()
   WHERE tenant_id = NEW.tenant_id AND location_id = NEW.location_id AND product_id = NEW.product_id
  RETURNING quantity_on_hand INTO new_qty;

  IF new_qty < 0 THEN
    SELECT coalesce(p.allow_negative_stock, s.allow_negative_stock) INTO allow_neg
      FROM app.products p JOIN app.tenant_settings s ON s.tenant_id = p.tenant_id
     WHERE p.tenant_id = NEW.tenant_id AND p.id = NEW.product_id;
    IF NOT coalesce(allow_neg, false) THEN
      RAISE EXCEPTION 'insufficient stock for product % at location % (would be %)',
        NEW.product_id, NEW.location_id, new_qty USING ERRCODE = 'BM003';
    END IF;
  END IF;
  RETURN NULL;
END $$;

-- =====================================================================
-- 4. Money ledger
-- =====================================================================
CREATE FUNCTION app.tg_fin_txn_apply() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
BEGIN
  PERFORM app.assert_day_open(NEW.tenant_id, NEW.location_id, NEW.business_date);
  UPDATE app.financial_accounts
     SET current_balance = current_balance + CASE NEW.direction WHEN 'in' THEN NEW.amount ELSE -NEW.amount END
   WHERE tenant_id = NEW.tenant_id AND id = NEW.account_id;
  RETURN NULL;
END $$;

-- System-posted transaction types may only be written by posting triggers
-- (which run as bms_owner). The API may insert only manual types.
CREATE FUNCTION app.tg_fin_txn_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF current_user <> 'bms_owner'
     AND NEW.txn_type NOT IN ('opening_balance', 'correction', 'other_payment') THEN
    RAISE EXCEPTION 'transaction type % is posted automatically from its source document', NEW.txn_type
      USING ERRCODE = 'BM004';
  END IF;
  IF NEW.txn_type = 'correction' AND NOT app.approved_request_exists('transaction_correction', NEW.reverses_transaction_id) THEN
    RAISE EXCEPTION 'correction requires an approved transaction_correction request' USING ERRCODE = 'BM007';
  END IF;
  IF NEW.txn_type = 'opening_balance' AND NOT app.has_permission('finance.account.manage') THEN
    RAISE EXCEPTION 'permission finance.account.manage required' USING ERRCODE = 'BM007';
  END IF;
  RETURN NEW;
END $$;

-- Reverse every ledger row produced by a source document.
CREATE FUNCTION app.reverse_ledger(p_tenant uuid, p_where text, p_id uuid, p_date date, p_reason text)
RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
BEGIN
  EXECUTE format($q$
    INSERT INTO app.financial_transactions (tenant_id, account_id, direction, amount, txn_type, business_unit_id,
           location_id, business_date, description, project_id, design_job_id, reverses_transaction_id)
    SELECT t.tenant_id, t.account_id, CASE t.direction WHEN 'in' THEN 'out'::app.txn_direction ELSE 'in' END,
           t.amount, 'reversal', t.business_unit_id, t.location_id, $3, left('Reversal: ' || $4, 500),
           t.project_id, t.design_job_id, t.id
      FROM app.financial_transactions t
     WHERE t.tenant_id = $1 AND t.%I = $2 AND t.txn_type <> 'reversal'
       AND NOT EXISTS (SELECT 1 FROM app.financial_transactions r
                        WHERE r.tenant_id = t.tenant_id AND r.reverses_transaction_id = t.id)$q$, p_where)
  USING p_tenant, p_id, p_date, p_reason;
END $$;

-- =====================================================================
-- 4b. Commission accrual (definer; staff cannot write commission rows)
-- =====================================================================
CREATE FUNCTION app.accrue_sale_commissions(p_tenant uuid, p_sale uuid) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
BEGIN
  INSERT INTO app.commission_entries (tenant_id, employee_id, commission_rule_id, sale_id, amount, earned_on)
  SELECT s.tenant_id, e.id, r.id, s.id, round(sum(sl.line_total) * r.percent / 100, 2), s.business_date
    FROM app.sales s
    JOIN app.employees e ON e.tenant_id = s.tenant_id AND e.membership_id = s.sold_by AND e.status = 'active'
    JOIN app.commission_rules r ON r.tenant_id = s.tenant_id AND r.active AND r.basis = 'sale_percent'
         AND s.business_date >= r.effective_from AND (r.effective_to IS NULL OR s.business_date < r.effective_to)
         AND (r.employee_id IS NULL OR r.employee_id = e.id)
    JOIN app.sale_lines sl ON sl.tenant_id = s.tenant_id AND sl.sale_id = s.id
    JOIN app.products p ON p.tenant_id = sl.tenant_id AND p.id = sl.product_id
   WHERE s.tenant_id = p_tenant AND s.id = p_sale
     AND (r.product_category_id IS NULL OR r.product_category_id = p.category_id)
   GROUP BY s.tenant_id, e.id, r.id, s.id, r.percent, s.business_date
  HAVING round(sum(sl.line_total) * r.percent / 100, 2) > 0
  ON CONFLICT DO NOTHING;
END $$;

CREATE FUNCTION app.reverse_return_commissions(p_tenant uuid, p_return uuid) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
BEGIN
  INSERT INTO app.commission_entries (tenant_id, employee_id, commission_rule_id, sale_id, sale_return_id, amount, earned_on)
  SELECT c.tenant_id, c.employee_id, c.commission_rule_id, c.sale_id, r.id,
         -round(c.amount * rv.returned_value / nullif(s.subtotal, 0), 2), r.business_date
    FROM app.sale_returns r
    JOIN app.sales s ON s.tenant_id = r.tenant_id AND s.id = r.sale_id
    JOIN app.commission_entries c ON c.tenant_id = s.tenant_id AND c.sale_id = s.id
         AND c.amount > 0 AND c.status IN ('accrued', 'included')
    CROSS JOIN LATERAL (SELECT coalesce(sum(rl.quantity * sl.line_total / sl.quantity), 0) AS returned_value
                          FROM app.sale_return_lines rl
                          JOIN app.sale_lines sl ON sl.tenant_id = rl.tenant_id AND sl.id = rl.sale_line_id
                         WHERE rl.tenant_id = r.tenant_id AND rl.sale_return_id = r.id) rv
   WHERE r.tenant_id = p_tenant AND r.id = p_return
     AND round(c.amount * rv.returned_value / nullif(s.subtotal, 0), 2) > 0;
END $$;

CREATE FUNCTION app.tg_project_commission() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
BEGIN
  IF NEW.status = 'completed' AND OLD.status <> 'completed' THEN
    INSERT INTO app.commission_entries (tenant_id, employee_id, commission_rule_id, project_id, amount, earned_on)
    SELECT NEW.tenant_id, e.id, r.id, NEW.id, r.fixed_amount, current_date
      FROM app.employees e
      JOIN app.commission_rules r ON r.tenant_id = e.tenant_id AND r.active AND r.basis = 'project_fixed'
           AND current_date >= r.effective_from AND (r.effective_to IS NULL OR current_date < r.effective_to)
           AND (r.employee_id IS NULL OR r.employee_id = e.id)
           AND (r.service_id IS NULL OR r.service_id = NEW.service_id)
     WHERE e.tenant_id = NEW.tenant_id AND e.membership_id = NEW.owner_id AND e.status = 'active'
    ON CONFLICT DO NOTHING;
  END IF;
  RETURN NULL;
END $$;

-- =====================================================================
-- 5. Sales
-- =====================================================================
CREATE FUNCTION app.tg_sales_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE pct numeric; lim numeric;
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
    -- Discount authority: seller's own limit, or an approved sale_discount request.
    IF NEW.discount_amount > 0 THEN
      pct := round(NEW.discount_amount * 100 / nullif(NEW.subtotal, 0), 2);
      lim := app.authority_limit(NEW.sold_by, 'sale_discount_pct');
      IF lim IS NOT NULL AND pct > lim AND NOT EXISTS (
           SELECT 1 FROM app.approval_requests a
            WHERE a.tenant_id = NEW.tenant_id AND a.sale_id = NEW.id AND a.approval_type = 'sale_discount'
              AND a.status = 'approved' AND a.requested_percent >= pct) THEN
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

CREATE FUNCTION app.tg_sales_post() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE expected integer; marked integer;
BEGIN
  IF NEW.status = 'completed' AND OLD.status <> 'completed' THEN
    UPDATE app.sale_lines sl SET unit_cost = p.average_cost
      FROM app.products p
     WHERE sl.tenant_id = NEW.tenant_id AND sl.sale_id = NEW.id
       AND p.tenant_id = sl.tenant_id AND p.id = sl.product_id;

    INSERT INTO app.stock_movements (tenant_id, location_id, product_id, movement_type, quantity_delta,
                                     unit_cost, business_date, sale_line_id)
    SELECT sl.tenant_id, NEW.location_id, sl.product_id, 'sale', -sl.quantity, sl.unit_cost, NEW.business_date, sl.id
      FROM app.sale_lines sl WHERE sl.tenant_id = NEW.tenant_id AND sl.sale_id = NEW.id;

    SELECT count(*) INTO expected FROM app.sale_line_units slu
      JOIN app.sale_lines sl ON sl.tenant_id = slu.tenant_id AND sl.id = slu.sale_line_id
     WHERE sl.tenant_id = NEW.tenant_id AND sl.sale_id = NEW.id;
    UPDATE app.product_units u
       SET status = 'sold', sold_at = now(),
           warranty_expires_on = CASE WHEN sl.warranty_months > 0
                                      THEN (current_date + make_interval(months => sl.warranty_months))::date END
      FROM app.sale_line_units slu
      JOIN app.sale_lines sl ON sl.tenant_id = slu.tenant_id AND sl.id = slu.sale_line_id
     WHERE sl.tenant_id = NEW.tenant_id AND sl.sale_id = NEW.id
       AND u.tenant_id = slu.tenant_id AND u.id = slu.product_unit_id
       AND u.status = 'in_stock' AND u.location_id = NEW.location_id;
    GET DIAGNOSTICS marked = ROW_COUNT;
    IF marked <> expected THEN
      RAISE EXCEPTION 'a serial number on this sale is not in stock at this location' USING ERRCODE = 'BM003';
    END IF;
    PERFORM app.accrue_sale_commissions(NEW.tenant_id, NEW.id);

  ELSIF NEW.status = 'voided' AND OLD.status = 'completed' THEN
    INSERT INTO app.stock_movements (tenant_id, location_id, product_id, movement_type, quantity_delta,
                                     unit_cost, business_date, sale_line_id)
    SELECT sl.tenant_id, NEW.location_id, sl.product_id, 'sale_void', sl.quantity, sl.unit_cost, NEW.business_date, sl.id
      FROM app.sale_lines sl WHERE sl.tenant_id = NEW.tenant_id AND sl.sale_id = NEW.id;
    UPDATE app.product_units u SET status = 'in_stock', sold_at = NULL, warranty_expires_on = NULL
      FROM app.sale_line_units slu JOIN app.sale_lines sl ON sl.tenant_id = slu.tenant_id AND sl.id = slu.sale_line_id
     WHERE sl.tenant_id = NEW.tenant_id AND sl.sale_id = NEW.id
       AND u.tenant_id = slu.tenant_id AND u.id = slu.product_unit_id;
    UPDATE app.commission_entries SET status = 'reversed'
     WHERE tenant_id = NEW.tenant_id AND sale_id = NEW.id AND status = 'accrued';
  END IF;
  RETURN NULL;
END $$;

-- Deferred integrity check at COMMIT: totals and serial counts must match lines.
CREATE FUNCTION app.tg_sales_check_totals() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE s app.sales%ROWTYPE; lines_total numeric; n_lines integer; bad_serial integer;
BEGIN
  SELECT * INTO s FROM app.sales WHERE tenant_id = NEW.tenant_id AND id = NEW.id;
  IF NOT FOUND OR s.status <> 'completed' THEN RETURN NULL; END IF;
  SELECT coalesce(sum(line_total), 0), count(*) INTO lines_total, n_lines
    FROM app.sale_lines WHERE tenant_id = s.tenant_id AND sale_id = s.id;
  IF n_lines = 0 THEN
    RAISE EXCEPTION 'completed sale % has no lines', s.receipt_no USING ERRCODE = 'BM008';
  END IF;
  IF lines_total <> s.subtotal THEN
    RAISE EXCEPTION 'sale % subtotal % <> sum of lines %', s.receipt_no, s.subtotal, lines_total USING ERRCODE = 'BM008';
  END IF;
  SELECT count(*) INTO bad_serial FROM app.sale_lines sl
    JOIN app.products p ON p.tenant_id = sl.tenant_id AND p.id = sl.product_id AND p.tracks_serial
   WHERE sl.tenant_id = s.tenant_id AND sl.sale_id = s.id
     AND sl.quantity <> (SELECT count(*) FROM app.sale_line_units u WHERE u.tenant_id = sl.tenant_id AND u.sale_line_id = sl.id);
  IF bad_serial > 0 THEN
    RAISE EXCEPTION 'serial/IMEI required for each unit of serialised products' USING ERRCODE = 'BM008';
  END IF;
  RETURN NULL;
END $$;

-- Lines of a finalised sale cannot change (except trigger-maintained columns).
CREATE FUNCTION app.tg_sale_lines_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE st app.sale_status;
BEGIN
  SELECT status INTO st FROM app.sales
   WHERE tenant_id = coalesce(NEW.tenant_id, OLD.tenant_id) AND id = coalesce(NEW.sale_id, OLD.sale_id);
  IF st IN ('completed', 'voided') THEN
    IF TG_OP <> 'UPDATE' OR current_user <> 'bms_owner'
       OR (NEW.product_id, NEW.quantity, NEW.unit_price, NEW.line_discount, NEW.line_total, NEW.sale_id)
          IS DISTINCT FROM (OLD.product_id, OLD.quantity, OLD.unit_price, OLD.line_discount, OLD.line_total, OLD.sale_id) THEN
      RAISE EXCEPTION 'lines of a completed sale are read-only' USING ERRCODE = 'BM004';
    END IF;
  END IF;
  RETURN coalesce(NEW, OLD);
END $$;

CREATE FUNCTION app.tg_sale_line_units_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE st app.sale_status;
BEGIN
  SELECT s.status INTO st FROM app.sale_lines sl JOIN app.sales s ON s.tenant_id = sl.tenant_id AND s.id = sl.sale_id
   WHERE sl.tenant_id = coalesce(NEW.tenant_id, OLD.tenant_id) AND sl.id = coalesce(NEW.sale_line_id, OLD.sale_line_id);
  IF st IN ('completed', 'voided') THEN
    RAISE EXCEPTION 'serials of a completed sale are read-only' USING ERRCODE = 'BM004';
  END IF;
  RETURN coalesce(NEW, OLD);
END $$;

-- =====================================================================
-- 6. Returns
-- =====================================================================
CREATE FUNCTION app.tg_returns_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE sale_st app.sale_status; lines_refund numeric;
BEGIN
  IF TG_OP = 'INSERT' THEN
    SELECT status INTO sale_st FROM app.sales WHERE tenant_id = NEW.tenant_id AND id = NEW.sale_id;
    IF sale_st <> 'completed' THEN
      RAISE EXCEPTION 'only completed sales can be returned' USING ERRCODE = 'BM001';
    END IF;
    PERFORM app.assert_day_open(NEW.tenant_id, NEW.location_id, NEW.business_date);
    RETURN NEW;
  END IF;
  IF NEW.status IN ('approved', 'rejected') AND OLD.status = 'requested' THEN
    NEW.decided_by := coalesce(NEW.decided_by, app.current_membership_id());
    NEW.decided_at := coalesce(NEW.decided_at, now());
    IF NEW.status = 'approved' THEN
      PERFORM app.assert_can_approve('refund', NEW.refund_amount);
    END IF;
  END IF;
  IF NEW.status = 'completed' AND OLD.status <> 'completed' THEN
    NEW.completed_at := coalesce(NEW.completed_at, now());
    SELECT coalesce(sum(refund_amount), 0) INTO lines_refund
      FROM app.sale_return_lines WHERE tenant_id = NEW.tenant_id AND sale_return_id = NEW.id;
    IF lines_refund <> NEW.refund_amount THEN
      RAISE EXCEPTION 'refund amount % <> sum of lines %', NEW.refund_amount, lines_refund USING ERRCODE = 'BM008';
    END IF;
    PERFORM app.assert_day_open(NEW.tenant_id, NEW.location_id, NEW.business_date);
  END IF;
  RETURN NEW;
END $$;

CREATE FUNCTION app.tg_returns_post() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE refund_cap numeric;
BEGIN
  IF NEW.status = 'completed' AND OLD.status <> 'completed' THEN
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

-- =====================================================================
-- 7. Purchasing & inventory documents
-- =====================================================================
CREATE FUNCTION app.tg_goods_receipt_line_post() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE gr app.goods_receipts%ROWTYPE; pl app.purchase_lines%ROWTYPE; p_status app.purchase_status;
        open_qty numeric;
BEGIN
  SELECT * INTO gr FROM app.goods_receipts WHERE tenant_id = NEW.tenant_id AND id = NEW.goods_receipt_id;
  SELECT * INTO pl FROM app.purchase_lines WHERE tenant_id = NEW.tenant_id AND id = NEW.purchase_line_id FOR UPDATE;
  IF pl.purchase_id <> gr.purchase_id THEN
    RAISE EXCEPTION 'purchase line belongs to another purchase' USING ERRCODE = 'BM008';
  END IF;
  SELECT status INTO p_status FROM app.purchases WHERE tenant_id = NEW.tenant_id AND id = gr.purchase_id;
  IF p_status NOT IN ('ordered', 'partially_received') THEN
    RAISE EXCEPTION 'purchase is % and cannot receive goods', p_status USING ERRCODE = 'BM001';
  END IF;
  UPDATE app.purchase_lines SET quantity_received = quantity_received + NEW.quantity
   WHERE tenant_id = NEW.tenant_id AND id = NEW.purchase_line_id;   -- CHECK caps at ordered
  INSERT INTO app.stock_movements (tenant_id, location_id, product_id, movement_type, quantity_delta,
                                   unit_cost, business_date, goods_receipt_line_id)
  VALUES (NEW.tenant_id, gr.location_id, pl.product_id, 'purchase_receipt', NEW.quantity, NEW.unit_cost,
          gr.business_date, NEW.id);
  SELECT coalesce(sum(quantity_ordered - quantity_received), 0) INTO open_qty
    FROM app.purchase_lines WHERE tenant_id = NEW.tenant_id AND purchase_id = gr.purchase_id;
  UPDATE app.purchases SET status = CASE WHEN open_qty = 0 THEN 'received'::app.purchase_status ELSE 'partially_received' END
   WHERE tenant_id = NEW.tenant_id AND id = gr.purchase_id
     AND status IS DISTINCT FROM CASE WHEN open_qty = 0 THEN 'received'::app.purchase_status ELSE 'partially_received' END;
  RETURN NULL;
END $$;

CREATE FUNCTION app.tg_adjustment_post() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
BEGIN
  IF NEW.status = 'posted' AND OLD.status <> 'posted' THEN
    INSERT INTO app.stock_movements (tenant_id, location_id, product_id, movement_type, quantity_delta,
                                     unit_cost, business_date, stock_adjustment_line_id)
    SELECT l.tenant_id, NEW.location_id, l.product_id, 'adjustment', l.quantity_delta,
           coalesce(l.unit_cost, p.average_cost), NEW.business_date, l.id
      FROM app.stock_adjustment_lines l JOIN app.products p ON p.tenant_id = l.tenant_id AND p.id = l.product_id
     WHERE l.tenant_id = NEW.tenant_id AND l.stock_adjustment_id = NEW.id;
    IF NOT FOUND THEN
      RAISE EXCEPTION 'adjustment has no lines' USING ERRCODE = 'BM008';
    END IF;
  END IF;
  RETURN NULL;
END $$;

CREATE FUNCTION app.tg_transfer_post() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
BEGIN
  IF NEW.status = 'dispatched' AND OLD.status = 'draft' THEN
    INSERT INTO app.stock_movements (tenant_id, location_id, product_id, movement_type, quantity_delta,
                                     unit_cost, business_date, stock_transfer_line_id)
    SELECT l.tenant_id, NEW.from_location_id, l.product_id, 'transfer_out', -l.quantity, p.average_cost,
           (NEW.dispatched_at AT TIME ZONE (SELECT timezone FROM app.tenants WHERE id = NEW.tenant_id))::date, l.id
      FROM app.stock_transfer_lines l JOIN app.products p ON p.tenant_id = l.tenant_id AND p.id = l.product_id
     WHERE l.tenant_id = NEW.tenant_id AND l.stock_transfer_id = NEW.id;
  ELSIF NEW.status = 'received' AND OLD.status = 'dispatched' THEN
    INSERT INTO app.stock_movements (tenant_id, location_id, product_id, movement_type, quantity_delta,
                                     unit_cost, business_date, stock_transfer_line_id)
    SELECT l.tenant_id, NEW.to_location_id, l.product_id, 'transfer_in', l.quantity, p.average_cost,
           (NEW.received_at AT TIME ZONE (SELECT timezone FROM app.tenants WHERE id = NEW.tenant_id))::date, l.id
      FROM app.stock_transfer_lines l JOIN app.products p ON p.tenant_id = l.tenant_id AND p.id = l.product_id
     WHERE l.tenant_id = NEW.tenant_id AND l.stock_transfer_id = NEW.id;
  END IF;
  RETURN NULL;
END $$;

-- Selling price change: needs permission (Admin) or an approved request; history row written automatically.
CREATE FUNCTION app.tg_product_price_guard() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE req uuid;
BEGIN
  IF NEW.selling_price IS DISTINCT FROM OLD.selling_price THEN
    SELECT id INTO req FROM app.approval_requests
     WHERE tenant_id = NEW.tenant_id AND product_id = NEW.id AND approval_type = 'product_price_change'
       AND status = 'approved' AND requested_amount = NEW.selling_price
     ORDER BY decided_at DESC LIMIT 1;
    IF req IS NULL AND NOT app.has_permission('shop.product.price_change') THEN
      RAISE EXCEPTION 'changing a selling price requires approval' USING ERRCODE = 'BM007';
    END IF;
    INSERT INTO app.product_price_changes (tenant_id, product_id, old_selling_price, new_selling_price,
                                           old_buying_price, new_buying_price, reason, approval_request_id)
    VALUES (NEW.tenant_id, NEW.id, OLD.selling_price, NEW.selling_price, OLD.buying_price, NEW.buying_price,
            coalesce(nullif(current_setting('app.change_note', true), ''), 'price update'), req);
  END IF;
  RETURN NEW;
END $$;

-- =====================================================================
-- 8. Payments received & allocations
-- =====================================================================
CREATE FUNCTION app.tg_payment_received_post() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
BEGIN
  IF TG_OP = 'INSERT' THEN
    INSERT INTO app.financial_transactions (tenant_id, account_id, direction, amount, txn_type, business_unit_id,
           location_id, business_date, description, payment_received_id)
    VALUES (NEW.tenant_id, NEW.account_id, 'in', NEW.amount, 'customer_receipt', NEW.business_unit_id,
            NEW.location_id, NEW.business_date, 'Receipt ' || NEW.receipt_no, NEW.id);
  ELSIF NEW.status = 'voided' AND OLD.status = 'posted' THEN
    IF NOT (app.has_permission('finance.payment.void') OR app.approved_request_exists('payment_void', NEW.id)) THEN
      RAISE EXCEPTION 'voiding a payment requires Admin approval' USING ERRCODE = 'BM007';
    END IF;
    PERFORM app.assert_day_open(NEW.tenant_id, NEW.location_id, NEW.business_date);
    -- undo allocation effects
    UPDATE app.sales s SET amount_paid = s.amount_paid - a.amount
      FROM app.payment_allocations a
     WHERE a.tenant_id = NEW.tenant_id AND a.payment_received_id = NEW.id
       AND s.tenant_id = a.tenant_id AND s.id = a.sale_id;
    UPDATE app.invoices i SET amount_paid = i.amount_paid - a.amount,
           status = CASE WHEN i.amount_paid - a.amount = 0 THEN 'issued'::app.invoice_status ELSE 'partially_paid' END
      FROM app.payment_allocations a
     WHERE a.tenant_id = NEW.tenant_id AND a.payment_received_id = NEW.id
       AND i.tenant_id = a.tenant_id AND i.id = a.invoice_id;
    PERFORM app.reverse_ledger(NEW.tenant_id, 'payment_received_id', NEW.id, NEW.business_date,
                               'payment ' || NEW.receipt_no || ' voided');
  ELSIF (NEW.amount, NEW.account_id, NEW.method, NEW.customer_id, NEW.business_date, NEW.location_id)
        IS DISTINCT FROM (OLD.amount, OLD.account_id, OLD.method, OLD.customer_id, OLD.business_date, OLD.location_id) THEN
    RAISE EXCEPTION 'posted payments are immutable; void and re-enter' USING ERRCODE = 'BM004';
  END IF;
  RETURN NULL;
END $$;

CREATE FUNCTION app.tg_payment_allocation_apply() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE pay app.payments_received%ROWTYPE; allocated numeric; tgt_customer uuid; tgt_status text;
BEGIN
  SELECT * INTO pay FROM app.payments_received WHERE tenant_id = NEW.tenant_id AND id = NEW.payment_received_id FOR UPDATE;
  IF pay.status <> 'posted' THEN
    RAISE EXCEPTION 'cannot allocate a voided payment' USING ERRCODE = 'BM005';
  END IF;
  SELECT coalesce(sum(amount), 0) INTO allocated FROM app.payment_allocations
   WHERE tenant_id = NEW.tenant_id AND payment_received_id = NEW.payment_received_id;
  IF allocated > pay.amount THEN
    RAISE EXCEPTION 'allocations % exceed payment amount %', allocated, pay.amount USING ERRCODE = 'BM005';
  END IF;

  IF NEW.sale_id IS NOT NULL THEN
    SELECT customer_id, status::text INTO tgt_customer, tgt_status FROM app.sales
     WHERE tenant_id = NEW.tenant_id AND id = NEW.sale_id FOR UPDATE;
    IF tgt_status NOT IN ('draft', 'held', 'pending_approval', 'completed') THEN
      RAISE EXCEPTION 'sale is % and cannot take payments', tgt_status USING ERRCODE = 'BM001';
    END IF;
    UPDATE app.sales SET amount_paid = amount_paid + NEW.amount
     WHERE tenant_id = NEW.tenant_id AND id = NEW.sale_id;      -- CHECK amount_paid <= total
  ELSE
    SELECT customer_id, status::text INTO tgt_customer, tgt_status FROM app.invoices
     WHERE tenant_id = NEW.tenant_id AND id = NEW.invoice_id FOR UPDATE;
    IF tgt_status NOT IN ('issued', 'partially_paid') THEN
      RAISE EXCEPTION 'invoice is % and cannot take payments', tgt_status USING ERRCODE = 'BM001';
    END IF;
    UPDATE app.invoices
       SET amount_paid = amount_paid + NEW.amount,
           status = CASE WHEN amount_paid + NEW.amount = total THEN 'paid'::app.invoice_status ELSE 'partially_paid' END
     WHERE tenant_id = NEW.tenant_id AND id = NEW.invoice_id;
  END IF;

  IF tgt_customer IS DISTINCT FROM pay.customer_id AND pay.customer_id IS NOT NULL THEN
    RAISE EXCEPTION 'payment customer does not match the document customer' USING ERRCODE = 'BM008';
  END IF;
  IF pay.customer_id IS NULL AND NEW.invoice_id IS NOT NULL THEN
    RAISE EXCEPTION 'invoice payments must name the customer' USING ERRCODE = 'BM008';
  END IF;
  RETURN NULL;
END $$;

-- =====================================================================
-- 9. Payments made & allocations
-- =====================================================================
CREATE FUNCTION app.tg_payment_made_post() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE tt app.txn_type;
BEGIN
  IF TG_OP = 'INSERT' THEN
    tt := CASE WHEN NEW.supplier_id IS NOT NULL THEN 'supplier_payment'
               WHEN NEW.contractor_id IS NOT NULL THEN 'contractor_payment'
               WHEN NEW.customer_id IS NOT NULL THEN 'refund'
               ELSE 'other_payment' END;
    INSERT INTO app.financial_transactions (tenant_id, account_id, direction, amount, txn_type, business_unit_id,
           location_id, business_date, description, payment_made_id)
    VALUES (NEW.tenant_id, NEW.account_id, 'out', NEW.amount, tt, NEW.business_unit_id,
            NEW.location_id, NEW.business_date, 'Payment ' || NEW.payment_no, NEW.id);
  ELSIF NEW.status = 'voided' AND OLD.status = 'posted' THEN
    IF NOT (app.has_permission('finance.payment.void') OR app.approved_request_exists('payment_void', NEW.id)) THEN
      RAISE EXCEPTION 'voiding a payment requires Admin approval' USING ERRCODE = 'BM007';
    END IF;
    PERFORM app.assert_day_open(NEW.tenant_id, NEW.location_id, NEW.business_date);
    UPDATE app.purchases p SET amount_paid = p.amount_paid - a.amount
      FROM app.payment_made_allocations a
     WHERE a.tenant_id = NEW.tenant_id AND a.payment_made_id = NEW.id AND p.tenant_id = a.tenant_id AND p.id = a.purchase_id;
    UPDATE app.contractor_bills b SET amount_paid = b.amount_paid - a.amount,
           status = CASE WHEN b.amount_paid - a.amount = 0 THEN 'open'::app.bill_status ELSE 'partially_paid' END
      FROM app.payment_made_allocations a
     WHERE a.tenant_id = NEW.tenant_id AND a.payment_made_id = NEW.id AND b.tenant_id = a.tenant_id AND b.id = a.contractor_bill_id;
    PERFORM app.reverse_ledger(NEW.tenant_id, 'payment_made_id', NEW.id, NEW.business_date,
                               'payment ' || NEW.payment_no || ' voided');
  ELSIF (NEW.amount, NEW.account_id, NEW.method, NEW.business_date, NEW.location_id)
        IS DISTINCT FROM (OLD.amount, OLD.account_id, OLD.method, OLD.business_date, OLD.location_id) THEN
    RAISE EXCEPTION 'posted payments are immutable; void and re-enter' USING ERRCODE = 'BM004';
  END IF;
  RETURN NULL;
END $$;

CREATE FUNCTION app.tg_payment_made_allocation_apply() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE pay app.payments_made%ROWTYPE; allocated numeric; refunded numeric; cap numeric; payee_ok boolean;
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
    SELECT supplier_id = pay.supplier_id INTO payee_ok FROM app.purchases WHERE tenant_id = NEW.tenant_id AND id = NEW.purchase_id;
    UPDATE app.purchases SET amount_paid = amount_paid + NEW.amount
     WHERE tenant_id = NEW.tenant_id AND id = NEW.purchase_id AND status <> 'cancelled';
  ELSIF NEW.contractor_bill_id IS NOT NULL THEN
    SELECT contractor_id = pay.contractor_id INTO payee_ok FROM app.contractor_bills WHERE tenant_id = NEW.tenant_id AND id = NEW.contractor_bill_id;
    UPDATE app.contractor_bills
       SET amount_paid = amount_paid + NEW.amount,
           status = CASE WHEN amount_paid + NEW.amount = amount THEN 'paid'::app.bill_status ELSE 'partially_paid' END
     WHERE tenant_id = NEW.tenant_id AND id = NEW.contractor_bill_id AND status IN ('open', 'partially_paid');
  ELSE
    SELECT r.status = 'completed' AND s.customer_id IS NOT DISTINCT FROM pay.customer_id, r.refund_amount
      INTO payee_ok, cap
      FROM app.sale_returns r JOIN app.sales s ON s.tenant_id = r.tenant_id AND s.id = r.sale_id
     WHERE r.tenant_id = NEW.tenant_id AND r.id = NEW.sale_return_id;
    SELECT coalesce(sum(a.amount), 0) INTO refunded FROM app.payment_made_allocations a
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

-- =====================================================================
-- 10. Expenses, transfers, payroll
-- =====================================================================
CREATE FUNCTION app.tg_expense_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.status IN ('paid', 'rejected', 'cancelled')
     AND (NEW.amount, NEW.category_id, NEW.account_id, NEW.business_unit_id, NEW.project_id, NEW.expense_date)
         IS DISTINCT FROM (OLD.amount, OLD.category_id, OLD.account_id, OLD.business_unit_id, OLD.project_id, OLD.expense_date) THEN
    RAISE EXCEPTION 'expense % is final', OLD.expense_no USING ERRCODE = 'BM004';
  END IF;
  IF OLD.status = 'approved' AND NEW.amount <> OLD.amount THEN
    RAISE EXCEPTION 'approved amount cannot change; reject and resubmit' USING ERRCODE = 'BM004';
  END IF;
  IF NEW.status IN ('approved', 'rejected') AND OLD.status = 'submitted' THEN
    NEW.decided_by := coalesce(NEW.decided_by, app.current_membership_id());
    NEW.decided_at := coalesce(NEW.decided_at, now());
    IF NEW.status = 'approved' THEN PERFORM app.assert_can_approve('expense', NEW.amount); END IF;
  END IF;
  IF NEW.status = 'paid' AND OLD.status = 'approved' THEN
    NEW.paid_at := coalesce(NEW.paid_at, now());
  END IF;
  RETURN NEW;
END $$;

CREATE FUNCTION app.tg_expense_post() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
BEGIN
  IF NEW.status = 'paid' AND OLD.status <> 'paid' THEN
    INSERT INTO app.financial_transactions (tenant_id, account_id, direction, amount, txn_type, business_unit_id,
           location_id, business_date, description, project_id, design_job_id, expense_id)
    VALUES (NEW.tenant_id, NEW.account_id, 'out', NEW.amount, 'expense', NEW.business_unit_id, NEW.location_id,
            NEW.paid_business_date, left('Expense ' || NEW.expense_no || ': ' || NEW.description, 500),
            NEW.project_id, NEW.design_job_id, NEW.id);
  END IF;
  RETURN NULL;
END $$;

CREATE FUNCTION app.tg_account_transfer_post() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE bu uuid;
BEGIN
  SELECT business_unit_id INTO bu FROM app.locations WHERE tenant_id = NEW.tenant_id AND id = NEW.location_id;
  INSERT INTO app.financial_transactions (tenant_id, account_id, direction, amount, txn_type, business_unit_id,
         location_id, business_date, description, account_transfer_id)
  VALUES (NEW.tenant_id, NEW.from_account_id, 'out', NEW.amount, 'transfer', bu, NEW.location_id, NEW.business_date,
          'Transfer ' || NEW.transfer_no, NEW.id),
         (NEW.tenant_id, NEW.to_account_id,   'in',  NEW.amount, 'transfer', bu, NEW.location_id, NEW.business_date,
          'Transfer ' || NEW.transfer_no, NEW.id);
  RETURN NULL;
END $$;

CREATE FUNCTION app.tg_payroll_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE sod boolean;
BEGIN
  IF NEW.status = 'approved' AND OLD.status = 'draft' THEN
    PERFORM app.assert_can_approve('payroll');
    NEW.approved_by := coalesce(NEW.approved_by, app.current_membership_id());
    NEW.approved_at := coalesce(NEW.approved_at, now());
    SELECT require_sod_payroll INTO sod FROM app.tenant_settings WHERE tenant_id = NEW.tenant_id;
    IF sod AND NEW.approved_by = NEW.generated_by THEN
      RAISE EXCEPTION 'payroll must be approved by someone other than who generated it' USING ERRCODE = 'BM007';
    END IF;
  END IF;
  IF NEW.status = 'paid' AND OLD.status = 'approved' THEN
    PERFORM app.assert_can_approve('payroll');
    NEW.paid_by := coalesce(NEW.paid_by, app.current_membership_id());
    NEW.paid_at := coalesce(NEW.paid_at, now());
  END IF;
  RETURN NEW;
END $$;

CREATE FUNCTION app.tg_payroll_post() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
BEGIN
  IF NEW.status = 'paid' AND OLD.status = 'approved' THEN
    INSERT INTO app.financial_transactions (tenant_id, account_id, direction, amount, txn_type, business_unit_id,
           location_id, business_date, description, payroll_line_id)
    SELECT l.tenant_id, NEW.account_id, 'out', l.net_pay, 'salary', NEW.business_unit_id, NEW.location_id,
           NEW.paid_business_date, format('Salary %s-%s', NEW.period_year, lpad(NEW.period_month::text, 2, '0')), l.id
      FROM app.payroll_lines l
     WHERE l.tenant_id = NEW.tenant_id AND l.payroll_run_id = NEW.id AND l.net_pay > 0;
    UPDATE app.payroll_lines SET paid_at = NEW.paid_at WHERE tenant_id = NEW.tenant_id AND payroll_run_id = NEW.id;
    UPDATE app.employee_advances a SET amount_repaid = a.amount_repaid + r.total,
           status = CASE WHEN a.amount_repaid + r.total = a.principal THEN 'settled'::app.advance_status ELSE a.status END
      FROM (SELECT ar.advance_id, sum(ar.amount) total FROM app.advance_repayments ar
              JOIN app.payroll_lines l ON l.tenant_id = ar.tenant_id AND l.id = ar.payroll_line_id
             WHERE l.tenant_id = NEW.tenant_id AND l.payroll_run_id = NEW.id GROUP BY ar.advance_id) r
     WHERE a.tenant_id = NEW.tenant_id AND a.id = r.advance_id;
  END IF;
  RETURN NULL;
END $$;

CREATE FUNCTION app.tg_payroll_lines_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE st app.payroll_status;
BEGIN
  SELECT status INTO st FROM app.payroll_runs
   WHERE tenant_id = coalesce(NEW.tenant_id, OLD.tenant_id) AND id = coalesce(NEW.payroll_run_id, OLD.payroll_run_id);
  IF st <> 'draft' AND NOT (TG_OP = 'UPDATE' AND current_user = 'bms_owner') THEN
    RAISE EXCEPTION 'payroll lines are locked once the run is %', st USING ERRCODE = 'BM004';
  END IF;
  RETURN coalesce(NEW, OLD);
END $$;

-- =====================================================================
-- 11. Approvals, day closing
-- =====================================================================
CREATE FUNCTION app.tg_approval_decide() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.status IN ('approved', 'rejected') AND OLD.status = 'pending' THEN
    NEW.decided_by := coalesce(NEW.decided_by, app.current_membership_id());
    NEW.decided_at := coalesce(NEW.decided_at, now());
    IF NEW.status = 'approved' THEN
      PERFORM app.assert_can_approve(
        CASE NEW.approval_type
          WHEN 'sale_discount'          THEN 'discount'
          WHEN 'job_price_change'       THEN 'price_change'
          WHEN 'sale_void'              THEN 'sale_void'
          WHEN 'transaction_correction' THEN 'txn_correction'
          WHEN 'payment_void'           THEN 'payment_void'
          WHEN 'day_reopen'             THEN 'day_reopen'
          WHEN 'product_price_change'   THEN 'product_price' END,
        NEW.requested_amount,
        coalesce(NEW.requested_percent,
                 CASE WHEN NEW.approval_type = 'job_price_change' AND NEW.current_amount > 0
                      THEN round(abs(NEW.requested_amount - NEW.current_amount) * 100 / NEW.current_amount, 2) END));
    END IF;
  END IF;
  RETURN NEW;
END $$;

CREATE FUNCTION app.tg_day_closing_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.status = 'closed' AND OLD.status IN ('open', 'reopened') THEN
    IF NOT app.has_permission('finance.day.close') THEN
      RAISE EXCEPTION 'permission finance.day.close required' USING ERRCODE = 'BM007';
    END IF;
    NEW.closed_by := coalesce(app.current_membership_id(), NEW.closed_by);
    NEW.closed_at := now();
  ELSIF NEW.status = 'reopened' AND OLD.status = 'closed' THEN
    IF NOT (app.has_permission('finance.day.reopen')
            OR (NEW.reopen_approval_id IS NOT NULL AND app.approved_request_exists('day_reopen', NEW.id))) THEN
      RAISE EXCEPTION 'reopening a closed day requires Admin approval' USING ERRCODE = 'BM007';
    END IF;
    NEW.reopened_by := coalesce(app.current_membership_id(), NEW.reopened_by);
    NEW.reopened_at := now();
  END IF;
  RETURN NEW;
END $$;

-- =====================================================================
-- 12. Services & design workflow automation
-- =====================================================================
CREATE FUNCTION app.tg_project_stage_status() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.status = 'in_progress' AND NEW.started_at IS NULL THEN NEW.started_at := now(); END IF;
  IF NEW.status = 'done' THEN NEW.completed_at := coalesce(NEW.completed_at, now());
  ELSE NEW.completed_at := NULL; END IF;
  RETURN NEW;
END $$;

CREATE FUNCTION app.tg_project_stage_history() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
BEGIN
  INSERT INTO app.project_stage_history (tenant_id, project_stage_id, from_status, to_status, note)
  VALUES (NEW.tenant_id, NEW.id, OLD.status, NEW.status, nullif(current_setting('app.change_note', true), ''));
  -- First stage started => project in progress.
  UPDATE app.projects SET status = 'in_progress'
   WHERE tenant_id = NEW.tenant_id AND id = NEW.project_id AND status = 'not_started' AND NEW.status = 'in_progress';
  RETURN NULL;
END $$;

CREATE FUNCTION app.tg_project_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.status = 'completed' AND OLD.status <> 'completed' THEN
    IF EXISTS (SELECT 1 FROM app.project_stages WHERE tenant_id = NEW.tenant_id AND project_id = NEW.id AND status <> 'done') THEN
      RAISE EXCEPTION 'all stages must be done before the project is completed' USING ERRCODE = 'BM001';
    END IF;
    NEW.completed_at := coalesce(NEW.completed_at, now());
  END IF;
  IF NEW.status = 'cancelled' AND OLD.status <> 'cancelled' THEN NEW.cancelled_at := coalesce(NEW.cancelled_at, now()); END IF;
  IF NEW.total_price <> OLD.total_price
     AND NOT (app.has_permission('services.price.approve') OR app.approved_request_exists('job_price_change', NEW.id)) THEN
    RAISE EXCEPTION 'changing a job price requires Manager approval' USING ERRCODE = 'BM007';
  END IF;
  RETURN NEW;
END $$;

CREATE FUNCTION app.tg_design_job_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'UPDATE' AND NEW.price <> OLD.price
     AND NOT (app.has_permission('services.price.approve') OR app.approved_request_exists('job_price_change', NEW.id)) THEN
    RAISE EXCEPTION 'changing a job price requires Manager approval' USING ERRCODE = 'BM007';
  END IF;
  IF NEW.status = 'delivered' THEN NEW.delivered_at := coalesce(NEW.delivered_at, now()); END IF;
  RETURN NEW;
END $$;

-- Version decision drives the job (spec §7A): approve => Final + job Approved;
-- changes requested => uses one revision, card back to In Design.
CREATE FUNCTION app.tg_design_version_post() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
BEGIN
  IF NEW.status = 'sent_to_client' AND OLD.status <> 'sent_to_client' THEN
    UPDATE app.design_jobs SET status = 'with_client'
     WHERE tenant_id = NEW.tenant_id AND id = NEW.design_job_id AND status IN ('in_design', 'internal_review');
  ELSIF NEW.status = 'approved' AND OLD.status <> 'approved' THEN
    UPDATE app.design_versions SET status = 'superseded'
     WHERE tenant_id = NEW.tenant_id AND design_job_id = NEW.design_job_id AND id <> NEW.id
       AND status IN ('draft', 'sent_to_client');
    UPDATE app.design_jobs SET status = 'approved', final_version_id = NEW.id, approved_at = now()
     WHERE tenant_id = NEW.tenant_id AND id = NEW.design_job_id;
  ELSIF NEW.status = 'changes_requested' AND OLD.status <> 'changes_requested' THEN
    UPDATE app.design_jobs SET revisions_used = revisions_used + 1, status = 'in_design'
     WHERE tenant_id = NEW.tenant_id AND id = NEW.design_job_id;
  END IF;
  RETURN NULL;
END $$;

CREATE FUNCTION app.tg_design_version_stamp() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.status = 'sent_to_client' AND OLD.status <> 'sent_to_client' THEN NEW.sent_at := now(); END IF;
  IF NEW.status IN ('approved', 'changes_requested') AND OLD.status = 'sent_to_client' THEN NEW.decided_at := now(); END IF;
  RETURN NEW;
END $$;

-- Client decision on the public link => version status (single use, not after expiry).
CREATE FUNCTION app.tg_design_link_decide() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
BEGIN
  IF OLD.decision IS NOT NULL THEN
    RAISE EXCEPTION 'this approval link has already been used' USING ERRCODE = 'BM004';
  END IF;
  IF NEW.decision IS NOT NULL THEN
    IF OLD.revoked_at IS NOT NULL OR OLD.expires_at <= now() THEN
      RAISE EXCEPTION 'approval link expired or revoked' USING ERRCODE = 'BM001';
    END IF;
    NEW.decided_at := now();
    UPDATE app.design_versions SET status = NEW.decision::text::app.design_version_status
     WHERE tenant_id = NEW.tenant_id AND id = NEW.design_version_id AND status = 'sent_to_client';
    IF NOT FOUND THEN
      RAISE EXCEPTION 'version is no longer awaiting the client' USING ERRCODE = 'BM001';
    END IF;
  END IF;
  RETURN NEW;
END $$;

-- Branding stage completes when all its linked design jobs are approved/printing/delivered.
CREATE FUNCTION app.tg_branding_stage_rollup() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
BEGIN
  IF NEW.project_stage_id IS NOT NULL AND NEW.status IN ('approved', 'printing', 'delivered')
     AND NOT EXISTS (SELECT 1 FROM app.design_jobs j
                      WHERE j.tenant_id = NEW.tenant_id AND j.project_stage_id = NEW.project_stage_id
                        AND j.status NOT IN ('approved', 'printing', 'delivered', 'cancelled')) THEN
    UPDATE app.project_stages SET status = 'done'
     WHERE tenant_id = NEW.tenant_id AND id = NEW.project_stage_id AND is_branding_stage AND status <> 'done';
  END IF;
  RETURN NULL;
END $$;

CREATE FUNCTION app.tg_service_package_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM app.services WHERE tenant_id = NEW.tenant_id AND id = NEW.package_service_id AND is_package) THEN
    RAISE EXCEPTION 'parent service is not a package' USING ERRCODE = 'BM008';
  END IF;
  IF EXISTS (SELECT 1 FROM app.services WHERE tenant_id = NEW.tenant_id AND id = NEW.component_service_id AND is_package) THEN
    RAISE EXCEPTION 'packages cannot contain packages' USING ERRCODE = 'BM008';
  END IF;
  RETURN NEW;
END $$;

-- Compensation history is append-only except closing the open period.
CREATE FUNCTION app.tg_compensation_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'DELETE' OR OLD.effective_to IS NOT NULL
     OR (NEW.basic_salary, NEW.effective_from, NEW.pay_method, NEW.payout_account_ciphertext, NEW.employee_id)
        IS DISTINCT FROM (OLD.basic_salary, OLD.effective_from, OLD.pay_method, OLD.payout_account_ciphertext, OLD.employee_id) THEN
    RAISE EXCEPTION 'compensation history is append-only; add a new row' USING ERRCODE = 'BM004';
  END IF;
  RETURN NEW;
END $$;

CREATE FUNCTION app.tg_leave_decide() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.status IN ('approved', 'rejected') AND OLD.status = 'requested' THEN
    PERFORM app.assert_can_approve('leave');
    NEW.decided_by := coalesce(NEW.decided_by, app.current_membership_id());
    NEW.decided_at := coalesce(NEW.decided_at, now());
    IF EXISTS (SELECT 1 FROM app.employees e WHERE e.tenant_id = NEW.tenant_id AND e.id = NEW.employee_id
                AND e.membership_id = NEW.decided_by) THEN
      RAISE EXCEPTION 'you cannot decide your own leave' USING ERRCODE = 'BM007';
    END IF;
  END IF;
  RETURN NEW;
END $$;
