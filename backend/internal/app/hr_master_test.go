package app_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHRMasterDataEmployeesAndAdjustments(t *testing.T) {
	e := newEnv(t, defaultNow)
	o := e.owner
	mgr, _ := e.user(t, "manager")
	staff, _ := e.user(t, "staff")

	// Departments and positions are text on the employee, not master tables.
	require.Equal(t, 410, o.call(t, http.MethodPost, "/api/v1/departments", map[string]any{"name": "Sales"}, "").code)
	require.Equal(t, 410, o.call(t, http.MethodPost, "/api/v1/positions", map[string]any{"name": "Cashier", "default_pay": "350000"}, "").code)
	require.Equal(t, 200, mgr.call(t, http.MethodGet, "/api/v1/departments", nil, "").code)
	require.Equal(t, 403, staff.call(t, http.MethodGet, "/api/v1/departments", nil, "").code)
	require.Equal(t, 403, mgr.call(t, http.MethodGet, "/api/v1/commission-rules", nil, "").code, "managers do not manage pay rules")

	amani := employee(t, o, "Amani", "400000", map[string]any{"position": "Cashier"})
	zawadi := employee(t, o, "Zawadi", "250000", nil)
	badPhone := o.call(t, http.MethodPost, "/api/v1/employees", map[string]any{"name": "Bad phone", "phone": "not-a-phone", "hire_date": "2026-01-01"}, "")
	require.GreaterOrEqual(t, badPhone.code, 400, badPhone.raw)
	require.Less(t, badPhone.code, 500, badPhone.raw)
	phone := uniquePhone()
	upd := o.call(t, http.MethodPut, "/api/v1/employees/"+amani, map[string]any{"phone": phone, "position": "Senior cashier"}, "")
	require.Equal(t, 200, upd.code, upd.raw)
	require.Equal(t, "Amani", str(upd.data["name"]))
	require.Equal(t, "Senior cashier", str(upd.data["position"]))
	require.True(t, dec(upd.data["base_pay"]).Equal(dec("400000")), "an update without pay keeps it")
	require.Equal(t, 404, o.call(t, http.MethodPut, "/api/v1/employees/"+key(), map[string]any{"phone": uniquePhone()}, "").code)
	require.Len(t, asList(o.call(t, http.MethodGet, "/api/v1/employees?q=zaw", nil, "").body["data"]), 1)
	require.NotNil(t, o.call(t, http.MethodGet, "/api/v1/employees", nil, "").body["meta"])

	own := o.call(t, http.MethodGet, "/api/v1/employees/"+amani, nil, "")
	require.True(t, dec(own.data["base_pay"]).Equal(dec("400000")))
	seen := mgr.call(t, http.MethodGet, "/api/v1/employees/"+amani, nil, "")
	require.Equal(t, 200, seen.code, seen.raw)
	require.Nil(t, seen.data["base_pay"], "a manager without hr.compensation.view sees no pay: "+seen.raw)
	for _, row := range asList(mgr.call(t, http.MethodGet, "/api/v1/employees", nil, "").body["data"]) {
		require.Nil(t, row["base_pay"])
	}

	rule := o.call(t, http.MethodPost, "/api/v1/commission-rules", map[string]any{"employee_id": amani, "calc": "percent_of_sales", "value": "2.5"}, "")
	require.Equal(t, 201, rule.code, rule.raw)
	wide := o.call(t, http.MethodPost, "/api/v1/commission-rules", map[string]any{"calc": "percent_of_sales", "value": "1", "name": "Shop floor"}, "")
	require.Equal(t, 201, wide.code, wide.raw, "a rule may apply to every seller")
	toggled := o.call(t, http.MethodPut, "/api/v1/commission-rules/"+str(rule.data["id"]), map[string]any{"is_active": false}, "")
	require.Equal(t, 200, toggled.code, toggled.raw)
	require.True(t, dec(toggled.data["percent"]).Equal(dec("2.5")), "toggling a rule keeps its percent")
	require.Equal(t, false, toggled.data["is_active"])
	require.Len(t, asList(o.call(t, http.MethodGet, "/api/v1/commission-rules", nil, "").body["data"]), 2)

	adj := o.call(t, http.MethodPost, "/api/v1/pay-adjustments", map[string]any{"employee_id": amani, "period": "2026-11-01", "kind": "bonus", "amount": "20000", "reason": "Target met"}, "")
	require.Equal(t, 201, adj.code, adj.raw)
	require.Equal(t, 422, o.call(t, http.MethodPost, "/api/v1/pay-adjustments", map[string]any{"employee_id": amani, "period": "2026-11-01", "kind": "bonus", "amount": "0", "reason": "none"}, "").code)
	require.Equal(t, 403, mgr.call(t, http.MethodPost, "/api/v1/pay-adjustments", map[string]any{"employee_id": amani, "period": "2026-11-01", "kind": "bonus", "amount": "1", "reason": "nope"}, "").code)
	require.Equal(t, 404, o.call(t, http.MethodPost, "/api/v1/pay-adjustments", map[string]any{"employee_id": key(), "period": "2026-11-01", "kind": "bonus", "amount": "1", "reason": "missing"}, "").code)
	list := o.call(t, http.MethodGet, "/api/v1/pay-adjustments?employee_id="+amani, nil, "")
	require.Len(t, asList(list.body["data"]), 1)
	require.Equal(t, 204, o.call(t, http.MethodDelete, "/api/v1/pay-adjustments/"+str(adj.data["id"]), nil, "").code)
	require.Equal(t, 404, o.call(t, http.MethodDelete, "/api/v1/pay-adjustments/"+str(adj.data["id"]), nil, "").code)

	housing := o.call(t, http.MethodPut, "/api/v1/employees/"+amani+"/components", map[string]any{"components": []map[string]any{{"name": "Housing", "amount": "15000"}}}, "")
	require.Equal(t, 200, housing.code, housing.raw)
	require.True(t, dec(find(asList(housing.body["data"]), "name", "Housing")["amount"]).Equal(dec("15000")))

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
	slip := find(asList(calc.data["payslips"]), "employee_id", zawadi)
	require.Len(t, asList(calc.data["payslips"]), 2, "both are in November's run")
	require.True(t, dec(slip["gross"]).LessThan(dec("250000")), "the leaver is paid for part of the month: "+str(slip["gross"]))
	require.True(t, dec(slip["gross"]).GreaterThan(dec("0")))
	amaniSlip := find(asList(calc.data["payslips"]), "employee_id", amani)
	require.True(t, dec(amaniSlip["allowance"]).Equal(dec("15000")))
	require.True(t, dec(amaniSlip["gross"]).Equal(dec("415000")))

	dec1 := o.call(t, http.MethodPost, "/api/v1/payroll-runs", map[string]any{"period_start": "2026-12-01", "period_end": "2026-12-31"}, "")
	require.Equal(t, 201, dec1.code, dec1.raw)
	later := o.call(t, http.MethodPost, "/api/v1/payroll-runs/"+str(dec1.data["id"])+"/calculate", nil, "")
	require.Equal(t, 200, later.code, later.raw)
	require.Len(t, asList(later.data["payslips"]), 1, "after the termination only the active employee is paid")
	require.Equal(t, amani, str(asList(later.data["payslips"])[0]["employee_id"]))

	other := newEnv(t, defaultNow)
	require.Equal(t, 404, other.owner.call(t, http.MethodGet, "/api/v1/employees/"+amani, nil, "").code)
	require.Empty(t, asList(other.owner.call(t, http.MethodGet, "/api/v1/pay-adjustments", nil, "").body["data"]))
}

// Leave is decided on the leave request itself. It does not create an approval request.
func TestHRLeaveDecidedThroughTheApprovalsInbox(t *testing.T) {
	e := newEnv(t, defaultNow)
	o := e.owner
	workWeek(t, o)
	emp := employee(t, o, "Lulu", "300000", nil)
	lulu := e.employeeLogin(t, emp, "staff")
	types := o.call(t, http.MethodGet, "/api/v1/leave-types", nil, "")
	require.Equal(t, 200, types.code, types.raw)
	annual := find(asList(types.body["data"]), "name", "Annual")
	require.NotEmpty(t, annual)
	typeID := str(annual["id"])

	request := func(start, end string) string {
		res := lulu.call(t, http.MethodPost, "/api/v1/leave-requests", map[string]any{"leave_type_id": typeID, "start_date": start, "end_date": end, "reason": "Family"}, "")
		require.Equal(t, 201, res.code, res.raw)
		require.Equal(t, "requested", str(res.data["status"]))
		return str(res.data["id"])
	}

	l1 := request("2026-11-24", "2026-11-25")
	ok := o.call(t, http.MethodPost, "/api/v1/leave-requests/"+l1+"/approve", map[string]any{"note": "Enjoy"}, "")
	require.Equal(t, 200, ok.code, ok.raw)
	require.Equal(t, "approved", str(find(asList(o.call(t, http.MethodGet, "/api/v1/leave-requests?employee_id="+emp, nil, "").body["data"]), "id", l1)["status"]))
	days := asList(o.call(t, http.MethodGet, "/api/v1/attendance/days?employee_id="+emp+"&status=leave", nil, "").body["data"])
	require.Len(t, days, 2)
	for _, d := range days {
		require.Equal(t, "on_leave", d["status"])
	}

	l2 := request("2026-12-01", "2026-12-01")
	require.Equal(t, 200, o.call(t, http.MethodPost, "/api/v1/leave-requests/"+l2+"/reject", map[string]any{"note": "Busy week"}, "").code)
	require.Equal(t, "rejected", str(find(asList(o.call(t, http.MethodGet, "/api/v1/leave-requests?status=rejected", nil, "").body["data"]), "id", l2)["status"]))

	l3 := request("2026-12-08", "2026-12-08")
	require.Equal(t, 200, lulu.call(t, http.MethodPost, "/api/v1/leave-requests/"+l3+"/cancel", nil, "").code)
	require.Equal(t, "cancelled", str(find(asList(o.call(t, http.MethodGet, "/api/v1/leave-requests?status=cancelled", nil, "").body["data"]), "id", l3)["status"]))

	dup := lulu.call(t, http.MethodPost, "/api/v1/leave-requests", map[string]any{"leave_type_id": typeID, "start_date": "2026-11-25", "end_date": "2026-11-26"}, "")
	require.Equal(t, 409, dup.code, dup.raw)
	require.Equal(t, 409, o.call(t, http.MethodPost, "/api/v1/leave-requests/"+l1+"/approve", nil, "").code)
	require.Equal(t, 403, lulu.call(t, http.MethodGet, "/api/v1/leave-requests", nil, "").code, "staff list their own leave through /me/leave, not the approver list")
}
