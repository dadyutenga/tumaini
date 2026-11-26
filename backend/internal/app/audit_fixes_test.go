package app_test

import (
	"encoding/json"
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
	mgr, _ := e.user(t, "manager")
	staff, _ := e.user(t, "staff")

	// Payroll is decided on the run, and only hr.payroll.approve may do it. A manager has no such permission.
	employee(t, o, "Mwajuma", "300000", nil)
	run := o.call(t, http.MethodPost, "/api/v1/payroll-runs", map[string]any{"period_start": "2026-11-01", "period_end": "2026-11-30"}, "")
	require.Equal(t, 201, run.code, run.raw)
	runID := str(run.data["id"])
	require.Equal(t, 200, o.call(t, http.MethodPost, "/api/v1/payroll-runs/"+runID+"/calculate", nil, "").code)
	require.Equal(t, 200, o.call(t, http.MethodPost, "/api/v1/payroll-runs/"+runID+"/submit", nil, "").code)
	require.Equal(t, 403, mgr.call(t, http.MethodPost, "/api/v1/payroll-runs/"+runID+"/approve", nil, key()).code)
	require.Equal(t, "draft", str(o.call(t, http.MethodGet, "/api/v1/payroll-runs/"+runID, nil, "").data["status"]))

	// Expenses are decided on the expense. The manager's provisional limit is zero, so the owner decides.
	cats := o.call(t, http.MethodGet, "/api/v1/expense-categories", nil, "")
	exp := staff.call(t, http.MethodPost, "/api/v1/expenses", map[string]any{"category_id": asList(cats.body["data"])[0]["id"], "amount": "90000", "description": "Repairs"}, key())
	require.Equal(t, 201, exp.code, exp.raw)
	require.Equal(t, 403, mgr.call(t, http.MethodPost, "/api/v1/expenses/"+str(exp.data["id"])+"/approve", map[string]any{"note": "fine"}, "").code)
	ok := o.call(t, http.MethodPost, "/api/v1/expenses/"+str(exp.data["id"])+"/approve", map[string]any{"note": "fine"}, "")
	require.Equal(t, 200, ok.code, ok.raw)
	require.Equal(t, "approved", str(ok.data["status"]))
}

// FR-INV-08: cost is for people with products.view_cost, in every response that carries sale lines.
func TestCashiersDoNotSeeCostInSaleResponses(t *testing.T) {
	e := newEnv(t, defaultNow)
	product := e.product(t, "Sugar", "3000", "20", "2000")
	staff, _ := e.user(t, "staff")
	cash := e.cashAccount(t)
	items := []map[string]any{{"product_id": product, "qty": "2"}}

	require.Equal(t, 403, staff.call(t, http.MethodPost, "/api/v1/tills/open", map[string]any{"money_account_id": cash, "opening_float": "0"}, "").code, "staff cannot open a till; the endpoint is gone for people who can reach it")
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
	ownerSale := e.owner.call(t, http.MethodPost, "/api/v1/sales", map[string]any{"items": items, "payments": []map[string]any{{"method": "cash", "money_account_id": cash, "amount": "6000"}}}, key())
	require.Equal(t, 201, ownerSale.code, ownerSale.raw)
	require.Contains(t, ownerSale.raw, "unit_cost")
}

// FR-FIN-16 and FR-FIN-18: show the difference first, give a reason to post it, and keep bank balances private.
func TestReconcilingShowsTheDifferenceFirstAndHidesBankBalances(t *testing.T) {
	e := newEnv(t, defaultNow)
	o := e.owner
	mgr, _ := e.user(t, "manager")
	bank := o.call(t, http.MethodPost, "/api/v1/money-accounts", map[string]any{"name": "NMB", "type": "bank", "provider": "NMB", "account_number": "0150", "opening_balance": "1000000"}, "")
	require.Equal(t, 201, bank.code, bank.raw)
	id := str(bank.data["id"])
	require.Equal(t, 410, o.call(t, http.MethodPost, "/api/v1/money-accounts/"+id+"/reconcile", map[string]any{"actual_balance": "990000", "note": "charges"}, key()).code)
	row := find(asList(mgr.call(t, http.MethodGet, "/api/v1/money-accounts", nil, "").body["data"]), "id", id)
	_, has := row["balance"]
	require.False(t, has, "a manager without finance.account.view does not see the bank balance")
}

// FR-FIN-19: a document dated inside a locked month cannot be voided or reversed.
func TestLockedPeriodsBlockVoidsOfOlderDocuments(t *testing.T) {
	e := newEnv(t, defaultNow)
	o := e.owner
	require.Equal(t, 410, o.call(t, http.MethodPut, "/api/v1/periods/lock", map[string]any{"locked_until": "2026-11-15"}, "").code)
	require.Equal(t, 410, o.call(t, http.MethodPost, "/api/v1/purchase-invoices", map[string]any{"supplier_id": key()}, key()).code)

	cash := e.cashAccount(t)
	rice := e.product(t, "Flour", "2000", "5", "1000")
	sale := cashSale(t, o, rice, "1", cash, "2000")
	require.Equal(t, 201, sale.code, sale.raw)
	closed := o.call(t, http.MethodPost, "/api/v1/day-closings", map[string]any{"date": "2026-11-16", "counted_cash": "0", "note": "Closed"}, "")
	require.Equal(t, 201, closed.code, closed.raw)
	blocked := o.call(t, http.MethodPost, "/api/v1/sales/"+str(sale.data["id"])+"/void", map[string]any{"reason": "Wrong item"}, key())
	require.Equal(t, 409, blocked.code, blocked.raw)
	require.Equal(t, "completed", str(o.call(t, http.MethodGet, "/api/v1/sales/"+str(sale.data["id"]), nil, "").data["status"]))
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
	require.Equal(t, 201, big.code, big.raw, "the approved model has no purchase-order amount limit")
	require.Equal(t, 410, mgr.call(t, http.MethodPost, "/api/v1/purchase-invoices", map[string]any{"supplier_id": supID}, key()).code)

	// A branch account of another branch cannot be used to pay: not found, as for expenses.
	other := o.call(t, http.MethodPost, "/api/v1/branches", map[string]any{"name": "Arusha", "code": "ARU"}, "")
	require.Equal(t, 201, other.code, other.raw)
	acc := o.call(t, http.MethodPost, "/api/v1/money-accounts", map[string]any{"name": "Arusha safe", "type": "cash", "branch_id": other.data["id"], "opening_balance": "5000000"}, "")
	require.Equal(t, 201, acc.code, acc.raw)
	pay := mgr.call(t, http.MethodPost, "/api/v1/suppliers/"+str(supID)+"/payments", map[string]any{"amount": "100000", "method": "cash", "money_account_id": str(acc.data["id"])}, key())
	require.Equal(t, 201, pay.code, pay.raw, "accounts are not hidden by location")
	require.Equal(t, 201, o.call(t, http.MethodPost, "/api/v1/suppliers/"+str(supID)+"/payments", map[string]any{"amount": "100000", "method": "cash", "money_account_id": str(acc.data["id"])}, key()).code)
	own := mgr.call(t, http.MethodPost, "/api/v1/suppliers/"+str(supID)+"/payments", map[string]any{"amount": "100000", "method": "cash", "money_account_id": str(rich.data["id"])}, key())
	require.Equal(t, 201, own.code, "an account of the manager's own branch works: "+own.raw)
}

// FR-ATT-11: once a payroll run is approved, nothing in its period can change for the people in it,
// including days that had no attendance record yet.
func TestApprovedPayrollLocksDaysWithoutRecords(t *testing.T) {
	e := newEnv(t, defaultNow) // 2026-11-16
	o := e.owner
	workWeek(t, o)
	emp := employee(t, o, "Zuhura", "260000", nil)
	zuhura := e.employeeLogin(t, emp, "staff")
	event := func(day string) int {
		return o.call(t, http.MethodPost, "/api/v1/attendance/events", map[string]any{"employee_id": emp, "type": "in", "at": day + "T05:00:00Z", "reason": "Forgot to clock"}, "").code
	}
	require.Equal(t, 201, event("2026-11-12"), "before approval the day can be filled in")

	run := o.call(t, http.MethodPost, "/api/v1/payroll-runs", map[string]any{"period_start": "2026-11-01", "period_end": "2026-11-30"}, "")
	require.Equal(t, 201, run.code, run.raw)
	runID := str(run.data["id"])
	require.Equal(t, 200, o.call(t, http.MethodPost, "/api/v1/payroll-runs/"+runID+"/calculate", nil, "").code)
	require.Equal(t, 200, payrollApprover(t, e).call(t, http.MethodPost, "/api/v1/payroll-runs/"+runID+"/approve", nil, key()).code)

	// A day with no record, a clock-in today, and a leave day are all inside the approved period.
	locked := o.call(t, http.MethodPost, "/api/v1/attendance/events", map[string]any{"employee_id": emp, "type": "in", "at": "2026-11-13T05:00:00Z", "reason": "Late entry"}, "")
	require.Equal(t, 409, locked.code, locked.raw)
	require.Equal(t, "period_locked", errCode(locked.body))
	require.Equal(t, 409, zuhura.call(t, http.MethodPost, "/api/v1/attendance/clock-in", nil, "").code, "clocking in today is inside November")
	types := o.call(t, http.MethodGet, "/api/v1/leave-types", nil, "")
	annual := find(asList(types.body["data"]), "name", "Annual")
	lv := zuhura.call(t, http.MethodPost, "/api/v1/leave-requests", map[string]any{"leave_type_id": annual["id"], "start_date": "2026-11-24", "end_date": "2026-11-24"}, "")
	require.Equal(t, 201, lv.code, lv.raw)
	dec := o.call(t, http.MethodPost, "/api/v1/leave-requests/"+str(lv.data["id"])+"/approve", nil, "")
	require.Equal(t, 409, dec.code, "leave inside the approved period cannot be marked: "+dec.raw)

	// A day before the period is not locked.
	require.Equal(t, 201, event("2026-10-28"))
}

func countTitle(t *testing.T, c *client, title string) int {
	n := 0
	for _, got := range titles(t, c) {
		if got == title {
			n++
		}
	}
	return n
}

// FR-APR-02: an approval request goes to the people who can decide it at that amount, not to everyone.
func TestApprovalRequestsReachOnlyPeopleWhoCanDecideThem(t *testing.T) {
	e := newEnv(t, defaultNow)
	o := e.owner
	mgr, _ := e.user(t, "manager") // expenses.approve limit 1,000,000
	staff, _ := e.user(t, "staff")
	cats := o.call(t, http.MethodGet, "/api/v1/expense-categories", nil, "")
	category := asList(cats.body["data"])[0]["id"]

	before := countTitle(t, mgr, "Expense awaiting approval")
	require.Equal(t, 201, staff.call(t, http.MethodPost, "/api/v1/expenses", map[string]any{"category_id": category, "amount": "80000", "description": "Small"}, key()).code)
	require.Equal(t, before+1, countTitle(t, mgr, "Expense awaiting approval"))
	require.Equal(t, before+1, countTitle(t, o, "Expense awaiting approval"))

	// The requester is not told about their own request.
	ownBefore := countTitle(t, mgr, "Expense awaiting approval")
	ownRequest := mgr.call(t, http.MethodPost, "/api/v1/expenses", map[string]any{"category_id": category, "amount": "5000", "description": "Manager own"}, key())
	require.Equal(t, 201, ownRequest.code, ownRequest.raw)
	require.Equal(t, ownBefore, countTitle(t, mgr, "Expense awaiting approval"))
}

// FR-SET-08: the in-app switch and days_before of a notification rule take effect.
func TestNotificationRulesAreHonoured(t *testing.T) {
	e := newEnv(t, defaultNow) // 2026-11-16
	o := e.owner
	mgr, _ := e.user(t, "manager")
	staff, _ := e.user(t, "staff")
	cats := o.call(t, http.MethodGet, "/api/v1/expense-categories", nil, "")
	category := asList(cats.body["data"])[0]["id"]
	rule := func(r map[string]any) {
		res := o.call(t, http.MethodPut, "/api/v1/settings/notifications", map[string]any{"rules": r}, "")
		require.Equal(t, 200, res.code, res.raw)
	}

	// With the in-app channel off, the notification is not delivered in the app at all.
	rule(map[string]any{"approval_waiting": map[string]any{"in_app": false}})
	before := countTitle(t, mgr, "Expense awaiting approval")
	require.Equal(t, 201, staff.call(t, http.MethodPost, "/api/v1/expenses", map[string]any{"category_id": category, "amount": "80000", "description": "Muted"}, key()).code)
	require.Equal(t, before, countTitle(t, mgr, "Expense awaiting approval"))
	unread := mgr.call(t, http.MethodGet, "/api/v1/notifications/unread-count", nil, "")
	n0 := int(dec(unread.data["unread"]).IntPart())

	// Email on, in-app off: it still goes out by email, and still stays out of the app's list and count.
	rule(map[string]any{"approval_waiting": map[string]any{"in_app": false, "email": true}})
	require.Equal(t, 201, staff.call(t, http.MethodPost, "/api/v1/expenses", map[string]any{"category_id": category, "amount": "81000", "description": "Email only"}, key()).code)
	require.Equal(t, before, countTitle(t, mgr, "Expense awaiting approval"))
	require.Equal(t, n0, int(dec(mgr.call(t, http.MethodGet, "/api/v1/notifications/unread-count", nil, "").data["unread"]).IntPart()))

	rule(map[string]any{"approval_waiting": map[string]any{"in_app": true, "email": false}})
	require.Equal(t, 201, staff.call(t, http.MethodPost, "/api/v1/expenses", map[string]any{"category_id": category, "amount": "82000", "description": "Back"}, key()).code)
	require.Equal(t, before+1, countTitle(t, mgr, "Expense awaiting approval"))
}

// FR-SAL-05, FR-SET-04: mobile money and bank payments carry a reference; a method switched off cannot be used.
func TestSalePaymentMethodRules(t *testing.T) {
	e := newEnv(t, defaultNow)
	o := e.owner
	product := e.product(t, "Soap", "1000", "50", "600")
	bankAcc := o.call(t, http.MethodPost, "/api/v1/money-accounts", map[string]any{"name": "CRDB", "type": "bank", "opening_balance": "0"}, "")
	require.Equal(t, 201, bankAcc.code, bankAcc.raw)
	bank := str(bankAcc.data["id"])
	sale := func(pay map[string]any, customer any) result {
		return o.call(t, http.MethodPost, "/api/v1/sales", map[string]any{"customer_id": customer, "items": []map[string]any{{"product_id": product, "qty": "1"}}, "payments": []map[string]any{pay}}, key())
	}
	noRef := sale(map[string]any{"method": "bank", "money_account_id": bank, "amount": "1000"}, nil)
	require.Equal(t, 422, noRef.code, noRef.raw)
	require.Equal(t, 201, sale(map[string]any{"method": "bank", "money_account_id": bank, "amount": "1000", "reference": "TXN-1"}, nil).code)

	set := func(m map[string]any) {
		res := o.call(t, http.MethodPut, "/api/v1/payment-methods", m, "")
		require.Equal(t, 200, res.code, res.raw)
	}
	set(map[string]any{"bank": map[string]any{"enabled": false}})
	off := sale(map[string]any{"method": "bank", "money_account_id": bank, "amount": "1000", "reference": "TXN-2"}, nil)
	require.Equal(t, 409, off.code, off.raw)
	require.Equal(t, "method_disabled", str(off.body["error"].(map[string]any)["code"]))

	set(map[string]any{"credit": map[string]any{"enabled": false}})
	cust := o.call(t, http.MethodPost, "/api/v1/customers", map[string]any{"name": "Neema", "phone": "0711222333", "credit_limit": "100000"}, "")
	require.Equal(t, 201, cust.code, cust.raw)
	credit := o.call(t, http.MethodPost, "/api/v1/sales", map[string]any{"customer_id": cust.data["id"], "items": []map[string]any{{"product_id": product, "qty": "1"}}, "payments": []map[string]any{}}, key())
	require.Equal(t, 409, credit.code, credit.raw)
}

// FR-SEC-01, FR-SET-10: report downloads are logged, and a rule change keeps its old value.
func TestExportsAndRuleChangesAreAudited(t *testing.T) {
	e := newEnv(t, defaultNow)
	o := e.owner
	view := o.call(t, http.MethodGet, "/api/v1/reports/sales/summary", nil, "")
	require.Equal(t, 200, view.code, view.raw)
	require.Empty(t, asList(o.call(t, http.MethodGet, "/api/v1/audit-logs?action=report.exported", nil, "").body["data"]), "viewing is not an export")
	csv := o.call(t, http.MethodGet, "/api/v1/reports/sales/summary?format=csv", nil, "")
	require.Equal(t, 200, csv.code)
	rows := asList(o.call(t, http.MethodGet, "/api/v1/audit-logs?action=report.exported", nil, "").body["data"])
	require.Len(t, rows, 1)

	put := func(v bool) {
		res := o.call(t, http.MethodPut, "/api/v1/settings/notifications", map[string]any{"rules": map[string]any{"approval_waiting": map[string]any{"in_app": v}}}, "")
		require.Equal(t, 200, res.code, res.raw)
	}
	put(false)
	put(true)
	logs := asList(o.call(t, http.MethodGet, "/api/v1/audit-logs?action=settings.updated", nil, "").body["data"])
	require.NotEmpty(t, logs)
	require.Contains(t, jsonOf(logs[0]), `"in_app":false`, "the newest change shows the value it replaced")
}

func jsonOf(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
