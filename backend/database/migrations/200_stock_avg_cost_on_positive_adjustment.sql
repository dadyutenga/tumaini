-- =====================================================================
-- 200_stock_avg_cost_on_positive_adjustment.sql — update moving weighted
-- average cost on positive stock adjustments with a unit_cost (opening
-- stock and stock-in entries), not only purchase receipts.
-- =====================================================================
SET search_path = app, public;

CREATE OR REPLACE FUNCTION app.tg_stock_movement_apply() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE
  new_qty numeric; allow_neg boolean; total_before numeric; avg_before numeric;
BEGIN
  PERFORM app.assert_day_open(NEW.tenant_id, NEW.location_id, NEW.business_date);

  INSERT INTO app.stock_balances AS b (tenant_id, location_id, product_id, quantity_on_hand)
  VALUES (NEW.tenant_id, NEW.location_id, NEW.product_id, 0)
  ON CONFLICT (tenant_id, location_id, product_id) DO NOTHING;

  -- Moving weighted average cost on receipts and positive costed adjustments (across all locations).
  IF NEW.movement_type = 'purchase_receipt'
     OR (NEW.movement_type = 'adjustment' AND NEW.quantity_delta > 0 AND NEW.unit_cost IS NOT NULL) THEN
    SELECT coalesce(sum(quantity_on_hand), 0) INTO total_before
      FROM app.stock_balances WHERE tenant_id = NEW.tenant_id AND product_id = NEW.product_id;
    SELECT average_cost INTO avg_before FROM app.products
     WHERE tenant_id = NEW.tenant_id AND id = NEW.product_id FOR UPDATE;
    UPDATE app.products
       SET average_cost = CASE WHEN greatest(total_before, 0) + NEW.quantity_delta = 0 THEN NEW.unit_cost
                               ELSE round((greatest(total_before, 0) * avg_before + NEW.quantity_delta * NEW.unit_cost)
                                          / (greatest(total_before, 0) + NEW.quantity_delta), 4) END,
           buying_price = CASE WHEN NEW.movement_type = 'purchase_receipt' THEN NEW.unit_cost ELSE buying_price END
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
