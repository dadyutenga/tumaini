package app_test

import (
	"context"
	"net/http"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"bms/internal/platform/db"
)

func createIssuedInvoice(t *testing.T, e *env, amount string) (string, string) {
	t.Helper()
	cl := e.owner.call(t, http.MethodPost, "/api/v1/clients", map[string]any{
		"name":  "Acme Client " + uuid.NewString()[:8],
		"phone": uniquePhone(),
	}, "")
	require.Equal(t, 201, cl.code, cl.raw)
	clientID := str(cl.data["id"])

	inv := e.owner.call(t, http.MethodPost, "/api/v1/service-invoices", map[string]any{
		"client_id": clientID,
		"lines": []map[string]any{
			{"description": "Consulting retainer", "qty": "1", "unit_price": amount},
		},
	}, "")
	require.Equal(t, 201, inv.code, inv.raw)
	invID := str(inv.data["id"])

	iss := e.owner.call(t, http.MethodPost, "/api/v1/service-invoices/"+invID+"/issue", map[string]any{}, "")
	require.Equal(t, 200, iss.code, iss.raw)
	return clientID, invID
}

func accountLedgerBalance(t *testing.T, e *env, accountID string) (current, ledgerSum decimal.Decimal) {
	t.Helper()
	err := e.app.DB.InJobsTx(context.Background(), db.Actor{}, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, "SELECT current_balance FROM app.financial_accounts WHERE id = $1", accountID).Scan(&current); err != nil {
			return err
		}
		return tx.QueryRow(ctx, "\n\t\t\tSELECT COALESCE(SUM(CASE direction WHEN 'in' THEN amount ELSE -amount END), 0)\n\t\t\tFROM app.financial_transactions WHERE account_id = $1", accountID).Scan(&ledgerSum)
	})
	require.NoError(t, err)
	return current, ledgerSum
}

// Question 1 & Checklist #7, #8, #12: Concurrent payments against the same invoice.
func TestFinancialIntegrity_ConcurrentInvoicePayments(t *testing.T) {
	e := newEnv(t, defaultNow)
	till := e.cashAccount(t)
	_, invID := createIssuedInvoice(t, e, "100000")

	cashier1, _ := e.user(t, "manager")
	cashier2, _ := e.user(t, "manager")

	// Two cashiers simultaneously try to collect the full 100,000 balance with different idempotency keys.
	start := make(chan struct{})
	codes := make([]int, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	for i, c := range []*client{cashier1, cashier2} {
		go func(idx int, cl *client) {
			defer wg.Done()
			<-start
			r := cl.call(t, http.MethodPost, "/api/v1/service-invoices/"+invID+"/payments", map[string]any{
				"amount":           "100000",
				"method":           "cash",
				"money_account_id": till,
			}, key())
			codes[idx] = r.code
		}(i, c)
	}
	close(start)
	wg.Wait()

	okCount, conflictCount := 0, 0
	for _, code := range codes {
		if code == 201 {
			okCount++
		} else if code == 409 {
			conflictCount++
		}
	}
	require.Equal(t, 1, okCount, "exactly one concurrent full payment may succeed, got %v", codes)
	require.Equal(t, 1, conflictCount, "second concurrent payment must be rejected with 409, got %v", codes)

	inv := e.owner.call(t, http.MethodGet, "/api/v1/service-invoices/"+invID, nil, "")
	require.Equal(t, 200, inv.code, inv.raw)
	require.Equal(t, "paid", str(inv.data["status"]))
	require.True(t, dec(inv.data["paid_total"]).Equal(rd("100000")), inv.raw)
	require.True(t, dec(inv.data["balance"]).IsZero(), inv.raw)

	cur, sum := accountLedgerBalance(t, e, till)
	require.True(t, cur.Equal(rd("100000")), "current_balance=%s", cur)
	require.True(t, sum.Equal(cur), "ledger=%s current=%s", sum, cur)

	// Overlapping concurrent partial payments (60,000 + 60,000 against a 100,000 invoice).
	_, inv2 := createIssuedInvoice(t, e, "100000")
	start2 := make(chan struct{})
	codes2 := make([]int, 2)
	wg.Add(2)
	for i, c := range []*client{cashier1, cashier2} {
		go func(idx int, cl *client) {
			defer wg.Done()
			<-start2
			r := cl.call(t, http.MethodPost, "/api/v1/service-invoices/"+inv2+"/payments", map[string]any{
				"amount":           "60000",
				"method":           "cash",
				"money_account_id": till,
			}, key())
			codes2[idx] = r.code
		}(i, c)
	}
	close(start2)
	wg.Wait()

	ok2, conf2 := 0, 0
	for _, code := range codes2 {
		if code == 201 {
			ok2++
		} else if code == 409 {
			conf2++
		}
	}
	require.Equal(t, 1, ok2, "only one 60k payment may fit on 100k invoice, got %v", codes2)
	require.Equal(t, 1, conf2, "second 60k payment must fail with 409 overpayment, got %v", codes2)

	// Remaining 40,000 settles the invoice cleanly.
	final := cashier1.call(t, http.MethodPost, "/api/v1/service-invoices/"+inv2+"/payments", map[string]any{
		"amount":           "40000",
		"method":           "cash",
		"money_account_id": till,
	}, key())
	require.Equal(t, 201, final.code, final.raw)

	inv2Get := e.owner.call(t, http.MethodGet, "/api/v1/service-invoices/"+inv2, nil, "")
	require.Equal(t, "paid", str(inv2Get.data["status"]))
	require.True(t, dec(inv2Get.data["paid_total"]).Equal(rd("100000")))

	cur, sum = accountLedgerBalance(t, e, till)
	require.True(t, cur.Equal(rd("200000")), "current_balance=%s", cur)
	require.True(t, sum.Equal(cur), "ledger=%s current=%s", sum, cur)
}

// Question 3 & Checklist #10, #11: Duplicate 5x submission idempotency & duplicate reference rejection.
func TestFinancialIntegrity_DuplicatePayment5xAndIdempotency(t *testing.T) {
	e := newEnv(t, defaultNow)
	till := e.cashAccount(t)
	_, invID := createIssuedInvoice(t, e, "100000")

	idemKey := key()
	body := map[string]any{
		"amount":           "25000",
		"method":           "cash",
		"money_account_id": till,
	}

	// Submit the exact same payment request 5 times concurrently with the same Idempotency-Key.
	start := make(chan struct{})
	results := make([]result, 5)
	var wg sync.WaitGroup
	wg.Add(5)
	for i := 0; i < 5; i++ {
		go func(idx int) {
			defer wg.Done()
			<-start
			results[idx] = e.owner.call(t, http.MethodPost, "/api/v1/service-invoices/"+invID+"/payments", body, idemKey)
		}(i)
	}
	close(start)
	wg.Wait()

	var paymentID, receiptNo string
	for i, r := range results {
		require.Equal(t, 201, r.code, "attempt %d: %s", i, r.raw)
		if i == 0 {
			paymentID = str(r.data["id"])
			receiptNo = str(r.data["number"])
		} else {
			require.Equal(t, paymentID, str(r.data["id"]), "all 5 replays must return the exact same payment id")
			require.Equal(t, receiptNo, str(r.data["number"]))
		}
	}

	// Only 25,000 was recorded — never 125,000!
	inv := e.owner.call(t, http.MethodGet, "/api/v1/service-invoices/"+invID, nil, "")
	require.True(t, dec(inv.data["paid_total"]).Equal(rd("25000")), inv.raw)
	cur, sum := accountLedgerBalance(t, e, till)
	require.True(t, cur.Equal(rd("25000")))
	require.True(t, sum.Equal(rd("25000")))

	// Reusing the same Idempotency-Key with a DIFFERENT payload is rejected with 409 idempotency_mismatch.
	mismatch := e.owner.call(t, http.MethodPost, "/api/v1/service-invoices/"+invID+"/payments", map[string]any{
		"amount":           "30000",
		"method":           "cash",
		"money_account_id": till,
	}, idemKey)
	require.Equal(t, 409, mismatch.code, mismatch.raw)

	// Duplicate non-cash reference with a DIFFERENT Idempotency-Key is blocked by payments_received_ref_uq.
	bank := e.owner.call(t, http.MethodPost, "/api/v1/money-accounts", map[string]any{
		"name": "CRDB Bank", "type": "bank", "opening_balance": "0",
	}, "")
	require.Equal(t, 201, bank.code, bank.raw)
	bankID := str(bank.data["id"])

	p1 := e.owner.call(t, http.MethodPost, "/api/v1/service-invoices/"+invID+"/payments", map[string]any{
		"amount": "10000", "method": "bank", "money_account_id": bankID, "reference": "BNK-DUP-001",
	}, key())
	require.Equal(t, 201, p1.code, p1.raw)

	p2 := e.owner.call(t, http.MethodPost, "/api/v1/service-invoices/"+invID+"/payments", map[string]any{
		"amount": "10000", "method": "bank", "money_account_id": bankID, "reference": "BNK-DUP-001",
	}, key())
	require.Equal(t, 409, p2.code, p2.raw)

	// Missing Idempotency-Key on a payment endpoint is rejected (400).
	noKey := e.owner.call(t, http.MethodPost, "/api/v1/service-invoices/"+invID+"/payments", map[string]any{
		"amount": "5000", "method": "cash", "money_account_id": till,
	}, "")
	require.Equal(t, 400, noKey.code, noKey.raw)
}

// Question 2: Atomicity — failed allocation rolls back payment, ledger, and account balance completely.
func TestFinancialIntegrity_TransactionAtomicityRollback(t *testing.T) {
	e := newEnv(t, defaultNow)
	till := e.cashAccount(t)
	clientID, invID := createIssuedInvoice(t, e, "50000")

	// Attempt to insert a payment_received + over-allocated payment_allocation inside a single transaction.
	// The allocation trigger / CHECK constraint must abort and roll back the payment & ledger too.
	var actor db.Actor
	err := e.app.DB.InJobsTx(context.Background(), db.Actor{}, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT user_id, id FROM app.memberships LIMIT 1`).
			Scan(&actor.UserID, &actor.MembershipID)
	})
	require.NoError(t, err)

	txErr := e.app.DB.InActorTx(context.Background(), actor, func(ctx context.Context, tx pgx.Tx) error {
		var payID uuid.UUID
		if err := tx.QueryRow(ctx, "\n\t\t\tINSERT INTO app.payments_received ( receipt_no, customer_id, business_unit_id, location_id, account_id, method, purpose, amount, business_date)\n\t\t\tVALUES ( 'RCP-ATOMIC-1', $1,\n\t\t\t        (SELECT business_unit_id FROM app.locations WHERE id = $2),\n\t\t\t        $2, $3, 'cash', 'final', 60000, '2026-11-16')\n\t\t\tRETURNING id", clientID, e.branch, till).Scan(&payID); err != nil {
			return err
		}
		// This allocation exceeds invoice total (50,000) and MUST fail, rolling back the payment & ledger.
		_, err := tx.Exec(ctx, "\n\t\t\tINSERT INTO app.payment_allocations (payment_received_id, invoice_id, amount)\n\t\t\tVALUES ($1, $2, 60000)", payID, invID)
		return err
	})
	require.Error(t, txErr)

	// Verify ZERO orphan payments_received, ZERO ledger rows, and ZERO balance change.
	var payCount, txnCount int
	err = e.app.DB.InJobsTx(context.Background(), db.Actor{}, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM app.payments_received WHERE receipt_no = 'RCP-ATOMIC-1'").Scan(&payCount); err != nil {
			return err
		}
		return tx.QueryRow(ctx, "SELECT count(*) FROM app.financial_transactions WHERE account_id = $1", till).Scan(&txnCount)
	})
	require.NoError(t, err)
	require.Equal(t, 0, payCount, "failed transaction must not leave orphan payment")
	require.Equal(t, 0, txnCount, "failed transaction must not leave orphan ledger entry")

	cur, sum := accountLedgerBalance(t, e, till)
	require.True(t, cur.IsZero())
	require.True(t, sum.IsZero())
}

// Question 4 & Checklist #14, #15: Immutability of finalized sales, invoices, payments, ledger; void preserves original.
func TestFinancialIntegrity_ImmutabilityAndVoidPreservation(t *testing.T) {
	e := newEnv(t, defaultNow)
	till := e.cashAccount(t)
	prodID := e.product(t, "Laptop", "50000", "5", "30000")

	// 1. Completed POS sale.
	saleRes := e.owner.call(t, http.MethodPost, "/api/v1/sales", map[string]any{
		"items":    []map[string]any{{"product_id": prodID, "qty": "1"}},
		"payments": []map[string]any{{"method": "cash", "money_account_id": till, "amount": "50000"}},
	}, key())
	require.Equal(t, 201, saleRes.code, saleRes.raw)
	saleID := str(saleRes.data["id"])

	// 2. Issued service invoice + payment.
	_, invID := createIssuedInvoice(t, e, "40000")
	payRes := e.owner.call(t, http.MethodPost, "/api/v1/service-invoices/"+invID+"/payments", map[string]any{
		"amount": "40000", "method": "cash", "money_account_id": till,
	}, key())
	require.Equal(t, 201, payRes.code, payRes.raw)
	payID := str(payRes.data["id"])

	// Fetch owner's actor to test direct SQL tampering via the app DB role (bms_app).
	var actor db.Actor
	err := e.app.DB.InJobsTx(context.Background(), db.Actor{}, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT user_id, id FROM app.memberships LIMIT 1`).
			Scan(&actor.UserID, &actor.MembershipID)
	})
	require.NoError(t, err)

	tamper := func(name, sql string, args ...any) {
		t.Helper()
		err := e.app.DB.InActorTx(context.Background(), actor, func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, sql, args...)
			return err
		})
		require.Error(t, err, "tampering must be blocked: %s", name)
	}

	tamper("edit completed sale total", `UPDATE app.sales SET subtotal = 1, total = 1 WHERE id = $1`, saleID)
	tamper("delete completed sale", `DELETE FROM app.sales WHERE id = $1`, saleID)
	tamper("edit completed sale line", `UPDATE app.sale_lines SET unit_price = 1, line_total = 1 WHERE sale_id = $1`, saleID)
	tamper("edit issued invoice total", `UPDATE app.invoices SET subtotal = 1, total = 1 WHERE id = $1`, invID)
	tamper("delete issued invoice", `DELETE FROM app.invoices WHERE id = $1`, invID)
	tamper("delete issued invoice lines", `DELETE FROM app.invoice_lines WHERE invoice_id = $1`, invID)
	tamper("edit posted payment amount", `UPDATE app.payments_received SET amount = 1 WHERE id = $1`, payID)
	tamper("delete posted payment", `DELETE FROM app.payments_received WHERE id = $1`, payID)
	tamper("edit financial_transactions", `UPDATE app.financial_transactions SET amount = 1 WHERE true`)
	tamper("delete financial_transactions", `DELETE FROM app.financial_transactions WHERE true`)

	// Voiding the invoice payment via API preserves the original record and posts a compensating reversal row.
	voidRes := e.owner.call(t, http.MethodPost, "/api/v1/payments/"+payID+"/void", map[string]any{
		"reason": "Duplicate receipt entered in error",
	}, key())
	require.Equal(t, 200, voidRes.code, voidRes.raw)

	var pStatus, voidReason string
	var origCount, revCount int
	err = e.app.DB.InJobsTx(context.Background(), db.Actor{}, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, "SELECT status::text, COALESCE(void_reason,'') FROM app.payments_received WHERE id = $1", payID).Scan(&pStatus, &voidReason); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM app.financial_transactions WHERE payment_received_id = $1 AND direction = 'in'", payID).Scan(&origCount); err != nil {
			return err
		}
		return tx.QueryRow(ctx, "\n\t\t\tSELECT count(*) FROM app.financial_transactions r\n\t\t\tJOIN app.financial_transactions o ON o.id = r.reverses_transaction_id\n\t\t\tWHERE o.payment_received_id = $1 AND r.txn_type = 'reversal' AND r.direction = 'out'", payID).Scan(&revCount)
	})
	require.NoError(t, err)
	require.Equal(t, "voided", pStatus)
	require.Equal(t, "Duplicate receipt entered in error", voidReason)
	require.Equal(t, 1, origCount, "original ledger row must remain intact")
	require.Equal(t, 1, revCount, "compensating reversal row must be appended")

	cur, sum := accountLedgerBalance(t, e, till)
	require.True(t, cur.Equal(rd("50000")), "only the 50k sale remains in cash")
	require.True(t, sum.Equal(cur))
}

// Question 5 & Checklist #5, #13: Refunds require authorization, respect sale-level discounts, and can never exceed paid amount.
func TestFinancialIntegrity_RefundsAndDiscountProRata(t *testing.T) {
	e := newEnv(t, defaultNow)
	till := e.cashAccount(t)
	prodID := e.product(t, "Monitor", "10000", "10", "6000")
	staff, _ := e.user(t, "staff")

	// Staff has a 5% discount limit: requesting a 10% discount cannot complete directly and is routed to approval (202).
	overLimitDisc := staff.call(t, http.MethodPost, "/api/v1/sales", map[string]any{
		"items":    []map[string]any{{"product_id": prodID, "qty": "1"}},
		"discount": map[string]any{"type": "percent", "value": "10"},
		"payments": []map[string]any{{"method": "cash", "money_account_id": till, "amount": "9000"}},
	}, key())
	require.Equal(t, 202, overLimitDisc.code, overLimitDisc.raw)
	require.Equal(t, "pending_approval", str(overLimitDisc.data["status"]), overLimitDisc.raw)

	// A user whose role lacks shop.discount.apply is outright rejected with 403.
	noDiscRole := e.owner.call(t, http.MethodPost, "/api/v1/roles", map[string]any{
		"name":        "No Discount Cashier",
		"permissions": []map[string]any{{"key": "shop.sale.create", "scope": "branch"}},
	}, "")
	require.Equal(t, 201, noDiscRole.code, noDiscRole.raw)
	ndPhone := uniquePhone()
	ndUser := e.owner.call(t, http.MethodPost, "/api/v1/users", map[string]any{
		"name": "ND Cashier", "phone": ndPhone, "password": "nd-password-1",
		"role_id": noDiscRole.data["id"], "branch_ids": []string{e.branch},
	}, "")
	require.Equal(t, 201, ndUser.code, ndUser.raw)
	ndClient := &client{app: e.app.Fiber, token: login(t, e.app.Fiber, ndPhone, "nd-password-1"), branch: e.branch}
	noDisc := ndClient.call(t, http.MethodPost, "/api/v1/sales", map[string]any{
		"items":    []map[string]any{{"product_id": prodID, "qty": "1"}},
		"discount": map[string]any{"type": "percent", "value": "5"},
		"payments": []map[string]any{{"method": "cash", "money_account_id": till, "amount": "9500"}},
	}, key())
	require.Equal(t, 403, noDisc.code, noDisc.raw)

	// Owner sells 2 units @ 10,000 with 10% sale-level discount -> subtotal 20,000, discount 2,000, total 18,000.
	sale := e.owner.call(t, http.MethodPost, "/api/v1/sales", map[string]any{
		"items":    []map[string]any{{"product_id": prodID, "qty": "2"}},
		"discount": map[string]any{"type": "percent", "value": "10"},
		"payments": []map[string]any{{"method": "cash", "money_account_id": till, "amount": "18000"}},
	}, key())
	require.Equal(t, 201, sale.code, sale.raw)
	saleID := str(sale.data["id"])
	require.True(t, dec(sale.data["total"]).Equal(rd("18000")), sale.raw)

	saleDetail := e.owner.call(t, http.MethodGet, "/api/v1/sales/"+saleID, nil, "")
	require.Equal(t, 200, saleDetail.code, saleDetail.raw)
	lineID := str(asList(saleDetail.data["items"])[0]["id"])

	// Return 1 unit: pro-rated discounted refund MUST be 9,000 (not 10,000!).
	ret1 := staff.call(t, http.MethodPost, "/api/v1/sales/"+saleID+"/returns", map[string]any{
		"reason":     "Defective screen",
		"resolution": "refund",
		"items":      []map[string]any{{"sale_item_id": lineID, "qty": "1"}},
	}, key())
	require.Equal(t, 201, ret1.code, ret1.raw)
	ret1ID := str(ret1.data["id"])
	require.True(t, dec(ret1.data["refund_total"]).Equal(rd("9000")), ret1.raw)

	// Requester cannot approve own return (SoD).
	selfApp := staff.call(t, http.MethodPost, "/api/v1/sale-returns/"+ret1ID+"/approve", nil, "")
	require.True(t, selfApp.code == 403, "self-approval must be forbidden: %d %s", selfApp.code, selfApp.raw)

	// Owner approves and completes return 1 -> refunds 9,000.
	require.Equal(t, 200, e.owner.call(t, http.MethodPost, "/api/v1/sale-returns/"+ret1ID+"/approve", nil, "").code)
	comp1 := e.owner.call(t, http.MethodPost, "/api/v1/sale-returns/"+ret1ID+"/complete", map[string]any{
		"money_account_id": till, "method": "cash",
	}, key())
	require.Equal(t, 200, comp1.code, comp1.raw)

	// Return 2nd unit: refunds remaining 9,000 (total refunded = 18,000 == amount_paid).
	ret2 := staff.call(t, http.MethodPost, "/api/v1/sales/"+saleID+"/returns", map[string]any{
		"reason":     "Customer returned second unit",
		"resolution": "refund",
		"items":      []map[string]any{{"sale_item_id": lineID, "qty": "1"}},
	}, key())
	require.Equal(t, 201, ret2.code, ret2.raw)
	ret2ID := str(ret2.data["id"])
	require.True(t, dec(ret2.data["refund_total"]).Equal(rd("9000")), ret2.raw)
	require.Equal(t, 200, e.owner.call(t, http.MethodPost, "/api/v1/sale-returns/"+ret2ID+"/approve", nil, "").code)
	comp2 := e.owner.call(t, http.MethodPost, "/api/v1/sale-returns/"+ret2ID+"/complete", map[string]any{
		"money_account_id": till, "method": "cash",
	}, key())
	require.Equal(t, 200, comp2.code, comp2.raw)

	cur, sum := accountLedgerBalance(t, e, till)
	require.True(t, cur.IsZero(), "full refund brings cash back to 0, got %s", cur)
	require.True(t, sum.IsZero())

	// Partially paid credit sale: total 20,000, paid 8,000. Refunding 1 unit (10,000 > 8,000 paid) must be rejected.
	cust := e.owner.call(t, http.MethodPost, "/api/v1/customers", map[string]any{
		"name": "Credit Buyer", "phone": uniquePhone(), "credit_limit": "50000", "payment_terms_days": 30,
	}, "")
	require.Equal(t, 201, cust.code, cust.raw)
	custID := str(cust.data["id"])

	creditSale := e.owner.call(t, http.MethodPost, "/api/v1/sales", map[string]any{
		"customer_id": custID,
		"due_date":    "2026-12-16",
		"items":       []map[string]any{{"product_id": prodID, "qty": "2"}},
		"payments":    []map[string]any{{"method": "cash", "money_account_id": till, "amount": "8000"}},
	}, key())
	require.Equal(t, 201, creditSale.code, creditSale.raw)
	creditSaleID := str(creditSale.data["id"])
	creditDetail := e.owner.call(t, http.MethodGet, "/api/v1/sales/"+creditSaleID, nil, "")
	creditLineID := str(asList(creditDetail.data["items"])[0]["id"])

	overRefund := staff.call(t, http.MethodPost, "/api/v1/sales/"+creditSaleID+"/returns", map[string]any{
		"reason":     "Exceeds paid amount",
		"resolution": "refund",
		"items":      []map[string]any{{"sale_item_id": creditLineID, "qty": "1"}},
	}, key())
	require.Equal(t, 409, overRefund.code, overRefund.raw)
}

// Checklist #12: Concurrent bidirectional account transfers (A -> B and B -> A) do not deadlock and conserve balance.
func TestFinancialIntegrity_ConcurrentBidirectionalTransfers(t *testing.T) {
	e := newEnv(t, defaultNow)
	a1 := e.owner.call(t, http.MethodPost, "/api/v1/money-accounts", map[string]any{
		"name": "Bank A", "type": "bank", "opening_balance": "100000",
	}, "")
	require.Equal(t, 201, a1.code, a1.raw)
	idA := str(a1.data["id"])

	a2 := e.owner.call(t, http.MethodPost, "/api/v1/money-accounts", map[string]any{
		"name": "Bank B", "type": "bank", "opening_balance": "100000",
	}, "")
	require.Equal(t, 201, a2.code, a2.raw)
	idB := str(a2.data["id"])

	start := make(chan struct{})
	codes := make([]int, 10)
	var wg sync.WaitGroup
	wg.Add(10)
	for i := 0; i < 10; i++ {
		from, to := idA, idB
		if i%2 == 1 {
			from, to = idB, idA
		}
		go func(idx int, f, tAcc string) {
			defer wg.Done()
			<-start
			r := e.owner.call(t, http.MethodPost, "/api/v1/money-transfers", map[string]any{
				"from_account_id": f, "to_account_id": tAcc, "amount": "1000", "note": "Concurrent rebalance",
			}, key())
			codes[idx] = r.code
		}(i, from, to)
	}
	close(start)
	wg.Wait()

	for i, code := range codes {
		require.Equal(t, 201, code, "transfer %d failed (possible deadlock): %v", i, codes)
	}
	balA, sumA := accountLedgerBalance(t, e, idA)
	balB, sumB := accountLedgerBalance(t, e, idB)
	require.True(t, balA.Equal(rd("100000")), "balA=%s", balA)
	require.True(t, balB.Equal(rd("100000")), "balB=%s", balB)
	require.True(t, sumA.Equal(balA))
	require.True(t, sumB.Equal(balB))
}

// Question 6 & Checklist #16, #17, #18, #19:
// Reports reconcile penny-for-penny (including sale-level discounts & multiple payroll runs) + day closing cash variance.
func TestFinancialIntegrity_ReportReconciliationAndDayClosing(t *testing.T) {
	e := newEnv(t, defaultNow)
	till := e.cashAccount(t)
	prodID := e.product(t, "Desk", "10000", "10", "4000")

	// 1. Discounted sale: 2 x 10,000 = 20,000, 10% sale discount = 2,000 -> total 18,000 cash.
	sale := e.owner.call(t, http.MethodPost, "/api/v1/sales", map[string]any{
		"items":    []map[string]any{{"product_id": prodID, "qty": "2"}},
		"discount": map[string]any{"type": "percent", "value": "10"},
		"payments": []map[string]any{{"method": "cash", "money_account_id": till, "amount": "18000"}},
	}, key())
	require.Equal(t, 201, sale.code, sale.raw)

	// 2. Service invoice 50,000, partially paid 30,000 cash (leaving 20,000 AR).
	_, invID := createIssuedInvoice(t, e, "50000")
	require.Equal(t, 201, e.owner.call(t, http.MethodPost, "/api/v1/service-invoices/"+invID+"/payments", map[string]any{
		"amount": "30000", "method": "cash", "money_account_id": till,
	}, key()).code)

	// 3. Expense 5,000 submitted by staff, approved & paid by owner from cash.
	cats := e.owner.call(t, http.MethodGet, "/api/v1/expense-categories", nil, "")
	require.Equal(t, 200, cats.code, cats.raw)
	staff, _ := e.user(t, "staff")
	exp := staff.call(t, http.MethodPost, "/api/v1/expenses", map[string]any{
		"category_id": str(asList(cats.body["data"])[0]["id"]), "amount": "5000", "description": "Office supplies",
	}, key())
	require.Equal(t, 201, exp.code, exp.raw)
	require.Equal(t, 200, e.owner.call(t, http.MethodPost, "/api/v1/expenses/"+str(exp.data["id"])+"/approve", map[string]any{"note": "ok"}, "").code)
	require.Equal(t, 200, e.owner.call(t, http.MethodPost, "/api/v1/expenses/"+str(exp.data["id"])+"/pay", map[string]any{"money_account_id": till}, key()).code)

	// 4. TWO payroll runs in the period (Oct & Nov 2026), each 10,000 salary, paid from cash.
	emp := e.owner.call(t, http.MethodPost, "/api/v1/employees", map[string]any{
		"name": "Amina Payroll", "phone": uniquePhone(), "hire_date": "2026-01-01",
		"base_pay": "10000", "pay_method": "cash", "branch_id": e.branch,
	}, "")
	require.Equal(t, 201, emp.code, emp.raw)

	approver := payrollApprover(t, e)
	for _, m := range []int{10, 11} {
		run := e.owner.call(t, http.MethodPost, "/api/v1/payroll-runs", map[string]any{
			"period_year": 2026, "period_month": m,
		}, "")
		require.Equal(t, 201, run.code, run.raw)
		runID := str(run.data["id"])
		require.Equal(t, 200, e.owner.call(t, http.MethodPost, "/api/v1/payroll-runs/"+runID+"/calculate", nil, "").code)
		require.Equal(t, 200, approver.call(t, http.MethodPost, "/api/v1/payroll-runs/"+runID+"/approve", nil, key()).code)
		payRun := approver.call(t, http.MethodPost, "/api/v1/payroll-runs/"+runID+"/pay", map[string]any{
			"money_account_id": till,
		}, key())
		require.Equal(t, 200, payRun.code, payRun.raw)
	}

	// Verify Sales Summary reconciles with discounted sale total (18,000, not 20,000!).
	ss := e.owner.call(t, http.MethodGet, "/api/v1/reports/sales/summary?from=2026-10-01&to=2026-11-30", nil, "")
	require.Equal(t, 200, ss.code, ss.raw)
	ssTot := ss.data["totals"].(map[string]any)
	require.True(t, dec(ssTot["discounts"]).Equal(rd("2000")), ss.raw)
	require.True(t, dec(ssTot["net_sales"]).Equal(rd("18000")), ss.raw)
	require.True(t, dec(ssTot["cost"]).Equal(rd("8000")), ss.raw)
	require.True(t, dec(ssTot["gross_profit"]).Equal(rd("10000")), ss.raw)

	// Verify P&L includes BOTH payroll runs (20,000) + expense (5,000) = 25,000 operating expenses!
	// Income = 18,000 (sales) + 50,000 (service invoice) = 68,000
	// Expenses = 8,000 (COGS) + 5,000 (office supplies) + 20,000 (2 payroll runs) = 33,000
	// Net Profit = 35,000!
	pl := e.owner.call(t, http.MethodGet, "/api/v1/reports/profit-loss?from=2026-10-01&to=2026-11-30", nil, "")
	require.Equal(t, 200, pl.code, pl.raw)
	require.True(t, dec(pl.data["sales_revenue"]).Equal(rd("18000")), pl.raw)
	require.True(t, dec(pl.data["service_income"]).Equal(rd("50000")), pl.raw)
	require.True(t, dec(pl.data["operating_expenses"]).Equal(rd("25000")), pl.raw)
	require.True(t, dec(pl.data["net_profit"]).Equal(rd("35000")), pl.raw)

	// Verify Cash Flow closing == account current_balance == ledger sum:
	// 18,000 + 30,000 - 5,000 - 10,000 - 10,000 = 23,000.
	cur, sum := accountLedgerBalance(t, e, till)
	require.True(t, cur.Equal(rd("23000")), "current=%s", cur)
	require.True(t, sum.Equal(rd("23000")), "ledger=%s", sum)

	cf := e.owner.call(t, http.MethodGet, "/api/v1/reports/cash-flow?from=2026-10-01&to=2026-11-30", nil, "")
	require.Equal(t, 200, cf.code, cf.raw)
	require.True(t, dec(cf.data["totals"].(map[string]any)["closing"]).Equal(rd("23000")), cf.raw)

	// Verify Balance Sheet balances.
	bs := e.owner.call(t, http.MethodGet, "/api/v1/reports/balance-sheet?as_of=2026-11-30", nil, "")
	require.Equal(t, 200, bs.code, bs.raw)
	require.Equal(t, true, bs.data["balanced"], bs.raw)

	// Verify Day Closing cash variance (shortage of 500 requires note, locks day, reopening requires reason).
	noNote := e.owner.call(t, http.MethodPost, "/api/v1/day-closings", map[string]any{
		"date": "2026-11-16", "counted_cash": "22500",
	}, "")
	require.Equal(t, 400, noNote.code, noNote.raw)

	closed := e.owner.call(t, http.MethodPost, "/api/v1/day-closings", map[string]any{
		"date": "2026-11-16", "counted_cash": "22500", "note": "500 TZS torn note shortage",
	}, "")
	require.Equal(t, 201, closed.code, closed.raw)
	require.Equal(t, "TZS", str(closed.data["currency"]), closed.raw)
	require.True(t, dec(closed.data["expected_cash"]).Equal(rd("23000")), closed.raw)
	require.True(t, dec(closed.data["counted_cash"]).Equal(rd("22500")), closed.raw)
	require.True(t, dec(closed.data["difference"]).Equal(rd("-500")), closed.raw)
	closeID := str(closed.data["id"])

	summary := e.owner.call(t, http.MethodGet, "/api/v1/day-closings/summary?date=2026-11-16", nil, "")
	require.Equal(t, 200, summary.code, summary.raw)
	require.Equal(t, "TZS", str(summary.data["currency"]), summary.raw)

	stmt := e.owner.call(t, http.MethodGet, "/api/v1/money-accounts/"+till+"/statement?from=2026-11-01&to=2026-11-30", nil, "")
	require.Equal(t, 200, stmt.code, stmt.raw)
	require.Equal(t, "TZS", str(stmt.data["currency"]), stmt.raw)

	// Posting a payment on the closed business date MUST fail (day locked).
	blocked := e.owner.call(t, http.MethodPost, "/api/v1/service-invoices/"+invID+"/payments", map[string]any{
		"amount": "5000", "method": "cash", "money_account_id": till, "date": "2026-11-16",
	}, key())
	require.Equal(t, 409, blocked.code, blocked.raw)

	// Reopening without a reason fails; with reason succeeds and unlocks the day.
	reopenNoReason := e.owner.call(t, http.MethodPost, "/api/v1/day-closings/"+closeID+"/reopen", map[string]any{"reason": ""}, "")
	require.Equal(t, 400, reopenNoReason.code, reopenNoReason.raw)
	reopenOK := e.owner.call(t, http.MethodPost, "/api/v1/day-closings/"+closeID+"/reopen", map[string]any{"reason": "Need to record late receipt"}, "")
	require.Equal(t, 200, reopenOK.code, reopenOK.raw)

	afterReopen := e.owner.call(t, http.MethodPost, "/api/v1/service-invoices/"+invID+"/payments", map[string]any{
		"amount": "5000", "method": "cash", "money_account_id": till, "date": "2026-11-16",
	}, key())
	require.Equal(t, 201, afterReopen.code, afterReopen.raw)
}

// TestFinancialIntegrity_NegativePaymentsAndConcurrentCreditLimit verifies that:
//  1. Negative payment amounts are explicitly rejected instead of silently ignored.
//  2. Non-cash overpayments are rejected while cash overpayments record exact change_due
//     and only post the net sale total into the ledger.
//  3. Concurrent credit sales for the same customer serialize on the customer row lock and
//     cannot breach the customer's credit_limit.
func TestFinancialIntegrity_NegativePaymentsAndConcurrentCreditLimit(t *testing.T) {
	e := newEnv(t, defaultNow)
	till := e.cashAccount(t)
	prod := e.product(t, "Flour 5kg", "10000", "50", "6000")

	// 1. Negative payment must be rejected with 400.
	neg := e.owner.call(t, http.MethodPost, "/api/v1/sales", map[string]any{
		"items":    []map[string]any{{"product_id": prod, "qty": "1"}},
		"payments": []map[string]any{{"method": "cash", "money_account_id": till, "amount": "-1000"}},
	}, key())
	require.Equal(t, 400, neg.code, neg.raw)

	// 2. Non-cash overpayment must be rejected; cash overpayment returns exact change_due
	//    while ledger receives only the sale total (10000).
	bank := e.owner.call(t, http.MethodPost, "/api/v1/money-accounts", map[string]any{
		"name": "Bank Overpay Check", "type": "bank",
	}, "")
	require.Equal(t, 201, bank.code, bank.raw)
	bankID := str(bank.data["id"])
	nonCashOver := e.owner.call(t, http.MethodPost, "/api/v1/sales", map[string]any{
		"items": []map[string]any{{"product_id": prod, "qty": "1"}},
		"payments": []map[string]any{
			{"method": "bank", "money_account_id": bankID, "amount": "15000", "reference": "REF-OVER"},
		},
	}, key())
	require.Equal(t, 400, nonCashOver.code, nonCashOver.raw)

	cashOver := e.owner.call(t, http.MethodPost, "/api/v1/sales", map[string]any{
		"items": []map[string]any{{"product_id": prod, "qty": "1"}},
		"payments": []map[string]any{
			{"method": "cash", "money_account_id": till, "amount": "15000"},
		},
	}, key())
	require.Equal(t, 201, cashOver.code, cashOver.raw)
	require.True(t, dec(cashOver.data["total"]).Equal(rd("10000")), cashOver.raw)
	require.True(t, dec(cashOver.data["paid_total"]).Equal(rd("10000")), cashOver.raw)
	require.True(t, dec(cashOver.data["change_due"]).Equal(rd("5000")), cashOver.raw)

	// 3. Concurrent credit sales cannot breach a customer's credit_limit.
	cust := e.owner.call(t, http.MethodPost, "/api/v1/customers", map[string]any{
		"name": "Capped Credit Customer", "credit_limit": "10000",
	}, "")
	require.Equal(t, 201, cust.code, cust.raw)
	custID := str(cust.data["id"])

	start := make(chan struct{})
	var wg sync.WaitGroup
	codes := make([]int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			r := e.owner.call(t, http.MethodPost, "/api/v1/sales", map[string]any{
				"customer_id": custID,
				"items":       []map[string]any{{"product_id": prod, "qty": "1"}},
				"payments":    []map[string]any{},
			}, key())
			codes[idx] = r.code
		}(i)
	}
	close(start)
	wg.Wait()

	created, conflicted := 0, 0
	for _, code := range codes {
		if code == 201 {
			created++
		} else if code == 409 {
			conflicted++
		}
	}
	require.Equal(t, 1, created, "exactly one concurrent credit sale should succeed: %v", codes)
	require.Equal(t, 1, conflicted, "second concurrent credit sale must hit 409 credit_limit_exceeded: %v", codes)
}

// TestFinancialIntegrity_ServiceDiscountAuthorityControls verifies that discounts on service
// quotations and invoices enforce permission controls and authority percentage limits.
func TestFinancialIntegrity_ServiceDiscountAuthorityControls(t *testing.T) {
	e := newEnv(t, defaultNow)
	clientRes := e.owner.call(t, http.MethodPost, "/api/v1/clients", map[string]any{"name": "Discount Test Client"}, "")
	require.Equal(t, 201, clientRes.code, clientRes.raw)
	clientID := str(clientRes.data["id"])

	// 1. User with services.quotation.draft & services.invoice.draft ONLY (no discount permission)
	//    cannot apply discounts, while zero-discount drafts succeed.
	noDiscUser, _, _, _ := createCustomRoleUser(t, e, "Draft Only Clerk", []string{
		"services.client.view_all", "services.quotation.draft", "services.invoice.draft",
	}, e.branch)

	deniedQuote := noDiscUser.call(t, http.MethodPost, "/api/v1/service-quotations", map[string]any{
		"client_id": clientID,
		"discount":  "5000",
		"lines":     []map[string]any{{"description": "Logo Design", "quantity": "1", "unit_price": "50000"}},
	}, "")
	require.Equal(t, 403, deniedQuote.code, deniedQuote.raw)

	deniedInv := noDiscUser.call(t, http.MethodPost, "/api/v1/service-invoices", map[string]any{
		"client_id": clientID,
		"discount":  "5000",
		"lines":     []map[string]any{{"description": "Logo Design", "quantity": "1", "unit_price": "50000"}},
	}, "")
	require.Equal(t, 403, deniedInv.code, deniedInv.raw)

	// Zero-discount draft succeeds, but updating it to add a discount is rejected with 403.
	okInv := noDiscUser.call(t, http.MethodPost, "/api/v1/service-invoices", map[string]any{
		"client_id": clientID,
		"lines":     []map[string]any{{"description": "Logo Design", "quantity": "1", "unit_price": "50000"}},
	}, "")
	require.Equal(t, 201, okInv.code, okInv.raw)
	invID := str(okInv.data["id"])
	editDenied := noDiscUser.call(t, http.MethodPut, "/api/v1/service-invoices/"+invID, map[string]any{
		"client_id": clientID,
		"discount":  "1000",
		"lines":     []map[string]any{{"description": "Logo Design", "quantity": "1", "unit_price": "50000"}},
	}, "")
	require.Equal(t, 403, editDenied.code, editDenied.raw)

	// 2. Staff with provisional 0% sale_discount_pct cap cannot apply a 10% invoice discount,
	//    while Owner (unlimited) can.
	staff, _ := e.user(t, "staff")
	cappedInv := staff.call(t, http.MethodPost, "/api/v1/service-invoices", map[string]any{
		"client_id": clientID,
		"discount":  "5000",
		"lines":     []map[string]any{{"description": "Consulting", "quantity": "1", "unit_price": "50000"}},
	}, "")
	require.Equal(t, 403, cappedInv.code, cappedInv.raw)

	ownerInv := e.owner.call(t, http.MethodPost, "/api/v1/service-invoices", map[string]any{
		"client_id": clientID,
		"discount":  "5000",
		"lines":     []map[string]any{{"description": "Consulting", "quantity": "1", "unit_price": "50000"}},
	}, "")
	require.Equal(t, 201, ownerInv.code, ownerInv.raw)
	require.True(t, dec(ownerInv.data["total"]).Equal(rd("45000")), ownerInv.raw)
}

// TestFinancialIntegrity_ConcurrentContractorAndSupplierPayments verifies that concurrent
// payouts against the same contractor bill or supplier purchase serialize cleanly via row locks
// so balances can never be corrupted or double-paid.
func TestFinancialIntegrity_ConcurrentContractorAndSupplierPayments(t *testing.T) {
	e := newEnv(t, defaultNow)
	// Seed a funded bank account for payouts.
	bankRes := e.owner.call(t, http.MethodPost, "/api/v1/money-accounts", map[string]any{
		"name": "Payout Bank", "type": "bank", "opening_balance": "500000",
	}, "")
	require.Equal(t, 201, bankRes.code, bankRes.raw)
	bankID := str(bankRes.data["id"])

	// 1. Concurrent contractor payouts against a single 40,000 bill.
	clientRes := e.owner.call(t, http.MethodPost, "/api/v1/clients", map[string]any{"name": "Bill Client"}, "")
	require.Equal(t, 201, clientRes.code, clientRes.raw)
	projRes := e.owner.call(t, http.MethodPost, "/api/v1/projects", map[string]any{
		"client_id": str(clientRes.data["id"]), "name": "Print Project", "total_price": "100000",
	}, "")
	require.Equal(t, 201, projRes.code, projRes.raw)
	ctRes := e.owner.call(t, http.MethodPost, "/api/v1/contractors", map[string]any{"name": "Print House"}, "")
	require.Equal(t, 201, ctRes.code, ctRes.raw)
	ctID := str(ctRes.data["id"])
	costRes := e.owner.call(t, http.MethodPost, "/api/v1/service-costs", map[string]any{
		"kind": "contractor", "project_id": str(projRes.data["id"]), "contractor_id": ctID,
		"description": "Offset printing", "amount": "40000", "paid_now": false,
	}, key())
	require.Equal(t, 201, costRes.code, costRes.raw)

	start1 := make(chan struct{})
	var wg1 sync.WaitGroup
	ctCodes := make([]int, 2)
	for i := 0; i < 2; i++ {
		wg1.Add(1)
		go func(idx int) {
			defer wg1.Done()
			<-start1
			r := e.owner.call(t, http.MethodPost, "/api/v1/contractors/"+ctID+"/payments", map[string]any{
				"amount": "40000", "method": "bank", "money_account_id": bankID, "reference": "CT-PAY",
			}, key())
			ctCodes[idx] = r.code
		}(i)
	}
	close(start1)
	wg1.Wait()

	ctOK, ctConflict := 0, 0
	for _, code := range ctCodes {
		if code == 201 {
			ctOK++
		} else if code == 409 {
			ctConflict++
		}
	}
	require.Equal(t, 1, ctOK, "exactly one concurrent contractor payout must succeed: %v", ctCodes)
	require.Equal(t, 1, ctConflict, "second concurrent contractor payout must be rejected as 409 overpayment: %v", ctCodes)

	// 2. Concurrent supplier payouts explicitly allocated to the same 60,000 purchase order.
	supRes := e.owner.call(t, http.MethodPost, "/api/v1/suppliers", map[string]any{"name": "Bulk Supplier"}, "")
	require.Equal(t, 201, supRes.code, supRes.raw)
	supID := str(supRes.data["id"])
	prodID := e.product(t, "Cooking Oil", "8000", "", "6000")
	poRes := e.owner.call(t, http.MethodPost, "/api/v1/purchase-orders", map[string]any{
		"supplier_id": supID,
		"items":       []map[string]any{{"product_id": prodID, "qty": "10", "unit_cost": "6000"}},
	}, "")
	require.Equal(t, 201, poRes.code, poRes.raw)
	poID := str(poRes.data["id"])

	start2 := make(chan struct{})
	var wg2 sync.WaitGroup
	supCodes := make([]int, 2)
	for i := 0; i < 2; i++ {
		wg2.Add(1)
		go func(idx int) {
			defer wg2.Done()
			<-start2
			r := e.owner.call(t, http.MethodPost, "/api/v1/suppliers/"+supID+"/payments", map[string]any{
				"amount": "60000", "method": "bank", "money_account_id": bankID, "reference": "SUP-PAY",
				"allocations": []map[string]any{{"purchase_id": poID, "amount": "60000"}},
			}, key())
			supCodes[idx] = r.code
		}(i)
	}
	close(start2)
	wg2.Wait()

	supOK, supConflict := 0, 0
	for _, code := range supCodes {
		if code == 201 {
			supOK++
		} else if code == 409 {
			supConflict++
		}
	}
	require.Equal(t, 1, supOK, "exactly one concurrent supplier allocation must succeed: %v", supCodes)
	require.Equal(t, 1, supConflict, "second concurrent supplier allocation must be rejected with 409: %v", supCodes)
}

// TestFinancialIntegrity_ServerComputedTotalsTaxAndCashOverage verifies:
// 1. Frontend-submitted totals/unit prices are never trusted; totals & tax are computed on the backend.
// 2. Tax totals reconcile cleanly with GET /api/v1/reports/tax.
// 3. Cash drawer overages (counted > expected) require an explicit explanation note and record the exact positive variance.
func TestFinancialIntegrity_ServerComputedTotalsTaxAndCashOverage(t *testing.T) {
	e := newEnv(t, defaultNow)
	till := e.cashAccount(t)

	// Configure a default 18% VAT rate and create a product priced at 11,800 (10,000 net + 1,800 VAT).
	rates := e.owner.call(t, http.MethodGet, "/api/v1/tax-rates", nil, "")
	require.Equal(t, 200, rates.code, rates.raw)
	exempt := find(asList(rates.body["data"]), "name", "Exempt")
	updTax := e.owner.call(t, http.MethodPut, "/api/v1/tax-rates/"+str(exempt["id"]), map[string]any{
		"name": "VAT 18%", "rate": "18", "is_default": true, "is_active": true,
	}, "")
	require.Equal(t, 200, updTax.code, updTax.raw)

	prod := e.product(t, "Taxed Appliance", "11800", "10", "7000")

	// Client attempts to spoof unit_price=1, subtotal=1, tax_total=0, total=1 while paying 11800.
	sale := e.owner.call(t, http.MethodPost, "/api/v1/sales", map[string]any{
		"subtotal":  "1",
		"tax_total": "0",
		"total":     "1",
		"items": []map[string]any{
			{"product_id": prod, "qty": "1", "unit_price": "1", "line_total": "1"},
		},
		"payments": []map[string]any{
			{"method": "cash", "money_account_id": till, "amount": "11800"},
		},
	}, key())
	require.Equal(t, 201, sale.code, sale.raw)
	require.True(t, dec(sale.data["subtotal"]).Equal(rd("11800")), "backend must ignore spoofed subtotal: %s", sale.raw)
	require.True(t, dec(sale.data["total"]).Equal(rd("11800")), "backend must ignore spoofed total: %s", sale.raw)
	require.True(t, dec(sale.data["tax_total"]).Equal(rd("1800")), "backend must compute 18% inclusive VAT: %s", sale.raw)

	// Tax report must reconcile exactly to the 1,800 output VAT recorded on the sale.
	taxRep := e.owner.call(t, http.MethodGet, "/api/v1/reports/tax?from=2026-11-01&to=2026-11-30", nil, "")
	require.Equal(t, 200, taxRep.code, taxRep.raw)
	require.Contains(t, taxRep.raw, "1800", "tax report should reconcile output VAT: %s", taxRep.raw)

	// Day closing overage (+750 above expected 11,800) must require a reason and record positive difference.
	noNote := e.owner.call(t, http.MethodPost, "/api/v1/day-closings", map[string]any{
		"date": "2026-11-16", "counted_cash": "12550",
	}, "")
	require.Equal(t, 400, noNote.code, noNote.raw)

	overage := e.owner.call(t, http.MethodPost, "/api/v1/day-closings", map[string]any{
		"date": "2026-11-16", "counted_cash": "12550", "note": "Unclaimed customer change left in drawer",
	}, "")
	require.Equal(t, 201, overage.code, overage.raw)
	require.True(t, dec(overage.data["expected_cash"]).Equal(rd("11800")), overage.raw)
	require.True(t, dec(overage.data["counted_cash"]).Equal(rd("12550")), overage.raw)
	require.True(t, dec(overage.data["difference"]).Equal(rd("750")), overage.raw)
}

// TestFinancialIntegrity_SnapshotIsolationNoTornReads reproduces the classic banking concurrency scenario:
// Account starts at TZS 500,000. Transaction B opens a REPEATABLE READ READ ONLY snapshot (InActorReadTx)
// and reads the balance; concurrently Transaction A withdraws TZS 100,000 and commits.
// Verifies:
// 1. Transaction B does not block Transaction A (MVCC).
// 2. Transaction B observes a consistent TZS 500,000 across repeated queries (no dirty or torn read).
// 3. A new transaction after A commits immediately observes TZS 400,000.
func TestFinancialIntegrity_SnapshotIsolationNoTornReads(t *testing.T) {
	e := newEnv(t, defaultNow)
	srcRes := e.owner.call(t, http.MethodPost, "/api/v1/money-accounts", map[string]any{
		"name": "Checking 500k", "type": "bank", "opening_balance": "500000",
	}, "")
	require.Equal(t, 201, srcRes.code, srcRes.raw)
	srcID := uuid.MustParse(str(srcRes.data["id"]))

	dstRes := e.owner.call(t, http.MethodPost, "/api/v1/money-accounts", map[string]any{
		"name": "Reserve Bank", "type": "bank",
	}, "")
	require.Equal(t, 201, dstRes.code, dstRes.raw)
	dstID := str(dstRes.data["id"])

	read1Done := make(chan struct{})
	writeDone := make(chan struct{})
	var firstRead, secondRead decimal.Decimal
	var readErr error

	// Transaction B: multi-query REPEATABLE READ READ ONLY snapshot.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		readErr = e.app.DB.InActorReadTx(context.Background(), db.Actor{}, func(ctx context.Context, tx pgx.Tx) error {
			if err := tx.QueryRow(ctx, `SELECT current_balance FROM app.financial_accounts WHERE id = $1`, srcID).Scan(&firstRead); err != nil {
				return err
			}
			close(read1Done)
			<-writeDone // wait until Transaction A has committed its 100,000 withdrawal
			return tx.QueryRow(ctx, `SELECT current_balance FROM app.financial_accounts WHERE id = $1`, srcID).Scan(&secondRead)
		})
	}()

	<-read1Done
	// Transaction A: withdraw 100,000 while Transaction B's read snapshot is still open.
	txA := e.owner.call(t, http.MethodPost, "/api/v1/money-transfers", map[string]any{
		"from_account_id": srcID.String(), "to_account_id": dstID, "amount": "100000", "note": "Concurrent withdrawal",
	}, key())
	require.Equal(t, 201, txA.code, txA.raw)
	close(writeDone)
	wg.Wait()

	require.NoError(t, readErr)
	require.True(t, firstRead.Equal(rd("500000")), "first read in snapshot should be 500000, got %s", firstRead)
	require.True(t, secondRead.Equal(rd("500000")), "second read in same REPEATABLE READ snapshot must still be 500000 (no torn read), got %s", secondRead)

	// Fresh read after Transaction A committed must see 400,000.
	var afterCommit decimal.Decimal
	err := e.app.DB.InActorReadTx(context.Background(), db.Actor{}, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT current_balance FROM app.financial_accounts WHERE id = $1`, srcID).Scan(&afterCommit)
	})
	require.NoError(t, err)
	require.True(t, afterCommit.Equal(rd("400000")), "fresh read after commit must be 400000, got %s", afterCommit)
}

// TestFinancialIntegrity_ConcurrentAccountOutflowsSerialized verifies that two concurrent
// withdrawals (TZS 300,000 each) from a TZS 500,000 non-overdraft account serialize cleanly
// via SELECT ... FOR UPDATE on app.financial_accounts: exactly one succeeds (200), one returns
// a clean 409 insufficient_funds, and the ending balance is exactly TZS 200,000.
func TestFinancialIntegrity_ConcurrentAccountOutflowsSerialized(t *testing.T) {
	e := newEnv(t, defaultNow)
	accRes := e.owner.call(t, http.MethodPost, "/api/v1/money-accounts", map[string]any{
		"name": "Ops Account 500k", "type": "bank", "opening_balance": "500000", "allow_overdraft": false,
	}, "")
	require.Equal(t, 201, accRes.code, accRes.raw)
	accID := str(accRes.data["id"])

	cats := e.owner.call(t, http.MethodGet, "/api/v1/expense-categories", nil, "")
	require.Equal(t, 200, cats.code, cats.raw)
	catID := str(asList(cats.body["data"])[0]["id"])
	staff, _ := e.user(t, "staff")

	expIDs := make([]string, 2)
	for i := 0; i < 2; i++ {
		ex := staff.call(t, http.MethodPost, "/api/v1/expenses", map[string]any{
			"category_id": catID, "amount": "300000", "description": "Concurrent payout test",
		}, key())
		require.Equal(t, 201, ex.code, ex.raw)
		expIDs[i] = str(ex.data["id"])
		ap := e.owner.call(t, http.MethodPost, "/api/v1/expenses/"+expIDs[i]+"/approve", nil, "")
		require.Equal(t, 200, ap.code, ap.raw)
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	codes := make([]int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			r := e.owner.call(t, http.MethodPost, "/api/v1/expenses/"+expIDs[idx]+"/pay", map[string]any{
				"money_account_id": accID,
			}, key())
			codes[idx] = r.code
		}(i)
	}
	close(start)
	wg.Wait()

	paid, rejected := 0, 0
	for _, code := range codes {
		if code == 200 {
			paid++
		} else if code == 409 {
			rejected++
		}
	}
	require.Equal(t, 1, paid, "exactly one concurrent 300k payout should succeed: %v", codes)
	require.Equal(t, 1, rejected, "second concurrent 300k payout must cleanly return 409 insufficient_funds: %v", codes)

	accts := e.owner.call(t, http.MethodGet, "/api/v1/money-accounts", nil, "")
	require.Equal(t, 200, accts.code, accts.raw)
	acct := find(asList(accts.body["data"]), "id", accID)
	require.True(t, dec(acct["balance"]).Equal(rd("200000")), "ending balance must be exactly 200000, got %v", acct["balance"])
}
