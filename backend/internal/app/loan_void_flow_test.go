package app_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// Paying a payroll run applies the scheduled advance deduction. A paid run cannot be
// reopened, so the deduction stays. A direct cash repayment reduces the balance itself
// and is idempotent.
func TestFinanceVoidRestoresLoans(t *testing.T) {
	e := newEnv(t, defaultNow)
	o := e.owner
	workWeek(t, o)
	emp := employee(t, o, "Juma", "100000", nil)
	approver := payrollApprover(t, e)
	cash := o.call(t, http.MethodPost, "/api/v1/money-accounts", map[string]any{"name": "Pay cash", "type": "cash", "opening_balance": "1000000"}, "")
	require.Equal(t, 201, cash.code, cash.raw)
	cashID := str(cash.data["id"])

	loan := o.call(t, http.MethodPost, "/api/v1/loans", map[string]any{"employee_id": emp, "principal": "20000", "monthly_deduction": "20000"}, key())
	require.Equal(t, 201, loan.code, loan.raw)
	require.Equal(t, "active", str(loan.data["status"]))
	loanID := str(loan.data["id"])
	disb := o.call(t, http.MethodPost, "/api/v1/loans/"+loanID+"/disburse", map[string]any{"money_account_id": cashID}, key())
	require.Equal(t, 200, disb.code, disb.raw)
	require.NotEmpty(t, str(disb.data["payment_id"]))

	run := o.call(t, http.MethodPost, "/api/v1/payroll-runs", map[string]any{"period_start": "2026-11-01", "period_end": "2026-11-30"}, "")
	require.Equal(t, 201, run.code, run.raw)
	runID := str(run.data["id"])
	require.Equal(t, 200, o.call(t, http.MethodPost, "/api/v1/payroll-runs/"+runID+"/calculate", nil, "").code)
	require.Equal(t, 200, approver.call(t, http.MethodPost, "/api/v1/payroll-runs/"+runID+"/approve", nil, key()).code)
	paid := o.call(t, http.MethodPost, "/api/v1/payroll-runs/"+runID+"/pay", map[string]any{"money_account_id": cashID}, key())
	require.Equal(t, 200, paid.code, paid.raw)
	require.Equal(t, "paid", str(paid.data["status"]))
	settled := o.call(t, http.MethodGet, "/api/v1/loans/"+loanID, nil, "")
	require.Equal(t, "settled", str(settled.data["status"]))
	require.True(t, dec(settled.data["balance"]).Equal(dec("0")))
	require.Len(t, asList(settled.data["repayments"]), 1)
	require.Equal(t, 409, o.call(t, http.MethodPost, "/api/v1/payroll-runs/"+runID+"/reverse", map[string]any{"reason": "Wrong account"}, key()).code)
	require.Equal(t, "settled", str(o.call(t, http.MethodGet, "/api/v1/loans/"+loanID, nil, "").data["status"]))

	loan2 := o.call(t, http.MethodPost, "/api/v1/loans", map[string]any{"employee_id": emp, "principal": "30000", "monthly_deduction": "10000"}, key())
	require.Equal(t, 201, loan2.code, loan2.raw)
	loan2ID := str(loan2.data["id"])
	require.Equal(t, 200, o.call(t, http.MethodPost, "/api/v1/loans/"+loan2ID+"/disburse", map[string]any{"money_account_id": cashID}, key()).code)
	repayKey := key()
	repay := o.call(t, http.MethodPost, "/api/v1/loans/"+loan2ID+"/repay", map[string]any{"amount": "5000", "money_account_id": cashID}, repayKey)
	require.Equal(t, 201, repay.code, repay.raw)
	require.True(t, dec(repay.data["balance"]).Equal(dec("25000")))
	again := o.call(t, http.MethodPost, "/api/v1/loans/"+loan2ID+"/repay", map[string]any{"amount": "5000", "money_account_id": cashID}, repayKey)
	require.Equal(t, 201, again.code, again.raw)
	require.True(t, dec(again.data["balance"]).Equal(dec("25000")), "the same idempotency key does not repay twice")
	require.Equal(t, 409, o.call(t, http.MethodPost, "/api/v1/loans/"+loan2ID+"/repay", map[string]any{"amount": "30000", "money_account_id": cashID}, key()).code)
}
