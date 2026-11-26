package app_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func titles(t *testing.T, c *client) []string {
	t.Helper()
	res := c.call(t, http.MethodGet, "/api/v1/notifications?per_page=100", nil, "")
	require.Equal(t, 200, res.code, res.raw)
	var out []string
	for _, n := range asList(res.body["data"]) {
		out = append(out, str(n["title"]))
	}
	return out
}

func TestScheduledSystemJobs(t *testing.T) {
	e := newEnv(t, defaultNow) // 08:20 in Dar es Salaam: the 08:00 and day-end jobs are due
	o := e.owner
	ctx := context.Background()

	// An approved expense that nobody has paid yet. The submitter and the approver must differ.
	staff, _ := e.user(t, "staff")
	cats := o.call(t, http.MethodGet, "/api/v1/expense-categories", nil, "")
	exp := staff.call(t, http.MethodPost, "/api/v1/expenses", map[string]any{"category_id": asList(cats.body["data"])[0]["id"], "amount": "5000", "description": "Cleaning"}, key())
	require.Equal(t, 201, exp.code, exp.raw)
	require.Equal(t, 200, o.call(t, http.MethodPost, "/api/v1/expenses/"+str(exp.data["id"])+"/approve", map[string]any{"note": "ok"}, "").code)

	require.NoError(t, e.app.Jobs.Schedule(ctx))
	jobCount := func() int {
		t.Helper()
		var n int
		require.NoError(t, e.app.DB.Jobs.QueryRow(ctx, `SELECT count(*) FROM app.job_queue WHERE tenant_id = $1 AND kind IN ('debt_reminders','owner_daily_summary','ledger_balance_check','stock_rebuild_check','session_cleanup')`, e.tenantID).Scan(&n))
		return n
	}
	queued := jobCount()
	require.GreaterOrEqual(t, queued, 5)

	// Scheduling again the same day adds nothing: the marks make it idempotent.
	require.NoError(t, e.app.Jobs.Schedule(ctx))
	require.Equal(t, queued, jobCount())

	e.runJobs(t)
	got := titles(t, o)
	require.Contains(t, got, "Daily summary")
	require.Contains(t, got, "Approved expenses unpaid")
	require.NotContains(t, got, "Ledger check failed", "a healthy ledger raises no alert")
	require.NotContains(t, got, "Stock check failed")

	var failed int
	require.NoError(t, e.app.DB.Jobs.QueryRow(ctx, `SELECT count(*) FROM app.job_queue WHERE tenant_id = $1 AND (status='failed' OR last_error IS NOT NULL)`, e.tenantID).Scan(&failed))
	require.Zero(t, failed)

	// Another business gets its own jobs and sees none of this business's alerts.
	other := newEnv(t, defaultNow)
	require.NoError(t, other.app.Jobs.Schedule(ctx))
	other.runJobs(t)
	require.NotContains(t, titles(t, other.owner), "Approved expenses unpaid")
}
