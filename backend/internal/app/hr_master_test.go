package app_test

import (
	"net/http"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestHRMasterDataEmployeesAndAdjustments(t *testing.T) {
	e := newEnv(t, defaultNow)
	o := e.owner
	mgr, _ := e.user(t, "manager")
	staff, _ := e.user(t, "staff")

	// Departments and positions: unique names, partial update, delete only when unused.
	dept := o.call(t, http.MethodPost, "/api/v1/departments", map[string]any{"name": "Sales"}, "")
	require.Equal(t, 201, dept.code, dept.raw)
	dID := str(dept.data["id"])
	require.Equal(t, 409, o.call(t, http.MethodPost, "/api/v1/departments", map[string]any{"name": "Sales"}, "").code)
	require.Equal(t, 400, o.call(t, http.MethodPost, "/api/v1/departments", map[string]any{"name": " "}, "").code)
	pos := o.call(t, http.MethodPost, "/api/v1/positions", map[string]any{"name": "Cashier", "default_pay": "350000"}, "")
	require.Equal(t, 201, pos.code, pos.raw)
	posID := str(pos.data["id"])
	require.Equal(t, 409, o.call(t, http.MethodPost, "/api/v1/positions", map[string]any{"name": "Cashier"}, "").code)
	rp := o.call(t, http.MethodPut, "/api/v1/positions/"+posID, map[string]any{"name": "Senior cashier"}, "")
	require.Equal(t, 200, rp.code, rp.raw)
	require.Equal(t, "350000.00", str(rp.data["default_pay"]), "renaming keeps the default pay")
	require.Equal(t, 404, o.call(t, http.MethodPut, "/api/v1/departments/"+key(), map[string]any{"name": "Ghost"}, "").code)
	require.Equal(t, 403, mgr.call(t, http.MethodGet, "/api/v1/commission-rules", nil, "").code, "managers do not manage pay rules")
	require.Equal(t, 200, mgr.call(t, http.MethodGet, "/api/v1/departments", nil, "").code)
	require.Equal(t, 403, staff.call(t, http.MethodGet, "/api/v1/departments", nil, "").code)

	// Employees: create with links, partial update, list filters, terminate.
	amani := employee(t, o, "Amani", "400000", map[string]any{"department_id": dID, "position_id": posID, "employment_type": "contract", "pay_type": "monthly"})
	zawadi := employee(t, o, "Zawadi", "250000", nil)
	badType := o.call(t, http.MethodPost, "/api/v1/employees", map[string]any{"name": "Bad type", "employment_type": "intern", "hire_date": "2026-01-01"}, "")
	require.GreaterOrEqual(t, badType.code, 400, "an unknown employment type is refused: "+badType.raw)
	require.Less(t, badType.code, 500, badType.raw)
	upd := o.call(t, http.MethodPut, "/api/v1/employees/"+amani, map[string]any{"phone": "0755123456", "address": "Mikocheni"}, "")
	require.Equal(t, 200, upd.code, upd.raw)
	require.Equal(t, "Amani", str(upd.data["name"]))
	require.Equal(t, "400000.00", str(upd.data["base_pay"]), "an update without pay keeps it")
	require.Equal(t, 404, o.call(t, http.MethodPut, "/api/v1/employees/"+key(), map[string]any{"phone": "1"}, "").code)
	require.Equal(t, 409, o.call(t, http.MethodDelete, "/api/v1/departments/"+dID, nil, "").code, "a department with employees cannot be deleted")
	byDept := o.call(t, http.MethodGet, "/api/v1/employees?department_id="+dID, nil, "")
	require.Len(t, asList(byDept.body["data"]), 1)
	require.Len(t, asList(o.call(t, http.MethodGet, "/api/v1/employees?q=zaw", nil, "").body["data"]), 1)
	require.NotNil(t, byDept.body["meta"])

	// Salary is visible only with employees.view_salary (FR-EMP-02).
	own := o.call(t, http.MethodGet, "/api/v1/employees/"+amani, nil, "")
	require.Equal(t, "400000.00", str(own.data["base_pay"]))
	seen := mgr.call(t, http.MethodGet, "/api/v1/employees/"+amani, nil, "")
	require.Equal(t, 200, seen.code, seen.raw)
	require.Nil(t, seen.data["base_pay"], "a manager without view_salary sees no pay: "+seen.raw)
	for _, row := range asList(mgr.call(t, http.MethodGet, "/api/v1/employees", nil, "").body["data"]) {
		require.Nil(t, row["base_pay"])
	}

	// Commission rules and one-off bonuses and deductions.
	rule := o.call(t, http.MethodPost, "/api/v1/commission-rules", map[string]any{"employee_id": amani, "calc": "percent_of_sales", "value": "2.5", "min_target": "100000"}, "")
	require.Equal(t, 201, rule.code, rule.raw)
	noTarget := o.call(t, http.MethodPost, "/api/v1/commission-rules", map[string]any{"calc": "percent_of_sales", "value": "2"}, "")
	require.GreaterOrEqual(t, noTarget.code, 400, "a rule needs an employee or a position: "+noTarget.raw)
	require.Less(t, noTarget.code, 500, noTarget.raw)
	toggled := o.call(t, http.MethodPut, "/api/v1/commission-rules/"+str(rule.data["id"]), map[string]any{"is_active": false}, "")
	require.Equal(t, 200, toggled.code, toggled.raw)
	require.Equal(t, "2.5000", str(toggled.data["value"]), "toggling a rule keeps its value")
	require.Len(t, asList(o.call(t, http.MethodGet, "/api/v1/commission-rules", nil, "").body["data"]), 1)

	adj := o.call(t, http.MethodPost, "/api/v1/pay-adjustments", map[string]any{"employee_id": amani, "period": "2026-11-01", "kind": "bonus", "amount": "20000", "reason": "Target met"}, "")
	require.Equal(t, 201, adj.code, adj.raw)
	require.Equal(t, 422, o.call(t, http.MethodPost, "/api/v1/pay-adjustments", map[string]any{"employee_id": amani, "period": "2026-11-01", "kind": "bonus", "amount": "0", "reason": "x"}, "").code)
	require.Equal(t, 403, mgr.call(t, http.MethodPost, "/api/v1/pay-adjustments", map[string]any{"employee_id": amani, "period": "2026-11-01", "kind": "bonus", "amount": "1", "reason": "x"}, "").code)
	require.Equal(t, 404, o.call(t, http.MethodPost, "/api/v1/pay-adjustments", map[string]any{"employee_id": key(), "period": "2026-11-01", "kind": "bonus", "amount": "1", "reason": "x"}, "").code)
	list := o.call(t, http.MethodGet, "/api/v1/pay-adjustments?employee_id="+amani, nil, "")
	require.Len(t, asList(list.body["data"]), 1)
	require.Equal(t, 204, o.call(t, http.MethodDelete, "/api/v1/pay-adjustments/"+str(adj.data["id"]), nil, "").code)
	require.Equal(t, 404, o.call(t, http.MethodDelete, "/api/v1/pay-adjustments/"+str(adj.data["id"]), nil, "").code)

	// Termination keeps the record and takes the person out of payroll.
	require.Equal(t, 422, o.call(t, http.MethodPost, "/api/v1/employees/"+zawadi+"/terminate", map[string]any{"date": "2026-11-15"}, "").code, "a reason is required")
	term := o.call(t, http.MethodPost, "/api/v1/employees/"+zawadi+"/terminate", map[string]any{"date": "2026-11-15", "reason": "Resigned"}, "")
	require.Equal(t, 200, term.code, term.raw)
	require.Equal(t, "terminated", str(term.data["status"]))
	require.Equal(t, 409, o.call(t, http.MethodPost, "/api/v1/employees/"+zawadi+"/terminate", map[string]any{"date": "2026-11-16", "reason": "Again"}, "").code)
	gone := o.call(t, http.MethodGet, "/api/v1/employees?status=terminated", nil, "")
	require.Len(t, asList(gone.body["data"]), 1)
	run := o.call(t, http.MethodPost, "/api/v1/payroll-runs", map[string]any{"period_start": "2026-11-01", "period_end": "2026-11-30"}, "")
	require.Equal(t, 201, run.code, run.raw)
	calc := o.call(t, http.MethodPost, "/api/v1/payroll-runs/"+str(run.data["id"])+"/calculate", nil, "")
	require.Equal(t, 200, calc.code, calc.raw)
	// She worked until the 15th, so November is prorated (FR-PAY-06); a later period excludes her entirely.
	slip := find(asList(calc.data["payslips"]), "employee_id", zawadi)
	require.Len(t, asList(calc.data["payslips"]), 2, "both are in November's run")
	require.True(t, dec(slip["gross"]).LessThan(decimalOf("250000")), "the leaver is paid for part of the month: "+str(slip["gross"]))
	require.True(t, dec(slip["gross"]).GreaterThan(decimalOf("0")))
	dec1 := o.call(t, http.MethodPost, "/api/v1/payroll-runs", map[string]any{"period_start": "2026-12-01", "period_end": "2026-12-31"}, "")
	require.Equal(t, 201, dec1.code, dec1.raw)
	later := o.call(t, http.MethodPost, "/api/v1/payroll-runs/"+str(dec1.data["id"])+"/calculate", nil, "")
	require.Equal(t, 200, later.code, later.raw)
	require.Len(t, asList(later.data["payslips"]), 1, "after the termination only the active employee is paid")
	require.Equal(t, amani, str(asList(later.data["payslips"])[0]["employee_id"]))

	// A department can go once empty of people; deleting twice is a 404.
	require.Equal(t, 200, o.call(t, http.MethodPut, "/api/v1/employees/"+amani, map[string]any{"department_id": nil}, "").code)
	empty := o.call(t, http.MethodPost, "/api/v1/departments", map[string]any{"name": "Temp"}, "")
	require.Equal(t, 204, o.call(t, http.MethodDelete, "/api/v1/departments/"+str(empty.data["id"]), nil, "").code)
	require.Equal(t, 404, o.call(t, http.MethodDelete, "/api/v1/departments/"+str(empty.data["id"]), nil, "").code)

	// Another business has none of it.
	other := newEnv(t, defaultNow)
	require.Equal(t, 404, other.owner.call(t, http.MethodGet, "/api/v1/employees/"+amani, nil, "").code)
	require.Empty(t, asList(other.owner.call(t, http.MethodGet, "/api/v1/pay-adjustments", nil, "").body["data"]))
}

// Leave can be decided from the approvals inbox as well as the leave screen; both reach the same result.
func TestHRLeaveDecidedThroughTheApprovalsInbox(t *testing.T) {
	e := newEnv(t, defaultNow)
	o := e.owner
	workWeek(t, o)
	emp := employee(t, o, "Lulu", "300000", nil)
	lulu := e.employeeLogin(t, emp, "staff")
	types := o.call(t, http.MethodGet, "/api/v1/leave-types", nil, "")
	annual := find(asList(types.body["data"]), "name", "Annual")
	request := func(start, end string) (string, string) {
		res := lulu.call(t, http.MethodPost, "/api/v1/leave-requests", map[string]any{"leave_type_id": annual["id"], "start_date": start, "end_date": end, "reason": "Family"}, "")
		require.Equal(t, 201, res.code, res.raw)
		reqs := o.call(t, http.MethodGet, "/api/v1/approvals?kind=leave&status=pending", nil, "")
		for _, a := range asList(reqs.body["data"]) {
			if str(a["subject_id"]) == str(res.data["id"]) {
				return str(res.data["id"]), str(a["id"])
			}
		}
		t.Fatalf("no approval request for leave %s", str(res.data["id"]))
		return "", ""
	}

	// Approve in the inbox: the leave is approved and the working days become leave days.
	l1, a1 := request("2026-11-24", "2026-11-25")
	ok := o.call(t, http.MethodPost, "/api/v1/approvals/"+a1+"/approve", map[string]any{"note": "Enjoy"}, key())
	require.Equal(t, 200, ok.code, ok.raw)
	require.Equal(t, "approved", str(find(asList(o.call(t, http.MethodGet, "/api/v1/leave-requests?employee_id="+emp, nil, "").body["data"]), "id", l1)["status"]))
	days := asList(o.call(t, http.MethodGet, "/api/v1/attendance/days?employee_id="+emp+"&status=leave", nil, "").body["data"])
	require.Len(t, days, 2)
	bal := o.call(t, http.MethodGet, "/api/v1/employees/"+emp+"/leave-balance", nil, "")
	require.Equal(t, 200, bal.code, bal.raw)

	// Reject in the inbox.
	l2, a2 := request("2026-12-01", "2026-12-01")
	require.Equal(t, 200, o.call(t, http.MethodPost, "/api/v1/approvals/"+a2+"/reject", map[string]any{"note": "Busy week"}, "").code)
	require.Equal(t, "rejected", str(find(asList(o.call(t, http.MethodGet, "/api/v1/leave-requests?status=rejected", nil, "").body["data"]), "id", l2)["status"]))

	// The requester withdraws a pending request through the inbox; the leave is cancelled.
	l3, a3 := request("2026-12-08", "2026-12-08")
	require.Equal(t, 200, lulu.call(t, http.MethodPost, "/api/v1/approvals/"+a3+"/cancel", nil, "").code)
	require.Equal(t, "cancelled", str(find(asList(o.call(t, http.MethodGet, "/api/v1/leave-requests?status=cancelled", nil, "").body["data"]), "id", l3)["status"]))

	// Overlapping dates are refused, and a decided request cannot be decided again.
	dup := lulu.call(t, http.MethodPost, "/api/v1/leave-requests", map[string]any{"leave_type_id": annual["id"], "start_date": "2026-11-25", "end_date": "2026-11-26"}, "")
	require.Equal(t, 409, dup.code, dup.raw)
	require.Equal(t, 409, o.call(t, http.MethodPost, "/api/v1/leave-requests/"+l1+"/approve", nil, "").code)
	require.Equal(t, 403, lulu.call(t, http.MethodGet, "/api/v1/leave-requests", nil, "").code, "staff list their own leave through /me/leave, not the approver list")
}

func decimalOf(v string) decimal.Decimal { return decimal.RequireFromString(v) }
