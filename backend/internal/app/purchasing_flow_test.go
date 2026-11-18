package app_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestPurchasingFlow(t *testing.T) {
	e := newEnv(t, defaultNow)
	c := e.owner
	mgr, _ := e.user(t, "manager")

	sup := c.call(t, http.MethodPost, "/api/v1/suppliers", map[string]any{
		"name": "Mill " + uuid.NewString()[:6], "contact_person": "Juma", "phone": uniquePhone(), "payment_terms_days": 30,
	}, "")
	require.Equal(t, 201, sup.code, sup.raw)
	supID := str(sup.data["id"])
	dup := c.call(t, http.MethodPost, "/api/v1/suppliers", map[string]any{"name": sup.data["name"]}, "")
	require.Equal(t, 409, dup.code, dup.raw)
	badEmail := c.call(t, http.MethodPost, "/api/v1/suppliers", map[string]any{"name": "X", "email": "not-an-email"}, "")
	require.Equal(t, 422, badEmail.code, badEmail.raw)
	upd := c.call(t, http.MethodPut, "/api/v1/suppliers/"+supID, map[string]any{"name": sup.data["name"], "address": "Kariakoo", "payment_terms_days": 14}, "")
	require.Equal(t, 200, upd.code, upd.raw)
	require.EqualValues(t, 14, upd.data["payment_terms_days"])

	flour := e.product(t, "Flour", "80000", "", "")
	bank := c.call(t, http.MethodPost, "/api/v1/money-accounts", map[string]any{"name": "CRDB " + uuid.NewString()[:4], "type": "bank", "opening_balance": "10000000"}, "")
	require.Equal(t, 201, bank.code, bank.raw)
	bankID := str(bank.data["id"])

	// 100 x 60,000 is above the Manager's 5,000,000 limit: 202, then rejected back to draft.
	po := mgr.call(t, http.MethodPost, "/api/v1/purchase-orders", map[string]any{
		"supplier_id": supID, "expected_date": "2026-11-20", "items": []map[string]any{{"product_id": flour, "qty": "100", "unit_cost": "60000"}},
	}, "")
	require.Equal(t, 201, po.code, po.raw)
	poID := str(po.data["id"])
	sub := mgr.call(t, http.MethodPost, "/api/v1/purchase-orders/"+poID+"/submit", nil, "")
	require.Equal(t, 202, sub.code, sub.raw)
	early := mgr.call(t, http.MethodPost, "/api/v1/goods-receipts", map[string]any{"po_id": poID, "items": []map[string]any{{"product_id": flour, "qty": "1", "unit_cost": "60000"}}}, key())
	require.Equal(t, 409, early.code, early.raw)
	rej := c.call(t, http.MethodPost, "/api/v1/approvals/"+str(sub.data["approval_request_id"])+"/reject", map[string]any{"note": "too much"}, key())
	require.Equal(t, 200, rej.code, rej.raw)
	got := mgr.call(t, http.MethodGet, "/api/v1/purchase-orders/"+poID, nil, "")
	require.Equal(t, "draft", str(got.data["status"]), got.raw)
	edit := mgr.call(t, http.MethodPut, "/api/v1/purchase-orders/"+poID, map[string]any{
		"supplier_id": supID, "items": []map[string]any{{"product_id": flour, "qty": "50", "unit_cost": "60000"}},
	}, "")
	require.Equal(t, 200, edit.code, edit.raw)
	require.True(t, dec(edit.data["total"]).Equal(invD("3000000")))
	sub2 := mgr.call(t, http.MethodPost, "/api/v1/purchase-orders/"+poID+"/submit", nil, "")
	require.Equal(t, 200, sub2.code, sub2.raw)
	editApproved := mgr.call(t, http.MethodPut, "/api/v1/purchase-orders/"+poID, map[string]any{"supplier_id": supID, "items": []map[string]any{{"product_id": flour, "qty": "1", "unit_cost": "1"}}}, "")
	require.Equal(t, 409, editApproved.code, editApproved.raw)

	// Receive 30 with 30,000 extra cost spread by value: unit cost becomes 61,000 (FR-PUR-08).
	r1 := mgr.call(t, http.MethodPost, "/api/v1/goods-receipts", map[string]any{
		"po_id": poID, "extra_costs": "30000", "delivery_note": "DN-1", "items": []map[string]any{{"product_id": flour, "qty": "30", "unit_cost": "60000"}},
	}, key())
	require.Equal(t, 201, r1.code, r1.raw)
	require.Equal(t, "partially_received", str(r1.data["po_status"]))
	require.True(t, dec(r1.data["total_value"]).Equal(invD("1830000")))
	grn := mgr.call(t, http.MethodGet, "/api/v1/goods-receipts/"+str(r1.data["id"]), nil, "")
	require.Equal(t, 200, grn.code, grn.raw)
	grnItem := asList(grn.data["items"])[0]
	require.True(t, dec(grnItem["unit_cost"]).Equal(invD("61000")), grn.raw)
	tooMuch := mgr.call(t, http.MethodPost, "/api/v1/goods-receipts", map[string]any{"po_id": poID, "items": []map[string]any{{"product_id": flour, "qty": "21", "unit_cost": "60000"}}}, key())
	require.Equal(t, 409, tooMuch.code, tooMuch.raw)

	detail := mgr.call(t, http.MethodGet, "/api/v1/purchase-orders/"+poID, nil, "")
	line := asList(detail.data["items"])[0]
	require.True(t, dec(line["qty_outstanding"]).Equal(invD("20")))
	require.Len(t, asList(detail.data["receipts"]), 1)

	cancel := mgr.call(t, http.MethodPost, "/api/v1/purchase-orders/"+poID+"/cancel", nil, "")
	require.Equal(t, 409, cancel.code, cancel.raw)
	closed := mgr.call(t, http.MethodPost, "/api/v1/purchase-orders/"+poID+"/close", nil, "")
	require.Equal(t, 200, closed.code, closed.raw)
	afterClose := mgr.call(t, http.MethodPost, "/api/v1/goods-receipts", map[string]any{"po_id": poID, "items": []map[string]any{{"product_id": flour, "qty": "1", "unit_cost": "60000"}}}, key())
	require.Equal(t, 409, afterClose.code, afterClose.raw)
	closedList := c.call(t, http.MethodGet, "/api/v1/purchase-orders?status=closed&supplier_id="+supID, nil, "")
	require.Len(t, asList(closedList.body["data"]), 1, closedList.raw)

	// A draft order with nothing received can be cancelled.
	po2 := c.call(t, http.MethodPost, "/api/v1/purchase-orders", map[string]any{"supplier_id": supID, "items": []map[string]any{{"product_id": flour, "qty": "5", "unit_cost": "1000"}}}, "")
	require.Equal(t, 201, po2.code, po2.raw)
	cancel2 := c.call(t, http.MethodPost, "/api/v1/purchase-orders/"+str(po2.data["id"])+"/cancel", nil, "")
	require.Equal(t, 200, cancel2.code, cancel2.raw)

	pdfReq := httptest.NewRequest(http.MethodGet, "/api/v1/purchase-orders/"+poID+"/pdf", nil)
	pdfReq.Header.Set("Authorization", "Bearer "+c.token)
	pdfReq.Header.Set("X-Branch-Id", c.branch)
	resp, err := c.app.Test(pdfReq, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.Equal(t, 200, resp.StatusCode)
	require.Equal(t, "application/pdf", resp.Header.Get("Content-Type"))
	require.True(t, strings.HasPrefix(string(body), "%PDF-"))

	// Invoice the receipt, then return 5 units: the invoice balance drops by 5 x 61,000.
	inv := c.call(t, http.MethodPost, "/api/v1/purchase-invoices", map[string]any{
		"supplier_id": supID, "receipt_ids": []string{str(r1.data["id"])}, "supplier_ref": "INV-" + uuid.NewString()[:6],
	}, key())
	require.Equal(t, 201, inv.code, inv.raw)
	invID := str(inv.data["id"])
	require.True(t, dec(inv.data["balance"]).Equal(invD("1830000")))
	require.Equal(t, "2026-11-30", str(inv.data["due_date"]), "invoice date plus 14 days")
	again := c.call(t, http.MethodPost, "/api/v1/purchase-invoices", map[string]any{"supplier_id": supID, "receipt_ids": []string{str(r1.data["id"])}}, key())
	require.Equal(t, 409, again.code, again.raw)

	ret := c.call(t, http.MethodPost, "/api/v1/purchase-returns", map[string]any{
		"receipt_id": r1.data["id"], "reason": "Wet bags", "settlement": "reduce_debt",
		"items": []map[string]any{{"receipt_item_id": grnItem["id"], "qty": "5"}},
	}, key())
	require.Equal(t, 201, ret.code, ret.raw)
	require.True(t, dec(ret.data["total"]).Equal(invD("305000")), ret.raw)
	tooMany := c.call(t, http.MethodPost, "/api/v1/purchase-returns", map[string]any{
		"receipt_id": r1.data["id"], "reason": "x", "settlement": "reduce_debt", "items": []map[string]any{{"receipt_item_id": grnItem["id"], "qty": "26"}},
	}, key())
	require.Equal(t, 409, tooMany.code, tooMany.raw)
	invAfter := c.call(t, http.MethodGet, "/api/v1/purchase-invoices/"+invID, nil, "")
	require.Equal(t, 200, invAfter.code, invAfter.raw)
	require.True(t, dec(invAfter.data["balance"]).Equal(invD("1525000")), invAfter.raw)
	levels := c.call(t, http.MethodGet, "/api/v1/stock/levels", nil, "")
	require.True(t, dec(find(asList(levels.body["data"]), "product_id", flour)["qty_on_hand"]).Equal(invD("25")))
	retGet := c.call(t, http.MethodGet, "/api/v1/purchase-returns/"+str(ret.data["id"]), nil, "")
	require.Equal(t, 200, retGet.code, retGet.raw)
	require.Len(t, asList(retGet.data["items"]), 1)
	voidReturned := c.call(t, http.MethodPost, "/api/v1/purchase-invoices/"+invID+"/void", map[string]any{"reason": "x"}, key())
	require.Equal(t, 409, voidReturned.code, voidReturned.raw)

	// Direct receipt without a PO, invoiced and paid on the spot (FR-PUR-06, FR-PUR-10).
	direct := c.call(t, http.MethodPost, "/api/v1/goods-receipts", map[string]any{
		"supplier_id": supID, "items": []map[string]any{{"product_id": flour, "qty": "10", "unit_cost": "5000"}},
	}, key())
	require.Equal(t, 201, direct.code, direct.raw)
	require.Equal(t, "received", str(direct.data["po_status"]))
	refund := c.call(t, http.MethodPost, "/api/v1/purchase-returns", map[string]any{
		"receipt_id": direct.data["id"], "reason": "x", "settlement": "refund", "money_account_id": bankID,
		"items": []map[string]any{{"receipt_item_id": uuid.NewString(), "qty": "1"}},
	}, key())
	require.Equal(t, 409, refund.code, refund.raw)
	cashInv := c.call(t, http.MethodPost, "/api/v1/purchase-invoices", map[string]any{
		"supplier_id": supID, "receipt_ids": []string{str(direct.data["id"])}, "tax_total": "9000",
		"pay_now": map[string]any{"money_account_id": bankID, "method": "bank"},
	}, key())
	require.Equal(t, 201, cashInv.code, cashInv.raw)
	require.True(t, dec(cashInv.data["balance"]).IsZero(), cashInv.raw)
	voidPaid := c.call(t, http.MethodPost, "/api/v1/purchase-invoices/"+str(cashInv.data["id"])+"/void", nil, key())
	require.Equal(t, 409, voidPaid.code, voidPaid.raw)

	// An unpaid invoice can be voided once, and its receipt is free to invoice again.
	direct2 := c.call(t, http.MethodPost, "/api/v1/goods-receipts", map[string]any{"supplier_id": supID, "items": []map[string]any{{"product_id": flour, "qty": "2", "unit_cost": "5000"}}}, key())
	require.Equal(t, 201, direct2.code, direct2.raw)
	inv3 := c.call(t, http.MethodPost, "/api/v1/purchase-invoices", map[string]any{"supplier_id": supID, "receipt_ids": []string{str(direct2.data["id"])}}, key())
	require.Equal(t, 201, inv3.code, inv3.raw)
	void := c.call(t, http.MethodPost, "/api/v1/purchase-invoices/"+str(inv3.data["id"])+"/void", map[string]any{"reason": "Wrong supplier"}, key())
	require.Equal(t, 200, void.code, void.raw)
	voidAgain := c.call(t, http.MethodPost, "/api/v1/purchase-invoices/"+str(inv3.data["id"])+"/void", nil, key())
	require.Equal(t, 409, voidAgain.code, voidAgain.raw)
	reInv := c.call(t, http.MethodPost, "/api/v1/purchase-invoices", map[string]any{"supplier_id": supID, "receipt_ids": []string{str(direct2.data["id"])}}, key())
	require.Equal(t, 201, reInv.code, reInv.raw)

	// Supplier payment is allocated oldest invoice first.
	pay := c.call(t, http.MethodPost, "/api/v1/suppliers/"+supID+"/payments", map[string]any{"amount": "1000000", "method": "bank", "money_account_id": bankID}, key())
	require.Equal(t, 201, pay.code, pay.raw)
	require.True(t, dec(pay.data["allocated"]).Equal(invD("1000000")), pay.raw)
	invPaid := c.call(t, http.MethodGet, "/api/v1/purchase-invoices/"+invID, nil, "")
	require.True(t, dec(invPaid.data["balance"]).Equal(invD("525000")), invPaid.raw)
	require.Len(t, asList(invPaid.data["payments"]), 1)

	unpaid := c.call(t, http.MethodGet, "/api/v1/suppliers/"+supID+"/unpaid", nil, "")
	require.Equal(t, 200, unpaid.code, unpaid.raw)
	require.Len(t, asList(unpaid.body["data"]), 2)
	profile := c.call(t, http.MethodGet, "/api/v1/suppliers/"+supID, nil, "")
	require.Equal(t, 200, profile.code, profile.raw)
	owed := dec(profile.data["balance"])
	require.True(t, owed.Equal(invD("535000")), profile.raw) // 525,000 + 10,000
	stmt := c.call(t, http.MethodGet, "/api/v1/suppliers/"+supID+"/statement", nil, "")
	require.Equal(t, 200, stmt.code, stmt.raw)
	require.True(t, dec(stmt.data["closing_balance"]).Equal(owed), stmt.raw)
	debts := c.call(t, http.MethodGet, "/api/v1/suppliers/debts", nil, "")
	require.Equal(t, 200, debts.code, debts.raw)
	require.True(t, dec(find(asList(debts.data["suppliers"]), "supplier_id", supID)["0_30"]).Equal(owed), debts.raw)
	list := c.call(t, http.MethodGet, "/api/v1/suppliers?q=Mill", nil, "")
	require.True(t, dec(find(asList(list.body["data"]), "id", supID)["balance"]).Equal(owed))
	purchases := c.call(t, http.MethodGet, "/api/v1/suppliers/"+supID+"/purchases", nil, "")
	require.Len(t, asList(purchases.data["purchase_invoices"]), 4)

	invList := c.call(t, http.MethodGet, "/api/v1/purchase-invoices?unpaid=true", nil, "")
	require.Len(t, asList(invList.body["data"]), 2, invList.raw)
	grnList := c.call(t, http.MethodGet, "/api/v1/goods-receipts?supplier_id="+supID, nil, "")
	require.Len(t, asList(grnList.body["data"]), 3)
	retList := c.call(t, http.MethodGet, "/api/v1/purchase-returns", nil, "")
	require.Len(t, asList(retList.body["data"]), 1)

	// Suggested order lines come from low stock.
	sugar := c.call(t, http.MethodPost, "/api/v1/products", map[string]any{"name": "Sugar", "sell_price": "3000", "reorder_level": "10"}, "")
	require.Equal(t, 201, sugar.code, sugar.raw)
	suggest := c.call(t, http.MethodGet, "/api/v1/purchase-orders/suggest?supplier_id="+supID, nil, "")
	require.Equal(t, 200, suggest.code, suggest.raw)
	require.True(t, dec(find(asList(suggest.body["data"]), "product_id", str(sugar.data["id"]))["qty"]).Equal(invD("20")))

	deact := c.call(t, http.MethodPost, "/api/v1/suppliers/"+supID+"/deactivate", nil, "")
	require.Equal(t, 200, deact.code, deact.raw)
	inactive := c.call(t, http.MethodPost, "/api/v1/purchase-orders", map[string]any{"supplier_id": supID, "items": []map[string]any{{"product_id": flour, "qty": "1", "unit_cost": "1"}}}, "")
	require.Equal(t, 409, inactive.code, inactive.raw)

	// Permissions and branch scope.
	staff, _ := e.user(t, "staff")
	denied := staff.call(t, http.MethodGet, "/api/v1/purchase-orders", nil, "")
	require.Equal(t, 403, denied.code, denied.raw)
	br := c.call(t, http.MethodPost, "/api/v1/branches", map[string]any{"name": "Branch P", "code": "BP"}, "")
	require.Equal(t, 201, br.code, br.raw)
	sup2 := c.call(t, http.MethodPost, "/api/v1/suppliers", map[string]any{"name": "Other " + uuid.NewString()[:6]}, "")
	cB := &client{app: c.app, token: c.token, branch: str(br.data["id"])}
	poB := cB.call(t, http.MethodPost, "/api/v1/purchase-orders", map[string]any{"supplier_id": sup2.data["id"], "items": []map[string]any{{"product_id": flour, "qty": "1", "unit_cost": "1"}}}, "")
	require.Equal(t, 201, poB.code, poB.raw)
	hidden := mgr.call(t, http.MethodGet, "/api/v1/purchase-orders/"+str(poB.data["id"]), nil, "")
	require.Equal(t, 404, hidden.code, hidden.raw)
	hiddenSubmit := mgr.call(t, http.MethodPost, "/api/v1/purchase-orders/"+str(poB.data["id"])+"/submit", nil, "")
	require.Equal(t, 404, hiddenSubmit.code, hiddenSubmit.raw)
}
