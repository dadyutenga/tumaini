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

// TestSalesIntegrity_InvoiceAndQuotationIsolation verifies that draft-only staff cannot
// view, download PDFs for, or edit invoices or quotations created by other users/clients,
// and another tenant cannot access them either.
func TestSalesIntegrity_InvoiceAndQuotationIsolation(t *testing.T) {
	e := newEnv(t, defaultNow)
	clientA := e.owner.call(t, http.MethodPost, "/api/v1/clients", map[string]any{"name": "Client Alpha"}, "")
	require.Equal(t, 201, clientA.code, clientA.raw)
	clientAID := str(clientA.data["id"])

	staffA, _ := e.user(t, "staff")
	staffB, _ := e.user(t, "staff")

	// Staff A creates a draft quotation and a draft invoice.
	qA := staffA.call(t, http.MethodPost, "/api/v1/service-quotations", map[string]any{
		"client_id": clientAID,
		"lines":     []map[string]any{{"description": "Brand Audit", "quantity": "1", "unit_price": "80000"}},
	}, "")
	require.Equal(t, 201, qA.code, qA.raw)
	qAID := str(qA.data["id"])

	invA := staffA.call(t, http.MethodPost, "/api/v1/service-invoices", map[string]any{
		"client_id": clientAID,
		"lines":     []map[string]any{{"description": "Brand Audit", "quantity": "1", "unit_price": "80000"}},
	}, "")
	require.Equal(t, 201, invA.code, invA.raw)
	invAID := str(invA.data["id"])

	// Staff A can view own records.
	require.Equal(t, 200, staffA.call(t, http.MethodGet, "/api/v1/service-quotations/"+qAID, nil, "").code)
	require.Equal(t, 200, staffA.call(t, http.MethodGet, "/api/v1/service-invoices/"+invAID, nil, "").code)

	// Staff B (draft-only) cannot view, list, download PDF, or edit Staff A's quotation or invoice.
	require.Equal(t, 404, staffB.call(t, http.MethodGet, "/api/v1/service-quotations/"+qAID, nil, "").code)
	require.Equal(t, 404, staffB.call(t, http.MethodGet, "/api/v1/service-quotations/"+qAID+"/pdf", nil, "").code)
	require.Equal(t, 404, staffB.call(t, http.MethodPut, "/api/v1/service-quotations/"+qAID, map[string]any{
		"client_id": clientAID,
		"lines":     []map[string]any{{"description": "Tampered", "quantity": "1", "unit_price": "1"}},
	}, "").code)

	require.Equal(t, 404, staffB.call(t, http.MethodGet, "/api/v1/service-invoices/"+invAID, nil, "").code)
	require.Equal(t, 404, staffB.call(t, http.MethodGet, "/api/v1/service-invoices/"+invAID+"/pdf", nil, "").code)
	require.Equal(t, 404, staffB.call(t, http.MethodPut, "/api/v1/service-invoices/"+invAID, map[string]any{
		"client_id": clientAID,
		"lines":     []map[string]any{{"description": "Tampered", "quantity": "1", "unit_price": "1"}},
	}, "").code)

	listB := staffB.call(t, http.MethodGet, "/api/v1/service-invoices", nil, "")
	require.Equal(t, 200, listB.code, listB.raw)
	require.Empty(t, asList(listB.body["data"]))

	// Owner (holds services.invoice.manage) CAN view, issue, and manage all invoices.
	require.Equal(t, 200, e.owner.call(t, http.MethodGet, "/api/v1/service-invoices/"+invAID, nil, "").code)

	// Cross-tenant isolation.
	other := newEnv(t, defaultNow)
	require.Equal(t, 404, other.owner.call(t, http.MethodGet, "/api/v1/service-invoices/"+invAID, nil, "").code)
	require.Equal(t, 404, other.owner.call(t, http.MethodGet, "/api/v1/service-quotations/"+qAID, nil, "").code)
}
