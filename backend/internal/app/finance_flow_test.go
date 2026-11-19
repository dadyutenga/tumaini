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

func requireBalanced(t *testing.T, c *client) {
	t.Helper()
	tb := c.call(t, http.MethodGet, "/api/v1/reports/trial-balance", nil, "")
	require.Equal(t, 200, tb.code, tb.raw)
	require.Equal(t, true, tb.data["balanced"], tb.raw)
}

func TestFinanceFlow(t *testing.T) {
	e := newEnv(t, defaultNow)
	manager, _ := e.user(t, "manager")
	staff, _ := e.user(t, "staff")
	cash := e.cashAccount(t)

	bank := e.owner.call(t, http.MethodPost, "/api/v1/money-accounts", map[string]any{"name": "CRDB", "type": "bank", "provider": "CRDB", "account_number": "0150", "opening_balance": "1000000"}, "")
	require.Equal(t, 201, bank.code, bank.raw)
	bankID := str(bank.data["id"])

	// Transfer from bank to cash with a fee: both balances move together.
	tr := e.owner.call(t, http.MethodPost, "/api/v1/money-transfers", map[string]any{"from_account_id": bankID, "to_account_id": cash, "amount": "500000", "fee": "1000"}, key())
	require.Equal(t, 201, tr.code, tr.raw)
	require.Equal(t, "499000.00", accountBalance(t, e.owner, bankID))
	require.Equal(t, "500000.00", accountBalance(t, e.owner, cash))
	tooMuch := e.owner.call(t, http.MethodPost, "/api/v1/money-transfers", map[string]any{"from_account_id": bankID, "to_account_id": cash, "amount": "9000000"}, key())
	require.Equal(t, 409, tooMuch.code, tooMuch.raw)
	transfers := e.owner.call(t, http.MethodGet, "/api/v1/money-transfers", nil, "")
	require.Equal(t, 200, transfers.code, transfers.raw)
	require.Len(t, asList(transfers.body["data"]), 1)

	txs := e.owner.call(t, http.MethodGet, "/api/v1/transactions?type=money_transfer", nil, "")
	require.Equal(t, 200, txs.code, txs.raw)
	require.Len(t, asList(txs.body["data"]), 2, "both sides of the transfer")
	requireBalanced(t, e.owner)

	st := e.owner.call(t, http.MethodGet, "/api/v1/money-accounts/"+bankID+"/statement?from=2026-11-01", nil, "")
	require.Equal(t, 200, st.code, st.raw)
	require.Equal(t, "499000.00", dec(st.data["closing_balance"]).StringFixed(2))
	require.Len(t, asList(st.data["lines"]), 2)

	// Bank balances are hidden from a manager without accounts.view_balance.
	ml := manager.call(t, http.MethodGet, "/api/v1/money-accounts", nil, "")
	require.Equal(t, 200, ml.code, ml.raw)
	row := find(asList(ml.body["data"]), "id", bankID)
	require.NotEmpty(t, row)
	_, has := row["balance"]
	require.False(t, has, "bank balance must be hidden")
	noStmt := manager.call(t, http.MethodGet, "/api/v1/money-accounts/"+bankID+"/statement", nil, "")
	require.Equal(t, 403, noStmt.code)
	require.Equal(t, 403, staff.call(t, http.MethodGet, "/api/v1/transactions", nil, "").code)

	// Other income and owner capital.
	cats := e.owner.call(t, http.MethodGet, "/api/v1/income-categories", nil, "")
	require.Equal(t, 200, cats.code, cats.raw)
	incCat := str(find(asList(cats.body["data"]), "name", "Other income")["id"])
	inc := e.owner.call(t, http.MethodPost, "/api/v1/income", map[string]any{"kind": "income", "category_id": incCat, "amount": "20000", "money_account_id": cash, "description": "Rent from tenant"}, key())
	require.Equal(t, 201, inc.code, inc.raw)
	capi := e.owner.call(t, http.MethodPost, "/api/v1/income", map[string]any{"kind": "capital", "amount": "100000", "money_account_id": cash, "description": "Owner injection"}, key())
	require.Equal(t, 201, capi.code, capi.raw)
	noCat := e.owner.call(t, http.MethodPost, "/api/v1/income", map[string]any{"kind": "income", "amount": "1", "money_account_id": cash, "description": "x"}, key())
	require.Equal(t, 422, noCat.code, noCat.raw)
	incomes := e.owner.call(t, http.MethodGet, "/api/v1/income", nil, "")
	require.Len(t, asList(incomes.body["data"]), 2)
	newCat := e.owner.call(t, http.MethodPost, "/api/v1/income-categories", map[string]any{"name": "Interest"}, "")
	require.Equal(t, 201, newCat.code, newCat.raw)
	require.Equal(t, "620000.00", accountBalance(t, e.owner, cash))

	// Drawings are for the Owner only.
	md := manager.call(t, http.MethodPost, "/api/v1/drawings", map[string]any{"amount": "1000", "money_account_id": cash}, key())
	require.Equal(t, 403, md.code, md.raw)
	dr := e.owner.call(t, http.MethodPost, "/api/v1/drawings", map[string]any{"amount": "10000", "money_account_id": cash, "note": "School fees"}, key())
	require.Equal(t, 201, dr.code, dr.raw)
	require.Equal(t, "610000.00", accountBalance(t, e.owner, cash))

	// Nothing statutory is owed yet.
	stat := e.owner.call(t, http.MethodPost, "/api/v1/statutory-payments", map[string]any{"amount": "5000", "money_account_id": bankID}, key())
	require.Equal(t, 409, stat.code, stat.raw)

	// Payments list and the void of a drawing.
	pays := e.owner.call(t, http.MethodGet, "/api/v1/payments?kind=drawing", nil, "")
	require.Equal(t, 200, pays.code, pays.raw)
	require.Len(t, asList(pays.body["data"]), 1)
	payID := str(asList(pays.body["data"])[0]["id"])
	vch := e.owner.call(t, http.MethodGet, "/api/v1/payments/"+payID+"/voucher", nil, "")
	require.Equal(t, 200, vch.code, vch.raw)
	require.Equal(t, "Payment voucher", vch.data["title"])
	pdfRes := e.owner.call(t, http.MethodGet, "/api/v1/payments/"+payID+"/voucher?format=pdf", nil, "")
	require.Equal(t, 200, pdfRes.code)
	require.True(t, strings.HasPrefix(pdfRes.raw, "%PDF-1.4"))
	require.Equal(t, 403, manager.call(t, http.MethodPost, "/api/v1/payments/"+payID+"/void", map[string]any{"reason": "x"}, key()).code)
	void := e.owner.call(t, http.MethodPost, "/api/v1/payments/"+payID+"/void", map[string]any{"reason": "Recorded by mistake"}, key())
	require.Equal(t, 200, void.code, void.raw)
	require.Equal(t, "voided", void.data["status"])
	require.Equal(t, "620000.00", accountBalance(t, e.owner, cash))
	twice := e.owner.call(t, http.MethodPost, "/api/v1/payments/"+payID+"/void", map[string]any{"reason": "again"}, key())
	require.Equal(t, 409, twice.code, twice.raw)
	requireBalanced(t, e.owner)

	// Voiding a sale payment reopens the sale balance.
	product := e.product(t, "Sugar", "2500", "10", "1500")
	sale := e.owner.call(t, http.MethodPost, "/api/v1/sales", map[string]any{
		"items":    []map[string]any{{"product_id": product, "qty": "2"}},
		"payments": []map[string]any{{"method": "bank", "money_account_id": bankID, "amount": "5000", "reference": "TX1"}},
	}, key())
	require.Equal(t, 201, sale.code, sale.raw)
	saleID := str(sale.data["id"])
	sp := e.owner.call(t, http.MethodGet, "/api/v1/payments?kind=sale&account_id="+bankID, nil, "")
	require.Len(t, asList(sp.body["data"]), 1, sp.raw)
	salePay := str(asList(sp.body["data"])[0]["id"])
	detail := e.owner.call(t, http.MethodGet, "/api/v1/payments/"+salePay, nil, "")
	require.Equal(t, 200, detail.code, detail.raw)
	require.Len(t, asList(detail.data["allocations"]), 1)
	require.Equal(t, "504000.00", accountBalance(t, e.owner, bankID))
	sv := e.owner.call(t, http.MethodPost, "/api/v1/payments/"+salePay+"/void", map[string]any{"reason": "Bounced"}, key())
	require.Equal(t, 200, sv.code, sv.raw)
	s := e.owner.call(t, http.MethodGet, "/api/v1/sales/"+saleID, nil, "")
	require.Equal(t, "5000.00", dec(s.data["balance"]).StringFixed(2))
	require.Equal(t, "0.00", dec(s.data["paid_total"]).StringFixed(2))
	require.Equal(t, "499000.00", accountBalance(t, e.owner, bankID))
	requireBalanced(t, e.owner)

	// Period lock rejects a back-dated posting.
	lockBad := e.owner.call(t, http.MethodPut, "/api/v1/periods/lock", map[string]any{"locked_until": "2026-12-31"}, "")
	require.Equal(t, 422, lockBad.code, lockBad.raw)
	lock := e.owner.call(t, http.MethodPut, "/api/v1/periods/lock", map[string]any{"locked_until": "2026-11-10"}, "")
	require.Equal(t, 200, lock.code, lock.raw)
	require.Equal(t, "2026-11-10", lock.data["locked_until"])
	back := e.owner.call(t, http.MethodPost, "/api/v1/income", map[string]any{"kind": "capital", "amount": "1000", "money_account_id": cash, "description": "Late", "date": "2026-11-05"}, key())
	require.Equal(t, 409, back.code, back.raw)
	require.Contains(t, back.raw, "period_locked")
	require.Equal(t, 403, manager.call(t, http.MethodGet, "/api/v1/periods/lock", nil, "").code)
	unlock := e.owner.call(t, http.MethodPut, "/api/v1/periods/lock", map[string]any{"locked_until": nil}, "")
	require.Equal(t, 200, unlock.code, unlock.raw)
	require.Nil(t, unlock.data["locked_until"])

	// Account edits, reconcile and deactivate rules.
	upd := e.owner.call(t, http.MethodPut, "/api/v1/money-accounts/"+bankID, map[string]any{"name": "CRDB Main"}, "")
	require.Equal(t, 200, upd.code, upd.raw)
	require.Equal(t, "CRDB Main", upd.data["name"])
	busy := e.owner.call(t, http.MethodPost, "/api/v1/money-accounts/"+bankID+"/deactivate", nil, "")
	require.Equal(t, 409, busy.code, busy.raw)
	rec := e.owner.call(t, http.MethodPost, "/api/v1/money-accounts/"+bankID+"/reconcile", map[string]any{"actual_balance": "499500", "note": "Statement"}, key())
	require.Equal(t, 201, rec.code, rec.raw)
	require.Equal(t, "500.00", dec(rec.data["difference"]).StringFixed(2))
	require.Equal(t, "499500.00", accountBalance(t, e.owner, bankID))
	empty := e.owner.call(t, http.MethodPost, "/api/v1/money-accounts", map[string]any{"name": "Spare wallet", "type": "mobile_money"}, "")
	require.Equal(t, 201, empty.code, empty.raw)
	off := e.owner.call(t, http.MethodPost, "/api/v1/money-accounts/"+str(empty.data["id"])+"/deactivate", nil, "")
	require.Equal(t, 200, off.code, off.raw)
	require.Equal(t, false, off.data["is_active"])
	useOff := e.owner.call(t, http.MethodPost, "/api/v1/money-transfers", map[string]any{"from_account_id": cash, "to_account_id": str(empty.data["id"]), "amount": "10"}, key())
	require.Equal(t, 409, useOff.code, useOff.raw)

	entries := e.owner.call(t, http.MethodGet, "/api/v1/ledger/entries?source_type=payment_void", nil, "")
	require.Equal(t, 200, entries.code, entries.raw)
	require.Len(t, asList(entries.body["data"]), 1)
	require.Len(t, asList(asList(entries.body["data"])[0]["lines"]), 2)
	chart := e.owner.call(t, http.MethodGet, "/api/v1/ledger/accounts", nil, "")
	require.Equal(t, 200, chart.code, chart.raw)
	require.Equal(t, true, chart.data["balanced"])
	staffPays := e.owner.call(t, http.MethodGet, "/api/v1/payments/staff", nil, "")
	require.Equal(t, 200, staffPays.code, staffPays.raw)
	requireBalanced(t, e.owner)

	// Unified debts endpoint returns both owed_to_us and we_owe lists.
	debts := e.owner.call(t, http.MethodGet, "/api/v1/debts", nil, "")
	require.Equal(t, 200, debts.code, debts.raw)
	require.NotNil(t, debts.data["owed_to_us"])
	require.NotNil(t, debts.data["we_owe"])

	// End-of-day closing flow: summary -> close -> duplicate conflict -> non-owner reopen forbidden -> owner reopen succeeds -> re-close succeeds.
	daySum := e.owner.call(t, http.MethodGet, "/api/v1/day-closings/summary?date=2026-11-19", nil, "")
	require.Equal(t, 200, daySum.code, daySum.raw)
	require.Equal(t, "2026-11-19", daySum.data["date"])
	closeRes := e.owner.call(t, http.MethodPost, "/api/v1/day-closings", map[string]any{"date": "2026-11-19", "counted_cash": "620000", "note": "All counted"}, "")
	require.Equal(t, 201, closeRes.code, closeRes.raw)
	closingID := str(closeRes.data["id"])
	require.Equal(t, "closed", closeRes.data["status"])
	dupClose := e.owner.call(t, http.MethodPost, "/api/v1/day-closings", map[string]any{"date": "2026-11-19", "counted_cash": "620000"}, "")
	require.Equal(t, 409, dupClose.code, dupClose.raw)
	listClose := e.owner.call(t, http.MethodGet, "/api/v1/day-closings", nil, "")
	require.Equal(t, 200, listClose.code, listClose.raw)
	require.Len(t, asList(listClose.body["data"]), 1)
	require.Equal(t, 403, manager.call(t, http.MethodPost, "/api/v1/day-closings/"+closingID+"/reopen", map[string]any{"reason": "Forgot receipt"}, "").code)
	reopenRes := e.owner.call(t, http.MethodPost, "/api/v1/day-closings/"+closingID+"/reopen", map[string]any{"reason": "Forgot cash receipt"}, "")
	require.Equal(t, 200, reopenRes.code, reopenRes.raw)
	require.Equal(t, "reopened", reopenRes.data["status"])
	recloseRes := e.owner.call(t, http.MethodPost, "/api/v1/day-closings", map[string]any{"date": "2026-11-19", "counted_cash": "620500", "note": "Recounted"}, "")
	require.Equal(t, 201, recloseRes.code, recloseRes.raw)
	require.Equal(t, "closed", recloseRes.data["status"])

	// Another business sees none of it.
	other := newEnv(t, defaultNow)
	require.Equal(t, 404, other.owner.call(t, http.MethodGet, "/api/v1/money-accounts/"+bankID, nil, "").code)
	require.Equal(t, 404, other.owner.call(t, http.MethodGet, "/api/v1/payments/"+payID, nil, "").code)
}
