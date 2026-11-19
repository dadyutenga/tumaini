#!/usr/bin/env bash
# Two POS sessions race to sell the LAST unit of a product.
# Expected: first commits; second blocks on the stock_balances row lock,
# then fails with BM003 (insufficient stock). Run after 01_suite.sql.
set -euo pipefail
DB="${1:-bms_test}"
PSQL="psql -X -q -At -v ON_ERROR_STOP=1 -d $DB"

# Setup (admin): product with exactly one unit in stock.
$PSQL <<'SQL'
SET ROLE bms_app;
SELECT test.ctx('adminA');
WITH p AS (INSERT INTO app.products (tenant_id, business_unit_id, name, selling_price)
           VALUES (test.id('A'), test.id('buA'), 'Last One', 5000) RETURNING id)
SELECT test.put('last', id) FROM p;
WITH a AS (INSERT INTO app.stock_adjustments (tenant_id, adjustment_no, location_id, reason, business_date)
           VALUES (test.id('A'), app.next_document_number('stock_adjustment'), test.id('locA'), 'found', current_date) RETURNING id)
SELECT test.put('adj_last', id) FROM a;
INSERT INTO app.stock_adjustment_lines (tenant_id, stock_adjustment_id, product_id, quantity_delta, unit_cost)
VALUES (test.id('A'), test.id('adj_last'), test.id('last'), 1, 3000);
UPDATE app.stock_adjustments SET status = 'posted', posted_at = now() WHERE id = test.id('adj_last');
SQL

sell() {  # $1 = label, $2 = sleep seconds before COMMIT
$PSQL <<SQL
SET ROLE bms_app;
BEGIN;
SELECT test.ctx('staffA');
WITH s AS (INSERT INTO app.sales (tenant_id, business_unit_id, location_id, business_date, customer_id, due_date)
           VALUES (test.id('A'), test.id('buA'), test.id('locA'), current_date, test.id('cust1'), current_date + 7) RETURNING id)
INSERT INTO app.sale_lines (tenant_id, sale_id, line_no, product_id, product_name, quantity, unit_price, line_total)
SELECT test.id('A'), id, 1, test.id('last'), 'Last One', 1, 5000, 5000 FROM s;
UPDATE app.sales SET subtotal = 5000, total = 5000 WHERE status = 'draft' AND sold_by = test.id('staffA.m')
  AND id IN (SELECT sale_id FROM app.sale_lines WHERE product_id = test.id('last'));
UPDATE app.sales SET status = 'completed', receipt_no = app.next_document_number('sale_receipt')
 WHERE status = 'draft' AND id IN (SELECT sale_id FROM app.sale_lines WHERE product_id = test.id('last'));
SELECT pg_sleep($2);
COMMIT;
SQL
}

( sell A 3 > /tmp/raceA.out 2>&1; echo "A exit=$?" >> /tmp/raceA.out ) &
sleep 1
( sell B 0 > /tmp/raceB.out 2>&1; echo "B exit=$?" >> /tmp/raceB.out ) &
wait

echo "--- session A"; cat /tmp/raceA.out
echo "--- session B"; cat /tmp/raceB.out
QTY=$($PSQL -c "SET row_security = off; SELECT quantity_on_hand FROM app.stock_balances b JOIN test.ids i ON i.id = b.product_id AND i.name = 'last'")
SOLD=$($PSQL -c "SET row_security = off; SELECT count(*) FROM app.sales s JOIN app.sale_lines l ON l.sale_id = s.id JOIN test.ids i ON i.id = l.product_id AND i.name = 'last' WHERE s.status = 'completed'")
echo "final stock=$QTY completed_sales=$SOLD"
if [[ "$QTY" == "0.000" && "$SOLD" == "1" ]] && grep -q "BM003\|insufficient stock" /tmp/raceB.out; then
  echo "CONCURRENCY TEST: PASS"
else
  echo "CONCURRENCY TEST: FAIL"; exit 1
fi
