package app_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"bms/internal/platform/db"
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

// TestSalesIntegrity_HistoricalPriceAndTaxPreservedAfterPriceChange directly tests the challenge:
// "If the product price changes tomorrow, does yesterday's invoice remain unchanged?"
// Also verifies unique transaction numbers, void audit preservation, and sales report reconciliation.
func TestSalesIntegrity_HistoricalPriceAndTaxPreservedAfterPriceChange(t *testing.T) {
	clk := &stepClock{t: defaultNow}
	e := newEnvWithClock(t, clk)
	till := e.cashAccount(t)

	// Set default tax rate to 18% VAT on Day 1.
	rates := e.owner.call(t, http.MethodGet, "/api/v1/tax-rates", nil, "")
	require.Equal(t, 200, rates.code, rates.raw)
	exempt := find(asList(rates.body["data"]), "name", "Exempt")
	taxID := str(exempt["id"])
	require.Equal(t, 200, e.owner.call(t, http.MethodPut, "/api/v1/tax-rates/"+taxID, map[string]any{
		"name": "VAT 18%", "rate": "18", "is_default": true, "is_active": true,
	}, "").code)

	prod := e.product(t, "Solar Panel", "11800", "20", "7000")

	// Day 1 sale at TZS 11,800 (18% inclusive VAT = 1,800).
	day1 := e.owner.call(t, http.MethodPost, "/api/v1/sales", map[string]any{
		"items":    []map[string]any{{"product_id": prod, "qty": "1"}},
		"payments": []map[string]any{{"method": "cash", "money_account_id": till, "amount": "11800"}},
	}, key())
	require.Equal(t, 201, day1.code, day1.raw)
	day1ID := str(day1.data["id"])
	day1No := str(day1.data["number"])
	require.NotEmpty(t, day1No)
	require.True(t, dec(day1.data["total"]).Equal(rd("11800")))
	require.True(t, dec(day1.data["tax_total"]).Equal(rd("1800")))

	// Advance to tomorrow, re-authenticate after token expiry, change product price to 20,000 AND change tax rate to 0%.
	clk.Advance(24 * time.Hour)
	e.owner.token = login(t, e.app.Fiber, e.phone, "correct-horse")
	require.Equal(t, 200, e.owner.call(t, http.MethodPut, "/api/v1/products/"+prod, map[string]any{
		"name": "Solar Panel", "sell_price": "20000", "price_change_reason": "Supplier tariff increase",
	}, "").code)
	require.Equal(t, 200, e.owner.call(t, http.MethodPut, "/api/v1/tax-rates/"+taxID, map[string]any{
		"name": "Exempt", "rate": "0", "is_default": true, "is_active": true,
	}, "").code)

	// Day 2 sale uses the new price (20,000) and new 0% tax rate, with a distinct unique number.
	day2 := e.owner.call(t, http.MethodPost, "/api/v1/sales", map[string]any{
		"items":    []map[string]any{{"product_id": prod, "qty": "1"}},
		"payments": []map[string]any{{"method": "cash", "money_account_id": till, "amount": "20000"}},
	}, key())
	require.Equal(t, 201, day2.code, day2.raw)
	day2No := str(day2.data["number"])
	require.NotEqual(t, day1No, day2No, "every sale must have a unique transaction number")
	require.True(t, dec(day2.data["total"]).Equal(rd("20000")))
	require.True(t, dec(day2.data["tax_total"]).Equal(rd("0")))

	// Yesterday's sale, receipt, and A4 invoice MUST remain completely unchanged (11,800 total / 1,800 VAT).
	oldSale := e.owner.call(t, http.MethodGet, "/api/v1/sales/"+day1ID, nil, "")
	require.Equal(t, 200, oldSale.code, oldSale.raw)
	require.True(t, dec(oldSale.data["total"]).Equal(rd("11800")), "historical sale total drifted: %s", oldSale.raw)
	require.True(t, dec(oldSale.data["tax_total"]).Equal(rd("1800")), "historical sale tax drifted: %s", oldSale.raw)
	oldItems := asList(oldSale.data["items"])
	require.True(t, dec(oldItems[0]["unit_price"]).Equal(rd("11800")), "historical unit_price drifted: %s", oldSale.raw)

	oldInv := e.owner.call(t, http.MethodGet, "/api/v1/sales/"+day1ID+"/invoice", nil, "")
	require.Equal(t, 200, oldInv.code, oldInv.raw)
	require.True(t, dec(oldInv.data["total"]).Equal(rd("11800")), "yesterday's invoice total changed after price update: %s", oldInv.raw)
	require.True(t, dec(oldInv.data["tax_total"]).Equal(rd("1800")), "yesterday's invoice tax changed after tax update: %s", oldInv.raw)

	// Voiding Day 2 sale preserves its record + audit history, and sales summary reconciles to Day 1 only (11,800).
	day2ID := str(day2.data["id"])
	require.Equal(t, 200, e.owner.call(t, http.MethodPost, "/api/v1/sales/"+day2ID+"/void", map[string]any{"reason": "Wrong item"}, key()).code)
	voidedView := e.owner.call(t, http.MethodGet, "/api/v1/sales/"+day2ID, nil, "")
	require.Equal(t, 200, voidedView.code, voidedView.raw)
	require.Equal(t, "voided", str(voidedView.data["status"]))

	auditLogs := e.owner.call(t, http.MethodGet, "/api/v1/audit-logs?action=sale.voided", nil, "")
	require.Equal(t, 200, auditLogs.code, auditLogs.raw)
	require.NotEmpty(t, asList(auditLogs.body["data"]), "cancelled/voided sale must retain audit history")

	summary := e.owner.call(t, http.MethodGet, "/api/v1/reports/sales/summary?from=2026-11-01&to=2026-11-30", nil, "")
	require.Equal(t, 200, summary.code, summary.raw)
	totals := summary.data["totals"].(map[string]any)
	require.True(t, dec(totals["gross_sales"]).Equal(rd("11800")), "sales summary gross_sales must reconcile to non-voided sales: %s", summary.raw)
	require.True(t, dec(totals["net_sales"]).Equal(rd("10000")), "sales summary net_sales must reconcile: %s", summary.raw)
	require.True(t, dec(totals["tax"]).Equal(rd("1800")), "sales summary tax must reconcile: %s", summary.raw)
}

// TestSalesIntegrity_InvoiceStatusAndPaymentConsistency verifies invoice number uniqueness,
// state-transition enforcement, and DB-level payment-status consistency constraints.
func TestSalesIntegrity_InvoiceStatusAndPaymentConsistency(t *testing.T) {
	e := newEnv(t, defaultNow)
	till := e.cashAccount(t)
	cRes := e.owner.call(t, http.MethodPost, "/api/v1/clients", map[string]any{"name": "Invoice Corp"}, "")
	require.Equal(t, 201, cRes.code, cRes.raw)
	cID := str(cRes.data["id"])

	inv1 := e.owner.call(t, http.MethodPost, "/api/v1/service-invoices", map[string]any{
		"client_id": cID,
		"lines":     []map[string]any{{"description": "Web Portal", "quantity": "1", "unit_price": "100000"}},
	}, "")
	require.Equal(t, 201, inv1.code, inv1.raw)
	inv1ID := uuid.MustParse(str(inv1.data["id"]))
	inv1No := str(inv1.data["number"])

	inv2 := e.owner.call(t, http.MethodPost, "/api/v1/service-invoices", map[string]any{
		"client_id": cID,
		"lines":     []map[string]any{{"description": "SEO", "quantity": "1", "unit_price": "50000"}},
	}, "")
	require.Equal(t, 201, inv2.code, inv2.raw)
	inv2No := str(inv2.data["number"])
	require.NotEqual(t, inv1No, inv2No, "invoice numbers must be unique")

	// Cannot pay a draft invoice before issuing.
	payDraft := e.owner.call(t, http.MethodPost, "/api/v1/service-invoices/"+inv1ID.String()+"/payments", map[string]any{
		"amount": "20000", "method": "cash", "money_account_id": till,
	}, key())
	require.Equal(t, 409, payDraft.code, payDraft.raw)

	// Issue -> cannot re-issue, cannot edit issued invoice.
	require.Equal(t, 200, e.owner.call(t, http.MethodPost, "/api/v1/service-invoices/"+inv1ID.String()+"/issue", nil, "").code)
	require.Equal(t, 409, e.owner.call(t, http.MethodPost, "/api/v1/service-invoices/"+inv1ID.String()+"/issue", nil, "").code)
	require.Equal(t, 409, e.owner.call(t, http.MethodPut, "/api/v1/service-invoices/"+inv1ID.String(), map[string]any{
		"client_id": cID, "lines": []map[string]any{{"description": "Changed", "quantity": "1", "unit_price": "1"}},
	}, "").code)

	// Direct DB attempt to duplicate invoice_no or contradict invoice status & amount_paid must fail.
	dupErr := e.app.DB.InActorTx(context.Background(), db.Actor{TenantID: e.tenantID}, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE app.invoices SET invoice_no = $2 WHERE id = $1`, uuid.MustParse(str(inv2.data["id"])), inv1No)
		return err
	})
	require.Error(t, dupErr, "duplicate invoice_no must be rejected by UNIQUE constraint")

	badPaidErr := e.app.DB.InActorTx(context.Background(), db.Actor{TenantID: e.tenantID}, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE app.invoices SET status = 'paid' WHERE id = $1`, inv1ID)
		return err
	})
	require.Error(t, badPaidErr, "setting status=paid while amount_paid=0 must be rejected")

	// Partial payment -> status becomes 'partial' (partially_paid); remaining payment -> 'paid'.
	require.Equal(t, 201, e.owner.call(t, http.MethodPost, "/api/v1/service-invoices/"+inv1ID.String()+"/payments", map[string]any{
		"amount": "40000", "method": "cash", "money_account_id": till,
	}, key()).code)
	mid := e.owner.call(t, http.MethodGet, "/api/v1/service-invoices/"+inv1ID.String(), nil, "")
	require.Equal(t, "partial", str(mid.data["status"]))
	require.True(t, dec(mid.data["balance"]).Equal(rd("60000")))

	require.Equal(t, 201, e.owner.call(t, http.MethodPost, "/api/v1/service-invoices/"+inv1ID.String()+"/payments", map[string]any{
		"amount": "60000", "method": "cash", "money_account_id": till,
	}, key()).code)
	final := e.owner.call(t, http.MethodGet, "/api/v1/service-invoices/"+inv1ID.String(), nil, "")
	require.Equal(t, "paid", str(final.data["status"]))
	require.True(t, dec(final.data["balance"]).Equal(rd("0")))
}
