package app_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func slipOf(t *testing.T, run result, employeeID string) map[string]any {
	t.Helper()
	s := find(asList(run.data["payslips"]), "employee_id", employeeID)
	require.NotEmpty(t, s, "payslip for %s in %s", employeeID, run.raw)
	return s
}

func TestPayrollFlow(t *testing.T) {
	e := newEnv(t, defaultNow)
	o := e.owner
	workWeek(t, o)

	// Peter earns 520,000 (daily 20,000, hourly 2,500); Rehema 260,000; Queen 260,000.
	pID := employee(t, o, "Peter", "520000", nil)
	qID := employee(t, o, "Queen", "260000", nil)
	rID := employee(t, o, "Rehema", "260000", nil)
	peter := e.employeeLogin(t, pID, "staff")
	rehema := e.employeeLogin(t, rID, "staff")

	// Components: a PAYE bracket table, a housing allowance and an employer
	// contribution for everyone; a union deduction for Peter only.
	paye := o.call(t, http.MethodPost, "/api/v1/salary-components", map[string]any{
		"name": "PAYE", "kind": "deduction", "calc": "bracket", "is_statutory": true,
		"brackets": []map[string]any{
			{"from": "0", "to": "270000", "rate": "0", "fixed": "0"},
			{"from": "270000", "to": "520000", "rate": "8", "fixed": "0"},
			{"from": "520000", "to": nil, "rate": "20", "fixed": "20000"},
		},
	}, "")
	require.Equal(t, 201, paye.code, paye.raw)
	bad := o.call(t, http.MethodPost, "/api/v1/salary-components", map[string]any{"name": "Bad", "kind": "deduction", "calc": "bracket"}, "")
	require.Equal(t, 400, bad.code, bad.raw)
	housing := o.call(t, http.MethodPost, "/api/v1/salary-components", map[string]any{"name": "Housing", "kind": "allowance", "calc": "percent_of_base", "value": "10"}, "")
	require.Equal(t, 201, housing.code, housing.raw)
	ssf := o.call(t, http.MethodPost, "/api/v1/salary-components", map[string]any{"name": "Employer SSF", "kind": "employer_contribution", "calc": "percent_of_gross", "value": "10"}, "")
	require.Equal(t, 201, ssf.code, ssf.raw)
	union := o.call(t, http.MethodPost, "/api/v1/salary-components", map[string]any{"name": "Union", "kind": "deduction", "calc": "fixed", "value": "5000", "applies_to_all": false}, "")
	require.Equal(t, 201, union.code, union.raw)
	comps := o.call(t, http.MethodPut, "/api/v1/employees/"+pID+"/components", map[string]any{"components": []map[string]any{{"component_id": union.data["id"], "value": "4000"}}}, "")
	require.Equal(t, 200, comps.code, comps.raw)
	require.Equal(t, "4000.0000", str(find(asList(comps.body["data"]), "name", "Union")["value"]))
	list := o.call(t, http.MethodGet, "/api/v1/salary-components", nil, "")
	require.Len(t, asList(list.body["data"]), 4)

	// One unpaid leave day for Peter (Wednesday the 18th).
	types := o.call(t, http.MethodGet, "/api/v1/leave-types", nil, "")
	unpaid := find(asList(types.body["data"]), "name", "Unpaid")
	lr := peter.call(t, http.MethodPost, "/api/v1/leave-requests", map[string]any{"leave_type_id": unpaid["id"], "start_date": "2026-11-18", "end_date": "2026-11-18"}, "")
	require.Equal(t, 201, lr.code, lr.raw)
	require.Equal(t, 200, o.call(t, http.MethodPost, "/api/v1/leave-requests/"+str(lr.data["id"])+"/approve", nil, "").code)

	// Overtime: 3 hours on the 13th approved, 3 hours on the 12th left pending.
	for _, day := range []string{"2026-11-12", "2026-11-13"} {
		for _, ev := range []struct{ typ, at string }{{"in", day + "T04:00:00Z"}, {"out", day + "T16:00:00Z"}} {
			r := o.call(t, http.MethodPost, "/api/v1/attendance/events", map[string]any{"employee_id": pID, "type": ev.typ, "at": ev.at, "reason": "Stock count"}, "")
			require.Equal(t, 201, r.code, r.raw)
		}
	}
	ot := o.call(t, http.MethodGet, "/api/v1/attendance/overtime?employee_id="+pID+"&from=2026-11-13&to=2026-11-13", nil, "")
	require.Len(t, asList(ot.body["data"]), 1)
	require.Equal(t, 200, o.call(t, http.MethodPost, "/api/v1/attendance/overtime/"+str(asList(ot.body["data"])[0]["id"])+"/approve", nil, "").code)

	// Commission: 5% of Peter's own sales. He sells 2 x 10,000 for cash.
	rule := o.call(t, http.MethodPost, "/api/v1/commission-rules", map[string]any{"employee_id": pID, "calc": "percent_of_sales", "value": "5"}, "")
	require.Equal(t, 201, rule.code, rule.raw)
	prod := e.product(t, "Shoes", "10000", "10", "6000")
	till := peter.call(t, http.MethodPost, "/api/v1/tills/open", map[string]any{"money_account_id": e.cashAccount(t), "opening_float": "0"}, "")
	require.Equal(t, 201, till.code, till.raw)
	sale := peter.call(t, http.MethodPost, "/api/v1/sales", map[string]any{
		"items":    []map[string]any{{"product_id": prod, "qty": "2"}},
		"payments": []map[string]any{{"method": "cash", "money_account_id": e.cashAccount(t), "amount": "20000"}},
	}, key())
	require.Equal(t, 201, sale.code, sale.raw)

	// One-off bonus and deduction for Peter; a deduction bigger than Queen's pay.
	for _, a := range []map[string]any{
		{"employee_id": pID, "period": "2026-11-01", "kind": "bonus", "amount": "10000", "reason": "Target"},
		{"employee_id": pID, "period": "2026-11-01", "kind": "deduction", "amount": "3000", "reason": "Shortage"},
		{"employee_id": qID, "period": "2026-11-15", "kind": "deduction", "amount": "270000", "reason": "Damage"},
	} {
		r := o.call(t, http.MethodPost, "/api/v1/pay-adjustments", a, "")
		require.Equal(t, 201, r.code, r.raw)
	}

	// Loans: Peter 30,000 repaid at 30,000; Queen 50,000 that her pay cannot cover.
	cash := o.call(t, http.MethodPost, "/api/v1/money-accounts", map[string]any{"name": "Payroll " + uuid.NewString()[:6], "type": "cash", "opening_balance": "3000000"}, "")
	require.Equal(t, 201, cash.code, cash.raw)
	cashID := str(cash.data["id"])
	loans := map[string]string{}
	for emp, amt := range map[string]string{pID: "30000", qID: "50000"} {
		l := o.call(t, http.MethodPost, "/api/v1/loans", map[string]any{"employee_id": emp, "principal": amt, "monthly_deduction": amt}, key())
		require.Equal(t, 201, l.code, l.raw)
		d := o.call(t, http.MethodPost, "/api/v1/loans/"+str(l.data["id"])+"/disburse", map[string]any{"money_account_id": cashID}, key())
		require.Equal(t, 200, d.code, d.raw)
		loans[emp] = str(l.data["id"])
	}

	// Run November and calculate.
	mgr, _ := e.user(t, "manager")
	require.Equal(t, 403, mgr.call(t, http.MethodGet, "/api/v1/payroll-runs", nil, "").code)
	run := o.call(t, http.MethodPost, "/api/v1/payroll-runs", map[string]any{"period_start": "2026-11-01", "period_end": "2026-11-30"}, "")
	require.Equal(t, 201, run.code, run.raw)
	runID := str(run.data["id"])
	require.Equal(t, 409, o.call(t, http.MethodPost, "/api/v1/payroll-runs", map[string]any{"period_start": "2026-11-15", "period_end": "2026-12-14"}, "").code, "overlapping run")
	calc := o.call(t, http.MethodPost, "/api/v1/payroll-runs/"+runID+"/calculate", nil, "")
	require.Equal(t, 200, calc.code, calc.raw)

	// Peter: absence 20,000 -> prorated 500,000; housing 50,000; overtime
	// 3h x 2,500 x 1.5 = 11,250 (the pending 3h do not count); commission
	// 1,000; bonus 10,000. Gross 572,250. PAYE 20,000 + 20% x 52,250 = 30,450;
	// union 4,000; shortage 3,000; loan 30,000. Net 504,800. Employer 57,225.
	ps := slipOf(t, calc, pID)
	require.Equal(t, "572250.00", str(ps["gross"]), calc.raw)
	require.Equal(t, "67450.00", str(ps["total_deductions"]))
	require.Equal(t, "504800.00", str(ps["net"]))
	require.Equal(t, "20000.00", str(ps["absence"]))
	require.Equal(t, "57225.00", str(ps["employer"]))
	require.Equal(t, "3.00", str(ps["overtime_hours"]))
	require.Equal(t, "1.0", str(ps["unpaid_leave_days"]))
	// Queen: gross 286,000; PAYE 1,280; the damage deduction is capped and the
	// loan repayment takes only what is left, so net is zero, never negative.
	qs := slipOf(t, calc, qID)
	require.Equal(t, "286000.00", str(qs["gross"]))
	require.Equal(t, "0.00", str(qs["net"]))
	require.Equal(t, "14720.00", str(qs["loan"]))
	rs := slipOf(t, calc, rID)
	require.Equal(t, "284720.00", str(rs["net"]))

	// Manual lines before approval need a reason and change the net.
	adjusted := o.call(t, http.MethodPut, "/api/v1/payroll-runs/"+runID+"/payslips/"+str(ps["id"])+"/lines", map[string]any{
		"lines": []map[string]any{{"kind": "bonus", "label": "Extra shift", "amount": "500"}}, "reason": "Covered a shift",
	}, "")
	require.Equal(t, 200, adjusted.code, adjusted.raw)
	require.Equal(t, "505300.00", str(adjusted.data["net"]))
	cleared := o.call(t, http.MethodPut, "/api/v1/payroll-runs/"+runID+"/payslips/"+str(ps["id"])+"/lines", map[string]any{"lines": []any{}, "reason": "Paid separately"}, "")
	require.Equal(t, "504800.00", str(cleared.data["net"]))
	require.Equal(t, 422, o.call(t, http.MethodPut, "/api/v1/payroll-runs/"+runID+"/payslips/"+str(ps["id"])+"/lines", map[string]any{"lines": []any{}}, "").code)

	// Submit then approve: the journal balances and attendance locks.
	sub := o.call(t, http.MethodPost, "/api/v1/payroll-runs/"+runID+"/submit", nil, "")
	require.Equal(t, 202, sub.code, sub.raw)
	require.Equal(t, 409, o.call(t, http.MethodPost, "/api/v1/payroll-runs/"+runID+"/calculate", nil, "").code)
	ap := o.call(t, http.MethodPost, "/api/v1/payroll-runs/"+runID+"/approve", nil, key())
	require.Equal(t, 200, ap.code, ap.raw)
	require.Equal(t, "approved", str(ap.data["status"]))
	tb := o.call(t, http.MethodGet, "/api/v1/reports/trial-balance", nil, "")
	require.Equal(t, true, tb.data["balanced"], tb.raw)
	pending := o.call(t, http.MethodGet, "/api/v1/approvals?status=pending", nil, "")
	require.Empty(t, find(asList(pending.body["data"]), "subject_id", runID), "approving closes the request")
	day13 := asList(o.call(t, http.MethodGet, "/api/v1/attendance/days?employee_id="+pID+"&from=2026-11-13&to=2026-11-13", nil, "").body["data"])
	require.Equal(t, true, day13[0]["is_locked"])
	require.Equal(t, 409, o.call(t, http.MethodPut, "/api/v1/attendance/days/"+str(day13[0]["id"]), map[string]any{"status": "absent", "reason": "late fix"}, "").code)
	require.Equal(t, 409, o.call(t, http.MethodPost, "/api/v1/pay-adjustments", map[string]any{"employee_id": pID, "period": "2026-11-20", "kind": "bonus", "amount": "1", "reason": "x"}, "").code)

	// Self-service payslip: own only, with PDF.
	mine := peter.call(t, http.MethodGet, "/api/v1/payslips/"+str(ps["id"]), nil, "")
	require.Equal(t, 200, mine.code, mine.raw)
	loanLine := find(asList(mine.data["lines"]), "kind", "loan")
	require.Equal(t, "0.00", str(loanLine["loan_balance_after"]))
	require.Equal(t, 404, peter.call(t, http.MethodGet, "/api/v1/payslips/"+str(rs["id"]), nil, "").code)
	pdfRes := peter.call(t, http.MethodGet, "/api/v1/payslips/"+str(ps["id"])+"?format=pdf", nil, "")
	require.Equal(t, 200, pdfRes.code)
	require.True(t, strings.HasPrefix(pdfRes.raw, "%PDF"))
	require.Len(t, asList(peter.call(t, http.MethodGet, "/api/v1/me/payslips", nil, "").body["data"]), 1)

	csv := o.call(t, http.MethodGet, "/api/v1/payroll-runs/"+runID+"/payment-list?format=csv", nil, "")
	require.Equal(t, 200, csv.code, csv.raw)
	require.Contains(t, csv.raw, "Peter")

	// Partial payment, then the rest: the run locks and loan balances fall.
	part := o.call(t, http.MethodPost, "/api/v1/payroll-runs/"+runID+"/pay", map[string]any{"money_account_id": cashID, "payslip_ids": []string{str(ps["id"])}}, key())
	require.Equal(t, 200, part.code, part.raw)
	require.Equal(t, "approved", str(part.data["status"]))
	require.Equal(t, "1", str(part.data["unpaid_payslips"]))
	require.Equal(t, 409, o.call(t, http.MethodPost, "/api/v1/payroll-runs/"+runID+"/reverse", map[string]any{"reason": "Mistake"}, key()).code, "a run with payments cannot be reversed")
	loanBefore := o.call(t, http.MethodGet, "/api/v1/loans/"+loans[pID], nil, "")
	require.Equal(t, "30000.00", str(loanBefore.data["balance"]), "loans fall only when the run is fully paid")
	rest := o.call(t, http.MethodPost, "/api/v1/payroll-runs/"+runID+"/pay", map[string]any{"money_account_id": cashID, "payslip_ids": "all"}, key())
	require.Equal(t, 200, rest.code, rest.raw)
	require.Equal(t, "locked", str(rest.data["status"]))
	require.Equal(t, "settled", str(o.call(t, http.MethodGet, "/api/v1/loans/"+loans[pID], nil, "").data["status"]))
	require.Equal(t, "35280.00", str(o.call(t, http.MethodGet, "/api/v1/loans/"+loans[qID], nil, "").data["balance"]))
	paid := o.call(t, http.MethodGet, "/api/v1/salary-payments?run_id="+runID, nil, "")
	require.Equal(t, 200, paid.code, paid.raw)
	require.Len(t, asList(paid.body["data"]), 2, "Queen's zero net needs no payment")
	myPay := rehema.call(t, http.MethodGet, "/api/v1/me/payments", nil, "")
	require.Len(t, asList(myPay.body["data"]), 1)
	require.Equal(t, "284720.00", str(asList(myPay.body["data"])[0]["amount"]))

	// December: approve with no payments, then reverse; attendance unlocks.
	dec := o.call(t, http.MethodPost, "/api/v1/payroll-runs", map[string]any{"period_start": "2026-12-01", "period_end": "2026-12-31"}, "")
	require.Equal(t, 201, dec.code, dec.raw)
	decID := str(dec.data["id"])
	require.Equal(t, 200, o.call(t, http.MethodPost, "/api/v1/payroll-runs/"+decID+"/calculate", nil, "").code)
	require.Equal(t, 200, o.call(t, http.MethodPost, "/api/v1/payroll-runs/"+decID+"/approve", nil, key()).code)
	rev := o.call(t, http.MethodPost, "/api/v1/payroll-runs/"+decID+"/reverse", map[string]any{"reason": "Wrong period"}, key())
	require.Equal(t, 200, rev.code, rev.raw)
	require.Equal(t, "reversed", str(rev.data["status"]))
	require.Equal(t, 409, o.call(t, http.MethodPost, "/api/v1/payroll-runs/"+decID+"/reverse", map[string]any{"reason": "Again"}, key()).code)
	tb = o.call(t, http.MethodGet, "/api/v1/reports/trial-balance", nil, "")
	require.Equal(t, true, tb.data["balanced"], tb.raw)
	again := o.call(t, http.MethodPost, "/api/v1/payroll-runs", map[string]any{"period_start": "2026-12-01", "period_end": "2026-12-31"}, "")
	require.Equal(t, 201, again.code, "a reversed run frees its period: %s", again.raw)

	runs := o.call(t, http.MethodGet, "/api/v1/payroll-runs?status=locked", nil, "")
	require.Len(t, asList(runs.body["data"]), 1)
	detail := o.call(t, http.MethodGet, "/api/v1/payroll-runs/"+runID, nil, "")
	require.Equal(t, 200, detail.code, detail.raw)
	require.Len(t, asList(detail.data["payslips"]), 3)
}
