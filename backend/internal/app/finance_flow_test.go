package app_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func accountBalance(t *testing.T, c *client, id string) string {
	t.Helper()
	res := c.call(t, http.MethodGet, "/api/v1/money-accounts/"+id, nil, "")
	require.Equal(t, 200, res.code, res.raw)
	return dec(res.data["balance"]).StringFixed(2)
}

func TestFinanceFlow(t *testing.T) {
	e := newEnv(t, defaultNow)
	manager, _ := e.user(t, "manager")
	staff, _ := e.user(t, "staff")
	cash := e.cashAccount(t)

	bank := e.owner.call(t, http.MethodPost, "/api/v1/money-accounts", map[string]any{"name": "CRDB", "type": "bank", "provider": "CRDB", "account_number": "0150", "opening_balance": "1000000"}, "")
	require.Equal(t, 201, bank.code, bank.raw)
	bankID := str(bank.data["id"])

	// Transfer with a fee is refused (fee should be recorded as an expense); plain transfer moves both balances.
	withFee := e.owner.call(t, http.MethodPost, "/api/v1/money-transfers", map[string]any{"from_account_id": bankID, "to_account_id": cash, "amount": "500000", "fee": "1000"}, key())
	require.Equal(t, 400, withFee.code, withFee.raw)
	tr := e.owner.call(t, http.MethodPost, "/api/v1/money-transfers", map[string]any{"from_account_id": bankID, "to_account_id": cash, "amount": "500000"}, key())
	require.Equal(t, 201, tr.code, tr.raw)
	require.Equal(t, "500000.00", accountBalance(t, e.owner, bankID))
	require.Equal(t, "500000.00", accountBalance(t, e.owner, cash))
	tooMuch := e.owner.call(t, http.MethodPost, "/api/v1/money-transfers", map[string]any{"from_account_id": bankID, "to_account_id": cash, "amount": "9000000"}, key())
	require.Equal(t, 409, tooMuch.code, tooMuch.raw)
	transfers := e.owner.call(t, http.MethodGet, "/api/v1/money-transfers", nil, "")
	require.Equal(t, 200, transfers.code, transfers.raw)
	require.Len(t, asList(transfers.body["data"]), 1)

	txs := e.owner.call(t, http.MethodGet, "/api/v1/transactions?type=transfer", nil, "")
	require.Equal(t, 200, txs.code, txs.raw)
	require.Len(t, asList(txs.body["data"]), 2, "both sides of the transfer")

	st := e.owner.call(t, http.MethodGet, "/api/v1/money-accounts/"+bankID+"/statement?from=2026-11-01", nil, "")
	require.Equal(t, 200, st.code, st.raw)
	require.Equal(t, "500000.00", dec(st.data["closing_balance"]).StringFixed(2))
	require.Len(t, asList(st.data["lines"]), 2)

	// Bank balances are hidden from a manager without finance.account.view_balance.
	ml := manager.call(t, http.MethodGet, "/api/v1/money-accounts", nil, "")
	require.Equal(t, 200, ml.code, ml.raw)
	row := find(asList(ml.body["data"]), "id", bankID)
	require.NotEmpty(t, row)
	_, has := row["balance"]
	require.False(t, has, "bank balance must be hidden")
	noStmt := manager.call(t, http.MethodGet, "/api/v1/money-accounts/"+bankID+"/statement", nil, "")
	require.Equal(t, 403, noStmt.code)
	require.Equal(t, 403, staff.call(t, http.MethodGet, "/api/v1/transactions", nil, "").code)

	// Removed legacy finance endpoints return 410 Gone cleanly.
	require.Equal(t, 410, e.owner.call(t, http.MethodPost, "/api/v1/income", map[string]any{"kind": "capital", "amount": "100", "money_account_id": cash, "description": "x"}, key()).code)
	require.Equal(t, 410, e.owner.call(t, http.MethodPost, "/api/v1/drawings", map[string]any{"amount": "100", "money_account_id": cash}, key()).code)
	require.Equal(t, 410, e.owner.call(t, http.MethodPost, "/api/v1/statutory-payments", map[string]any{"amount": "100", "money_account_id": bankID}, key()).code)
	require.Equal(t, 410, e.owner.call(t, http.MethodPut, "/api/v1/periods/lock", map[string]any{"locked_until": "2026-11-10"}, "").code)
	require.Equal(t, 410, e.owner.call(t, http.MethodPost, "/api/v1/money-accounts/"+bankID+"/reconcile", map[string]any{"actual_balance": "500000", "note": "x"}, key()).code)

	// Voiding a sale payment reopens the sale balance and restores the bank balance.
	product := e.product(t, "Sugar", "2500", "10", "1500")
	cust := e.owner.call(t, http.MethodPost, "/api/v1/customers", map[string]any{"name": "Juma", "phone": uniquePhone(), "credit_limit": "50000"}, "")
	require.Equal(t, 201, cust.code, cust.raw)
	sale := e.owner.call(t, http.MethodPost, "/api/v1/sales", map[string]any{
		"customer_id": cust.data["id"],
		"items":       []map[string]any{{"product_id": product, "qty": "2"}},
		"payments":    []map[string]any{{"method": "bank", "money_account_id": bankID, "amount": "5000", "reference": "TX1001"}},
	}, key())
	require.Equal(t, 201, sale.code, sale.raw)
	saleID := str(sale.data["id"])
	sp := e.owner.call(t, http.MethodGet, "/api/v1/payments?kind=sale&account_id="+bankID, nil, "")
	require.Equal(t, 200, sp.code, sp.raw)
	require.Len(t, asList(sp.body["data"]), 1, sp.raw)
	salePay := str(asList(sp.body["data"])[0]["id"])
	detail := e.owner.call(t, http.MethodGet, "/api/v1/payments/"+salePay, nil, "")
	require.Equal(t, 200, detail.code, detail.raw)
	require.Len(t, asList(detail.data["allocations"]), 1)
	vch := e.owner.call(t, http.MethodGet, "/api/v1/payments/"+salePay+"/voucher", nil, "")
	require.Equal(t, 200, vch.code, vch.raw)
	pdfRes := e.owner.call(t, http.MethodGet, "/api/v1/payments/"+salePay+"/voucher?format=pdf", nil, "")
	require.Equal(t, 200, pdfRes.code)
	require.True(t, strings.HasPrefix(pdfRes.raw, "%PDF-1.4"))
	require.Equal(t, "505000.00", accountBalance(t, e.owner, bankID))

	// Staff gets 403; manager without finance.payment.void requests approval (202); owner approves and voids; voiding twice conflicts.
	require.Equal(t, 403, staff.call(t, http.MethodPost, "/api/v1/payments/"+salePay+"/void", map[string]any{"reason": "Bounced"}, key()).code)
	reqVoid := manager.call(t, http.MethodPost, "/api/v1/payments/"+salePay+"/void", map[string]any{"reason": "Bounced"}, key())
	require.Equal(t, 202, reqVoid.code, reqVoid.raw)
	sv := e.owner.call(t, http.MethodPost, "/api/v1/approvals/"+str(reqVoid.data["approval_request_id"])+"/approve", map[string]any{"note": "Confirmed bounced"}, key())
	require.Equal(t, 200, sv.code, sv.raw)
	twice := e.owner.call(t, http.MethodPost, "/api/v1/payments/"+salePay+"/void", map[string]any{"reason": "again"}, key())
	require.Equal(t, 409, twice.code, twice.raw)
	s := e.owner.call(t, http.MethodGet, "/api/v1/sales/"+saleID, nil, "")
	require.Equal(t, "5000.00", dec(s.data["balance"]).StringFixed(2))
	require.Equal(t, "0.00", dec(s.data["paid_total"]).StringFixed(2))
	require.Equal(t, "500000.00", accountBalance(t, e.owner, bankID))

	// Account edits and deactivate rules.
	upd := e.owner.call(t, http.MethodPut, "/api/v1/money-accounts/"+bankID, map[string]any{"name": "CRDB Main"}, "")
	require.Equal(t, 200, upd.code, upd.raw)
	require.Equal(t, "CRDB Main", upd.data["name"])
	busy := e.owner.call(t, http.MethodPost, "/api/v1/money-accounts/"+bankID+"/deactivate", nil, "")
	require.Equal(t, 409, busy.code, busy.raw)
	empty := e.owner.call(t, http.MethodPost, "/api/v1/money-accounts", map[string]any{"name": "Spare wallet", "type": "mobile_money"}, "")
	require.Equal(t, 201, empty.code, empty.raw)
	off := e.owner.call(t, http.MethodPost, "/api/v1/money-accounts/"+str(empty.data["id"])+"/deactivate", nil, "")
	require.Equal(t, 200, off.code, off.raw)
	require.Equal(t, false, off.data["is_active"])
	useOff := e.owner.call(t, http.MethodPost, "/api/v1/money-transfers", map[string]any{"from_account_id": cash, "to_account_id": str(empty.data["id"]), "amount": "10"}, key())
	require.Equal(t, 409, useOff.code, useOff.raw)

	staffPays := e.owner.call(t, http.MethodGet, "/api/v1/payments/staff", nil, "")
	require.Equal(t, 200, staffPays.code, staffPays.raw)

	// Unified debts endpoint returns both owed_to_us and we_owe lists.
	debts := e.owner.call(t, http.MethodGet, "/api/v1/debts", nil, "")
	require.Equal(t, 200, debts.code, debts.raw)
	require.NotNil(t, debts.data["owed_to_us"])
	require.NotNil(t, debts.data["we_owe"])

	// End-of-day closing flow: summary -> close -> duplicate conflict -> non-owner reopen forbidden -> owner reopen succeeds -> re-close succeeds.
	daySum := e.owner.call(t, http.MethodGet, "/api/v1/day-closings/summary?date=2026-11-19", nil, "")
	require.Equal(t, 200, daySum.code, daySum.raw)
	require.Equal(t, "2026-11-19", daySum.data["date"])
	closeRes := e.owner.call(t, http.MethodPost, "/api/v1/day-closings", map[string]any{"date": "2026-11-19", "counted_cash": "500000", "note": "All counted"}, "")
	require.Equal(t, 201, closeRes.code, closeRes.raw)
	closingID := str(closeRes.data["id"])
	require.Equal(t, "closed", closeRes.data["status"])
	dupClose := e.owner.call(t, http.MethodPost, "/api/v1/day-closings", map[string]any{"date": "2026-11-19", "counted_cash": "500000"}, "")
	require.Equal(t, 409, dupClose.code, dupClose.raw)
	listClose := e.owner.call(t, http.MethodGet, "/api/v1/day-closings", nil, "")
	require.Equal(t, 200, listClose.code, listClose.raw)
	require.Len(t, asList(listClose.body["data"]), 1)
	require.Equal(t, 403, manager.call(t, http.MethodPost, "/api/v1/day-closings/"+closingID+"/reopen", map[string]any{"reason": "Forgot receipt"}, "").code)
	reopenRes := e.owner.call(t, http.MethodPost, "/api/v1/day-closings/"+closingID+"/reopen", map[string]any{"reason": "Forgot cash receipt"}, "")
	require.Equal(t, 200, reopenRes.code, reopenRes.raw)
	require.Equal(t, "reopened", reopenRes.data["status"])
	recloseRes := e.owner.call(t, http.MethodPost, "/api/v1/day-closings", map[string]any{"date": "2026-11-19", "counted_cash": "500500", "note": "Recounted"}, "")
	require.Equal(t, 201, recloseRes.code, recloseRes.raw)
	require.Equal(t, "closed", recloseRes.data["status"])

	// Another business sees none of it.
	other := newEnv(t, defaultNow)
	require.Equal(t, 404, other.owner.call(t, http.MethodGet, "/api/v1/money-accounts/"+bankID, nil, "").code)
	require.Equal(t, 404, other.owner.call(t, http.MethodGet, "/api/v1/payments/"+salePay, nil, "").code)
}
