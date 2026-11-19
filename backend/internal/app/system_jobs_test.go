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

	// An approved expense that nobody has paid yet, and a sale so the summary has figures.
	cats := o.call(t, http.MethodGet, "/api/v1/expense-categories", nil, "")
	exp := o.call(t, http.MethodPost, "/api/v1/expenses", map[string]any{"category_id": asList(cats.body["data"])[0]["id"], "amount": "5000", "description": "Cleaning"}, key())
	require.Equal(t, 201, exp.code, exp.raw)

	require.NoError(t, e.app.Jobs.Schedule(ctx))
	var queued int
	require.NoError(t, e.app.DB.Pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE kind IN ('debt_reminders','owner_daily_summary','ledger_balance_check','stock_rebuild_check','session_cleanup')`).Scan(&queued))
	require.GreaterOrEqual(t, queued, 5)

	// Scheduling again the same day adds nothing: the marks make it idempotent.
	require.NoError(t, e.app.Jobs.Schedule(ctx))
	var again int
	require.NoError(t, e.app.DB.Pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE kind IN ('debt_reminders','owner_daily_summary','ledger_balance_check','stock_rebuild_check','session_cleanup')`).Scan(&again))
	require.Equal(t, queued, again)

	e.runJobs(t)
	got := titles(t, o)
	require.Contains(t, got, "Daily summary")
	require.Contains(t, got, "Approved expenses unpaid")
	require.NotContains(t, got, "Ledger check failed", "a healthy ledger raises no alert")
	require.NotContains(t, got, "Stock check failed")

	var failed int
	require.NoError(t, e.app.DB.Pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE status='failed' OR last_error IS NOT NULL`).Scan(&failed))
	require.Zero(t, failed)

	// Another business gets its own jobs and sees none of this business's alerts.
	other := newEnv(t, defaultNow)
	require.NoError(t, other.app.Jobs.Schedule(ctx))
	other.runJobs(t)
	require.NotContains(t, titles(t, other.owner), "Approved expenses unpaid")
}
