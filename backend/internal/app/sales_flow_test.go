package app_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"bms/internal/app"
	"bms/internal/config"
	"bms/internal/platform/clock"
)

func stockQty(t *testing.T, c *client, product string) decimal.Decimal {
	t.Helper()
	res := c.call(t, http.MethodGet, "/api/v1/stock/levels?per_page=100", nil, "")
	require.Equal(t, 200, res.code, res.raw)
	return dec(find(asList(res.body["data"]), "product_id", product)["qty_on_hand"])
}

func openTill(t *testing.T, c *client, account string) string {
	t.Helper()
	res := c.call(t, http.MethodPost, "/api/v1/tills/open", map[string]any{"money_account_id": account, "opening_float": "0"}, "")
	require.Equal(t, 201, res.code, res.raw)
	return str(res.data["id"])
}

func cashSale(t *testing.T, c *client, product, qty, account, amount string) result {
	t.Helper()
	return c.call(t, http.MethodPost, "/api/v1/sales", map[string]any{
		"items":    []map[string]any{{"product_id": product, "qty": qty}},
		"payments": []map[string]any{{"method": "cash", "money_account_id": account, "amount": amount}},
	}, key())
}

func TestSalesVoidReceiptAndPayments(t *testing.T) {
	e := newEnv(t, defaultNow)
	o := e.owner
	cash := e.cashAccount(t)
	rice := e.product(t, "Rice", "1000", "10", "500")
	ownerTill := openTill(t, o, cash)

	sale := cashSale(t, o, rice, "2", cash, "2000")
	require.Equal(t, 201, sale.code, sale.raw)
	saleID := str(sale.data["id"])
	require.True(t, stockQty(t, o, rice).Equal(decimal.NewFromInt(8)))

	// Filtered history and full detail.
	list := o.call(t, http.MethodGet, "/api/v1/sales?q="+str(sale.data["number"])+"&method=cash", nil, "")
	require.Equal(t, 200, list.code, list.raw)
	require.Len(t, asList(list.body["data"]), 1)
	require.EqualValues(t, 1, list.body["meta"].(map[string]any)["total"])
	detail := o.call(t, http.MethodGet, "/api/v1/sales/"+saleID, nil, "")
	require.Equal(t, 200, detail.code, detail.raw)
	require.Len(t, asList(detail.data["payments"]), 1)
	require.NotEmpty(t, asList(detail.data["audit"]))

	// First print is the original, the second is a copy.
	r1 := o.call(t, http.MethodGet, "/api/v1/sales/"+saleID+"/receipt", nil, "")
	require.Equal(t, 200, r1.code, r1.raw)
	require.Equal(t, false, r1.data["copy"])
	r2 := o.call(t, http.MethodGet, "/api/v1/sales/"+saleID+"/receipt", nil, "")
	require.Equal(t, true, r2.data["copy"])

	inv := o.call(t, http.MethodGet, "/api/v1/sales/"+saleID+"/invoice?format=pdf", nil, "")
	require.Equal(t, 200, inv.code)
	require.True(t, strings.HasPrefix(inv.raw, "%PDF"))

	// A cashier cannot void.
	staff, _ := e.user(t, "staff")
	denied := staff.call(t, http.MethodPost, "/api/v1/sales/"+saleID+"/void", map[string]any{"reason": "mistake"}, key())
	require.Equal(t, 403, denied.code, denied.raw)

	// Same-day void by the Owner: stock back, sale voided, cash reversed.
	void := o.call(t, http.MethodPost, "/api/v1/sales/"+saleID+"/void", map[string]any{"reason": "wrong item"}, key())
	require.Equal(t, 200, void.code, void.raw)
	require.Equal(t, "voided", void.data["status"])
	require.True(t, stockQty(t, o, rice).Equal(decimal.NewFromInt(10)))
	again := o.call(t, http.MethodPost, "/api/v1/sales/"+saleID+"/void", map[string]any{"reason": "again"}, key())
	require.Equal(t, 409, again.code, again.raw)
	tillRep := o.call(t, http.MethodGet, "/api/v1/tills/"+ownerTill, nil, "")
	require.Equal(t, 200, tillRep.code, tillRep.raw)
	require.True(t, dec(tillRep.data["expected_cash"]).IsZero(), tillRep.raw)

	// Credit sale, then payments against the sale.
	cust := o.call(t, http.MethodPost, "/api/v1/customers", map[string]any{"name": "Juma", "phone": uniquePhone(), "credit_limit": "100000"}, "")
	require.Equal(t, 201, cust.code, cust.raw)
	custID := str(cust.data["id"])
	credit := o.call(t, http.MethodPost, "/api/v1/sales", map[string]any{
		"customer_id": custID, "items": []map[string]any{{"product_id": rice, "qty": "3"}},
		"payments": []map[string]any{{"method": "cash", "money_account_id": cash, "amount": "1000"}},
	}, key())
	require.Equal(t, 201, credit.code, credit.raw)
	creditID := str(credit.data["id"])
	require.True(t, dec(credit.data["balance"]).Equal(decimal.NewFromInt(2000)))
	over := o.call(t, http.MethodPost, "/api/v1/sales/"+creditID+"/payments", map[string]any{"amount": "2500", "method": "cash", "money_account_id": cash}, key())
	require.Equal(t, 409, over.code, over.raw)
	pay := o.call(t, http.MethodPost, "/api/v1/sales/"+creditID+"/payments", map[string]any{"amount": "1500", "method": "cash", "money_account_id": cash}, key())
	require.Equal(t, 201, pay.code, pay.raw)
	require.True(t, dec(pay.data["balance"]).Equal(decimal.NewFromInt(500)))
	blocked := o.call(t, http.MethodPost, "/api/v1/sales/"+creditID+"/void", map[string]any{"reason": "x"}, key())
	require.Equal(t, 409, blocked.code, blocked.raw)

	// Customers: unpaid, statement, write-off, deactivate.
	unpaid := o.call(t, http.MethodGet, "/api/v1/customers/"+custID+"/unpaid", nil, "")
	require.Equal(t, 200, unpaid.code, unpaid.raw)
	require.Len(t, asList(unpaid.data["sales"]), 1)
	stmt := o.call(t, http.MethodGet, "/api/v1/customers/"+custID+"/statement", nil, "")
	require.Equal(t, 200, stmt.code, stmt.raw)
	require.True(t, dec(stmt.data["closing_balance"]).Equal(decimal.NewFromInt(500)), stmt.raw)
	require.Len(t, asList(stmt.data["lines"]), 3) // sale, payment at sale, later payment
	early := o.call(t, http.MethodPost, "/api/v1/customers/"+custID+"/deactivate", nil, "")
	require.Equal(t, 409, early.code, early.raw)
	noWO := staff.call(t, http.MethodPost, "/api/v1/customers/"+custID+"/write-off", map[string]any{"reason": "gone"}, key())
	require.Equal(t, 403, noWO.code, noWO.raw)
	wo := o.call(t, http.MethodPost, "/api/v1/customers/"+custID+"/write-off", map[string]any{"reason": "customer left town"}, key())
	require.Equal(t, 201, wo.code, wo.raw)
	require.True(t, dec(wo.data["amount"]).Equal(decimal.NewFromInt(500)))
	profile := o.call(t, http.MethodGet, "/api/v1/customers/"+custID, nil, "")
	require.True(t, dec(profile.data["balance"]).IsZero(), profile.raw)
	require.True(t, dec(profile.data["total_purchases"]).Equal(decimal.NewFromInt(3000)), profile.raw)
	deact := o.call(t, http.MethodPost, "/api/v1/customers/"+custID+"/deactivate", nil, "")
	require.Equal(t, 200, deact.code, deact.raw)
	custs := o.call(t, http.MethodGet, "/api/v1/customers?per_page=1", nil, "")
	require.Equal(t, 200, custs.code, custs.raw)
	require.Len(t, asList(custs.body["data"]), 1)
	require.EqualValues(t, 2, custs.body["meta"].(map[string]any)["total"])
	tb := o.call(t, http.MethodGet, "/api/v1/reports/trial-balance", nil, "")
	require.Equal(t, 200, tb.code, tb.raw)
	require.Equal(t, true, tb.data["balanced"], tb.raw)

	// The next business day the first credit sale can no longer be voided.
	next := laterApp(t, defaultNow.Add(24*time.Hour))
	oc := &client{app: next.Fiber, token: login(t, next.Fiber, e.phone, "correct-horse"), branch: e.branch}
	late := cashSale(t, o, rice, "1", cash, "1000")
	require.Equal(t, 201, late.code, late.raw)
	tooLate := oc.call(t, http.MethodPost, "/api/v1/sales/"+str(late.data["id"])+"/void", map[string]any{"reason": "late"}, key())
	require.Equal(t, 409, tooLate.code, tooLate.raw)
	require.Contains(t, tooLate.raw, "void_not_same_day")
}

func laterApp(t *testing.T, now time.Time) *app.App {
	t.Helper()
	cfg, err := config.Load()
	require.NoError(t, err)
	a, err := app.New(context.Background(), cfg, clock.Fixed{T: now})
	require.NoError(t, err)
	t.Cleanup(a.Close)
	return a
}

func TestSalesApprovalsAndScope(t *testing.T) {
	e := newEnv(t, defaultNow)
	o := e.owner
	cash := e.cashAccount(t)
	big := e.product(t, "Generator", "2000000", "2", "100")
	rice := e.product(t, "Rice", "1000", "20", "500")

	// A manager's void above their 1,000,000 limit waits for approval.
	mgr, _ := e.user(t, "manager")
	openTill(t, mgr, cash)
	s := cashSale(t, mgr, big, "1", cash, "2000000")
	require.Equal(t, 201, s.code, s.raw)
	v := mgr.call(t, http.MethodPost, "/api/v1/sales/"+str(s.data["id"])+"/void", map[string]any{"reason": "customer changed mind"}, key())
	require.Equal(t, 202, v.code, v.raw)
	reqID := str(v.data["approval_request_id"])
	dup := mgr.call(t, http.MethodPost, "/api/v1/sales/"+str(s.data["id"])+"/void", map[string]any{"reason": "again"}, key())
	require.Equal(t, 409, dup.code, dup.raw)
	ok := o.call(t, http.MethodPost, "/api/v1/approvals/"+reqID+"/approve", map[string]any{"note": "fine"}, key())
	require.Equal(t, 200, ok.code, ok.raw)
	got := o.call(t, http.MethodGet, "/api/v1/sales/"+str(s.data["id"]), nil, "")
	require.Equal(t, "voided", got.data["status"], got.raw)
	require.True(t, stockQty(t, o, big).Equal(decimal.NewFromInt(2)))

	// A cashier's discount above 5% waits; approval completes the sale.
	staff, _ := e.user(t, "staff")
	openTill(t, staff, cash)
	disc := staff.call(t, http.MethodPost, "/api/v1/sales", map[string]any{
		"items":    []map[string]any{{"product_id": rice, "qty": "1", "discount": map[string]any{"type": "percent", "value": "10"}}},
		"payments": []map[string]any{{"method": "cash", "money_account_id": cash, "amount": "900"}},
	}, key())
	require.Equal(t, 202, disc.code, disc.raw)
	ok = o.call(t, http.MethodPost, "/api/v1/approvals/"+str(disc.data["approval_request_id"])+"/approve", map[string]any{}, key())
	require.Equal(t, 200, ok.code, ok.raw)
	done := o.call(t, http.MethodGet, "/api/v1/sales/"+str(disc.data["id"]), nil, "")
	require.Equal(t, "completed", done.data["status"], done.raw)

	// Returns: list per sale, list all, detail; a return above the remainder is rejected.
	sale := cashSale(t, staff, rice, "2", cash, "2000")
	require.Equal(t, 201, sale.code, sale.raw)
	detail := o.call(t, http.MethodGet, "/api/v1/sales/"+str(sale.data["id"]), nil, "")
	item := str(asList(detail.data["items"])[0]["id"])
	ret := o.call(t, http.MethodPost, "/api/v1/sales/"+str(sale.data["id"])+"/returns", map[string]any{
		"reason": "broken bag", "refund_method": "cash", "money_account_id": cash, "items": []map[string]any{{"sale_item_id": item, "qty": "1"}},
	}, key())
	require.Equal(t, 201, ret.code, ret.raw)
	tooMany := o.call(t, http.MethodPost, "/api/v1/sales/"+str(sale.data["id"])+"/returns", map[string]any{
		"reason": "more", "refund_method": "cash", "money_account_id": cash, "items": []map[string]any{{"sale_item_id": item, "qty": "2"}},
	}, key())
	require.Equal(t, 409, tooMany.code, tooMany.raw)
	perSale := o.call(t, http.MethodGet, "/api/v1/sales/"+str(sale.data["id"])+"/returns", nil, "")
	require.Equal(t, 200, perSale.code, perSale.raw)
	require.Len(t, asList(perSale.body["data"]), 1)
	all := o.call(t, http.MethodGet, "/api/v1/sale-returns", nil, "")
	require.Equal(t, 200, all.code, all.raw)
	require.NotEmpty(t, asList(all.body["data"]))
	one := o.call(t, http.MethodGet, "/api/v1/sale-returns/"+str(ret.data["id"]), nil, "")
	require.Equal(t, 200, one.code, one.raw)
	require.Len(t, asList(one.data["items"]), 1)

	// Branch scope: a manager of branch B gets 404 on branch A's sale and till.
	b := o.call(t, http.MethodPost, "/api/v1/branches", map[string]any{"name": "Branch B", "code": "BB"}, "")
	require.Equal(t, 201, b.code, b.raw)
	mgrB, _ := e.user(t, "manager", str(b.data["id"]))
	miss := mgrB.call(t, http.MethodGet, "/api/v1/sales/"+str(sale.data["id"]), nil, "")
	require.Equal(t, 404, miss.code, miss.raw)
	tills := o.call(t, http.MethodGet, "/api/v1/tills?status=open", nil, "")
	require.Equal(t, 200, tills.code, tills.raw)
	anyTill := str(asList(tills.body["data"])[0]["id"])
	missTill := mgrB.call(t, http.MethodGet, "/api/v1/tills/"+anyTill, nil, "")
	require.Equal(t, 404, missTill.code, missTill.raw)
	noView := staff.call(t, http.MethodGet, "/api/v1/tills", nil, "")
	require.Equal(t, 403, noView.code, noView.raw)
}

func TestSalesHeldQuotationsAndTills(t *testing.T) {
	e := newEnv(t, defaultNow)
	o := e.owner
	cash := e.cashAccount(t)
	rice := e.product(t, "Rice", "1000", "10", "500")
	till := openTill(t, o, cash)

	// Held carts reserve no stock and are not sales.
	held := o.call(t, http.MethodPost, "/api/v1/sales/hold", map[string]any{"items": []map[string]any{{"product_id": rice, "qty": "4"}}, "note": "table 3"}, "")
	require.Equal(t, 201, held.code, held.raw)
	require.Equal(t, "held", held.data["status"])
	require.True(t, stockQty(t, o, rice).Equal(decimal.NewFromInt(10)))
	heldList := o.call(t, http.MethodGet, "/api/v1/sales/held", nil, "")
	require.Len(t, asList(heldList.body["data"]), 1)
	salesList := o.call(t, http.MethodGet, "/api/v1/sales", nil, "")
	require.Empty(t, asList(salesList.body["data"]))
	del := o.call(t, http.MethodDelete, "/api/v1/sales/held/"+str(held.data["id"]), nil, "")
	require.Equal(t, 204, del.code, del.raw)
	del2 := o.call(t, http.MethodDelete, "/api/v1/sales/held/"+str(held.data["id"]), nil, "")
	require.Equal(t, 404, del2.code, del2.raw)
	held2 := o.call(t, http.MethodPost, "/api/v1/sales/hold", map[string]any{"items": []map[string]any{{"product_id": rice, "qty": "1"}}}, "")
	resumed := o.call(t, http.MethodPost, "/api/v1/sales", map[string]any{
		"held_id": str(held2.data["id"]), "items": []map[string]any{{"product_id": rice, "qty": "1"}},
		"payments": []map[string]any{{"method": "cash", "money_account_id": cash, "amount": "1000"}},
	}, key())
	require.Equal(t, 201, resumed.code, resumed.raw)
	require.Empty(t, asList(o.call(t, http.MethodGet, "/api/v1/sales/held", nil, "").body["data"]))

	// Quotations: no stock or money effect; converting keeps the quoted price.
	q := o.call(t, http.MethodPost, "/api/v1/quotations", map[string]any{"items": []map[string]any{{"product_id": rice, "qty": "2"}}}, "")
	require.Equal(t, 201, q.code, q.raw)
	require.Equal(t, "draft", q.data["status"])
	qid := str(q.data["id"])
	require.True(t, stockQty(t, o, rice).Equal(decimal.NewFromInt(9)))
	upd := o.call(t, http.MethodPut, "/api/v1/quotations/"+qid, map[string]any{"items": []map[string]any{{"product_id": rice, "qty": "3"}}}, "")
	require.Equal(t, 200, upd.code, upd.raw)
	require.True(t, dec(upd.data["total"]).Equal(decimal.NewFromInt(3000)))
	price := o.call(t, http.MethodPut, "/api/v1/products/"+rice, map[string]any{"sell_price": "5000"}, "")
	require.Equal(t, 200, price.code, price.raw)
	qs := o.call(t, http.MethodGet, "/api/v1/quotations", nil, "")
	require.Len(t, asList(qs.body["data"]), 1)
	conv := o.call(t, http.MethodPost, "/api/v1/quotations/"+qid+"/convert", map[string]any{
		"payments": []map[string]any{{"method": "cash", "money_account_id": cash, "amount": "3000"}},
	}, key())
	require.Equal(t, 201, conv.code, conv.raw)
	require.True(t, dec(conv.data["total"]).Equal(decimal.NewFromInt(3000)), conv.raw)
	require.True(t, stockQty(t, o, rice).Equal(decimal.NewFromInt(6)))
	after := o.call(t, http.MethodGet, "/api/v1/quotations/"+qid, nil, "")
	require.Equal(t, "converted", after.data["status"], after.raw)
	twice := o.call(t, http.MethodPost, "/api/v1/quotations/"+qid+"/convert", map[string]any{}, key())
	require.Equal(t, 409, twice.code, twice.raw)
	locked := o.call(t, http.MethodPut, "/api/v1/quotations/"+qid, map[string]any{"items": []map[string]any{{"product_id": rice, "qty": "1"}}}, "")
	require.Equal(t, 409, locked.code, locked.raw)

	// Pay-out to the bank, then close with the expected cash.
	bank := o.call(t, http.MethodPost, "/api/v1/money-accounts", map[string]any{"name": "CRDB", "type": "bank"}, "")
	require.Equal(t, 201, bank.code, bank.raw)
	tooMuch := o.call(t, http.MethodPost, "/api/v1/tills/"+till+"/pay-out", map[string]any{"to_account_id": str(bank.data["id"]), "amount": "999999"}, key())
	require.Equal(t, 409, tooMuch.code, tooMuch.raw)
	po := o.call(t, http.MethodPost, "/api/v1/tills/"+till+"/pay-out", map[string]any{"to_account_id": str(bank.data["id"]), "amount": "2500", "reference": "deposit"}, key())
	require.Equal(t, 201, po.code, po.raw)
	rep := o.call(t, http.MethodGet, "/api/v1/tills/"+till, nil, "")
	require.Equal(t, 200, rep.code, rep.raw)
	require.True(t, dec(rep.data["payout_total"]).Equal(decimal.NewFromInt(2500)), rep.raw)
	require.True(t, dec(rep.data["expected_cash"]).Equal(decimal.NewFromInt(1500)), rep.raw)
	closeRes := o.call(t, http.MethodPost, "/api/v1/tills/"+till+"/close", map[string]any{"counted_cash": "1500"}, "")
	require.Equal(t, 200, closeRes.code, closeRes.raw)
	require.True(t, dec(closeRes.data["difference"]).IsZero(), closeRes.raw)
	cur := o.call(t, http.MethodGet, "/api/v1/tills/current", nil, "")
	require.Equal(t, 200, cur.code, cur.raw)
	require.Nil(t, cur.body["data"])
	list := o.call(t, http.MethodGet, "/api/v1/tills", nil, "")
	require.Len(t, asList(list.body["data"]), 1)
}
