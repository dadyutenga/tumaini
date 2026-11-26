package app_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func slipOf(t *testing.T, run result, employeeID string) map[string]any {
	t.Helper()
	s := find(asList(run.data["payslips"]), "employee_id", employeeID)
	require.NotEmpty(t, s, "payslip for %s in %s", employeeID, run.raw)
	return s
}

func payrollApprover(t *testing.T, e *env) *client {
	t.Helper()
	role := e.owner.call(t, http.MethodPost, "/api/v1/roles", map[string]any{
		"name": "Payroll approver", "permissions": []map[string]any{{"key": "hr.payroll.approve", "scope": "all"}},
	}, "")
	require.Equal(t, 201, role.code, role.raw)
	phone := uniquePhone()
	user := e.owner.call(t, http.MethodPost, "/api/v1/users", map[string]any{
		"name": "Approver", "phone": phone, "password": "approver-pass-1", "role_id": role.data["id"], "branch_ids": []string{e.branch},
	}, "")
	require.Equal(t, 201, user.code, user.raw)
	return &client{app: e.app.Fiber, token: login(t, e.app.Fiber, phone, "approver-pass-1"), branch: e.branch}
}

func TestPayrollFlow(t *testing.T) {
	e := newEnv(t, defaultNow)
	o := e.owner
	workWeek(t, o)

	// Peter 520,000 (daily 20,000, hourly 2,500). Queen 260,000.
	pID := employee(t, o, "Peter", "520000", nil)
	qID := employee(t, o, "Queen", "260000", nil)
	peter := e.employeeLogin(t, pID, "staff")

	require.Equal(t, 410, o.call(t, http.MethodPost, "/api/v1/salary-components", map[string]any{
		"name": "PAYE", "kind": "deduction", "calc": "fixed", "value": "1",
	}, "").code, "salary component masters are not in the approved model")

	housing := o.call(t, http.MethodPut, "/api/v1/employees/"+pID+"/components", map[string]any{
		"components": []map[string]any{{"name": "Housing", "amount": "50000"}},
	}, "")
	require.Equal(t, 200, housing.code, housing.raw)

	types := o.call(t, http.MethodGet, "/api/v1/leave-types", nil, "")
	require.Equal(t, 200, types.code, types.raw)
	unpaid := find(asList(types.body["data"]), "name", "Unpaid")
	require.NotEmpty(t, unpaid)
	lr := peter.call(t, http.MethodPost, "/api/v1/leave-requests", map[string]any{
		"leave_type_id": unpaid["id"], "start_date": "2026-11-18", "end_date": "2026-11-18",
	}, "")
	require.Equal(t, 201, lr.code, lr.raw)
	require.Equal(t, 200, o.call(t, http.MethodPost, "/api/v1/leave-requests/"+str(lr.data["id"])+"/approve", nil, "").code)

	// 07:00–19:00 local is 12 hours: 4 hours of overtime against an 8 hour day.
	for _, ev := range []struct{ typ, at string }{{"in", "2026-11-13T04:00:00Z"}, {"out", "2026-11-13T16:00:00Z"}} {
		r := o.call(t, http.MethodPost, "/api/v1/attendance/events", map[string]any{"employee_id": pID, "type": ev.typ, "at": ev.at, "reason": "Stock count"}, "")
		require.Equal(t, 201, r.code, r.raw)
	}

	rule := o.call(t, http.MethodPost, "/api/v1/commission-rules", map[string]any{"employee_id": pID, "calc": "percent_of_sales", "value": "5"}, "")
	require.Equal(t, 201, rule.code, rule.raw)
	prod := e.product(t, "Shoes", "10000", "10", "6000")
	sale := cashSale(t, peter, prod, "2", e.cashAccount(t), "20000")
	require.Equal(t, 201, sale.code, sale.raw)

	for _, a := range []map[string]any{
		{"employee_id": pID, "period": "2026-11-01", "kind": "bonus", "amount": "10000", "reason": "Target"},
		{"employee_id": pID, "period": "2026-11-01", "kind": "deduction", "amount": "3000", "reason": "Shortage"},
		{"employee_id": qID, "period": "2026-11-15", "kind": "deduction", "amount": "270000", "reason": "Damage"},
	} {
		r := o.call(t, http.MethodPost, "/api/v1/pay-adjustments", a, "")
		require.Equal(t, 201, r.code, r.raw)
	}

	cash := o.call(t, http.MethodPost, "/api/v1/money-accounts", map[string]any{"name": "Payroll cash", "type": "cash", "opening_balance": "3000000"}, "")
	require.Equal(t, 201, cash.code, cash.raw)
	cashID := str(cash.data["id"])
	loan := o.call(t, http.MethodPost, "/api/v1/loans", map[string]any{"employee_id": pID, "principal": "30000", "monthly_deduction": "30000"}, key())
	require.Equal(t, 201, loan.code, loan.raw)
	require.Equal(t, 200, o.call(t, http.MethodPost, "/api/v1/loans/"+str(loan.data["id"])+"/disburse", map[string]any{"money_account_id": cashID}, key()).code)
	queenLoan := o.call(t, http.MethodPost, "/api/v1/loans", map[string]any{"employee_id": qID, "principal": "50000", "monthly_deduction": "50000"}, key())
	require.Equal(t, 201, queenLoan.code, queenLoan.raw)

	run := o.call(t, http.MethodPost, "/api/v1/payroll-runs", map[string]any{"period_start": "2026-11-01", "period_end": "2026-11-30"}, "")
	require.Equal(t, 201, run.code, run.raw)
	require.Equal(t, "draft", str(run.data["status"]))
	runID := str(run.data["id"])
	calc := o.call(t, http.MethodPost, "/api/v1/payroll-runs/"+runID+"/calculate", nil, "")
	require.Equal(t, 200, calc.code, calc.raw)
	require.Equal(t, "draft", str(calc.data["status"]), "calculation leaves the run in draft")

	// Peter: base 500,000 after one unpaid day, housing 50,000, overtime 15,000, commission 1,000, bonus 10,000.
	// Deductions 3,000 and the full 30,000 loan. Queen: the 270,000 deduction is capped at her 260,000 pay, so the loan is not taken.
	peterSlip := slipOf(t, calc, pID)
	require.True(t, dec(peterSlip["base_pay"]).Equal(dec("500000")))
	require.True(t, dec(peterSlip["allowance"]).Equal(dec("50000")))
	require.True(t, dec(peterSlip["overtime"]).Equal(dec("15000")))
	require.True(t, dec(peterSlip["commission"]).Equal(dec("1000")))
	require.True(t, dec(peterSlip["bonuses"]).Equal(dec("10000")))
	require.True(t, dec(peterSlip["deductions"]).Equal(dec("3000")))
	require.True(t, dec(peterSlip["loan"]).Equal(dec("30000")))
	require.True(t, dec(peterSlip["gross"]).Equal(dec("576000")))
	require.True(t, dec(peterSlip["net"]).Equal(dec("543000")))
	queenSlip := slipOf(t, calc, qID)
	require.True(t, dec(queenSlip["gross"]).Equal(dec("260000")))
	require.True(t, dec(queenSlip["deductions"]).Equal(dec("260000")))
	require.True(t, dec(queenSlip["loan"]).Equal(dec("0")))
	require.True(t, dec(queenSlip["net"]).Equal(dec("0")))

	// The generator cannot approve their own run (require_sod_payroll).
	require.Equal(t, 403, o.call(t, http.MethodPost, "/api/v1/payroll-runs/"+runID+"/approve", nil, key()).code)
	approver := payrollApprover(t, e)
	approved := approver.call(t, http.MethodPost, "/api/v1/payroll-runs/"+runID+"/approve", nil, key())
	require.Equal(t, 200, approved.code, approved.raw)
	require.Equal(t, "approved", str(approved.data["status"]))

	paid := o.call(t, http.MethodPost, "/api/v1/payroll-runs/"+runID+"/pay", map[string]any{"money_account_id": cashID}, key())
	require.Equal(t, 200, paid.code, paid.raw)
	require.Equal(t, "paid", str(paid.data["status"]))
	settled := o.call(t, http.MethodGet, "/api/v1/loans/"+str(loan.data["id"]), nil, "")
	require.Equal(t, "settled", str(settled.data["status"]))
	require.True(t, dec(settled.data["balance"]).Equal(dec("0")))
	still := o.call(t, http.MethodGet, "/api/v1/loans/"+str(queenLoan.data["id"]), nil, "")
	require.Equal(t, "active", str(still.data["status"]))
	require.True(t, dec(still.data["balance"]).Equal(dec("50000")))

	require.Equal(t, 409, o.call(t, http.MethodPost, "/api/v1/payroll-runs/"+runID+"/reverse", map[string]any{"reason": "Too late"}, key()).code)

	mine := peter.call(t, http.MethodGet, "/api/v1/me/payslips", nil, "")
	require.Equal(t, 200, mine.code, mine.raw)
	require.NotEmpty(t, asList(mine.body["data"]))
}
