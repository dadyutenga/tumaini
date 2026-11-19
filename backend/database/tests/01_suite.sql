-- =====================================================================
-- BMS integration test suite. Run as superuser on a freshly migrated DB:
--   psql -v ON_ERROR_STOP=1 -f tests/01_suite.sql
-- Business statements execute as bms_app (the API role) so RLS, column
-- grants and triggers are exercised exactly as in production.
-- =====================================================================
\set ON_ERROR_STOP 1
SET client_min_messages = notice;

-- ---------- Harness -----------------------------------------------------
DROP SCHEMA IF EXISTS test CASCADE;
CREATE SCHEMA test;
CREATE TABLE test.ids (name text PRIMARY KEY, id uuid NOT NULL);
CREATE TABLE test.results (n serial, label text, ok boolean, detail text);
GRANT USAGE ON SCHEMA test TO bms_app, bms_worker;
GRANT SELECT, INSERT, UPDATE ON test.ids TO bms_app, bms_worker;
GRANT SELECT, INSERT ON test.results TO bms_app, bms_worker;
GRANT USAGE ON SEQUENCE test.results_n_seq TO bms_app, bms_worker;

CREATE FUNCTION test.id(p text) RETURNS uuid LANGUAGE sql STABLE AS
$$ SELECT id FROM test.ids WHERE name = p $$;
CREATE FUNCTION test.put(p text, v uuid) RETURNS uuid LANGUAGE sql AS
$$ INSERT INTO test.ids VALUES (p, v) ON CONFLICT (name) DO UPDATE SET id = excluded.id RETURNING id $$;

-- Switch request context to a named actor (tenant + membership + user).
CREATE FUNCTION test.ctx(actor text) RETURNS void LANGUAGE plpgsql AS $$
BEGIN
  PERFORM set_config('app.tenant_id',     coalesce(test.id(actor || '.tenant')::text, ''), false);
  PERFORM set_config('app.membership_id', coalesce(test.id(actor || '.m')::text, ''), false);
  PERFORM set_config('app.user_id',       coalesce(test.id(actor || '.u')::text, ''), false);
  PERFORM set_config('app.request_id',    'test-' || actor, false);
END $$;

CREATE FUNCTION test.ok(label text, cond boolean, detail text DEFAULT NULL) RETURNS void LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO test.results (label, ok, detail) VALUES (label, coalesce(cond, false), detail);
  IF NOT coalesce(cond, false) THEN RAISE WARNING 'FAIL: % %', label, coalesce(detail, ''); END IF;
END $$;

-- Execute SQL that must fail with a given SQLSTATE (deferred checks forced).
CREATE FUNCTION test.fails(label text, sql text, expected text) RETURNS void LANGUAGE plpgsql AS $$
DECLARE st text; msg text;
BEGIN
  BEGIN
    EXECUTE sql;
    SET CONSTRAINTS ALL IMMEDIATE;
    SET CONSTRAINTS ALL DEFERRED;
    RAISE EXCEPTION 'expected failure did not happen' USING ERRCODE = 'TS001';
  EXCEPTION WHEN OTHERS THEN
    GET STACKED DIAGNOSTICS st = RETURNED_SQLSTATE, msg = MESSAGE_TEXT;
    PERFORM test.ok(label, st = expected, format('expected %s got %s: %s', expected, st, msg));
  END;
END $$;
GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA test TO bms_app, bms_worker;

-- ---------- Fixtures: users (global identity) -------------------------
INSERT INTO iam.users (full_name, phone, email) VALUES
 ('Owner A',   '+255700000001', 'owner.a@example.test'),
 ('Manager A', '+255700000002', 'manager.a@example.test'),
 ('Staff A',   '+255700000003', 'staff.a@example.test'),
 ('Staff A2',  '+255700000004', NULL),
 ('Owner B',   '+255700000005', 'owner.b@example.test');
INSERT INTO test.ids SELECT 'adminA.u', id FROM iam.users WHERE phone = '+255700000001';
INSERT INTO test.ids SELECT 'mgrA.u',   id FROM iam.users WHERE phone = '+255700000002';
INSERT INTO test.ids SELECT 'staffA.u', id FROM iam.users WHERE phone = '+255700000003';
INSERT INTO test.ids SELECT 'staff2A.u',id FROM iam.users WHERE phone = '+255700000004';
INSERT INTO test.ids SELECT 'adminB.u', id FROM iam.users WHERE phone = '+255700000005';

-- ---------- Provision two tenants (platform worker) --------------------
SET ROLE bms_worker;
SELECT test.put('A', app.provision_tenant('duka-a', 'Duka A Ltd', test.id('adminA.u')));
SELECT set_config('app.tenant_id', '', false);
SELECT test.put('B', app.provision_tenant('duka-b', 'Duka B Ltd', test.id('adminB.u')));
SELECT set_config('app.tenant_id', '', false);
RESET ROLE;

-- Ids of provisioned rows (setup only, as superuser).
INSERT INTO test.ids
SELECT x.k, x.v FROM (
  SELECT 'adminA.m' k, m.id v FROM app.memberships m WHERE m.user_id = test.id('adminA.u')
  UNION ALL SELECT 'adminB.m', m.id FROM app.memberships m WHERE m.user_id = test.id('adminB.u')
  UNION ALL SELECT 'locA', l.id FROM app.locations l WHERE l.tenant_id = test.id('A')
  UNION ALL SELECT 'locB', l.id FROM app.locations l WHERE l.tenant_id = test.id('B')
  UNION ALL SELECT 'buA', b.id FROM app.business_units b WHERE b.tenant_id = test.id('A') AND b.code = 'A'
  UNION ALL SELECT 'buB_A', b.id FROM app.business_units b WHERE b.tenant_id = test.id('A') AND b.code = 'B'
  UNION ALL SELECT 'buB', b.id FROM app.business_units b WHERE b.tenant_id = test.id('B') AND b.code = 'A'
  UNION ALL SELECT 'role.manager', r.id FROM app.roles r WHERE r.tenant_id = test.id('A') AND r.code = 'manager'
  UNION ALL SELECT 'role.staff', r.id FROM app.roles r WHERE r.tenant_id = test.id('A') AND r.code = 'staff'
  UNION ALL SELECT 'role.admin', r.id FROM app.roles r WHERE r.tenant_id = test.id('A') AND r.code = 'admin'
) x;
INSERT INTO test.ids VALUES ('adminA.tenant', test.id('A')), ('adminB.tenant', test.id('B')),
  ('mgrA.tenant', test.id('A')), ('staffA.tenant', test.id('A')), ('staff2A.tenant', test.id('A'));

-- =====================================================================
SET ROLE bms_app;
-- =====================================================================
SELECT test.ctx('adminA');

-- Team setup by Admin A.
WITH m AS (INSERT INTO app.memberships (tenant_id, user_id, status, joined_at, default_location_id)
           VALUES (test.id('A'), test.id('mgrA.u'), 'active', now(), test.id('locA')) RETURNING id)
SELECT test.put('mgrA.m', id) FROM m;
WITH m AS (INSERT INTO app.memberships (tenant_id, user_id, status, joined_at, default_location_id)
           VALUES (test.id('A'), test.id('staffA.u'), 'active', now(), test.id('locA')) RETURNING id)
SELECT test.put('staffA.m', id) FROM m;
WITH m AS (INSERT INTO app.memberships (tenant_id, user_id, status, joined_at, default_location_id)
           VALUES (test.id('A'), test.id('staff2A.u'), 'active', now(), test.id('locA')) RETURNING id)
SELECT test.put('staff2A.m', id) FROM m;
INSERT INTO app.membership_roles (tenant_id, membership_id, role_id) VALUES
 (test.id('A'), test.id('mgrA.m'), test.id('role.manager')),
 (test.id('A'), test.id('staffA.m'), test.id('role.staff')),
 (test.id('A'), test.id('staff2A.m'), test.id('role.staff'));
-- Admin sets real limits: staff 5% discount; manager approves discount up to 20%, refunds 100k, expenses 50k.
UPDATE app.authority_limits SET limit_value = 5  WHERE tenant_id = test.id('A') AND role_id = test.id('role.staff') AND limit_code = 'sale_discount_pct';
UPDATE app.authority_limits SET limit_value = 20 WHERE tenant_id = test.id('A') AND role_id = test.id('role.manager') AND limit_code = 'discount_approve_pct';
UPDATE app.authority_limits SET limit_value = 100000 WHERE tenant_id = test.id('A') AND role_id = test.id('role.manager') AND limit_code = 'refund_approve_amount';
UPDATE app.authority_limits SET limit_value = 50000  WHERE tenant_id = test.id('A') AND role_id = test.id('role.manager') AND limit_code = 'expense_approve_amount';

-- Accounts + opening balance.
WITH a AS (INSERT INTO app.financial_accounts (tenant_id, name, kind, location_id) VALUES
  (test.id('A'), 'Till A', 'cash', test.id('locA')) RETURNING id) SELECT test.put('till', id) FROM a;
WITH a AS (INSERT INTO app.financial_accounts (tenant_id, name, kind, provider) VALUES
  (test.id('A'), 'M-Pesa', 'mobile_money', 'Vodacom M-Pesa') RETURNING id) SELECT test.put('mpesa', id) FROM a;
WITH a AS (INSERT INTO app.financial_accounts (tenant_id, name, kind, provider) VALUES
  (test.id('A'), 'CRDB', 'bank', 'CRDB Bank') RETURNING id) SELECT test.put('bank', id) FROM a;
INSERT INTO app.financial_transactions (tenant_id, account_id, direction, amount, txn_type, business_unit_id,
  location_id, business_date, description)
VALUES (test.id('A'), test.id('till'), 'in', 100000, 'opening_balance', test.id('buA'), test.id('locA'), current_date, 'Opening float'),
       (test.id('A'), test.id('bank'), 'in', 5000000, 'opening_balance', test.id('buA'), test.id('locA'), current_date, 'Opening bank');
SELECT test.ok('T01 opening balance posts to account',
  (SELECT current_balance FROM app.financial_accounts WHERE id = test.id('till')) = 100000);

-- Catalogue.
WITH c AS (INSERT INTO app.product_categories (tenant_id, name) VALUES (test.id('A'), 'Accessories') RETURNING id)
SELECT test.put('cat.acc', id) FROM c;
WITH p AS (INSERT INTO app.products (tenant_id, business_unit_id, category_id, barcode, name, selling_price)
           VALUES (test.id('A'), test.id('buA'), test.id('cat.acc'), '600100', 'Charger', 15000) RETURNING id)
SELECT test.put('charger', id) FROM p;
WITH p AS (INSERT INTO app.products (tenant_id, business_unit_id, barcode, name, selling_price, tracks_serial, warranty_months)
           VALUES (test.id('A'), test.id('buA'), '600200', 'Phone A', 300000, true, 12) RETURNING id)
SELECT test.put('phone', id) FROM p;
SELECT test.fails('T02 duplicate barcode rejected',
  format($$INSERT INTO app.products (tenant_id, business_unit_id, barcode, name, selling_price) VALUES (%L,%L,'600100','Dup',1)$$,
         test.id('A'), test.id('buA')), '23505');

-- Commission rule: 2% of sales.
INSERT INTO app.commission_rules (tenant_id, name, basis, percent, effective_from)
VALUES (test.id('A'), 'Sales 2%', 'sale_percent', 2, current_date - 1);

-- Employees (HR) with compensation.
WITH e AS (INSERT INTO app.employees (tenant_id, employee_no, membership_id, full_name, position, start_date)
           VALUES (test.id('A'), 'EMP-0001', test.id('staffA.m'), 'Staff A', 'Cashier', current_date - 100) RETURNING id)
SELECT test.put('empStaff', id) FROM e;
WITH e AS (INSERT INTO app.employees (tenant_id, employee_no, membership_id, full_name, position, start_date)
           VALUES (test.id('A'), 'EMP-0002', test.id('staff2A.m'), 'Staff A2', 'Designer', current_date - 50) RETURNING id)
SELECT test.put('empStaff2', id) FROM e;
WITH e AS (INSERT INTO app.employees (tenant_id, employee_no, membership_id, full_name, position, start_date)
           VALUES (test.id('A'), 'EMP-0003', test.id('mgrA.m'), 'Manager A', 'Secretary', current_date - 300) RETURNING id)
SELECT test.put('empMgr', id) FROM e;
INSERT INTO app.employee_compensation (tenant_id, employee_id, effective_from, basic_salary, pay_method, reason) VALUES
 (test.id('A'), test.id('empStaff'),  current_date - 100, 400000, 'cash', 'hire'),
 (test.id('A'), test.id('empStaff2'), current_date - 50,  450000, 'cash', 'hire'),
 (test.id('A'), test.id('empMgr'),    current_date - 300, 900000, 'cash', 'hire');
SELECT test.fails('T03 overlapping salary periods rejected',
  format($$INSERT INTO app.employee_compensation (tenant_id, employee_id, effective_from, basic_salary, pay_method, reason)
           VALUES (%L,%L,current_date,500000,'cash','raise')$$, test.id('A'), test.id('empStaff')), '23P01');

-- ---------- Purchasing & stock ----------------------------------------
WITH s AS (INSERT INTO app.suppliers (tenant_id, name, phone) VALUES (test.id('A'), 'Kariakoo Wholesale', '+255711000001') RETURNING id)
SELECT test.put('supplier', id) FROM s;
WITH p AS (INSERT INTO app.purchases (tenant_id, purchase_no, supplier_id, business_unit_id, location_id, status,
                                      order_date, subtotal, total)
           VALUES (test.id('A'), app.next_document_number('purchase'), test.id('supplier'), test.id('buA'),
                   test.id('locA'), 'ordered', current_date, 480000, 480000) RETURNING id)
SELECT test.put('po', id) FROM p;
WITH l AS (INSERT INTO app.purchase_lines (tenant_id, purchase_id, line_no, product_id, quantity_ordered, unit_cost, line_total)
           VALUES (test.id('A'), test.id('po'), 1, test.id('charger'), 10, 8000, 80000) RETURNING id)
SELECT test.put('pl.charger', id) FROM l;
WITH l AS (INSERT INTO app.purchase_lines (tenant_id, purchase_id, line_no, product_id, quantity_ordered, unit_cost, line_total)
           VALUES (test.id('A'), test.id('po'), 2, test.id('phone'), 2, 200000, 400000) RETURNING id)
SELECT test.put('pl.phone', id) FROM l;
WITH g AS (INSERT INTO app.goods_receipts (tenant_id, receipt_no, purchase_id, location_id, business_date)
           VALUES (test.id('A'), app.next_document_number('goods_receipt'), test.id('po'), test.id('locA'), current_date) RETURNING id)
SELECT test.put('grn', id) FROM g;
INSERT INTO app.goods_receipt_lines (tenant_id, goods_receipt_id, purchase_line_id, quantity, unit_cost)
VALUES (test.id('A'), test.id('grn'), test.id('pl.charger'), 10, 8000);
WITH g AS (INSERT INTO app.goods_receipt_lines (tenant_id, goods_receipt_id, purchase_line_id, quantity, unit_cost)
           VALUES (test.id('A'), test.id('grn'), test.id('pl.phone'), 2, 200000) RETURNING id)
SELECT test.put('grl.phone', id) FROM g;
WITH u AS (INSERT INTO app.product_units (tenant_id, product_id, location_id, serial_no, imei) VALUES
  (test.id('A'), test.id('phone'), test.id('locA'), 'SN-001', '356938035643801') RETURNING id) SELECT test.put('unit1', id) FROM u;
WITH u AS (INSERT INTO app.product_units (tenant_id, product_id, location_id, serial_no, imei) VALUES
  (test.id('A'), test.id('phone'), test.id('locA'), 'SN-002', '356938035643802') RETURNING id) SELECT test.put('unit2', id) FROM u;
INSERT INTO app.goods_receipt_line_units VALUES (test.id('A'), test.id('grl.phone'), test.id('unit1')),
                                                (test.id('A'), test.id('grl.phone'), test.id('unit2'));

SELECT test.ok('T04 goods receipt increases stock',
  (SELECT quantity_on_hand FROM app.stock_balances WHERE product_id = test.id('charger')) = 10);
SELECT test.ok('T05 purchase auto-marked received',
  (SELECT status FROM app.purchases WHERE id = test.id('po')) = 'received');
SELECT test.ok('T06 moving average cost set from receipt',
  (SELECT average_cost FROM app.products WHERE id = test.id('charger')) = 8000);
SELECT test.fails('T07 receiving more on a fully received purchase rejected',
  format($$WITH g AS (INSERT INTO app.goods_receipts (tenant_id, receipt_no, purchase_id, location_id, business_date)
           VALUES (%L,'GRN-X',%L,%L,current_date) RETURNING id)
           INSERT INTO app.goods_receipt_lines (tenant_id, goods_receipt_id, purchase_line_id, quantity, unit_cost)
           SELECT %L, id, %L, 1, 8000 FROM g$$, test.id('A'), test.id('po'), test.id('locA'), test.id('A'), test.id('pl.charger')), 'BM001');
SELECT test.fails('T08 stock ledger is append-only for the API (no grant)',
  'UPDATE app.stock_movements SET quantity_delta = 99', '42501');
SELECT test.fails('T09 API cannot write stock balances directly',
  format($$UPDATE app.stock_balances SET quantity_on_hand = 999 WHERE product_id = %L$$, test.id('charger')), '42501');

-- ---------- POS sale (staff) -------------------------------------------
SELECT test.ctx('staffA');
WITH s AS (INSERT INTO app.sales (tenant_id, business_unit_id, location_id, business_date, client_ref)
           VALUES (test.id('A'), test.id('buA'), test.id('locA'), current_date, 'aaaaaaaa-0000-0000-0000-000000000001') RETURNING id)
SELECT test.put('sale1', id) FROM s;
INSERT INTO app.sale_lines (tenant_id, sale_id, line_no, product_id, product_name, quantity, unit_price, line_total)
VALUES (test.id('A'), test.id('sale1'), 1, test.id('charger'), 'Charger', 2, 15000, 30000);
UPDATE app.sales SET subtotal = 30000, total = 30000 WHERE id = test.id('sale1');
WITH p AS (INSERT INTO app.payments_received (tenant_id, receipt_no, business_unit_id, location_id, account_id, method,
                                              purpose, amount, business_date)
           VALUES (test.id('A'), app.next_document_number('payment_receipt'), test.id('buA'), test.id('locA'),
                   test.id('till'), 'cash', 'sale', 30000, current_date) RETURNING id)
SELECT test.put('pay1', id) FROM p;
INSERT INTO app.payment_allocations (tenant_id, payment_received_id, sale_id, amount)
VALUES (test.id('A'), test.id('pay1'), test.id('sale1'), 30000);
UPDATE app.sales SET status = 'completed', receipt_no = app.next_document_number('sale_receipt') WHERE id = test.id('sale1');

SELECT test.ok('T10 sale reduces stock', (SELECT quantity_on_hand FROM app.stock_balances WHERE product_id = test.id('charger')) = 8);
SELECT test.ok('T11 COGS snapshot captured', (SELECT unit_cost FROM app.sale_lines WHERE sale_id = test.id('sale1')) = 8000);
SELECT test.ok('T12 receipt number R-0001', (SELECT receipt_no FROM app.sales WHERE id = test.id('sale1')) = 'R-0001');
SELECT test.ok('T13 payment posted to till ledger', (SELECT current_balance FROM app.financial_accounts WHERE id = test.id('till')) = 130000);
SELECT test.ok('T14 sale fully paid', (SELECT balance_due FROM app.sales WHERE id = test.id('sale1')) = 0);
SELECT test.ok('T15 commission accrued 2% for seller (staff sees own)',
  (SELECT amount FROM app.commission_entries WHERE sale_id = test.id('sale1')) = 600);

SELECT test.fails('T16 duplicate POS submission (client_ref) rejected',
  format($$INSERT INTO app.sales (tenant_id, business_unit_id, location_id, business_date, client_ref)
           VALUES (%L,%L,%L,current_date,'aaaaaaaa-0000-0000-0000-000000000001')$$, test.id('A'), test.id('buA'), test.id('locA')), '23505');
SELECT test.fails('T17 completed sale is read-only',
  format($$UPDATE app.sales SET subtotal = 1, total = 1 WHERE id = %L$$, test.id('sale1')), 'BM004');
SELECT test.fails('T18 completed sale lines are read-only',
  format($$UPDATE app.sale_lines SET quantity = 1, line_total = 15000 WHERE sale_id = %L$$, test.id('sale1')), 'BM004');
SELECT test.fails('T19 invalid state transition completed -> draft',
  format($$UPDATE app.sales SET status = 'draft' WHERE id = %L$$, test.id('sale1')), 'BM001');
SELECT test.fails('T20 API cannot set amount_paid directly',
  format($$UPDATE app.sales SET amount_paid = 0 WHERE id = %L$$, test.id('sale1')), '42501');
SELECT test.fails('T21 API cannot post system ledger types',
  format($$INSERT INTO app.financial_transactions (tenant_id, account_id, direction, amount, txn_type, business_unit_id,
           location_id, business_date, description, payment_received_id) VALUES (%L,%L,'in',1,'customer_receipt',%L,%L,current_date,'x',%L)$$,
         test.id('A'), test.id('till'), test.id('buA'), test.id('locA'), test.id('pay1')), 'BM004');

-- Insufficient stock.
WITH s AS (INSERT INTO app.sales (tenant_id, business_unit_id, location_id, business_date)
           VALUES (test.id('A'), test.id('buA'), test.id('locA'), current_date) RETURNING id)
SELECT test.put('sale_big', id) FROM s;
INSERT INTO app.sale_lines (tenant_id, sale_id, line_no, product_id, product_name, quantity, unit_price, line_total)
VALUES (test.id('A'), test.id('sale_big'), 1, test.id('charger'), 'Charger', 100, 15000, 1500000);
UPDATE app.sales SET subtotal = 1500000, total = 1500000 WHERE id = test.id('sale_big');
SELECT test.fails('T22 unpaid sale without customer cannot complete as credit',
  format($$UPDATE app.sales SET status='completed', receipt_no='R-X1', customer_id=NULL WHERE id=%L$$, test.id('sale_big')), 'BM008');
-- (credit sale with no customer trips first) — now with a customer + due date, stock check fires:
WITH c AS (INSERT INTO app.customers (tenant_id, full_name, phone, is_shop_customer)
           VALUES (test.id('A'), 'Mama Amina', '+255754000001', true) RETURNING id)
SELECT test.put('cust1', id) FROM c;
SELECT test.fails('T23 insufficient stock raises BM003',
  format($$UPDATE app.sales SET status='completed', receipt_no='R-X1', customer_id=%L, due_date=current_date+30 WHERE id=%L$$,
         test.id('cust1'), test.id('sale_big')), 'BM003');
DELETE FROM app.sales WHERE id = test.id('sale_big');
SELECT test.ok('T24 unpaid draft sale can be deleted', NOT EXISTS (SELECT 1 FROM app.sales WHERE id = test.id('sale_big')));
SELECT test.fails('T25 duplicate customer phone rejected',
  format($$INSERT INTO app.customers (tenant_id, full_name, phone, is_shop_customer) VALUES (%L,'Dup','+255754000001',true)$$, test.id('A')), '23505');

-- Discount above staff limit.
WITH s AS (INSERT INTO app.sales (tenant_id, business_unit_id, location_id, business_date, customer_id)
           VALUES (test.id('A'), test.id('buA'), test.id('locA'), current_date, test.id('cust1')) RETURNING id)
SELECT test.put('sale_disc', id) FROM s;
INSERT INTO app.sale_lines (tenant_id, sale_id, line_no, product_id, product_name, quantity, unit_price, line_total)
VALUES (test.id('A'), test.id('sale_disc'), 1, test.id('charger'), 'Charger', 2, 15000, 30000);
UPDATE app.sales SET subtotal = 30000, discount_type = 'percent', discount_value = 10, discount_amount = 3000, total = 27000
 WHERE id = test.id('sale_disc');
WITH p AS (INSERT INTO app.payments_received (tenant_id, receipt_no, customer_id, business_unit_id, location_id, account_id,
                                              method, purpose, amount, reference, business_date)
           VALUES (test.id('A'), app.next_document_number('payment_receipt'), test.id('cust1'), test.id('buA'), test.id('locA'),
                   test.id('mpesa'), 'mobile_money', 'sale', 27000, 'QHX123ABC', current_date) RETURNING id)
SELECT test.put('pay_disc', id) FROM p;
INSERT INTO app.payment_allocations (tenant_id, payment_received_id, sale_id, amount)
VALUES (test.id('A'), test.id('pay_disc'), test.id('sale_disc'), 27000);
SELECT test.fails('T26 discount above seller limit needs approval',
  format($$UPDATE app.sales SET status='completed', receipt_no='R-X2' WHERE id=%L$$, test.id('sale_disc')), 'BM007');
SELECT test.fails('T27 same M-Pesa reference cannot be recorded twice',
  format($$INSERT INTO app.payments_received (tenant_id, receipt_no, customer_id, business_unit_id, location_id, account_id,
           method, purpose, amount, reference, business_date) VALUES (%L,'RC-DUP',%L,%L,%L,%L,'mobile_money','sale',1,'QHX123ABC',current_date)$$,
         test.id('A'), test.id('cust1'), test.id('buA'), test.id('locA'), test.id('mpesa')), '23505');
SELECT test.fails('T28 payment method must match account kind',
  format($$INSERT INTO app.payments_received (tenant_id, receipt_no, customer_id, business_unit_id, location_id, account_id,
           method, purpose, amount, business_date) VALUES (%L,'RC-BAD',%L,%L,%L,%L,'cash','sale',1,current_date)$$,
         test.id('A'), test.id('cust1'), test.id('buA'), test.id('locA'), test.id('mpesa')), 'BM008');
WITH a AS (INSERT INTO app.approval_requests (tenant_id, approval_type, reason, requested_percent, sale_id)
           VALUES (test.id('A'), 'sale_discount', 'Loyal customer', 10, test.id('sale_disc')) RETURNING id)
SELECT test.put('appr_disc', id) FROM a;
SELECT test.fails('T29 staff cannot approve (no permission)',
  format($$UPDATE app.approval_requests SET status='approved' WHERE id=%L$$, test.id('appr_disc')), 'BM007');
SELECT test.fails('T30 duplicate pending approval for same sale rejected',
  format($$INSERT INTO app.approval_requests (tenant_id, approval_type, reason, requested_percent, sale_id)
           VALUES (%L,'sale_discount','again',10,%L)$$, test.id('A'), test.id('sale_disc')), '23505');

SELECT test.ctx('mgrA');
UPDATE app.approval_requests SET status = 'approved' WHERE id = test.id('appr_disc');
SELECT test.ok('T31 manager approves within limit; approver stamped',
  (SELECT decided_by FROM app.approval_requests WHERE id = test.id('appr_disc')) = test.id('mgrA.m'));
WITH a AS (INSERT INTO app.approval_requests (tenant_id, approval_type, reason, requested_percent, sale_id)
           VALUES (test.id('A'), 'sale_discount', 'test', 10, test.id('sale1')) RETURNING id)
SELECT test.put('appr_selfvoid', id) FROM a;
SELECT test.fails('T32 nobody approves their own request (SoD)',
  format($$UPDATE app.approval_requests SET status='approved', decided_by=%L WHERE id=%L$$, test.id('mgrA.m'), test.id('appr_selfvoid')), '23514');
UPDATE app.approval_requests SET status = 'cancelled' WHERE id = test.id('appr_selfvoid');

SELECT test.ctx('staffA');
UPDATE app.sales SET status = 'completed', receipt_no = app.next_document_number('sale_receipt') WHERE id = test.id('sale_disc');
SELECT test.ok('T33 approved discount lets sale complete', (SELECT status FROM app.sales WHERE id = test.id('sale_disc')) = 'completed');

-- Serialised product.
WITH s AS (INSERT INTO app.sales (tenant_id, business_unit_id, location_id, business_date, customer_id)
           VALUES (test.id('A'), test.id('buA'), test.id('locA'), current_date, test.id('cust1')) RETURNING id)
SELECT test.put('sale_phone', id) FROM s;
WITH l AS (INSERT INTO app.sale_lines (tenant_id, sale_id, line_no, product_id, product_name, quantity, unit_price, line_total, warranty_months)
           VALUES (test.id('A'), test.id('sale_phone'), 1, test.id('phone'), 'Phone A', 1, 300000, 300000, 12) RETURNING id)
SELECT test.put('sl_phone', id) FROM l;
UPDATE app.sales SET subtotal = 300000, total = 300000, due_date = current_date + 30 WHERE id = test.id('sale_phone');
SELECT test.fails('T34 serialised product needs a serial per unit (deferred check)',
  format($$UPDATE app.sales SET status='completed', receipt_no='R-X3' WHERE id=%L$$, test.id('sale_phone')), 'BM008');
INSERT INTO app.sale_line_units VALUES (test.id('A'), test.id('sl_phone'), test.id('unit1'));
UPDATE app.sales SET status = 'completed', receipt_no = app.next_document_number('sale_receipt') WHERE id = test.id('sale_phone');
SELECT test.ok('T35 credit sale with customer + due date completes; unit sold with warranty',
  (SELECT status = 'sold' AND warranty_expires_on IS NOT NULL FROM app.product_units WHERE id = test.id('unit1')));
SELECT test.ok('T36 credit sale appears in receivables',
  EXISTS (SELECT 1 FROM app.v_receivables WHERE doc_id = test.id('sale_phone') AND balance_due = 300000));
WITH s AS (INSERT INTO app.sales (tenant_id, business_unit_id, location_id, business_date, customer_id, due_date)
           VALUES (test.id('A'), test.id('buA'), test.id('locA'), current_date, test.id('cust1'), current_date + 7) RETURNING id)
SELECT test.put('sale_phone2', id) FROM s;
WITH l AS (INSERT INTO app.sale_lines (tenant_id, sale_id, line_no, product_id, product_name, quantity, unit_price, line_total)
           VALUES (test.id('A'), test.id('sale_phone2'), 1, test.id('phone'), 'Phone A', 1, 300000, 300000) RETURNING id)
SELECT test.put('sl_phone2', id) FROM l;
INSERT INTO app.sale_line_units VALUES (test.id('A'), test.id('sl_phone2'), test.id('unit1'));
UPDATE app.sales SET subtotal = 300000, total = 300000 WHERE id = test.id('sale_phone2');
SELECT test.fails('T37 an already-sold serial cannot be sold again',
  format($$UPDATE app.sales SET status='completed', receipt_no='R-X4' WHERE id=%L$$, test.id('sale_phone2')), 'BM003');
DELETE FROM app.sales WHERE id = test.id('sale_phone2');

-- Partial payment of credit sale on a later allocation; over-allocation blocked.
WITH p AS (INSERT INTO app.payments_received (tenant_id, receipt_no, customer_id, business_unit_id, location_id, account_id,
                                              method, purpose, amount, business_date)
           VALUES (test.id('A'), app.next_document_number('payment_receipt'), test.id('cust1'), test.id('buA'), test.id('locA'),
                   test.id('till'), 'cash', 'debt_payment', 100000, current_date) RETURNING id)
SELECT test.put('pay_debt', id) FROM p;
SELECT test.fails('T38 allocation larger than the payment rejected',
  format($$INSERT INTO app.payment_allocations (tenant_id, payment_received_id, sale_id, amount) VALUES (%L,%L,%L,150000)$$,
         test.id('A'), test.id('pay_debt'), test.id('sale_phone')), 'BM005');
INSERT INTO app.payment_allocations (tenant_id, payment_received_id, sale_id, amount)
VALUES (test.id('A'), test.id('pay_debt'), test.id('sale_phone'), 100000);
SELECT test.ok('T39 debt payment reduces balance', (SELECT balance_due FROM app.sales WHERE id = test.id('sale_phone')) = 200000);

-- ---------- Returns ------------------------------------------------------
WITH r AS (INSERT INTO app.sale_returns (tenant_id, return_no, sale_id, location_id, resolution, reason, refund_amount, business_date)
           VALUES (test.id('A'), app.next_document_number('sale_return'), test.id('sale1'), test.id('locA'), 'refund',
                   'Faulty charger', 15000, current_date) RETURNING id)
SELECT test.put('ret1', id) FROM r;
INSERT INTO app.sale_return_lines (tenant_id, sale_return_id, sale_line_id, quantity, condition, restock, refund_amount)
SELECT test.id('A'), test.id('ret1'), id, 1, 'resellable', true, 15000 FROM app.sale_lines WHERE sale_id = test.id('sale1');
SELECT test.fails('T40 requester cannot approve own return',
  format($$UPDATE app.sale_returns SET status='approved' WHERE id=%L$$, test.id('ret1')), 'BM007');
SELECT test.ctx('mgrA');
UPDATE app.sale_returns SET status = 'approved' WHERE id = test.id('ret1');
UPDATE app.sale_returns SET status = 'completed' WHERE id = test.id('ret1');
SELECT test.ok('T41 return restocks and records returned qty',
  (SELECT quantity_on_hand FROM app.stock_balances WHERE product_id = test.id('charger')) = 7
  AND (SELECT returned_quantity FROM app.sale_lines WHERE sale_id = test.id('sale1')) = 1);
SELECT test.ok('T42a manager cannot see a cashier''s commission (restricted)',
  (SELECT count(*) FROM app.commission_entries) = 0);
SELECT test.ctx('adminA');
SELECT test.ok('T42 commission reversed proportionally on return',
  (SELECT sum(amount) FROM app.commission_entries WHERE sale_id = test.id('sale1')) = 300);
SELECT test.ctx('mgrA');
WITH m AS (INSERT INTO app.payments_made (tenant_id, payment_no, business_unit_id, location_id, account_id, method, amount,
                                          business_date, other_payee_name)
           VALUES (test.id('A'), app.next_document_number('payment_made'), test.id('buA'), test.id('locA'), test.id('till'),
                   'cash', 15000, current_date, 'Walk-in customer') RETURNING id)
SELECT test.put('refund1', id) FROM m;
INSERT INTO app.payment_made_allocations (tenant_id, payment_made_id, sale_return_id, amount)
VALUES (test.id('A'), test.id('refund1'), test.id('ret1'), 15000);
SELECT test.ok('T43 refund paid out of till', (SELECT current_balance FROM app.financial_accounts WHERE id = test.id('till')) = 215000);
WITH r AS (INSERT INTO app.sale_returns (tenant_id, return_no, sale_id, location_id, resolution, reason, refund_amount, business_date)
           VALUES (test.id('A'), app.next_document_number('sale_return'), test.id('sale1'), test.id('locA'), 'refund',
                   'second', 30000, current_date) RETURNING id)
SELECT test.put('ret2', id) FROM r;
INSERT INTO app.sale_return_lines (tenant_id, sale_return_id, sale_line_id, quantity, condition, restock, refund_amount)
SELECT test.id('A'), test.id('ret2'), id, 2, 'resellable', true, 30000 FROM app.sale_lines WHERE sale_id = test.id('sale1');
SELECT test.ctx('adminA');
UPDATE app.sale_returns SET status = 'approved' WHERE id = test.id('ret2');
SELECT test.fails('T44 cannot return more than was sold',
  format($$UPDATE app.sale_returns SET status='completed' WHERE id=%L$$, test.id('ret2')), '23514');
UPDATE app.sale_returns SET status = 'cancelled' WHERE id = test.id('ret2');

-- ---------- Void flow ---------------------------------------------------
SELECT test.ctx('mgrA');
SELECT test.fails('T45 manager cannot void a sale without Admin approval',
  format($$UPDATE app.sales SET status='voided', void_reason='mistake' WHERE id=%L$$, test.id('sale_disc')), 'BM007');
SELECT test.ctx('adminA');
SELECT test.fails('T46 sale with live payments cannot be voided',
  format($$UPDATE app.sales SET status='voided', void_reason='mistake' WHERE id=%L$$, test.id('sale_disc')), 'BM005');
UPDATE app.payments_received SET status = 'voided', voided_at = now(), voided_by = test.id('adminA.m'), void_reason = 'wrong sale'
 WHERE id = test.id('pay_disc');
UPDATE app.sales SET status = 'voided', void_reason = 'mistake' WHERE id = test.id('sale_disc');
SELECT test.ok('T47 payment void reverses ledger (M-Pesa back to 0)',
  (SELECT current_balance FROM app.financial_accounts WHERE id = test.id('mpesa')) = 0
  AND (SELECT count(*) FROM app.financial_transactions WHERE txn_type = 'reversal') = 1);
SELECT test.ok('T48 sale void restores stock', (SELECT quantity_on_hand FROM app.stock_balances WHERE product_id = test.id('charger')) = 9);
SELECT test.fails('T49 ledger rows cannot be updated by the API',
  'UPDATE app.financial_transactions SET amount = 1', '42501');

-- ---------- Expenses -----------------------------------------------------
WITH c AS (INSERT INTO app.expense_categories (tenant_id, name) VALUES (test.id('A'), 'Utilities') RETURNING id)
SELECT test.put('expcat', id) FROM c;
SELECT test.ctx('staffA');
WITH e AS (INSERT INTO app.expenses (tenant_id, expense_no, category_id, description, amount, expense_date, business_unit_id, location_id)
           VALUES (test.id('A'), app.next_document_number('expense'), test.id('expcat'), 'Electricity token', 80000, current_date,
                   test.id('buA'), test.id('locA')) RETURNING id)
SELECT test.put('exp1', id) FROM e;
SELECT test.ctx('mgrA');
SELECT test.fails('T50 manager cannot approve expense above limit',
  format($$UPDATE app.expenses SET status='approved' WHERE id=%L$$, test.id('exp1')), 'BM007');
SELECT test.ctx('adminA');
UPDATE app.expenses SET status = 'approved' WHERE id = test.id('exp1');
UPDATE app.expenses SET status = 'paid', account_id = test.id('till'), paid_business_date = current_date WHERE id = test.id('exp1');
SELECT test.ok('T51 paid expense posts to ledger', (SELECT current_balance FROM app.financial_accounts WHERE id = test.id('till')) = 135000);
SELECT test.fails('T52 paid expense is final',
  format($$UPDATE app.expenses SET amount = 1 WHERE id=%L$$, test.id('exp1')), 'BM004');

-- ---------- Product price change -----------------------------------------
SELECT test.ctx('staffA');
SELECT test.fails('T53 staff cannot change a selling price',
  format($$UPDATE app.products SET selling_price = 1000 WHERE id=%L$$, test.id('charger')), 'BM007');
SELECT test.ctx('adminA');
SELECT set_config('app.change_note', 'supplier price rise', false);
UPDATE app.products SET selling_price = 16000 WHERE id = test.id('charger');
SELECT set_config('app.change_note', '', false);
SELECT test.ok('T54 price change history written automatically',
  (SELECT count(*) FROM app.product_price_changes WHERE product_id = test.id('charger') AND new_selling_price = 16000) = 1);

-- ---------- End of day ----------------------------------------------------
SELECT test.ctx('mgrA');
WITH d AS (INSERT INTO app.day_closings (tenant_id, location_id, business_date) VALUES (test.id('A'), test.id('locA'), current_date) RETURNING id)
SELECT test.put('day', id) FROM d;
SELECT test.ok('T55 expected cash computed from ledger',
  (SELECT expected FROM app.expected_account_balances(test.id('locA'), current_date) WHERE account_id = test.id('till')) = 135000);
SELECT test.fails('T56 cash difference requires a note',
  format($$UPDATE app.day_closings SET status='closed', expected_cash=135000, counted_cash=134000 WHERE id=%L$$, test.id('day')), '23514');
UPDATE app.day_closings SET status = 'closed', expected_cash = 135000, counted_cash = 134000, note = 'short 1000 - coins'
 WHERE id = test.id('day');
SELECT test.ctx('staffA');
SELECT test.fails('T57 no new sales on a closed day',
  format($$INSERT INTO app.sales (tenant_id, business_unit_id, location_id, business_date) VALUES (%L,%L,%L,current_date)$$,
         test.id('A'), test.id('buA'), test.id('locA')), 'BM002');
SELECT test.fails('T58 no payments on a closed day',
  format($$INSERT INTO app.payments_received (tenant_id, receipt_no, customer_id, business_unit_id, location_id, account_id, method,
           purpose, amount, business_date) VALUES (%L,'RC-Z',%L,%L,%L,%L,'cash','debt_payment',1,current_date)$$,
         test.id('A'), test.id('cust1'), test.id('buA'), test.id('locA'), test.id('till')), 'BM002');
SELECT test.ctx('mgrA');
SELECT test.fails('T59 manager cannot reopen a closed day',
  format($$UPDATE app.day_closings SET status='reopened', reopen_reason='late sale' WHERE id=%L$$, test.id('day')), 'BM007');
WITH a AS (INSERT INTO app.approval_requests (tenant_id, approval_type, reason, day_closing_id)
           VALUES (test.id('A'), 'day_reopen', 'Late M-Pesa sale', test.id('day')) RETURNING id)
SELECT test.put('appr_reopen', id) FROM a;
SELECT test.ctx('adminA');
UPDATE app.approval_requests SET status = 'approved' WHERE id = test.id('appr_reopen');
SELECT test.ctx('mgrA');
UPDATE app.day_closings SET status = 'reopened', reopen_reason = 'Late M-Pesa sale', reopen_approval_id = test.id('appr_reopen')
 WHERE id = test.id('day');
SELECT test.ok('T60 approved reopen unlocks the day', (SELECT status FROM app.day_closings WHERE id = test.id('day')) = 'reopened');

-- ---------- Services: project, stages, invoice ---------------------------
SELECT test.ctx('mgrA');
WITH s AS (INSERT INTO app.services (tenant_id, code, name, category, standard_price, is_package)
           VALUES (test.id('A'), 'STARTUP', 'Startup package', 'business_registration', 1500000, false) RETURNING id)
SELECT test.put('svc', id) FROM s;
INSERT INTO app.service_stages (tenant_id, service_id, seq, name) VALUES
 (test.id('A'), test.id('svc'), 1, 'Client intake'), (test.id('A'), test.id('svc'), 2, 'Branding');
WITH c AS (INSERT INTO app.customers (tenant_id, full_name, phone, client_status) VALUES (test.id('A'), 'Client X', '+255754000002', 'active') RETURNING id)
SELECT test.put('client', id) FROM c;
WITH p AS (INSERT INTO app.projects (tenant_id, project_no, customer_id, service_id, business_unit_id, title, owner_id, start_date, total_price)
           VALUES (test.id('A'), app.next_document_number('project'), test.id('client'), test.id('svc'), test.id('buB_A'),
                   'Client X Ltd', test.id('mgrA.m'), current_date, 1500000) RETURNING id)
SELECT test.put('proj', id) FROM p;
INSERT INTO app.project_stages (tenant_id, project_id, service_stage_id, seq, name, is_branding_stage)
SELECT test.id('A'), test.id('proj'), id, seq, name, seq = 2 FROM app.service_stages WHERE service_id = test.id('svc');
SELECT set_config('app.change_note', 'Docs collected', false);
UPDATE app.project_stages SET status = 'in_progress' WHERE project_id = test.id('proj') AND seq = 1;
UPDATE app.project_stages SET status = 'done' WHERE project_id = test.id('proj') AND seq = 1;
SELECT set_config('app.change_note', '', false);
SELECT test.ok('T61 stage changes write history with note; project moves to in_progress',
  (SELECT count(*) FROM app.project_stage_history h JOIN app.project_stages s ON s.id = h.project_stage_id
    WHERE s.project_id = test.id('proj')) = 2
  AND (SELECT status FROM app.projects WHERE id = test.id('proj')) = 'in_progress');
SELECT test.fails('T62 project cannot complete with open stages',
  format($$UPDATE app.projects SET status='completed' WHERE id=%L$$, test.id('proj')), 'BM001');
SELECT test.ctx('staffA');
SELECT test.fails('T63 staff cannot change a job price without approval',
  format($$UPDATE app.projects SET total_price = 1000000 WHERE id=%L$$, test.id('proj')), 'BM007');

-- Design job linked to the Branding stage, through client approval.
SELECT test.ctx('mgrA');
WITH j AS (INSERT INTO app.design_jobs (tenant_id, job_no, customer_id, business_unit_id, project_id, project_stage_id, job_type,
                                        title, brief, designer_id, price, revisions_included)
           SELECT test.id('A'), app.next_document_number('design_job'), test.id('client'), test.id('buB_A'), test.id('proj'), id,
                  'logo', 'Logo for Client X', 'Modern, blue', test.id('staff2A.m'), 200000, 3
             FROM app.project_stages WHERE project_id = test.id('proj') AND seq = 2 RETURNING id)
SELECT test.put('job', id) FROM j;
SELECT test.ctx('staff2A');
UPDATE app.design_jobs SET status = 'in_design' WHERE id = test.id('job');
WITH f AS (INSERT INTO app.files (tenant_id, storage_key, original_name, mime_type, size_bytes, sha256)
           VALUES (test.id('A'), test.id('A')::text || '/design/v1.png', 'v1.png', 'image/png', 1000, digest('v1', 'sha256')) RETURNING id)
SELECT test.put('f1', id) FROM f;
WITH v AS (INSERT INTO app.design_versions (tenant_id, design_job_id, version_no, preview_file_id)
           VALUES (test.id('A'), test.id('job'), 1, test.id('f1')) RETURNING id) SELECT test.put('v1', id) FROM v;
UPDATE app.design_versions SET status = 'sent_to_client' WHERE id = test.id('v1');
WITH l AS (INSERT INTO app.design_approval_links (tenant_id, design_job_id, design_version_id, token_hash, expires_at, sent_via)
           VALUES (test.id('A'), test.id('job'), test.id('v1'), digest('tok1', 'sha256'), now() + interval '14 days', 'whatsapp') RETURNING id)
SELECT test.put('link1', id) FROM l;
-- Client (no login): API resolves the token, sets tenant context only.
SELECT set_config('app.membership_id', '', false), set_config('app.user_id', '', false);
UPDATE app.design_approval_links SET decision = 'changes_requested' WHERE id = test.id('link1');
INSERT INTO app.design_feedback (tenant_id, design_job_id, design_version_id, author_type, approval_link_id, comment)
VALUES (test.id('A'), test.id('job'), test.id('v1'), 'client', test.id('link1'), 'Make the text bigger');
SELECT test.ok('T64 client change request: revision counted, card back to In Design',
  (SELECT revisions_used = 1 AND status = 'in_design' FROM app.design_jobs WHERE id = test.id('job')));
SELECT test.fails('T65 approval link is single-use',
  format($$UPDATE app.design_approval_links SET decision='approved' WHERE id=%L$$, test.id('link1')), 'BM004');
SELECT test.ctx('staff2A');
WITH f AS (INSERT INTO app.files (tenant_id, storage_key, original_name, mime_type, size_bytes, sha256)
           VALUES (test.id('A'), test.id('A')::text || '/design/v2.png', 'v2.png', 'image/png', 1000, digest('v2', 'sha256')) RETURNING id)
SELECT test.put('f2', id) FROM f;
WITH v AS (INSERT INTO app.design_versions (tenant_id, design_job_id, version_no, preview_file_id)
           VALUES (test.id('A'), test.id('job'), 2, test.id('f2')) RETURNING id) SELECT test.put('v2', id) FROM v;
UPDATE app.design_versions SET status = 'sent_to_client' WHERE id = test.id('v2');
WITH l AS (INSERT INTO app.design_approval_links (tenant_id, design_job_id, design_version_id, token_hash, expires_at, sent_via)
           VALUES (test.id('A'), test.id('job'), test.id('v2'), digest('tok2', 'sha256'), now() + interval '14 days', 'sms') RETURNING id)
SELECT test.put('link2', id) FROM l;
SELECT set_config('app.membership_id', '', false), set_config('app.user_id', '', false);
UPDATE app.design_approval_links SET decision = 'approved' WHERE id = test.id('link2');
SELECT test.ok('T66 client approval marks Final and job Approved',
  (SELECT status = 'approved' AND final_version_id = test.id('v2') FROM app.design_jobs WHERE id = test.id('job')));
SELECT test.ok('T67 branding stage auto-completes when its design jobs are approved',
  (SELECT status FROM app.project_stages WHERE project_id = test.id('proj') AND seq = 2) = 'done');
SELECT test.ctx('mgrA');
UPDATE app.projects SET status = 'completed' WHERE id = test.id('proj');
SELECT test.ok('T68 project completes once all stages done', (SELECT status FROM app.projects WHERE id = test.id('proj')) = 'completed');

-- Invoice + payment with deposit.
WITH i AS (INSERT INTO app.invoices (tenant_id, invoice_no, customer_id, business_unit_id, project_id, issue_date, due_date,
                                     subtotal, total)
           VALUES (test.id('A'), app.next_document_number('invoice'), test.id('client'), test.id('buB_A'), test.id('proj'),
                   current_date, current_date + 14, 1500000, 1500000) RETURNING id) SELECT test.put('inv', id) FROM i;
INSERT INTO app.invoice_lines (tenant_id, invoice_id, line_no, service_id, description, unit_price, line_total)
VALUES (test.id('A'), test.id('inv'), 1, test.id('svc'), 'Startup package', 1500000, 1500000);
UPDATE app.invoices SET status = 'issued', issued_at = now() WHERE id = test.id('inv');
WITH p AS (INSERT INTO app.payments_received (tenant_id, receipt_no, customer_id, business_unit_id, location_id, account_id,
                                              method, purpose, amount, reference, business_date)
           VALUES (test.id('A'), app.next_document_number('payment_receipt'), test.id('client'), test.id('buB_A'), test.id('locA'),
                   test.id('bank'), 'bank_transfer', 'deposit', 500000, 'CRDB-778812', current_date) RETURNING id)
SELECT test.put('pay_dep', id) FROM p;
INSERT INTO app.payment_allocations (tenant_id, payment_received_id, invoice_id, amount)
VALUES (test.id('A'), test.id('pay_dep'), test.id('inv'), 500000);
SELECT test.ok('T69 invoice deposit -> partially_paid', (SELECT status FROM app.invoices WHERE id = test.id('inv')) = 'partially_paid');
SELECT test.fails('T70 payment from another customer cannot settle this invoice',
  format($$WITH p AS (INSERT INTO app.payments_received (tenant_id, receipt_no, customer_id, business_unit_id, location_id, account_id,
           method, purpose, amount, business_date) VALUES (%L,'RC-W',%L,%L,%L,%L,'cash','installment',1000,current_date) RETURNING id)
           INSERT INTO app.payment_allocations (tenant_id, payment_received_id, invoice_id, amount) SELECT %L, id, %L, 1000 FROM p$$,
         test.id('A'), test.id('cust1'), test.id('buB_A'), test.id('locA'), test.id('till'), test.id('A'), test.id('inv')), 'BM008');

-- ---------- HR confidentiality -------------------------------------------
SELECT test.ctx('staffA');
SELECT test.ok('T71 staff sees only own salary row', (SELECT count(*) FROM app.employee_compensation) = 1
  AND (SELECT employee_id FROM app.employee_compensation) = test.id('empStaff'));
SELECT test.ctx('mgrA');
SELECT test.ok('T72 manager sees own salary only (others hidden by default)',
  (SELECT count(*) FROM app.employee_compensation) = 1);
SELECT test.ok('T73 manager can still see employee records (attendance/leave)', (SELECT count(*) FROM app.employees) = 3);
SELECT test.ctx('adminA');
SELECT test.ok('T74 admin sees all salaries', (SELECT count(*) FROM app.employee_compensation) = 3);
SELECT test.ctx('staffA');
SELECT test.fails('T75 staff cannot insert salary rows',
  format($$INSERT INTO app.employee_compensation (tenant_id, employee_id, effective_from, basic_salary, pay_method, reason)
           VALUES (%L,%L,current_date+400,9000000,'cash','self raise')$$, test.id('A'), test.id('empStaff')), '42501');
SELECT test.fails('T76 staff cannot insert own commission',
  format($$INSERT INTO app.commission_entries (tenant_id, employee_id, commission_rule_id, sale_id, amount, earned_on)
           SELECT %L, %L, id, %L, 99999, current_date FROM app.commission_rules LIMIT 1$$,
         test.id('A'), test.id('empStaff'), test.id('sale_phone')), '42501');

-- Leave.
SELECT test.ctx('adminA');
WITH lt AS (INSERT INTO app.leave_types (tenant_id, name) VALUES (test.id('A'), 'Annual') RETURNING id) SELECT test.put('lt', id) FROM lt;
SELECT test.ctx('staffA');
INSERT INTO app.leave_requests (tenant_id, employee_id, leave_type_id, start_date, end_date, days)
VALUES (test.id('A'), test.id('empStaff'), test.id('lt'), current_date + 10, current_date + 14, 5);
SELECT test.fails('T77 overlapping leave requests rejected',
  format($$INSERT INTO app.leave_requests (tenant_id, employee_id, leave_type_id, start_date, end_date, days)
           VALUES (%L,%L,%L,current_date+12,current_date+20,8)$$, test.id('A'), test.id('empStaff'), test.id('lt')), '23P01');

-- ---------- Payroll -------------------------------------------------------
SELECT test.ctx('mgrA');
SELECT test.fails('T78 manager cannot create payroll (RLS)',
  format($$INSERT INTO app.payroll_runs (tenant_id, period_year, period_month, location_id, business_unit_id)
           VALUES (%L,2026,11,%L,%L)$$, test.id('A'), test.id('locA'), test.id('buA')), '42501');
SELECT test.ctx('adminA');
WITH r AS (INSERT INTO app.payroll_runs (tenant_id, period_year, period_month, location_id, business_unit_id)
           VALUES (test.id('A'), 2026, 11, test.id('locA'), test.id('buA')) RETURNING id) SELECT test.put('run', id) FROM r;
INSERT INTO app.payroll_lines (tenant_id, payroll_run_id, employee_id, basic_salary, commission, pay_method)
VALUES (test.id('A'), test.id('run'), test.id('empStaff'), 400000, 300, 'cash'),
       (test.id('A'), test.id('run'), test.id('empStaff2'), 450000, 0, 'cash');
SELECT test.fails('T79 payroll generator cannot approve own run (SoD)',
  format($$UPDATE app.payroll_runs SET status='approved' WHERE id=%L$$, test.id('run')), 'BM007');
INSERT INTO app.membership_permission_overrides (tenant_id, membership_id, permission_code, effect, reason) VALUES
 (test.id('A'), test.id('mgrA.m'), 'hr.payroll.approve', 'allow', 'second approver'),
 (test.id('A'), test.id('mgrA.m'), 'hr.payroll.view', 'allow', 'second approver');
SELECT test.ctx('mgrA');
UPDATE app.payroll_runs SET status = 'approved' WHERE id = test.id('run');
SELECT test.ctx('adminA');
SELECT test.fails('T80 approved payroll lines are locked',
  format($$UPDATE app.payroll_lines SET bonuses = 100000 WHERE payroll_run_id=%L$$, test.id('run')), 'BM004');
UPDATE app.payroll_runs SET status = 'paid', account_id = test.id('bank'), paid_business_date = current_date WHERE id = test.id('run');
SELECT test.ok('T81 payroll pay posts one salary txn per employee',
  (SELECT count(*) FROM app.financial_transactions WHERE txn_type = 'salary') = 2
  AND (SELECT current_balance FROM app.financial_accounts WHERE id = test.id('bank')) = 5000000 + 500000 - 850300);
SELECT test.fails('T82 payroll cannot be paid twice',
  format($$UPDATE app.payroll_runs SET status='approved' WHERE id=%L$$, test.id('run')), 'BM001');
SELECT test.fails('T83 second payroll for same month rejected',
  format($$INSERT INTO app.payroll_runs (tenant_id, period_year, period_month, location_id, business_unit_id)
           VALUES (%L,2026,11,%L,%L)$$, test.id('A'), test.id('locA'), test.id('buA')), '23505');

-- ---------- IAM ----------------------------------------------------------
SELECT test.fails('T84 last active Admin cannot be removed',
  format($$UPDATE app.memberships SET status='suspended', deactivated_at=now() WHERE id=%L$$, test.id('adminA.m')), 'BM007');
SELECT test.ok('T85 has_permission honours user override',
  (SELECT count(*) FROM (SELECT 1) x WHERE (SELECT set_config('app.membership_id', test.id('mgrA.m')::text, false)) IS NOT NULL
     AND app.has_permission('hr.payroll.approve')) = 1);
SELECT test.ctx('adminA');

-- ---------- Tenant isolation ---------------------------------------------
SELECT test.ctx('adminB');
SELECT test.ok('T86 tenant B sees none of tenant A customers', (SELECT count(*) FROM app.customers) = 0);
SELECT test.ok('T87 tenant B sees none of tenant A sales or ledger',
  (SELECT count(*) FROM app.sales) = 0 AND (SELECT count(*) FROM app.financial_transactions) = 0);
SELECT test.fails('T88 cannot insert rows into another tenant',
  format($$INSERT INTO app.customers (tenant_id, full_name, is_shop_customer) VALUES (%L,'X',true)$$, test.id('A')), '42501');
SELECT test.fails('T89 cross-tenant reference blocked by composite FK',
  format($$INSERT INTO app.sales (tenant_id, business_unit_id, location_id, business_date) VALUES (%L,%L,%L,current_date)$$,
         test.id('B'), test.id('buB'), test.id('locA')), '23503');
DO $$ DECLARE n integer; BEGIN
  UPDATE app.products SET name = 'hacked' WHERE id = test.id('charger');
  GET DIAGNOSTICS n = ROW_COUNT;
  PERFORM test.ok('T90 tenant B cannot update tenant A rows (0 rows affected)', n = 0);
END $$;
SELECT set_config('app.tenant_id', '', false), set_config('app.membership_id', '', false), set_config('app.user_id', '', false);
SELECT test.ok('T91 no tenant context => no rows', (SELECT count(*) FROM app.products) = 0 AND (SELECT count(*) FROM app.tenants) = 0);
SELECT set_config('app.user_id', test.id('adminA.u')::text, false);
SELECT test.ok('T92 signed-in user (pre tenant pick) sees only own memberships',
  (SELECT count(*) FROM app.memberships) = 1 AND (SELECT count(*) FROM app.tenants) = 1);

-- ---------- Audit --------------------------------------------------------
SELECT test.ctx('staffA');
SELECT test.ok('T93 staff cannot read audit log', (SELECT count(*) FROM audit.audit_log) = 0);
SELECT test.ctx('adminA');
SELECT test.ok('T94 admin reads tenant audit trail with old/new values',
  EXISTS (SELECT 1 FROM audit.audit_log WHERE entity_table = 'products' AND entity_id = test.id('charger')
           AND action = 'update' AND old_values ->> 'selling_price' = '15000.00' AND new_values ->> 'selling_price' = '16000.00'
           AND actor_membership_id = test.id('adminA.m')));
SELECT test.ok('T95 audit contains no rows of tenant B for tenant A admin',
  NOT EXISTS (SELECT 1 FROM audit.audit_log WHERE tenant_id = test.id('B')));
SELECT test.fails('T96 API cannot modify audit rows', 'UPDATE audit.audit_log SET reason = $$x$$', '42501');
SELECT test.fails('T97 API cannot insert audit rows directly',
  $$INSERT INTO audit.audit_log (actor_type, action, row_hash) VALUES ('user','fake','\x00')$$, '42501');
SELECT audit.log_event('report.export', 'reports', NULL, 'success', 'P&L November');
SELECT test.ok('T98 explicit business event logged', EXISTS (SELECT 1 FROM audit.audit_log WHERE action = 'report.export'));
SELECT test.ok('T99 sensitive ciphertext never stored in audit',
  NOT EXISTS (SELECT 1 FROM audit.audit_log WHERE new_values::text LIKE '%\\x%' AND entity_table IN ('customers','employees')));

-- ---------- Reports ------------------------------------------------------
SELECT test.ok('T100 P&L view returns shop revenue for the month',
  (SELECT net_revenue FROM app.v_profit_and_loss_monthly WHERE business_unit_id = test.id('buA')
    AND month = date_trunc('month', current_date)::date) IS NOT NULL);
SELECT test.ok('T101 project profit = received - costs',
  (SELECT received FROM app.v_project_financials WHERE project_id = test.id('proj')) = 500000);
SELECT test.ok('T102 pending approvals view lists open leave',
  (SELECT count(*) FROM app.v_pending_approvals WHERE kind = 'leave') = 1);
SELECT test.ctx('staffA');
SELECT test.ok('T103 staff catalogue exposes no cost columns',
  NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'app' AND table_name = 'v_staff_catalog'
               AND column_name IN ('buying_price', 'average_cost')));

RESET ROLE;
-- ---------- Tamper evidence (superuser) ---------------------------------
SET ROLE bms_worker;
SELECT audit.seal_tenant(test.id('A'), interval '0');
SELECT test.ok('T104 sealed audit chain verifies intact', NOT EXISTS (SELECT 1 FROM audit.verify_tenant(test.id('A'))));
RESET ROLE;
SELECT test.fails('T105 even superuser UPDATE on audit hits immutability trigger',
  'UPDATE audit.audit_log SET reason = $$tamper$$ WHERE action = $$report.export$$', 'BM004');
SET session_replication_role = replica;   -- simulate an attacker disabling triggers
UPDATE audit.audit_log SET reason = 'tampered' WHERE action = 'report.export';
SET session_replication_role = origin;
SELECT test.ok('T106 tampering detected by hash chain verification',
  EXISTS (SELECT 1 FROM audit.verify_tenant(test.id('A')) WHERE problem LIKE '%altered%'));

-- ---------- Summary --------------------------------------------------------
SELECT count(*) FILTER (WHERE ok) AS passed, count(*) FILTER (WHERE NOT ok) AS failed, count(*) AS total FROM test.results;
SELECT label, detail FROM test.results WHERE NOT ok ORDER BY n;
