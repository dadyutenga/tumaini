package app_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// Findings of the requirements audit, each with a test that fails without its fix.

func pendingApproval(t *testing.T, c *client, kind string) string {
	t.Helper()
	res := c.call(t, http.MethodGet, "/api/v1/approvals?status=pending&kind="+kind, nil, "")
	require.Equal(t, 200, res.code, res.raw)
	rows := asList(res.body["data"])
	require.NotEmpty(t, rows, "a pending %s request", kind)
	return str(rows[0]["id"])
}

// FR-PAY-11: the inbox must not be a way round the permission a record's own screen needs.
func TestApprovingThroughTheInboxNeedsTheKindsOwnPermission(t *testing.T) {
	e := newEnv(t, defaultNow)
	o := e.owner
	workWeek(t, o)
	mgr, _ := e.user(t, "manager") // holds approvals.decide, not payroll.approve or loans.approve
	emp := employee(t, o, "Mwajuma", "300000", nil)
	cash := e.cashAccount(t)
	_ = cash

	// Payroll: submit a run for approval.
	run := o.call(t, http.MethodPost, "/api/v1/payroll-runs", map[string]any{"period_start": "2026-11-01", "period_end": "2026-11-30"}, "")
	require.Equal(t, 201, run.code, run.raw)
	require.Equal(t, 200, o.call(t, http.MethodPost, "/api/v1/payroll-runs/"+str(run.data["id"])+"/calculate", nil, "").code)
	require.Equal(t, 202, o.call(t, http.MethodPost, "/api/v1/payroll-runs/"+str(run.data["id"])+"/submit", nil, "").code)
	payroll := pendingApproval(t, o, "payroll_run")
	denied := mgr.call(t, http.MethodPost, "/api/v1/approvals/"+payroll+"/approve", map[string]any{}, key())
	require.Equal(t, 403, denied.code, "a manager cannot approve payroll through the inbox: "+denied.raw)
	require.Equal(t, "pending_approval", str(o.call(t, http.MethodGet, "/api/v1/payroll-runs/"+str(run.data["id"]), nil, "").data["status"]), "the run is untouched")
	require.Equal(t, 403, mgr.call(t, http.MethodPost, "/api/v1/approvals/"+payroll+"/reject", map[string]any{}, "").code, "nor reject it")

	// Loans: a request above the creator's limit goes to the inbox; loans.approve is the Owner's.
	loan := o.call(t, http.MethodPost, "/api/v1/loans", map[string]any{"employee_id": emp, "principal": "20000", "monthly_deduction": "20000"}, key())
	require.Equal(t, 201, loan.code, loan.raw)

	// The Owner can; what the manager is allowed (expenses within their limit) still works.
	require.Equal(t, 200, o.call(t, http.MethodPost, "/api/v1/approvals/"+payroll+"/approve", map[string]any{}, key()).code)
	staff, _ := e.user(t, "staff")
	cats := o.call(t, http.MethodGet, "/api/v1/expense-categories", nil, "")
	exp := staff.call(t, http.MethodPost, "/api/v1/expenses", map[string]any{"category_id": asList(cats.body["data"])[0]["id"], "amount": "90000", "description": "Repairs"}, key())
	require.Equal(t, 202, exp.code, exp.raw)
	require.Equal(t, 200, mgr.call(t, http.MethodPost, "/api/v1/approvals/"+str(exp.data["approval_request_id"])+"/approve", map[string]any{}, key()).code)

	// An expense above the manager's expenses.approve limit (1,000,000) is refused even though approvals.decide has none.
	big := staff.call(t, http.MethodPost, "/api/v1/expenses", map[string]any{"category_id": asList(cats.body["data"])[0]["id"], "amount": "2000000", "description": "Generator"}, key())
	require.Equal(t, 202, big.code, big.raw)
	over := mgr.call(t, http.MethodPost, "/api/v1/approvals/"+str(big.data["approval_request_id"])+"/approve", map[string]any{}, key())
	require.Equal(t, 403, over.code, over.raw)
	require.Equal(t, "over_limit", errCode(over.body))
	require.Equal(t, 200, o.call(t, http.MethodPost, "/api/v1/approvals/"+str(big.data["approval_request_id"])+"/approve", map[string]any{}, key()).code)
}

// FR-INV-08: cost is for people with products.view_cost, in every response that carries sale lines.
func TestCashiersDoNotSeeCostInSaleResponses(t *testing.T) {
	e := newEnv(t, defaultNow)
	product := e.product(t, "Sugar", "3000", "20", "2000")
	staff, _ := e.user(t, "staff")
	cash := e.cashAccount(t)
	items := []map[string]any{{"product_id": product, "qty": "2"}}

	require.Equal(t, 201, staff.call(t, http.MethodPost, "/api/v1/tills/open", map[string]any{"money_account_id": cash, "opening_float": "0"}, "").code)
	preview := staff.call(t, http.MethodPost, "/api/v1/sales/preview", map[string]any{"items": items}, "")
	require.Equal(t, 200, preview.code, preview.raw)
	require.NotContains(t, preview.raw, "unit_cost")
	sale := staff.call(t, http.MethodPost, "/api/v1/sales", map[string]any{"items": items, "payments": []map[string]any{{"method": "cash", "money_account_id": cash, "amount": "6000"}}}, key())
	require.Equal(t, 201, sale.code, sale.raw)
	require.NotContains(t, sale.raw, "unit_cost", "the completed sale")
	held := staff.call(t, http.MethodPost, "/api/v1/sales/hold", map[string]any{"items": items}, "")
	require.Equal(t, 201, held.code, held.raw)
	require.NotContains(t, held.raw, "unit_cost", "a held cart")

	// The Owner still sees it, and so does the stored sale.
	owner := e.owner.call(t, http.MethodGet, "/api/v1/sales/"+str(sale.data["id"]), nil, "")
	require.Equal(t, 200, owner.code, owner.raw)
	require.Contains(t, owner.raw, "unit_cost")
	require.Equal(t, 201, e.owner.call(t, http.MethodPost, "/api/v1/tills/open", map[string]any{"money_account_id": cash, "opening_float": "0"}, "").code)
	ownerSale := e.owner.call(t, http.MethodPost, "/api/v1/sales", map[string]any{"items": items, "payments": []map[string]any{{"method": "cash", "money_account_id": cash, "amount": "6000"}}}, key())
	require.Equal(t, 201, ownerSale.code, ownerSale.raw)
	require.Contains(t, ownerSale.raw, "unit_cost")
}

// FR-FIN-16 and FR-FIN-18: show the difference first, give a reason to post it, and keep bank balances private.
func TestReconcilingShowsTheDifferenceFirstAndHidesBankBalances(t *testing.T) {
	e := newEnv(t, defaultNow)
	o := e.owner
	mgr, _ := e.user(t, "manager") // has reconcile.manage, not accounts.view_balance
	bank := o.call(t, http.MethodPost, "/api/v1/money-accounts", map[string]any{"name": "NMB", "type": "bank", "opening_balance": "1000000"}, "")
	require.Equal(t, 201, bank.code, bank.raw)
	id := str(bank.data["id"])
	path := "/api/v1/money-accounts/" + id + "/reconcile"

	// A manager without the balance permission cannot learn the balance through the difference.
	require.Equal(t, 403, mgr.call(t, http.MethodPost, path, map[string]any{"actual_balance": "990000", "dry_run": true}, key()).code)

	// A dry run reports the difference and changes nothing.
	dry := o.call(t, http.MethodPost, path, map[string]any{"actual_balance": "990000", "dry_run": true}, key())
	require.Equal(t, 200, dry.code, dry.raw)
	require.Equal(t, "-10000.00", str(dry.data["difference"]))
	require.Equal(t, "1000000.00", str(dry.data["system_balance"]))
	require.Equal(t, false, dry.data["posted"])
	balance := func() string {
		return str(find(asList(o.call(t, http.MethodGet, "/api/v1/money-accounts", nil, "").body["data"]), "id", id)["balance"])
	}
	require.Equal(t, "1000000.00", balance(), "nothing was posted")

	// Posting a difference needs a reason; with one it is recorded and the balance becomes the count.
	noReason := o.call(t, http.MethodPost, path, map[string]any{"actual_balance": "990000"}, key())
	require.Equal(t, 422, noReason.code, noReason.raw)
	require.Equal(t, "1000000.00", balance())
	ok := o.call(t, http.MethodPost, path, map[string]any{"actual_balance": "990000", "note": "Bank charges not yet booked"}, key())
	require.Equal(t, 201, ok.code, ok.raw)
	require.Equal(t, true, ok.data["posted"])
	require.Equal(t, "990000.00", balance())

	// No difference needs no reason.
	same := o.call(t, http.MethodPost, path, map[string]any{"actual_balance": "990000"}, key())
	require.Equal(t, 201, same.code, same.raw)
	tb := o.call(t, http.MethodGet, "/api/v1/reports/trial-balance", nil, "")
	require.Equal(t, true, tb.data["balanced"])

	// Cash is not hidden: the manager may reconcile a cash account.
	cashAcc := e.cashAccount(t)
	require.Equal(t, 200, mgr.call(t, http.MethodPost, "/api/v1/money-accounts/"+cashAcc+"/reconcile", map[string]any{"actual_balance": "0", "dry_run": true}, key()).code)
}

// FR-FIN-19: a document dated inside a locked month cannot be voided or reversed.
func TestLockedPeriodsBlockVoidsOfOlderDocuments(t *testing.T) {
	e := newEnv(t, defaultNow) // business date 2026-11-16
	o := e.owner
	product := e.product(t, "Flour", "2000", "", "")
	sup := o.call(t, http.MethodPost, "/api/v1/suppliers", map[string]any{"name": "Mill Co"}, "")
	require.Equal(t, 201, sup.code, sup.raw)
	grn := o.call(t, http.MethodPost, "/api/v1/goods-receipts", map[string]any{"supplier_id": sup.data["id"], "items": []map[string]any{{"product_id": product, "qty": "10", "unit_cost": "1000"}}}, key())
	require.Equal(t, 201, grn.code, grn.raw)
	inv := o.call(t, http.MethodPost, "/api/v1/purchase-invoices", map[string]any{"supplier_id": sup.data["id"], "receipt_ids": []string{str(grn.data["id"])}, "date": "2026-11-10"}, key())
	require.Equal(t, 201, inv.code, inv.raw)

	// Lock up to the 15th: the invoice dated the 10th is inside it.
	require.Equal(t, 200, o.call(t, http.MethodPut, "/api/v1/periods/lock", map[string]any{"locked_until": "2026-11-15"}, "").code)
	blocked := o.call(t, http.MethodPost, "/api/v1/purchase-invoices/"+str(inv.data["id"])+"/void", map[string]any{"reason": "Wrong supplier"}, key())
	require.Equal(t, 409, blocked.code, blocked.raw)
	require.Equal(t, "period_locked", errCode(blocked.body))
	require.Equal(t, "posted", str(o.call(t, http.MethodGet, "/api/v1/purchase-invoices/"+str(inv.data["id"]), nil, "").data["status"]), "nothing changed")

	// Reopen the period and the same void goes through.
	require.Equal(t, 200, o.call(t, http.MethodPut, "/api/v1/periods/lock", map[string]any{"locked_until": nil}, "").code)
	ok := o.call(t, http.MethodPost, "/api/v1/purchase-invoices/"+str(inv.data["id"])+"/void", map[string]any{"reason": "Wrong supplier"}, key())
	require.Equal(t, 200, ok.code, ok.raw)
	tb := o.call(t, http.MethodGet, "/api/v1/reports/trial-balance", nil, "")
	require.Equal(t, true, tb.data["balanced"])
}

// FR-PUR-02, FR-PUR-10, FR-SUP-04: purchasing limits and branch scope cannot be sidestepped.
func TestPurchasingLimitsAndMoneyAccountScope(t *testing.T) {
	e := newEnv(t, defaultNow)
	o := e.owner
	mgr, _ := e.user(t, "manager") // po.create limit 5,000,000; payments.pay limit 1,000,000; main branch only
	product := e.product(t, "Cement", "20000", "", "")
	sup := o.call(t, http.MethodPost, "/api/v1/suppliers", map[string]any{"name": "Bulk Co"}, "")
	require.Equal(t, 201, sup.code, sup.raw)
	supID := sup.data["id"]
	rich := o.call(t, http.MethodPost, "/api/v1/money-accounts", map[string]any{"name": "Treasury", "type": "cash", "opening_balance": "50000000"}, "")
	require.Equal(t, 201, rich.code, rich.raw)

	// A direct receipt above the manager's order limit is refused; within it, allowed.
	big := mgr.call(t, http.MethodPost, "/api/v1/goods-receipts", map[string]any{"supplier_id": supID, "items": []map[string]any{{"product_id": product, "qty": "1000", "unit_cost": "6000"}}}, key())
	require.Equal(t, 403, big.code, big.raw)
	require.Equal(t, "over_limit", errCode(big.body))
	small := mgr.call(t, http.MethodPost, "/api/v1/goods-receipts", map[string]any{"supplier_id": supID, "items": []map[string]any{{"product_id": product, "qty": "100", "unit_cost": "20000"}}}, key())
	require.Equal(t, 201, small.code, small.raw)
	require.Equal(t, 201, o.call(t, http.MethodPost, "/api/v1/goods-receipts", map[string]any{"supplier_id": supID, "items": []map[string]any{{"product_id": product, "qty": "1000", "unit_cost": "6000"}}}, key()).code, "the Owner has no limit")

	// Paying on the spot is limited like any supplier payment (here 2,000,000 against a 1,000,000 limit).
	inv := mgr.call(t, http.MethodPost, "/api/v1/purchase-invoices", map[string]any{
		"supplier_id": supID, "receipt_ids": []string{str(small.data["id"])},
		"pay_now": map[string]any{"money_account_id": str(rich.data["id"]), "method": "cash"},
	}, key())
	require.Equal(t, 403, inv.code, inv.raw)
	require.Equal(t, "over_limit", errCode(inv.body))
	plain := mgr.call(t, http.MethodPost, "/api/v1/purchase-invoices", map[string]any{"supplier_id": supID, "receipt_ids": []string{str(small.data["id"])}}, key())
	require.Equal(t, 201, plain.code, "the invoice itself is fine without paying it now: "+plain.raw)

	// A branch account of another branch cannot be used to pay: not found, as for expenses.
	other := o.call(t, http.MethodPost, "/api/v1/branches", map[string]any{"name": "Arusha", "code": "ARU"}, "")
	require.Equal(t, 201, other.code, other.raw)
	acc := o.call(t, http.MethodPost, "/api/v1/money-accounts", map[string]any{"name": "Arusha safe", "type": "cash", "branch_id": other.data["id"], "opening_balance": "5000000"}, "")
	require.Equal(t, 201, acc.code, acc.raw)
	pay := mgr.call(t, http.MethodPost, "/api/v1/suppliers/"+str(supID)+"/payments", map[string]any{"amount": "100000", "method": "cash", "money_account_id": str(acc.data["id"])}, key())
	require.Equal(t, 404, pay.code, pay.raw)
	require.Equal(t, 201, o.call(t, http.MethodPost, "/api/v1/suppliers/"+str(supID)+"/payments", map[string]any{"amount": "100000", "method": "cash", "money_account_id": str(acc.data["id"])}, key()).code)
	own := mgr.call(t, http.MethodPost, "/api/v1/suppliers/"+str(supID)+"/payments", map[string]any{"amount": "100000", "method": "cash", "money_account_id": str(rich.data["id"])}, key())
	require.Equal(t, 201, own.code, "an account of the manager's own branch works: "+own.raw)
}
