package app_test

import (
	"context"
	"net/http"
	"strings"
	"sync"
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

// TestInventoryIntegrity_ConcurrentOversellNegativeStockAndReturns verifies:
//   - Stock cannot go negative unless explicitly enabled by tenant settings
//   - Stock deductions happen atomically with sales (failed sales leave stock untouched)
//   - Concurrent sales against the last unit cannot oversell inventory
//   - Stock returns link to the original sale and damaged returns are never restocked
func TestInventoryIntegrity_ConcurrentOversellNegativeStockAndReturns(t *testing.T) {
	e := newEnv(t, defaultNow)
	till := e.cashAccount(t)
	cashierA, _ := e.user(t, "staff")
	cashierB, _ := e.user(t, "staff")

	// 1. Default rule: cannot oversell / go negative, and failed sales never deduct stock.
	limited := e.product(t, "Limited Edition Headphones", "50000", "1", "30000")
	oversell := cashierA.call(t, http.MethodPost, "/api/v1/sales", map[string]any{
		"items":    []map[string]any{{"product_id": limited, "qty": "2"}},
		"payments": []map[string]any{{"method": "cash", "money_account_id": till, "amount": "100000"}},
	}, key())
	require.Equal(t, 409, oversell.code, oversell.raw)
	// The cashier reads this message: it names the product and shop, never their ids.
	require.Contains(t, oversell.raw, "Not enough stock: Limited Edition Headphones at ")
	require.Contains(t, oversell.raw, "(short by 1)")
	require.NotContains(t, oversell.raw, limited)
	require.True(t, stockQty(t, e.owner, limited).Equal(rd("1")))

	// Underpaid walk-in sale fails atomically (409 customer_required) without deducting stock.
	failedSale := cashierA.call(t, http.MethodPost, "/api/v1/sales", map[string]any{
		"items":    []map[string]any{{"product_id": limited, "qty": "1"}},
		"payments": []map[string]any{{"method": "cash", "money_account_id": till, "amount": "1000"}},
	}, key())
	require.Equal(t, 409, failedSale.code, failedSale.raw)
	require.True(t, stockQty(t, e.owner, limited).Equal(rd("1")), "failed sale must not deduct stock")

	// 2. Concurrent sales of the last remaining unit: exactly 1 succeeds (201), 1 fails (409), stock ends at 0.
	start := make(chan struct{})
	codes := make([]int, 2)
	var wg sync.WaitGroup
	for i, c := range []*client{cashierA, cashierB} {
		wg.Add(1)
		go func(idx int, cl *client) {
			defer wg.Done()
			<-start
			res := cl.call(t, http.MethodPost, "/api/v1/sales", map[string]any{
				"items":    []map[string]any{{"product_id": limited, "qty": "1"}},
				"payments": []map[string]any{{"method": "cash", "money_account_id": till, "amount": "50000"}},
			}, key())
			codes[idx] = res.code
		}(i, c)
	}
	close(start)
	wg.Wait()

	succeeded, rejected := 0, 0
	for _, code := range codes {
		switch code {
		case 201:
			succeeded++
		case 409:
			rejected++
		}
	}
	require.Equal(t, 1, succeeded, "exactly one concurrent cashier should buy the last unit, got codes=%v", codes)
	require.Equal(t, 1, rejected, "the second concurrent cashier must be blocked from overselling, got codes=%v", codes)
	require.True(t, stockQty(t, e.owner, limited).Equal(rd("0")), "stock must never go negative from concurrent sales")

	// 3. Explicit business rule opt-in: enabling allow_negative_stock permits selling below zero.
	require.Equal(t, 200, e.owner.call(t, http.MethodPut, "/api/v1/settings/system", map[string]any{"allow_negative_stock": true}, "").code)
	negAllowed := cashierA.call(t, http.MethodPost, "/api/v1/sales", map[string]any{
		"items":    []map[string]any{{"product_id": limited, "qty": "1"}},
		"payments": []map[string]any{{"method": "cash", "money_account_id": till, "amount": "50000"}},
	}, key())
	require.Equal(t, 201, negAllowed.code, negAllowed.raw)
	require.True(t, stockQty(t, e.owner, limited).Equal(rd("-1")))

	// Turning allow_negative_stock back off immediately re-enforces the guard.
	require.Equal(t, 200, e.owner.call(t, http.MethodPut, "/api/v1/settings/system", map[string]any{"allow_negative_stock": false}, "").code)
	blockedAgain := cashierA.call(t, http.MethodPost, "/api/v1/sales", map[string]any{
		"items":    []map[string]any{{"product_id": limited, "qty": "1"}},
		"payments": []map[string]any{{"method": "cash", "money_account_id": till, "amount": "50000"}},
	}, key())
	require.Equal(t, 409, blockedAgain.code, blockedAgain.raw)

	// 4. Stock returns link to the original sale: resellable returns restock, damaged returns do NOT restock.
	mug := e.product(t, "Ceramic Mug", "10000", "5", "4000")
	sale := cashierA.call(t, http.MethodPost, "/api/v1/sales", map[string]any{
		"items":    []map[string]any{{"product_id": mug, "qty": "3"}},
		"payments": []map[string]any{{"method": "cash", "money_account_id": till, "amount": "30000"}},
	}, key())
	require.Equal(t, 201, sale.code, sale.raw)
	saleID := str(sale.data["id"])
	saleDetail := e.owner.call(t, http.MethodGet, "/api/v1/sales/"+saleID, nil, "")
	saleItemID := str(asList(saleDetail.data["items"])[0]["id"])
	require.True(t, stockQty(t, e.owner, mug).Equal(rd("2")))

	// Contradictory request (damaged + restock:true) is rejected.
	badRet := cashierA.call(t, http.MethodPost, "/api/v1/sales/"+saleID+"/returns", map[string]any{
		"reason": "Cracked handle",
		"items":  []map[string]any{{"sale_item_id": saleItemID, "qty": "1", "condition": "damaged", "restock": true}},
	}, key())
	require.Equal(t, 400, badRet.code, badRet.raw)

	// Resellable return (restock:true) increases stock from 2 -> 3 and links to sale_id.
	retGood := cashierA.call(t, http.MethodPost, "/api/v1/sales/"+saleID+"/returns", map[string]any{
		"reason": "Unopened return",
		"items":  []map[string]any{{"sale_item_id": saleItemID, "qty": "1", "condition": "resellable", "restock": true}},
	}, key())
	require.Equal(t, 201, retGood.code, retGood.raw)
	retGoodID := str(retGood.data["id"])
	require.Equal(t, saleID, str(retGood.data["sale_id"]))
	require.Equal(t, 200, e.owner.call(t, http.MethodPost, "/api/v1/sale-returns/"+retGoodID+"/approve", nil, "").code)
	require.Equal(t, 200, e.owner.call(t, http.MethodPost, "/api/v1/sale-returns/"+retGoodID+"/complete", map[string]any{
		"money_account_id": till, "method": "cash",
	}, key()).code)
	require.True(t, stockQty(t, e.owner, mug).Equal(rd("3")), "resellable return must restore stock")

	// Damaged return (restock:false) refunds customer but MUST NOT add unsellable stock back to stock_balances.
	retDmg := cashierA.call(t, http.MethodPost, "/api/v1/sales/"+saleID+"/returns", map[string]any{
		"reason": "Broken in transit",
		"items":  []map[string]any{{"sale_item_id": saleItemID, "qty": "1", "condition": "damaged", "restock": false}},
	}, key())
	require.Equal(t, 201, retDmg.code, retDmg.raw)
	retDmgID := str(retDmg.data["id"])
	require.Equal(t, saleID, str(retDmg.data["sale_id"]))
	require.Equal(t, 200, e.owner.call(t, http.MethodPost, "/api/v1/sale-returns/"+retDmgID+"/approve", nil, "").code)
	require.Equal(t, 200, e.owner.call(t, http.MethodPost, "/api/v1/sale-returns/"+retDmgID+"/complete", map[string]any{
		"money_account_id": till, "method": "cash",
	}, key()).code)
	require.True(t, stockQty(t, e.owner, mug).Equal(rd("3")), "damaged return must not restock sellable inventory")
}

// TestInventoryIntegrity_AdjustmentsTransfersPOReconciliationValuationAndImmutability verifies:
//   - Stock adjustments and entries require explicit authorization
//   - Damaged and expired stock have separate loss tracking
//   - Stock transfers record source & destination and enforce available stock
//   - PO and goods receipts reconcile ordered vs received quantities
//   - Low-stock alerts and inventory valuation match underlying balances
//   - Inventory history cannot be silently rewritten and products with history cannot be hard-deleted
func TestInventoryIntegrity_AdjustmentsTransfersPOReconciliationValuationAndImmutability(t *testing.T) {
	e := newEnv(t, defaultNow)
	till := e.cashAccount(t)
	staff, _ := e.user(t, "staff")
	mgr, _ := e.user(t, "manager")

	// Create a product with reorder_level = 5 and opening stock 10 @ 2,000.
	prodRes := e.owner.call(t, http.MethodPost, "/api/v1/products", map[string]any{
		"name": "Olive Oil 1L", "sell_price": "5000", "reorder_level": "5",
	}, "")
	require.Equal(t, 201, prodRes.code, prodRes.raw)
	prodID := str(prodRes.data["id"])
	require.Equal(t, 201, e.owner.call(t, http.MethodPost, "/api/v1/stock-entries", map[string]any{
		"direction": "in", "reason": "opening",
		"items": []map[string]any{{"product_id": prodID, "qty": "10", "unit_cost": "2000"}},
	}, key()).code)

	// 1. Authorization: cashier/staff without inventory permissions cannot adjust or enter stock.
	require.Equal(t, 403, staff.call(t, http.MethodPost, "/api/v1/stock-entries", map[string]any{
		"direction": "out", "reason": "damaged",
		"items": []map[string]any{{"product_id": prodID, "qty": "1"}},
	}, key()).code)
	require.Equal(t, 403, staff.call(t, http.MethodPost, "/api/v1/stock-adjustments", map[string]any{
		"reason": "Unauthorized tweak",
		"items":  []map[string]any{{"product_id": prodID, "qty_counted": "100"}},
	}, "").code)

	// Authorized manager creates and posts a stock adjustment (10 -> 9), and audit logs record it.
	adj := mgr.call(t, http.MethodPost, "/api/v1/stock-adjustments", map[string]any{
		"reason": "Cycle count variance",
		"items":  []map[string]any{{"product_id": prodID, "qty_counted": "9"}},
	}, "")
	require.Equal(t, 201, adj.code, adj.raw)
	adjID := str(adj.data["id"])
	require.Equal(t, 200, mgr.call(t, http.MethodPost, "/api/v1/stock-adjustments/"+adjID+"/post", nil, key()).code)
	require.True(t, stockQty(t, e.owner, prodID).Equal(rd("9")))

	// 2. Damaged & Expired stock have separate stock-out handling and appear in loss reporting.
	dmg := e.owner.call(t, http.MethodPost, "/api/v1/stock-entries", map[string]any{
		"direction": "out", "reason": "damaged",
		"items": []map[string]any{{"product_id": prodID, "qty": "1"}},
	}, key())
	require.Equal(t, 201, dmg.code, dmg.raw)
	exp := e.owner.call(t, http.MethodPost, "/api/v1/stock-entries", map[string]any{
		"direction": "out", "reason": "expired",
		"items": []map[string]any{{"product_id": prodID, "qty": "1"}},
	}, key())
	require.Equal(t, 201, exp.code, exp.raw)
	require.True(t, stockQty(t, e.owner, prodID).Equal(rd("7")))

	losses := e.owner.call(t, http.MethodGet, "/api/v1/reports/inventory/losses", nil, "")
	require.Equal(t, 200, losses.code, losses.raw)
	require.NotEmpty(t, asList(losses.data["rows"]), "damaged and expired entries must appear in inventory losses report")

	// 3. Stock transfers record source & destination, block over-dispatch, and trigger accurate low-stock alerts.
	br := e.owner.call(t, http.MethodPost, "/api/v1/branches", map[string]any{"name": "Warehouse B"}, "")
	require.Equal(t, 201, br.code, br.raw)
	branchB := str(br.data["id"])

	overTr := e.owner.call(t, http.MethodPost, "/api/v1/stock-transfers", map[string]any{
		"to_branch_id": branchB,
		"items":        []map[string]any{{"product_id": prodID, "qty": "50"}},
	}, "")
	require.Equal(t, 201, overTr.code, overTr.raw)
	require.Equal(t, 409, e.owner.call(t, http.MethodPost, "/api/v1/stock-transfers/"+str(overTr.data["id"])+"/dispatch", nil, key()).code)

	tr := e.owner.call(t, http.MethodPost, "/api/v1/stock-transfers", map[string]any{
		"to_branch_id": branchB,
		"items":        []map[string]any{{"product_id": prodID, "qty": "4"}},
	}, "")
	require.Equal(t, 201, tr.code, tr.raw)
	trID := str(tr.data["id"])
	require.Equal(t, 200, e.owner.call(t, http.MethodPost, "/api/v1/stock-transfers/"+trID+"/dispatch", nil, key()).code)
	cB := &client{app: e.owner.app, token: e.owner.token, branch: branchB}
	require.Equal(t, 200, cB.call(t, http.MethodPost, "/api/v1/stock-transfers/"+trID+"/receive", map[string]any{
		"items": []map[string]any{{"product_id": prodID, "qty_received": "4"}},
	}, key()).code)

	trView := e.owner.call(t, http.MethodGet, "/api/v1/stock-transfers/"+trID, nil, "")
	require.Equal(t, 200, trView.code, trView.raw)
	require.Equal(t, e.branch, str(trView.data["from_branch_id"]), "transfer must record source branch")
	require.Equal(t, branchB, str(trView.data["to_branch_id"]), "transfer must record destination branch")
	require.True(t, stockQty(t, e.owner, prodID).Equal(rd("3")), "Main branch must have 7 - 4 = 3")
	require.True(t, stockQty(t, cB, prodID).Equal(rd("4")), "Branch B must have 4")

	// Low-stock report reflects Main branch qty_on_hand=3 (<= reorder_level=5).
	lowRep := e.owner.call(t, http.MethodGet, "/api/v1/reports/inventory/low-stock", nil, "")
	require.Equal(t, 200, lowRep.code, lowRep.raw)
	lowRow := find(asList(lowRep.data["rows"]), "product_id", prodID)
	require.NotNil(t, lowRow, "low-stock report must flag Olive Oil when Main qty=3 <= reorder_level=5: %s", lowRep.raw)
	require.True(t, dec(lowRow["qty_on_hand"]).Equal(rd("3")), "low-stock alert must use accurate quantity: %v", lowRow)

	// 4. PO & Goods Receipt reconciliation + consistent inventory valuation across endpoints.
	sup := e.owner.call(t, http.MethodPost, "/api/v1/suppliers", map[string]any{"name": "Med Imports"}, "")
	require.Equal(t, 201, sup.code, sup.raw)
	supID := str(sup.data["id"])
	po := mgr.call(t, http.MethodPost, "/api/v1/purchase-orders", map[string]any{
		"supplier_id": supID,
		"items":       []map[string]any{{"product_id": prodID, "qty": "10", "unit_cost": "3000"}},
	}, "")
	require.Equal(t, 201, po.code, po.raw)
	poID := str(po.data["id"])
	require.Equal(t, 200, mgr.call(t, http.MethodPost, "/api/v1/purchase-orders/"+poID+"/submit", nil, "").code)

	// Cannot receive more than ordered (11 > 10).
	require.Equal(t, 409, mgr.call(t, http.MethodPost, "/api/v1/goods-receipts", map[string]any{
		"po_id": poID, "items": []map[string]any{{"product_id": prodID, "qty": "11", "unit_cost": "3000"}},
	}, key()).code)

	gr1 := mgr.call(t, http.MethodPost, "/api/v1/goods-receipts", map[string]any{
		"po_id": poID, "items": []map[string]any{{"product_id": prodID, "qty": "6", "unit_cost": "3000"}},
	}, key())
	require.Equal(t, 201, gr1.code, gr1.raw)
	require.Equal(t, "partially_received", str(gr1.data["po_status"]))
	gr1ID := str(gr1.data["id"])

	gr2 := mgr.call(t, http.MethodPost, "/api/v1/goods-receipts", map[string]any{
		"po_id": poID, "items": []map[string]any{{"product_id": prodID, "qty": "4", "unit_cost": "3000"}},
	}, key())
	require.Equal(t, 201, gr2.code, gr2.raw)
	require.Equal(t, "received", str(gr2.data["po_status"]))
	require.True(t, stockQty(t, e.owner, prodID).Equal(rd("13")))

	// Inventory valuation reconciles identically across /stock/valuation (per-branch) and /reports/inventory/valuation (all-branch):
	// 7 remaining units @ 2,000 (14,000) + 10 received units @ 3,000 (30,000) = 44,000.00 across 17 units (13 Main + 4 Branch B).
	mainVal := e.owner.call(t, http.MethodGet, "/api/v1/stock/valuation?as_of=2099-12-31", nil, "")
	require.Equal(t, 200, mainVal.code, mainVal.raw)
	branchBVal := cB.call(t, http.MethodGet, "/api/v1/stock/valuation?as_of=2099-12-31", nil, "")
	require.Equal(t, 200, branchBVal.code, branchBVal.raw)
	repVal := e.owner.call(t, http.MethodGet, "/api/v1/reports/inventory/valuation", nil, "")
	require.Equal(t, 200, repVal.code, repVal.raw)
	repTotal := dec(repVal.data["totals"].(map[string]any)["value"])
	require.True(t, repTotal.Equal(rd("44000")), "expected exact weighted-average inventory value 44000, got: %s", repVal.raw)
	require.True(t, dec(mainVal.data["total"]).Add(dec(branchBVal.data["total"])).Equal(repTotal),
		"sum of per-branch /stock/valuation (%s + %s) must reconcile to /reports/inventory/valuation (%s)",
		mainVal.data["total"], branchBVal.data["total"], repTotal)

	// 5. Inventory history cannot be silently rewritten, and products with historical transactions remain traceable.
	sale := e.owner.call(t, http.MethodPost, "/api/v1/sales", map[string]any{
		"items":    []map[string]any{{"product_id": prodID, "qty": "1"}},
		"payments": []map[string]any{{"method": "cash", "money_account_id": till, "amount": "5000"}},
	}, key())
	require.Equal(t, 201, sale.code, sale.raw)
	saleID := str(sale.data["id"])

	for name, tc := range map[string]struct {
		sql string
		arg uuid.UUID
	}{
		"update stock_movements":         {`UPDATE app.stock_movements SET quantity_delta = 999 WHERE tenant_id = $1`, e.tenantID},
		"delete stock_movements":         {`DELETE FROM app.stock_movements WHERE tenant_id = $1`, e.tenantID},
		"update posted stock_adj":        {`UPDATE app.stock_adjustments SET reason = 'theft' WHERE id = $1`, uuid.MustParse(adjID)},
		"delete posted stock_adj":        {`DELETE FROM app.stock_adjustments WHERE id = $1`, uuid.MustParse(adjID)},
		"update received transfer":       {`UPDATE app.stock_transfers SET note = 'Tampered' WHERE id = $1`, uuid.MustParse(trID)},
		"delete received transfer":       {`DELETE FROM app.stock_transfers WHERE id = $1`, uuid.MustParse(trID)},
		"update goods_receipts":          {`UPDATE app.goods_receipts SET notes = 'Tampered' WHERE id = $1`, uuid.MustParse(gr1ID)},
		"delete goods_receipts":          {`DELETE FROM app.goods_receipts WHERE id = $1`, uuid.MustParse(gr1ID)},
		"hard delete historical product": {`DELETE FROM app.products WHERE id = $1`, uuid.MustParse(prodID)},
	} {
		err := e.app.DB.InActorTx(context.Background(), db.Actor{TenantID: e.tenantID}, func(ctx context.Context, tx pgx.Tx) error {
			_, qErr := tx.Exec(ctx, tc.sql, tc.arg)
			return qErr
		})
		require.Error(t, err, "DB immutability guard must block %s", name)
		msg := err.Error()
		require.True(t, strings.Contains(msg, "BM004") || strings.Contains(msg, "42501"),
			"DB immutability trigger (BM004) or role privilege denial (42501) must fire for %s, got: %v", name, err)
	}

	// Deactivating the product blocks new sales while keeping historical sale & stock card completely traceable.
	require.Equal(t, 200, e.owner.call(t, http.MethodPost, "/api/v1/products/"+prodID+"/deactivate", nil, "").code)
	require.Equal(t, 404, e.owner.call(t, http.MethodPost, "/api/v1/sales/preview", map[string]any{
		"items": []map[string]any{{"product_id": prodID, "qty": "1"}},
	}, "").code)

	histSale := e.owner.call(t, http.MethodGet, "/api/v1/sales/"+saleID, nil, "")
	require.Equal(t, 200, histSale.code, histSale.raw)
	require.Equal(t, "Olive Oil 1L", str(asList(histSale.data["items"])[0]["product_name"]))

	stockCard := e.owner.call(t, http.MethodGet, "/api/v1/products/"+prodID+"/stock-card", nil, "")
	require.Equal(t, 200, stockCard.code, stockCard.raw)
	require.NotEmpty(t, asList(stockCard.body["data"]), "deactivated product must preserve full stock movement history")
}

// TestInventoryIntegrity_DirectStockIncreaseBlocked directly tests the challenge:
// "Can someone increase stock directly through an API request without creating an authorized stock adjustment?"
// Verifies mass-assignment on product endpoints, negative-quantity sign-flip attacks,
// unauthorized stock-in/receipt/transfer calls, and direct SQL writes to stock tables are all blocked.
func TestInventoryIntegrity_DirectStockIncreaseBlocked(t *testing.T) {
	e := newEnv(t, defaultNow)
	till := e.cashAccount(t)
	staff, _ := e.user(t, "staff")

	// 1. Mass-assignment on product creation & update (qty_on_hand / quantity_on_hand / stock) must never create stock.
	created := e.owner.call(t, http.MethodPost, "/api/v1/products", map[string]any{
		"name":             "Watch",
		"sell_price":       "25000",
		"qty_on_hand":      "500",
		"quantity_on_hand": "500",
		"stock":            "500",
	}, "")
	require.Equal(t, 201, created.code, created.raw)
	prodID := str(created.data["id"])
	require.True(t, stockQty(t, e.owner, prodID).Equal(rd("0")), "POST /products must never accept direct stock quantities")

	updated := e.owner.call(t, http.MethodPut, "/api/v1/products/"+prodID, map[string]any{
		"name":             "Watch",
		"sell_price":       "25000",
		"qty_on_hand":      "500",
		"quantity_on_hand": "500",
		"stock":            "500",
	}, "")
	require.Equal(t, 200, updated.code, updated.raw)
	require.True(t, stockQty(t, e.owner, prodID).Equal(rd("0")), "PUT /products/:id must never allow direct stock edits")

	// 2. Negative-quantity sign-flip attacks on sales and stock-out entries must be rejected.
	negSale := e.owner.call(t, http.MethodPost, "/api/v1/sales", map[string]any{
		"items":    []map[string]any{{"product_id": prodID, "qty": "-5"}},
		"payments": []map[string]any{{"method": "cash", "money_account_id": till, "amount": "1"}},
	}, key())
	require.Equal(t, 400, negSale.code, negSale.raw)

	negOut := e.owner.call(t, http.MethodPost, "/api/v1/stock-entries", map[string]any{
		"direction": "out", "reason": "damaged",
		"items": []map[string]any{{"product_id": prodID, "qty": "-5"}},
	}, key())
	require.Equal(t, 422, negOut.code, negOut.raw)
	require.True(t, stockQty(t, e.owner, prodID).Equal(rd("0")))

	// 3. Unauthorized staff cannot increase stock via stock-entries, stock-adjustments, or goods-receipts.
	sup := e.owner.call(t, http.MethodPost, "/api/v1/suppliers", map[string]any{"name": "Timepiece Ltd"}, "")
	require.Equal(t, 201, sup.code, sup.raw)
	supID := str(sup.data["id"])

	require.Equal(t, 403, staff.call(t, http.MethodPost, "/api/v1/stock-entries", map[string]any{
		"direction": "in", "reason": "found",
		"items": []map[string]any{{"product_id": prodID, "qty": "50", "unit_cost": "10000"}},
	}, key()).code)
	require.Equal(t, 403, staff.call(t, http.MethodPost, "/api/v1/stock-adjustments", map[string]any{
		"reason": "Ghost stock",
		"items":  []map[string]any{{"product_id": prodID, "qty_counted": "50"}},
	}, "").code)
	require.Equal(t, 403, staff.call(t, http.MethodPost, "/api/v1/goods-receipts", map[string]any{
		"supplier_id": supID,
		"items":       []map[string]any{{"product_id": prodID, "qty": "50", "unit_cost": "10000"}},
	}, key()).code)
	require.True(t, stockQty(t, e.owner, prodID).Equal(rd("0")))

	// 4. Direct SQL INSERT into app.stock_movements or UPDATE/INSERT on app.stock_balances is forbidden for bms_app.
	pUUID := uuid.MustParse(prodID)
	bUUID := uuid.MustParse(e.branch)
	for name, sqlStmt := range map[string]string{
		"direct insert stock_balances": `INSERT INTO app.stock_balances (tenant_id, location_id, product_id, quantity_on_hand) VALUES ($1,$2,$3,500)`,
		"direct update stock_balances": `UPDATE app.stock_balances SET quantity_on_hand = 500 WHERE tenant_id = $1 AND location_id = $2 AND product_id = $3`,
		"direct insert stock_movements": `INSERT INTO app.stock_movements (tenant_id, location_id, product_id, movement_type, quantity_delta, unit_cost, business_date)
		                                  VALUES ($1,$2,$3,'adjustment',500,10000,'2026-11-27')`,
	} {
		err := e.app.DB.InActorTx(context.Background(), db.Actor{TenantID: e.tenantID}, func(ctx context.Context, tx pgx.Tx) error {
			_, qErr := tx.Exec(ctx, sqlStmt, e.tenantID, bUUID, pUUID)
			return qErr
		})
		require.Error(t, err, "bms_app must be denied %s", name)
		require.Contains(t, err.Error(), "42501", "expected PostgreSQL privilege denial (42501) for %s, got: %v", name, err)
	}
	require.True(t, stockQty(t, e.owner, prodID).Equal(rd("0")))
}
