package app_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSalesIntegrity_ReceiptsBlockedOnUncompletedAndVoidedSales verifies that official
// receipts and shop invoices cannot be generated, printed, or sent for held,
// pending-approval, or voided sales, and unauthorized users cannot generate receipts
// for another cashier's completed sale.
func TestSalesIntegrity_ReceiptsBlockedOnUncompletedAndVoidedSales(t *testing.T) {
	e := newEnv(t, defaultNow)
	till := e.cashAccount(t)
	prod := e.product(t, "Laptop Charger", "45000", "10", "25000")

	// 1. Held sale cannot generate a receipt or invoice.
	held := e.owner.call(t, http.MethodPost, "/api/v1/sales/hold", map[string]any{
		"items": []map[string]any{{"product_id": prod, "qty": "1"}},
	}, "")
	require.Equal(t, 201, held.code, held.raw)
	heldID := str(held.data["id"])
	require.Equal(t, 409, e.owner.call(t, http.MethodGet, "/api/v1/sales/"+heldID+"/receipt", nil, "").code)
	require.Equal(t, 409, e.owner.call(t, http.MethodGet, "/api/v1/sales/"+heldID+"/receipt?format=pdf", nil, "").code)
	require.Equal(t, 409, e.owner.call(t, http.MethodPost, "/api/v1/sales/"+heldID+"/receipt/printed", nil, "").code)
	require.Equal(t, 409, e.owner.call(t, http.MethodGet, "/api/v1/sales/"+heldID+"/invoice", nil, "").code)

	// 2. Pending-approval sale (discount exceeding staff limit) cannot generate a receipt.
	cashierA, _ := e.user(t, "staff")
	cashierB, _ := e.user(t, "staff")
	pending := cashierA.call(t, http.MethodPost, "/api/v1/sales", map[string]any{
		"items":    []map[string]any{{"product_id": prod, "qty": "1"}},
		"discount": map[string]any{"type": "percent", "value": "10"},
		"payments": []map[string]any{{"method": "cash", "money_account_id": till, "amount": "40500"}},
	}, key())
	require.Equal(t, 202, pending.code, pending.raw)
	pendingID := str(pending.data["id"])
	require.Equal(t, 409, cashierA.call(t, http.MethodGet, "/api/v1/sales/"+pendingID+"/receipt", nil, "").code)
	require.Equal(t, 409, e.owner.call(t, http.MethodGet, "/api/v1/sales/"+pendingID+"/invoice", nil, "").code)

	// 3. Completed sale CAN generate a receipt for its cashier/owner, NOT for another cashier,
	//    and once voided, receipt/invoice generation is blocked with 409.
	done := cashierA.call(t, http.MethodPost, "/api/v1/sales", map[string]any{
		"items":    []map[string]any{{"product_id": prod, "qty": "1"}},
		"payments": []map[string]any{{"method": "cash", "money_account_id": till, "amount": "45000"}},
	}, key())
	require.Equal(t, 201, done.code, done.raw)
	saleID := str(done.data["id"])

	require.Equal(t, 200, cashierA.call(t, http.MethodGet, "/api/v1/sales/"+saleID+"/receipt", nil, "").code)
	require.Equal(t, 404, cashierB.call(t, http.MethodGet, "/api/v1/sales/"+saleID+"/receipt", nil, "").code)
	require.Equal(t, 404, cashierB.call(t, http.MethodGet, "/api/v1/sales/"+saleID+"/invoice", nil, "").code)

	voided := e.owner.call(t, http.MethodPost, "/api/v1/sales/"+saleID+"/void", map[string]any{"reason": "Customer cancelled"}, key())
	require.Equal(t, 200, voided.code, voided.raw)
	require.Equal(t, 409, e.owner.call(t, http.MethodGet, "/api/v1/sales/"+saleID+"/receipt", nil, "").code)
	require.Equal(t, 409, e.owner.call(t, http.MethodGet, "/api/v1/sales/"+saleID+"/invoice", nil, "").code)
}
