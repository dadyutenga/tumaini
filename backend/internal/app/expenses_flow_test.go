package app_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"bms/internal/platform/jobs"
)

func pettyBalance(t *testing.T, c *client, id string) string {
	t.Helper()
	res := c.call(t, http.MethodGet, "/api/v1/petty-cash", nil, "")
	require.Equal(t, 200, res.code, res.raw)
	row := find(asList(res.body["data"]), "id", id)
	require.NotEmpty(t, row, res.raw)
	return dec(row["balance"]).StringFixed(2)
}

func TestExpensesFlow(t *testing.T) {
	e := newEnv(t, defaultNow)
	cashier, _ := e.user(t, "staff")
	manager, _ := e.user(t, "manager")

	cats := cashier.call(t, http.MethodGet, "/api/v1/expense-categories", nil, "")
	require.Equal(t, 200, cats.code, cats.raw)
	category := str(find(asList(cats.body["data"]), "name", "Transport")["id"])
	require.NotEmpty(t, category)

	petty := e.owner.call(t, http.MethodPost, "/api/v1/money-accounts", map[string]any{"name": "Petty A", "type": "petty_cash", "opening_balance": "150000", "allow_overdraft": true}, "")
	require.Equal(t, 201, petty.code, petty.raw)
	pettyID := str(petty.data["id"])
	require.Equal(t, false, petty.data["allow_overdraft"], "petty cash never allows overdraft")
	bank := e.owner.call(t, http.MethodPost, "/api/v1/money-accounts", map[string]any{"name": "Bank A", "type": "bank", "opening_balance": "1000000"}, "")
	require.Equal(t, 201, bank.code, bank.raw)
	bankID := str(bank.data["id"])

	// Expenses enter as submitted and are approved directly on app.expenses (never app.approval_requests).
	sub := cashier.call(t, http.MethodPost, "/api/v1/expenses", map[string]any{"category_id": category, "amount": "100000", "description": "Delivery van fuel"}, key())
	require.Equal(t, 201, sub.code, sub.raw)
	expID := str(sub.data["id"])
	require.Equal(t, "submitted", str(sub.data["status"]))

	// Validation: missing description and non-positive amount.
	bad := cashier.call(t, http.MethodPost, "/api/v1/expenses", map[string]any{"category_id": category, "amount": "0"}, key())
	require.Equal(t, 422, bad.code, bad.raw)

	mine := cashier.call(t, http.MethodGet, "/api/v1/expenses", nil, "")
	require.Equal(t, 200, mine.code, mine.raw)
	require.Len(t, asList(mine.body["data"]), 1)
	require.EqualValues(t, 1, mine.body["meta"].(map[string]any)["total"])

	edit := cashier.call(t, http.MethodPut, "/api/v1/expenses/"+expID, map[string]any{"category_id": category, "amount": "120000", "description": "Delivery van fuel and oil"}, "")
	require.Equal(t, 200, edit.code, edit.raw)
	require.Equal(t, "120000.00", dec(edit.data["amount"]).StringFixed(2))

	denied := cashier.call(t, http.MethodPost, "/api/v1/expenses/"+expID+"/approve", nil, "")
	require.Equal(t, 403, denied.code, denied.raw)

	// Provisional role limit (A-09): Manager starts with expense_approve_amount = 0 and must escalate to Admin.
	escalate := manager.call(t, http.MethodPost, "/api/v1/expenses/"+expID+"/approve", map[string]any{"note": "fine"}, "")
	require.Equal(t, 403, escalate.code, escalate.raw)

	ok := e.owner.call(t, http.MethodPost, "/api/v1/expenses/"+expID+"/approve", map[string]any{"note": "fine"}, "")
	require.Equal(t, 200, ok.code, ok.raw)
	require.Equal(t, "approved", ok.data["status"])

	edited := cashier.call(t, http.MethodPut, "/api/v1/expenses/"+expID, map[string]any{"category_id": category, "amount": "1", "description": "x"}, "")
	require.Equal(t, 409, edited.code, "approved expenses cannot be edited")

	paid := manager.call(t, http.MethodPost, "/api/v1/expenses/"+expID+"/pay", map[string]any{"money_account_id": pettyID}, key())
	require.Equal(t, 200, paid.code, paid.raw)
	require.Equal(t, "30000.00", pettyBalance(t, e.owner, pettyID))

	// A second expense: approved by owner, blocked on insufficient petty cash until topped up.
	small := cashier.call(t, http.MethodPost, "/api/v1/expenses", map[string]any{"category_id": category, "amount": "40000", "description": "Water bill"}, key())
	require.Equal(t, 201, small.code, small.raw)
	smallID := str(small.data["id"])
	require.Equal(t, 200, e.owner.call(t, http.MethodPost, "/api/v1/expenses/"+smallID+"/approve", map[string]any{"note": "ok"}, "").code)
	short := manager.call(t, http.MethodPost, "/api/v1/expenses/"+smallID+"/pay", map[string]any{"money_account_id": pettyID}, key())
	require.Equal(t, 409, short.code, short.raw)

	require.Equal(t, 403, manager.call(t, http.MethodPost, "/api/v1/petty-cash/"+pettyID+"/top-up", map[string]any{"from_account_id": bankID, "amount": "50000"}, key()).code)
	top := e.owner.call(t, http.MethodPost, "/api/v1/petty-cash/"+pettyID+"/top-up", map[string]any{"from_account_id": bankID, "amount": "50000"}, key())
	require.Equal(t, 201, top.code, top.raw)
	require.Equal(t, "80000.00", pettyBalance(t, e.owner, pettyID))
	paid2 := manager.call(t, http.MethodPost, "/api/v1/expenses/"+smallID+"/pay", map[string]any{"money_account_id": pettyID}, key())
	require.Equal(t, 200, paid2.code, paid2.raw)
	require.Equal(t, "40000.00", pettyBalance(t, e.owner, pettyID))

	// The expenses show in profit and loss.
	pl := e.owner.call(t, http.MethodGet, "/api/v1/reports/profit-loss", nil, "")
	require.Equal(t, 200, pl.code, pl.raw)
	require.Equal(t, "160000.00", dec(pl.data["expenses"]).StringFixed(2))

	// Paid expense reversal is removed (use transaction correction instead).
	rev := e.owner.call(t, http.MethodPost, "/api/v1/expenses/"+expID+"/reverse", map[string]any{"reason": "Entered twice"}, key())
	require.Equal(t, 410, rev.code, rev.raw)

	// Rejection keeps the decision note.
	sub2 := cashier.call(t, http.MethodPost, "/api/v1/expenses", map[string]any{"category_id": category, "amount": "70000", "description": "Printer"}, key())
	require.Equal(t, 201, sub2.code, sub2.raw)
	rej := manager.call(t, http.MethodPost, "/api/v1/expenses/"+str(sub2.data["id"])+"/reject", map[string]any{"note": "No receipt"}, "")
	require.Equal(t, 200, rej.code, rej.raw)
	require.Equal(t, "rejected", rej.data["status"])
	require.Equal(t, "No receipt", rej.data["reject_reason"])

	// A petty cash reconciliation records the counted balance and difference (40000 expected vs 30000 counted).
	rec := manager.call(t, http.MethodPost, "/api/v1/petty-cash/"+pettyID+"/reconcile", map[string]any{"actual_balance": "30000", "note": "Count"}, key())
	require.Equal(t, 201, rec.code, rec.raw)
	require.Equal(t, "-10000.00", dec(rec.data["difference"]).StringFixed(2))

	// Categories: create, duplicate 409, update, deactivate, staff 403.
	cat := e.owner.call(t, http.MethodPost, "/api/v1/expense-categories", map[string]any{"name": "Cleaning"}, "")
	require.Equal(t, 201, cat.code, cat.raw)
	dup := e.owner.call(t, http.MethodPost, "/api/v1/expense-categories", map[string]any{"name": "Cleaning"}, "")
	require.Equal(t, 409, dup.code, dup.raw)
	ren := e.owner.call(t, http.MethodPut, "/api/v1/expense-categories/"+str(cat.data["id"]), map[string]any{"name": "Cleaning supplies", "is_active": false}, "")
	require.Equal(t, 200, ren.code, ren.raw)
	require.Equal(t, false, ren.data["is_active"])
	cashCats := cashier.call(t, http.MethodPost, "/api/v1/expense-categories", map[string]any{"name": "Nope"}, "")
	require.Equal(t, 403, cashCats.code)

	// Filters and location scoping.
	filtered := e.owner.call(t, http.MethodGet, "/api/v1/expenses?status=paid&from=2026-11-01&to=2026-11-30", nil, "")
	require.Equal(t, 200, filtered.code, filtered.raw)
	paidRows := asList(filtered.body["data"])
	require.Len(t, paidRows, 2)
	require.NotEmpty(t, str(paidRows[0]["location_name"]))

	branch2Res := e.owner.call(t, http.MethodPost, "/api/v1/branches", map[string]any{"name": "Kariakoo Shop"}, "")
	require.Equal(t, 201, branch2Res.code, branch2Res.raw)
	branch2 := str(branch2Res.data["id"])
	// Asking for branch2 while header is scoped to main branch returns 400.
	badLoc := e.owner.call(t, http.MethodGet, "/api/v1/expenses?location_id="+branch2, nil, "")
	require.Equal(t, 400, badLoc.code, badLoc.raw)
	// Create an expense at branch2, then filter under X-Branch-Id: all.
	e.owner.as(t, branch2)
	b2Exp := e.owner.call(t, http.MethodPost, "/api/v1/expenses", map[string]any{"category_id": category, "amount": "15000", "description": "Kariakoo power"}, key())
	require.Equal(t, 201, b2Exp.code, b2Exp.raw)
	e.owner.as(t, "all")
	allList := e.owner.call(t, http.MethodGet, "/api/v1/expenses", nil, "")
	require.Equal(t, 200, allList.code, allList.raw)
	require.GreaterOrEqual(t, len(asList(allList.body["data"])), 4)
	onlyB2 := e.owner.call(t, http.MethodGet, "/api/v1/expenses?location_id="+branch2, nil, "")
	require.Equal(t, 200, onlyB2.code, onlyB2.raw)
	b2Rows := asList(onlyB2.body["data"])
	require.Len(t, b2Rows, 1)
	require.Equal(t, "Kariakoo Shop", str(b2Rows[0]["location_name"]))
	e.owner.as(t, e.branch)

}

func TestExpensesRecurringJob(t *testing.T) {
	e := newEnv(t, defaultNow)
	cats := e.owner.call(t, http.MethodGet, "/api/v1/expense-categories", nil, "")
	rent := str(find(asList(cats.body["data"]), "name", "Rent")["id"])

	tpl := e.owner.call(t, http.MethodPost, "/api/v1/recurring-expenses", map[string]any{
		"category_id": rent, "amount": "300000", "description": "Shop rent", "frequency": "monthly", "start_date": "2026-10-16",
	}, "")
	require.Equal(t, 201, tpl.code, tpl.raw)
	tplID := str(tpl.data["id"])
	require.Equal(t, "2026-10-16", tpl.data["next_run"])

	// The scheduler enqueues the daily job (08:20 local is after 00:30); it catches up October and November.
	require.NoError(t, e.app.Jobs.Schedule(context.Background()))
	e.runJobs(t)
	count := func() int {
		res := e.owner.call(t, http.MethodGet, "/api/v1/expenses?q=Shop%20rent", nil, "")
		require.Equal(t, 200, res.code, res.raw)
		return len(asList(res.body["data"]))
	}
	require.Equal(t, 2, count())

	// Running the job again for the same date creates nothing new.
	require.NoError(t, e.app.Jobs.Enqueue(context.Background(), jobs.RecurringExpenses, map[string]any{"date": "2026-11-16"}))
	e.runJobs(t)
	require.Equal(t, 2, count())

	list := e.owner.call(t, http.MethodGet, "/api/v1/recurring-expenses", nil, "")
	require.Equal(t, 200, list.code, list.raw)
	row := find(asList(list.body["data"]), "id", tplID)
	require.Equal(t, "2026-12-16", row["next_run"])

	// Generated expenses are submitted and wait for approval on app.expenses.
	gen := e.owner.call(t, http.MethodGet, "/api/v1/expenses?q=Shop%20rent&status=submitted", nil, "")
	require.Len(t, asList(gen.body["data"]), 2)

	pause := e.owner.call(t, http.MethodPost, "/api/v1/recurring-expenses/"+tplID+"/pause", nil, "")
	require.Equal(t, 200, pause.code, pause.raw)
	require.Equal(t, false, pause.data["is_active"])
	pause2 := e.owner.call(t, http.MethodPost, "/api/v1/recurring-expenses/"+tplID+"/pause", nil, "")
	require.Equal(t, 409, pause2.code)
	resume := e.owner.call(t, http.MethodPost, "/api/v1/recurring-expenses/"+tplID+"/resume", nil, "")
	require.Equal(t, 200, resume.code, resume.raw)
	require.Equal(t, "2026-12-16", resume.data["next_run"])

	upd := e.owner.call(t, http.MethodPut, "/api/v1/recurring-expenses/"+tplID, map[string]any{
		"category_id": rent, "amount": "350000", "description": "Shop rent", "frequency": "monthly", "start_date": "2026-10-16",
	}, "")
	require.Equal(t, 200, upd.code, upd.raw)
	require.Equal(t, "350000.00", dec(upd.data["amount"]).StringFixed(2))

	staff, _ := e.user(t, "staff")
	forbidden := staff.call(t, http.MethodGet, "/api/v1/recurring-expenses", nil, "")
	require.Equal(t, 403, forbidden.code)
}
