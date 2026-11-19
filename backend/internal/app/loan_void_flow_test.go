package app_test

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// Voiding a payment must put back what it settled: the loan deductions taken
// when a run is fully paid, and a direct cash repayment.
func TestFinanceVoidRestoresLoans(t *testing.T) {
	e := newEnv(t, defaultNow)
	o := e.owner
	workWeek(t, o)
	emp := employee(t, o, "Juma", "100000", nil)
	cash := o.call(t, http.MethodPost, "/api/v1/money-accounts", map[string]any{"name": "Pay " + uuid.NewString()[:6], "type": "cash", "opening_balance": "1000000"}, "")
	require.Equal(t, 201, cash.code, cash.raw)
	cashID := str(cash.data["id"])

	loan := o.call(t, http.MethodPost, "/api/v1/loans", map[string]any{"employee_id": emp, "principal": "20000", "monthly_deduction": "20000"}, key())
	require.Equal(t, 201, loan.code, loan.raw)
	loanID := str(loan.data["id"])
	require.Equal(t, 200, o.call(t, http.MethodPost, "/api/v1/loans/"+loanID+"/disburse", map[string]any{"money_account_id": cashID}, key()).code)

	// A full payroll run settles the loan.
	run := o.call(t, http.MethodPost, "/api/v1/payroll-runs", map[string]any{"period_start": "2026-11-01", "period_end": "2026-11-30"}, "")
	require.Equal(t, 201, run.code, run.raw)
	runID := str(run.data["id"])
	require.Equal(t, 200, o.call(t, http.MethodPost, "/api/v1/payroll-runs/"+runID+"/calculate", nil, "").code)
	require.Equal(t, 200, o.call(t, http.MethodPost, "/api/v1/payroll-runs/"+runID+"/approve", nil, key()).code)
	paid := o.call(t, http.MethodPost, "/api/v1/payroll-runs/"+runID+"/pay", map[string]any{"money_account_id": cashID, "payslip_ids": "all"}, key())
	require.Equal(t, 200, paid.code, paid.raw)
	require.Equal(t, "locked", str(paid.data["status"]))
	settled := o.call(t, http.MethodGet, "/api/v1/loans/"+loanID, nil, "")
	require.Equal(t, "settled", str(settled.data["status"]))
	require.Equal(t, "0.00", str(settled.data["balance"]))

	// Voiding the salary payment reopens the run and puts the deduction back on the loan.
	payments := o.call(t, http.MethodGet, "/api/v1/salary-payments?run_id="+runID, nil, "")
	require.Equal(t, 200, payments.code, payments.raw)
	rows := asList(payments.body["data"])
	require.Len(t, rows, 1)
	void := o.call(t, http.MethodPost, "/api/v1/payments/"+str(rows[0]["id"])+"/void", map[string]any{"reason": "Wrong account"}, key())
	require.Equal(t, 200, void.code, void.raw)
	back := o.call(t, http.MethodGet, "/api/v1/loans/"+loanID, nil, "")
	require.Equal(t, "20000.00", str(back.data["balance"]))
	require.Equal(t, "disbursed", str(back.data["status"]))
	reps := asList(back.data["repayments"])
	require.Len(t, reps, 1, "the repayment stays as history")
	require.NotNil(t, reps[0]["voided_at"])
	require.Equal(t, "approved", str(o.call(t, http.MethodGet, "/api/v1/payroll-runs/"+runID, nil, "").data["status"]))

	// Paying the run again settles the loan again, with a fresh repayment row.
	again := o.call(t, http.MethodPost, "/api/v1/payroll-runs/"+runID+"/pay", map[string]any{"money_account_id": cashID, "payslip_ids": "all"}, key())
	require.Equal(t, 200, again.code, again.raw)
	require.Equal(t, "settled", str(o.call(t, http.MethodGet, "/api/v1/loans/"+loanID, nil, "").data["status"]))

	// A direct cash repayment, then its void.
	loan2 := o.call(t, http.MethodPost, "/api/v1/loans", map[string]any{"employee_id": emp, "principal": "30000", "monthly_deduction": "10000"}, key())
	require.Equal(t, 201, loan2.code, loan2.raw)
	loan2ID := str(loan2.data["id"])
	require.Equal(t, 200, o.call(t, http.MethodPost, "/api/v1/loans/"+loan2ID+"/disburse", map[string]any{"money_account_id": cashID}, key()).code)
	repay := o.call(t, http.MethodPost, "/api/v1/loans/"+loan2ID+"/repay", map[string]any{"amount": "5000", "money_account_id": cashID}, key())
	require.Equal(t, 201, repay.code, repay.raw)
	require.Equal(t, "25000.00", str(o.call(t, http.MethodGet, "/api/v1/loans/"+loan2ID, nil, "").data["balance"]))
	payID := str(repay.data["payment_id"])
	require.NotEmpty(t, payID, repay.raw)
	require.Equal(t, 200, o.call(t, http.MethodPost, "/api/v1/payments/"+payID+"/void", map[string]any{"reason": "Counted twice"}, key()).code)
	after := o.call(t, http.MethodGet, "/api/v1/loans/"+loan2ID, nil, "")
	require.Equal(t, "30000.00", str(after.data["balance"]))
	require.NotNil(t, asList(after.data["repayments"])[0]["voided_at"])
	tb := o.call(t, http.MethodGet, "/api/v1/reports/trial-balance", nil, "")
	require.Equal(t, true, tb.data["balanced"])
}
