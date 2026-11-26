package app_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func stockQty(t *testing.T, c *client, product string) decimal.Decimal {
	t.Helper()
	res := c.call(t, http.MethodGet, "/api/v1/stock/levels?per_page=100", nil, "")
	require.Equal(t, 200, res.code, res.raw)
	return dec(find(asList(res.body["data"]), "product_id", product)["qty_on_hand"])
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
	require.Equal(t, "0.00", accountBalance(t, o, cash))

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
	blocked := o.call(t, http.MethodPost, "/api/v1/sales/"+creditID+"/void", map[string]any{"reason": "wrong sale"}, key())
	require.Equal(t, 409, blocked.code, blocked.raw)

	// Customers: unpaid, statement, deactivate blocked while owing, pay remainder, then deactivate.
	unpaid := o.call(t, http.MethodGet, "/api/v1/customers/"+custID+"/unpaid", nil, "")
	require.Equal(t, 200, unpaid.code, unpaid.raw)
	require.Len(t, asList(unpaid.data["sales"]), 1)
	stmt := o.call(t, http.MethodGet, "/api/v1/customers/"+custID+"/statement", nil, "")
	require.Equal(t, 200, stmt.code, stmt.raw)
	require.True(t, dec(stmt.data["closing_balance"]).Equal(decimal.NewFromInt(500)), stmt.raw)
	require.Len(t, asList(stmt.data["lines"]), 3) // sale, payment at sale, later payment
	early := o.call(t, http.MethodPost, "/api/v1/customers/"+custID+"/deactivate", nil, "")
	require.Equal(t, 409, early.code, early.raw)
	payRest := o.call(t, http.MethodPost, "/api/v1/sales/"+creditID+"/payments", map[string]any{"amount": "500", "method": "cash", "money_account_id": cash}, key())
	require.Equal(t, 201, payRest.code, payRest.raw)
	profile := o.call(t, http.MethodGet, "/api/v1/customers/"+custID, nil, "")
	require.True(t, dec(profile.data["balance"]).IsZero(), profile.raw)
	require.True(t, dec(profile.data["total_purchases"]).Equal(decimal.NewFromInt(3000)), profile.raw)
	deact := o.call(t, http.MethodPost, "/api/v1/customers/"+custID+"/deactivate", nil, "")
	require.Equal(t, 200, deact.code, deact.raw)

	// Once a business day is closed, sales from that day can no longer be voided.
	late := cashSale(t, o, rice, "1", cash, "1000")
	require.Equal(t, 201, late.code, late.raw)
	closed := o.call(t, http.MethodPost, "/api/v1/day-closings", map[string]any{"date": "2026-11-16", "counted_cash": "4000", "note": "End of day"}, "")
	require.Equal(t, 201, closed.code, closed.raw)
	tooLate := o.call(t, http.MethodPost, "/api/v1/sales/"+str(late.data["id"])+"/void", map[string]any{"reason": "late void"}, key())
	require.Equal(t, 409, tooLate.code, tooLate.raw)
}

func TestSalesApprovalsAndScope(t *testing.T) {
	e := newEnv(t, defaultNow)
	o := e.owner
	cash := e.cashAccount(t)
	big := e.product(t, "Generator", "2000000", "2", "100")
	rice := e.product(t, "Rice", "1000", "20", "500")

	// A manager's void requests Admin approval (sale_void -> Admin); approval voids the sale and restores stock.
	mgr, _ := e.user(t, "manager")
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
	disc := staff.call(t, http.MethodPost, "/api/v1/sales", map[string]any{
		"items":    []map[string]any{{"product_id": rice, "qty": "1", "discount": map[string]any{"type": "percent", "value": "10"}}},
		"payments": []map[string]any{{"method": "cash", "money_account_id": cash, "amount": "900"}},
	}, key())
	require.Equal(t, 202, disc.code, disc.raw)
	ok = o.call(t, http.MethodPost, "/api/v1/approvals/"+str(disc.data["approval_request_id"])+"/approve", map[string]any{}, key())
	require.Equal(t, 200, ok.code, ok.raw)
	comp := staff.call(t, http.MethodPost, "/api/v1/sales/"+str(disc.data["id"])+"/complete", map[string]any{
		"payments": []map[string]any{{"method": "cash", "money_account_id": cash, "amount": "900"}},
	}, key())
	require.Equal(t, 200, comp.code, comp.raw)
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

	// Removed tills endpoints return 410.
	require.Equal(t, 410, o.call(t, http.MethodPost, "/api/v1/tills/open", map[string]any{"money_account_id": cash, "opening_float": "0"}, "").code)
}

func TestSalesHeldQuotationsAndTills(t *testing.T) {
	e := newEnv(t, defaultNow)
	o := e.owner
	cash := e.cashAccount(t)
	rice := e.product(t, "Rice", "1000", "10", "500")

	// Held carts reserve no stock and are not completed sales.
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

	// Removed shop quotations return 410; GET /tills stays empty while mutating till routes return 410.
	require.Equal(t, 410, o.call(t, http.MethodPost, "/api/v1/quotations", map[string]any{"items": []map[string]any{{"product_id": rice, "qty": "2"}}}, "").code)
	tills := o.call(t, http.MethodGet, "/api/v1/tills", nil, "")
	require.Equal(t, 200, tills.code, tills.raw)
	require.Empty(t, asList(tills.body["data"]))
	require.Equal(t, 410, o.call(t, http.MethodPost, "/api/v1/tills/open", map[string]any{"money_account_id": cash, "opening_float": "0"}, "").code)
}
