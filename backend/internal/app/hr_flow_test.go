package app_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// workWeek is a no-op on the approved schema: there is no schedules table.
// The default calendar is Monday–Saturday, 08:00–17:00, with a 60 minute break.
func workWeek(t *testing.T, c *client) {
	t.Helper()
	res := c.call(t, http.MethodPut, "/api/v1/schedules", map[string]any{"days": []map[string]any{}}, "")
	require.Equal(t, 410, res.code, res.raw)
	require.Equal(t, "feature_removed", errCode(res.body))
}

func employee(t *testing.T, c *client, name, base string, extra map[string]any) string {
	t.Helper()
	body := map[string]any{"name": name, "base_pay": base, "hire_date": "2026-01-01", "phone": uniquePhone()}
	for k, v := range extra {
		body[k] = v
	}
	res := c.call(t, http.MethodPost, "/api/v1/employees", body, "")
	require.Equal(t, 201, res.code, res.raw)
	return str(res.data["id"])
}

// employeeLogin creates a staff login linked to the employee and returns its client.
func (e *env) employeeLogin(t *testing.T, empID, slug string) *client {
	t.Helper()
	phone := uniquePhone()
	res := e.owner.call(t, http.MethodPost, "/api/v1/employees/"+empID+"/user", map[string]any{
		"phone": phone, "password": "employee-pass-1", "role_id": e.roleID(t, slug),
	}, "")
	require.Equal(t, 201, res.code, res.raw)
	return &client{app: e.app.Fiber, token: login(t, e.app.Fiber, phone, "employee-pass-1"), branch: e.branch}
}

func TestHRFlow(t *testing.T) {
	e := newEnv(t, defaultNow)
	o := e.owner
	workWeek(t, o)
	require.Equal(t, 410, o.call(t, http.MethodPost, "/api/v1/departments", map[string]any{"name": "Shop floor"}, "").code)
	require.Equal(t, 410, o.call(t, http.MethodPost, "/api/v1/positions", map[string]any{"name": "Cashier", "default_pay": "300000"}, "").code)
	require.Equal(t, 410, o.call(t, http.MethodPost, "/api/v1/holidays", map[string]any{"day": "2026-12-09", "name": "Independence Day"}, "").code)

	annual := o.call(t, http.MethodPost, "/api/v1/leave-types", map[string]any{"name": "Study", "days_per_year": "10", "is_paid": true}, "")
	require.Equal(t, 201, annual.code, annual.raw)

	a := o.call(t, http.MethodPost, "/api/v1/employees", map[string]any{
		"name": "Asha", "position": "Cashier", "hire_date": "2026-01-01", "phone": uniquePhone(), "base_pay": "300000",
	}, "")
	require.Equal(t, 201, a.code, a.raw)
	require.Equal(t, "300000.00", str(a.data["base_pay"]))
	require.Equal(t, "Cashier", str(a.data["position"]))
	aID := str(a.data["id"])
	staff := e.employeeLogin(t, aID, "staff")

	// A second location. Listing follows the active location header, not a per-person branch grant.
	br := o.call(t, http.MethodPost, "/api/v1/branches", map[string]any{"name": "Branch B"}, "")
	require.Equal(t, 201, br.code, br.raw)
	bID := employee(t, o, "Baraka", "400000", map[string]any{"branch_id": br.data["id"], "position": "Storekeeper"})
	mgr, _ := e.user(t, "manager")

	list := mgr.call(t, http.MethodGet, "/api/v1/employees", nil, "")
	require.Equal(t, 200, list.code, list.raw)
	require.Len(t, asList(list.body["data"]), 1)
	_, hasSalary := asList(list.body["data"])[0]["base_pay"]
	require.False(t, hasSalary, "manager has hr.employee.view but not hr.compensation.view")
	require.Equal(t, 200, mgr.call(t, http.MethodGet, "/api/v1/employees/"+bID, nil, "").code, "a location is not a permission boundary")
	otherLoc := mgr.call(t, http.MethodGet, "/api/v1/employees?branch_id="+str(br.data["id"]), nil, "")
	require.Equal(t, 200, otherLoc.code, otherLoc.raw)
	require.Len(t, asList(otherLoc.body["data"]), 1)
	require.Equal(t, 403, staff.call(t, http.MethodGet, "/api/v1/employees", nil, "").code)

	// Leave: request, approve, balance.
	req := staff.call(t, http.MethodPost, "/api/v1/leave-requests", map[string]any{
		"leave_type_id": annual.data["id"], "start_date": "2026-11-23", "end_date": "2026-11-25", "reason": "Family",
	}, "")
	require.Equal(t, 201, req.code, req.raw)
	require.True(t, dec(req.data["days"]).Equal(dec("3")))
	require.Equal(t, "requested", str(req.data["status"]))
	require.Equal(t, 403, staff.call(t, http.MethodPost, "/api/v1/leave-requests/"+str(req.data["id"])+"/approve", nil, "").code)
	bal := o.call(t, http.MethodGet, "/api/v1/employees/"+aID+"/leave-balance?year=2026", nil, "")
	require.Equal(t, 200, bal.code, bal.raw)
	require.True(t, dec(find(asList(bal.body["data"]), "name", "Study")["pending"]).Equal(dec("3")))

	ok := mgr.call(t, http.MethodPost, "/api/v1/leave-requests/"+str(req.data["id"])+"/approve", map[string]any{"note": "Enjoy"}, "")
	require.Equal(t, 200, ok.code, ok.raw)
	require.Equal(t, "approved", str(ok.data["status"]))
	days := o.call(t, http.MethodGet, "/api/v1/attendance/days?employee_id="+aID+"&from=2026-11-23&to=2026-11-25", nil, "")
	require.Len(t, asList(days.body["data"]), 3)
	for _, d := range asList(days.body["data"]) {
		require.Equal(t, "on_leave", d["status"])
	}
	bal = staff.call(t, http.MethodGet, "/api/v1/me/leave", nil, "")
	require.Equal(t, 200, bal.code, bal.raw)
	annualBal := find(asList(bal.data["balance"]), "name", "Study")
	require.True(t, dec(annualBal["taken"]).Equal(dec("3")))
	require.True(t, dec(annualBal["remaining"]).Equal(dec("7")))
	// Leave lives on its own table; it does not open an approval request.
	inbox := o.call(t, http.MethodGet, "/api/v1/approvals?status=pending", nil, "")
	require.Equal(t, 200, inbox.code, inbox.raw)
	require.Empty(t, find(asList(inbox.body["data"]), "subject_id", str(req.data["id"])))

	over := staff.call(t, http.MethodPost, "/api/v1/leave-requests", map[string]any{
		"leave_type_id": annual.data["id"], "start_date": "2026-12-01", "end_date": "2026-12-10",
	}, "")
	require.Equal(t, 409, over.code, over.raw)
	overlap := staff.call(t, http.MethodPost, "/api/v1/leave-requests", map[string]any{
		"leave_type_id": annual.data["id"], "start_date": "2026-11-25", "end_date": "2026-11-26",
	}, "")
	require.Equal(t, 409, overlap.code, overlap.raw)

	// Cancelling future approved leave releases the days.
	cancel := staff.call(t, http.MethodPost, "/api/v1/leave-requests/"+str(req.data["id"])+"/cancel", nil, "")
	require.Equal(t, 200, cancel.code, cancel.raw)
	require.Equal(t, "cancelled", str(cancel.data["status"]))
	days = o.call(t, http.MethodGet, "/api/v1/attendance/days?employee_id="+aID+"&from=2026-11-23&to=2026-11-25", nil, "")
	require.Empty(t, asList(days.body["data"]))

	// Attendance: clock in late, clock out, double clock-in rejected.
	in := staff.call(t, http.MethodPost, "/api/v1/attendance/clock-in", nil, "")
	require.Equal(t, 200, in.code, in.raw)
	require.Equal(t, "late", str(in.data["status"]))
	require.Equal(t, "20", str(in.data["late_minutes"]))
	require.Equal(t, true, in.data["needs_review"])
	require.Equal(t, 409, staff.call(t, http.MethodPost, "/api/v1/attendance/clock-in", nil, "").code)
	outc := staff.call(t, http.MethodPost, "/api/v1/attendance/clock-out", nil, "")
	require.Equal(t, 200, outc.code, outc.raw)
	require.Equal(t, false, outc.data["needs_review"])
	require.Equal(t, 409, staff.call(t, http.MethodPost, "/api/v1/attendance/clock-out", nil, "").code)
	// Only attendance.edit may clock someone else in.
	require.Equal(t, 403, staff.call(t, http.MethodPost, "/api/v1/attendance/clock-in", map[string]any{"employee_id": bID}, "").code)
	late := mgr.call(t, http.MethodGet, "/api/v1/attendance/late", nil, "")
	require.Equal(t, 200, late.code, late.raw)
	require.NotEmpty(t, find(asList(late.body["data"]), "employee_id", aID))
	today := mgr.call(t, http.MethodGet, "/api/v1/attendance/today", nil, "")
	require.Equal(t, 200, today.code, today.raw)
	require.Equal(t, "out", str(find(asList(today.data["employees"]), "employee_id", aID)["state"]))

	// A missed day entered by hand: 07:00 to 19:00 local on Friday the 13th.
	ev := mgr.call(t, http.MethodPost, "/api/v1/attendance/events", map[string]any{"employee_id": aID, "type": "in", "at": "2026-11-13T04:00:00Z", "reason": "Forgot to clock in"}, "")
	require.Equal(t, 201, ev.code, ev.raw)
	require.Equal(t, true, ev.data["needs_review"])
	ev = mgr.call(t, http.MethodPost, "/api/v1/attendance/events", map[string]any{"employee_id": aID, "type": "out", "at": "2026-11-13T16:00:00Z", "reason": "Forgot to clock out"}, "")
	require.Equal(t, 201, ev.code, ev.raw)
	require.Equal(t, "240", str(ev.data["overtime_minutes"]), "12 hours worked against an 8 hour day")
	require.Equal(t, 400, mgr.call(t, http.MethodPost, "/api/v1/attendance/events", map[string]any{"employee_id": aID, "type": "in", "at": "2027-01-01T04:00:00Z", "reason": "future"}, "").code)
	ot := mgr.call(t, http.MethodGet, "/api/v1/attendance/overtime?employee_id="+aID, nil, "")
	require.NotEmpty(t, find(asList(ot.body["data"]), "id", str(ev.data["id"])))
	require.Equal(t, 403, staff.call(t, http.MethodPost, "/api/v1/attendance/overtime/"+str(ev.data["id"])+"/approve", nil, "").code)
	approved := mgr.call(t, http.MethodPost, "/api/v1/attendance/overtime/"+str(ev.data["id"])+"/approve", nil, "")
	require.Equal(t, 200, approved.code, approved.raw)
	require.Equal(t, "240", str(approved.data["overtime_minutes"]))

	// Correcting a day needs a reason of at least 5 characters. A correction recomputes overtime.
	require.Equal(t, 422, mgr.call(t, http.MethodPut, "/api/v1/attendance/days/"+str(ev.data["id"]), map[string]any{"status": "half_day"}, "").code)
	fix := mgr.call(t, http.MethodPut, "/api/v1/attendance/days/"+str(ev.data["id"]), map[string]any{"status": "half_day", "reason": "Left at noon"}, "")
	require.Equal(t, 200, fix.code, fix.raw)
	require.Equal(t, "half_day", str(fix.data["status"]))
	rejected := mgr.call(t, http.MethodPost, "/api/v1/attendance/overtime/"+str(ev.data["id"])+"/reject", nil, "")
	require.Equal(t, 200, rejected.code, rejected.raw)
	require.Equal(t, "0", str(rejected.data["overtime_minutes"]))

	summary := mgr.call(t, http.MethodGet, "/api/v1/attendance/summary?month=2026-11", nil, "")
	require.Equal(t, 200, summary.code, summary.raw)
	row := find(asList(summary.data["employees"]), "employee_id", aID)
	require.Equal(t, "1", str(row["late_count"]))
	require.True(t, dec(row["overtime_hours"]).Equal(dec("0")))

	// Day close: Saturday the 14th is a working day. Chausiku never clocked in
	// and is marked absent; Daudi clocked in and never out, so is flagged.
	cID := employee(t, o, "Chausiku", "200000", nil)
	dID := employee(t, o, "Daudi", "200000", nil)
	ev = mgr.call(t, http.MethodPost, "/api/v1/attendance/events", map[string]any{"employee_id": dID, "type": "in", "at": "2026-11-14T05:00:00Z", "reason": "Shared device"}, "")
	require.Equal(t, 201, ev.code, ev.raw)
	require.NoError(t, e.app.Jobs.Enqueue(context.Background(), "attendance_close", map[string]any{"date": "2026-11-14"}))
	e.runJobs(t)
	cDay := asList(o.call(t, http.MethodGet, "/api/v1/attendance/days?employee_id="+cID+"&from=2026-11-14&to=2026-11-14", nil, "").body["data"])
	require.Len(t, cDay, 1)
	require.Equal(t, "absent", cDay[0]["status"])
	dDay := asList(o.call(t, http.MethodGet, "/api/v1/attendance/days?employee_id="+dID+"&from=2026-11-14&to=2026-11-14", nil, "").body["data"])
	require.Len(t, dDay, 1)
	require.Equal(t, true, dDay[0]["needs_review"])
	require.Nil(t, dDay[0]["last_out"], "a missing clock-out is never guessed")

	// Loans are active as soon as they are recorded. Disburse, repay, schedule.
	cash := o.call(t, http.MethodPost, "/api/v1/money-accounts", map[string]any{"name": "HR cash " + uuid.NewString()[:6], "type": "cash", "opening_balance": "500000"}, "")
	require.Equal(t, 201, cash.code, cash.raw)
	require.Equal(t, 403, mgr.call(t, http.MethodPost, "/api/v1/loans", map[string]any{"employee_id": aID, "principal": "30000"}, "").code)
	loan := o.call(t, http.MethodPost, "/api/v1/loans", map[string]any{"employee_id": aID, "principal": "30000", "monthly_deduction": "10000"}, key())
	require.Equal(t, 201, loan.code, loan.raw)
	require.Equal(t, "active", str(loan.data["status"]))
	disb := o.call(t, http.MethodPost, "/api/v1/loans/"+str(loan.data["id"])+"/disburse", map[string]any{"money_account_id": cash.data["id"]}, key())
	require.Equal(t, 200, disb.code, disb.raw)
	repayKey := key()
	repay := o.call(t, http.MethodPost, "/api/v1/loans/"+str(loan.data["id"])+"/repay", map[string]any{"amount": "10000", "money_account_id": cash.data["id"]}, repayKey)
	require.Equal(t, 201, repay.code, repay.raw)
	require.Equal(t, "20000.00", str(repay.data["balance"]))
	again := o.call(t, http.MethodPost, "/api/v1/loans/"+str(loan.data["id"])+"/repay", map[string]any{"amount": "10000", "money_account_id": cash.data["id"]}, repayKey)
	require.Equal(t, 201, again.code, again.raw)
	require.Equal(t, "20000.00", str(again.data["balance"]), "same idempotency key does not repay twice")
	require.Equal(t, 409, o.call(t, http.MethodPost, "/api/v1/loans/"+str(loan.data["id"])+"/repay", map[string]any{"amount": "25000", "money_account_id": cash.data["id"]}, key()).code)
	detail := o.call(t, http.MethodGet, "/api/v1/loans/"+str(loan.data["id"]), nil, "")
	require.Equal(t, 200, detail.code, detail.raw)
	require.Len(t, asList(detail.data["schedule"]), 2)
	require.True(t, dec(detail.data["repaid"]).Equal(dec("10000")))
	myLoans := staff.call(t, http.MethodGet, "/api/v1/me/loans", nil, "")
	require.Len(t, asList(myLoans.body["data"]), 1)

	// Advances are active immediately and repaid in one deduction. A manager cannot create them.
	require.Equal(t, 403, mgr.call(t, http.MethodPost, "/api/v1/loans", map[string]any{"employee_id": aID, "principal": "50000", "kind": "advance"}, key()).code)
	big := o.call(t, http.MethodPost, "/api/v1/loans", map[string]any{"employee_id": aID, "principal": "50000", "kind": "advance"}, key())
	require.Equal(t, 201, big.code, big.raw)
	require.Equal(t, "active", str(big.data["status"]))
	require.True(t, dec(big.data["monthly_deduction"]).Equal(dec("50000")))

	// Bonuses and deductions, commission rules, components.
	adj := o.call(t, http.MethodPost, "/api/v1/pay-adjustments", map[string]any{"employee_id": aID, "period": "2026-11-01", "kind": "bonus", "amount": "5000", "reason": "Best seller"}, "")
	require.Equal(t, 201, adj.code, adj.raw)
	require.Equal(t, 422, o.call(t, http.MethodPost, "/api/v1/pay-adjustments", map[string]any{"employee_id": aID, "period": "2026-11-01", "kind": "tip", "amount": "5000", "reason": "x"}, "").code)
	require.Equal(t, 204, o.call(t, http.MethodDelete, "/api/v1/pay-adjustments/"+str(adj.data["id"]), nil, "").code)
	rule := o.call(t, http.MethodPost, "/api/v1/commission-rules", map[string]any{"employee_id": aID, "calc": "percent_of_sales", "value": "2"}, "")
	require.Equal(t, 201, rule.code, rule.raw)
	require.Equal(t, "sale_percent", str(rule.data["basis"]))
	require.Equal(t, 400, o.call(t, http.MethodPost, "/api/v1/commission-rules", map[string]any{"calc": "unknown", "value": "2"}, "").code)
	upd := o.call(t, http.MethodPut, "/api/v1/commission-rules/"+str(rule.data["id"]), map[string]any{"percent": "3"}, "")
	require.Equal(t, 200, upd.code, upd.raw)
	require.True(t, dec(upd.data["percent"]).Equal(dec("3")))

	allow := o.call(t, http.MethodPut, "/api/v1/employees/"+aID+"/components", map[string]any{"components": []map[string]any{{"name": "Housing", "amount": "20000"}}}, "")
	require.Equal(t, 200, allow.code, allow.raw)
	require.Equal(t, "Housing", str(find(asList(allow.body["data"]), "name", "Housing")["name"]))
	require.Equal(t, 410, o.call(t, http.MethodPost, "/api/v1/salary-components", map[string]any{"name": "PAYE", "kind": "deduction", "calc": "fixed", "value": "1"}, "").code)

	// Self-service sees only own records.
	me := staff.call(t, http.MethodGet, "/api/v1/me/employee", nil, "")
	require.Equal(t, 200, me.code, me.raw)
	require.Equal(t, aID, str(me.data["id"]))
	myDays := staff.call(t, http.MethodGet, "/api/v1/me/attendance", nil, "")
	for _, d := range asList(myDays.body["data"]) {
		require.NotEqual(t, cID, str(d["employee_id"]))
	}
	require.Equal(t, 200, staff.call(t, http.MethodGet, "/api/v1/me/payments", nil, "").code)
	require.Equal(t, 200, staff.call(t, http.MethodGet, "/api/v1/me/payslips", nil, "").code)
	require.Equal(t, 404, mgr.call(t, http.MethodGet, "/api/v1/me/employee", nil, "").code, "manager login is not linked to an employee")

	// Statement and terminate: the login of a terminated employee stops working.
	st := o.call(t, http.MethodGet, "/api/v1/employees/"+aID+"/statement?from=2026-11-01&to=2026-11-30", nil, "")
	require.Equal(t, 200, st.code, st.raw)
	require.NotEmpty(t, asList(st.data["payments"]))
	term := o.call(t, http.MethodPost, "/api/v1/employees/"+aID+"/terminate", map[string]any{"date": "2026-11-30", "reason": "Resigned"}, "")
	require.Equal(t, 200, term.code, term.raw)
	require.Equal(t, "terminated", str(term.data["status"]))
	require.Equal(t, 401, staff.call(t, http.MethodGet, "/api/v1/me/employee", nil, "").code)
	require.Equal(t, 409, o.call(t, http.MethodPut, "/api/v1/employees/"+aID, map[string]any{"name": "Asha B"}, "").code)
}
