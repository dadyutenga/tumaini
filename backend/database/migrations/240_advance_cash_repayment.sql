-- A cash repayment of a staff advance is not a payroll line, so the API cannot
-- update the trigger-maintained amount_repaid column or insert the ledger row.
-- This definer does both, as bms_owner, after the caller has checked the amount.
CREATE FUNCTION app.apply_advance_cash_repayment(
  p_advance uuid, p_amount numeric, p_account uuid, p_location uuid, p_date date, p_description text
) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE adv record; bu uuid;
BEGIN
  SELECT * INTO adv FROM app.employee_advances
   WHERE tenant_id = app.current_tenant_id() AND id = p_advance FOR UPDATE;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'advance not found' USING ERRCODE = 'BM008';
  END IF;
  IF adv.status <> 'active' THEN
    RAISE EXCEPTION 'only an active advance can be repaid' USING ERRCODE = 'BM004';
  END IF;
  IF p_amount <= 0 OR p_amount > (adv.principal - adv.amount_repaid) THEN
    RAISE EXCEPTION 'repayment exceeds the remaining balance' USING ERRCODE = 'BM005';
  END IF;
  SELECT business_unit_id INTO bu FROM app.locations WHERE tenant_id = adv.tenant_id AND id = p_location;
  INSERT INTO app.financial_transactions
    (tenant_id, account_id, direction, amount, txn_type, business_unit_id, location_id, business_date, description)
  VALUES (adv.tenant_id, p_account, 'in', p_amount, 'other_payment', bu, p_location, p_date, left(p_description, 500));
  UPDATE app.employee_advances
     SET amount_repaid = amount_repaid + p_amount,
         status = CASE WHEN amount_repaid + p_amount = principal THEN 'settled'::app.advance_status ELSE status END
   WHERE tenant_id = adv.tenant_id AND id = adv.id;
END $$;
REVOKE ALL ON FUNCTION app.apply_advance_cash_repayment(uuid, numeric, uuid, uuid, date, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION app.apply_advance_cash_repayment(uuid, numeric, uuid, uuid, date, text) TO bms_app;
