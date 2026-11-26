package app_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// Above 200 employees a calculation is queued as a job and answers 202 (API reference, payroll).
func TestPayrollLargeRunCalculatesInBackground(t *testing.T) {
	if testing.Short() {
		t.Skip("creates 201 employees")
	}
	e := newEnv(t, defaultNow)
	o := e.owner
	for i := 0; i < 201; i++ {
		employee(t, o, "Staff "+uuidShort(i), "10000", nil)
	}
	run := o.call(t, http.MethodPost, "/api/v1/payroll-runs", map[string]any{"period_start": "2026-11-01", "period_end": "2026-11-30"}, "")
	require.Equal(t, 201, run.code, run.raw)
	runID := str(run.data["id"])

	calc := o.call(t, http.MethodPost, "/api/v1/payroll-runs/"+runID+"/calculate", nil, "")
	require.Equal(t, 202, calc.code, calc.raw)
	before := o.call(t, http.MethodGet, "/api/v1/payroll-runs/"+runID, nil, "")
	require.Equal(t, "draft", str(before.data["status"]), "nothing happens until the job runs")
	require.Empty(t, asList(before.data["payslips"]))

	e.runJobs(t)
	after := o.call(t, http.MethodGet, "/api/v1/payroll-runs/"+runID, nil, "")
	require.Equal(t, 200, after.code, after.raw)
	require.Equal(t, "draft", str(after.data["status"]), "a calculated run stays draft until it is approved")
	require.Len(t, asList(after.data["payslips"]), 201)
	require.True(t, dec(after.data["gross_total"]).Equal(dec("2010000")))
}

func uuidShort(i int) string {
	const digits = "0123456789"
	if i == 0 {
		return "0"
	}
	out := ""
	for i > 0 {
		out = string(digits[i%10]) + out
		i /= 10
	}
	return out
}
