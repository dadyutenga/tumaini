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

	flour := e.product(t, "Flour", "80000", "", "")
	bank := c.call(t, http.MethodPost, "/api/v1/money-accounts", map[string]any{"name": "CRDB " + uuid.NewString()[:4], "type": "bank", "opening_balance": "10000000"}, "")
	require.Equal(t, 201, bank.code, bank.raw)
	bankID := str(bank.data["id"])

	// Draft PO can be edited, then submitted directly to ordered; ordered PO cannot be edited.
	po := mgr.call(t, http.MethodPost, "/api/v1/purchase-orders", map[string]any{
		"supplier_id": supID, "expected_date": "2026-11-20", "items": []map[string]any{{"product_id": flour, "qty": "100", "unit_cost": "60000"}},
	}, "")
	require.Equal(t, 201, po.code, po.raw)
	poID := str(po.data["id"])
	early := mgr.call(t, http.MethodPost, "/api/v1/goods-receipts", map[string]any{"po_id": poID, "items": []map[string]any{{"product_id": flour, "qty": "1", "unit_cost": "60000"}}}, key())
	require.Equal(t, 409, early.code, early.raw)
	edit := mgr.call(t, http.MethodPut, "/api/v1/purchase-orders/"+poID, map[string]any{
		"supplier_id": supID, "items": []map[string]any{{"product_id": flour, "qty": "50", "unit_cost": "60000"}},
	}, "")
	require.Equal(t, 200, edit.code, edit.raw)
	require.True(t, dec(edit.data["total"]).Equal(invD("3000000")))
	sub := mgr.call(t, http.MethodPost, "/api/v1/purchase-orders/"+poID+"/submit", nil, "")
	require.Equal(t, 200, sub.code, sub.raw)
	require.Equal(t, "ordered", str(sub.data["status"]))
	editOrdered := mgr.call(t, http.MethodPut, "/api/v1/purchase-orders/"+poID, map[string]any{"supplier_id": supID, "items": []map[string]any{{"product_id": flour, "qty": "1", "unit_cost": "1"}}}, "")
	require.Equal(t, 409, editOrdered.code, editOrdered.raw)

	// Receive 30 with 30,000 extra cost spread by value: unit cost becomes 61,000.
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

	// A draft order with nothing received can be cancelled.
	po2 := c.call(t, http.MethodPost, "/api/v1/purchase-orders", map[string]any{"supplier_id": supID, "items": []map[string]any{{"product_id": flour, "qty": "5", "unit_cost": "1000"}}}, "")
	require.Equal(t, 201, po2.code, po2.raw)
	cancel2 := c.call(t, http.MethodPost, "/api/v1/purchase-orders/"+str(po2.data["id"])+"/cancel", nil, "")
	require.Equal(t, 200, cancel2.code, cancel2.raw)

	// PO PDF rendering works.
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

	// Removed separate purchase-invoices and purchase-returns endpoints return 410.
	require.Equal(t, 410, c.call(t, http.MethodPost, "/api/v1/purchase-invoices", map[string]any{"supplier_id": supID, "receipt_ids": []string{str(r1.data["id"])}}, key()).code)
	require.Equal(t, 410, c.call(t, http.MethodPost, "/api/v1/purchase-returns", map[string]any{"receipt_id": r1.data["id"], "reason": "Wet", "settlement": "reduce_debt", "items": []map[string]any{{"product_id": flour, "qty": "1"}}}, key()).code)

	// Direct goods receipt creates and receives a purchase in one step.
	direct := c.call(t, http.MethodPost, "/api/v1/goods-receipts", map[string]any{
		"supplier_id": supID, "items": []map[string]any{{"product_id": flour, "qty": "10", "unit_cost": "5000"}},
	}, key())
	require.Equal(t, 201, direct.code, direct.raw)
	require.Equal(t, "received", str(direct.data["po_status"]))

	// Supplier payment allocates oldest unpaid purchase first and reduces supplier balance.
	pay := c.call(t, http.MethodPost, "/api/v1/suppliers/"+supID+"/payments", map[string]any{
		"amount": "1000000", "method": "bank", "money_account_id": bankID, "reference": "SUP-PAY-001",
	}, key())
	require.Equal(t, 201, pay.code, pay.raw)
	require.True(t, dec(pay.data["allocated"]).Equal(invD("1000000")), pay.raw)

	unpaid := c.call(t, http.MethodGet, "/api/v1/suppliers/"+supID+"/unpaid", nil, "")
	require.Equal(t, 200, unpaid.code, unpaid.raw)
	require.Len(t, asList(unpaid.body["data"]), 2)
	profile := c.call(t, http.MethodGet, "/api/v1/suppliers/"+supID, nil, "")
	require.Equal(t, 200, profile.code, profile.raw)
	owed := dec(profile.data["balance"])
	require.True(t, owed.Equal(invD("2050000")), profile.raw) // (3,000,000 + 50,000) - 1,000,000
	stmt := c.call(t, http.MethodGet, "/api/v1/suppliers/"+supID+"/statement", nil, "")
	require.Equal(t, 200, stmt.code, stmt.raw)
	require.True(t, dec(stmt.data["closing_balance"]).Equal(owed), stmt.raw)
	debts := c.call(t, http.MethodGet, "/api/v1/suppliers/debts", nil, "")
	require.Equal(t, 200, debts.code, debts.raw)
	require.True(t, dec(find(asList(debts.data["suppliers"]), "supplier_id", supID)["0_30"]).Equal(owed), debts.raw)
	list := c.call(t, http.MethodGet, "/api/v1/suppliers?q=Mill", nil, "")
	require.True(t, dec(find(asList(list.body["data"]), "id", supID)["balance"]).Equal(owed))

	grnList := c.call(t, http.MethodGet, "/api/v1/goods-receipts?supplier_id="+supID, nil, "")
	require.Len(t, asList(grnList.body["data"]), 2)

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

	// Staff without shop.purchase.manage is denied.
	staff, _ := e.user(t, "staff")
	denied := staff.call(t, http.MethodGet, "/api/v1/purchase-orders", nil, "")
	require.Equal(t, 403, denied.code, denied.raw)
}
