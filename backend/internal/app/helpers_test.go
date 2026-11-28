package app_test

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"bms/internal/app"
	"bms/internal/config"
	"bms/internal/platform/clock"
)

// env is one registered business on a fresh app instance, for flow tests.
type env struct {
	app    *app.App
	owner  *client
	branch string
	phone  string
}

// defaultNow is a Monday morning in Dar es Salaam (05:20 UTC is 08:20 local).
var defaultNow = time.Date(2026, 11, 16, 5, 20, 0, 0, time.UTC)

// stepClock is a clock tests can move forward.
type stepClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *stepClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *stepClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// newEnv starts the app with a fixed clock and registers a business with its Owner.
func newEnv(t *testing.T, now time.Time) *env {
	t.Helper()
	return newEnvWithClock(t, clock.Fixed{T: now})
}

// newApp starts the app on a database of its own, before anyone has set up the business.
func newApp(t *testing.T, clk clock.Clock) *app.App {
	t.Helper()
	freshDatabase(t)
	t.Setenv("JWT_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("RATE_LIMIT_SCOPE", uuid.NewString()) // each test business has its own shared budget
	t.Setenv("ARGON_MEMORY", "8192")
	t.Setenv("ARGON_TIME", "1")
	t.Setenv("ARGON_THREADS", "1")
	t.Setenv("COOKIE_SECURE", "false")
	t.Setenv("STORAGE_PATH", t.TempDir())
	cfg, err := config.Load()
	require.NoError(t, err)
	a, err := app.New(context.Background(), cfg, clk)
	require.NoError(t, err)
	t.Cleanup(a.Close)
	return a
}

// newEnvWithClock is newEnv with any clock, for tests that move time.
func newEnvWithClock(t *testing.T, clk clock.Clock) *env {
	t.Helper()
	a := newApp(t, clk)
	c := &client{app: a.Fiber}
	phone := uniquePhone()
	reg := c.call(t, http.MethodPost, "/api/v1/setup", map[string]any{
		"business_name": "Flow Shop " + phone, "owner_name": "Owner", "phone": phone,
		"email": "owner+" + phone + "@demo.test", "password": "correct-horse",
	}, "")
	require.Equal(t, 201, reg.code, reg.raw)
	c.token, c.branch = str(reg.data["access_token"]), str(reg.data["branch_id"])
	return &env{app: a, owner: c, branch: c.branch, phone: phone}
}

// roleID returns the id of a role by slug: owner, manager or staff.
func (e *env) roleID(t *testing.T, slug string) string {
	t.Helper()
	res := e.owner.call(t, http.MethodGet, "/api/v1/roles", nil, "")
	require.Equal(t, 200, res.code, res.raw)
	id := str(find(asList(res.body["data"]), "slug", slug)["id"])
	require.NotEmpty(t, id, "role %s", slug)
	return id
}

// user creates a user with a role in the given branches (default: main branch)
// and returns a logged-in client with that branch active.
func (e *env) user(t *testing.T, slug string, branches ...string) (*client, string) {
	t.Helper()
	if len(branches) == 0 {
		branches = []string{e.branch}
	}
	phone := uniquePhone()
	res := e.owner.call(t, http.MethodPost, "/api/v1/users", map[string]any{
		"name": slug + " user", "phone": phone, "password": slug + "-pass-123", "role_id": e.roleID(t, slug), "branch_ids": branches,
	}, "")
	require.Equal(t, 201, res.code, res.raw)
	return &client{app: e.app.Fiber, token: login(t, e.app.Fiber, phone, slug+"-pass-123"), branch: branches[0]}, str(res.data["id"])
}

// cashAccount returns the id of the first cash money account.
func (e *env) cashAccount(t *testing.T) string {
	t.Helper()
	res := e.owner.call(t, http.MethodGet, "/api/v1/money-accounts?type=cash", nil, "")
	require.Equal(t, 200, res.code, res.raw)
	rows := asList(res.body["data"])
	require.NotEmpty(t, rows)
	return str(rows[0]["id"])
}

// product creates a stock product and receives qty at cost into the active branch.
func (e *env) product(t *testing.T, name, price, qty, cost string) string {
	t.Helper()
	p := e.owner.call(t, http.MethodPost, "/api/v1/products", map[string]any{"name": name, "sell_price": price}, "")
	require.Equal(t, 201, p.code, p.raw)
	id := str(p.data["id"])
	if qty != "" {
		in := e.owner.call(t, http.MethodPost, "/api/v1/stock-entries", map[string]any{
			"direction": "in", "reason": "opening", "items": []map[string]any{{"product_id": id, "qty": qty, "unit_cost": cost}},
		}, key())
		require.Equal(t, 201, in.code, in.raw)
	}
	return id
}

// runJobs processes every queued background job synchronously.
func (e *env) runJobs(t *testing.T) {
	t.Helper()
	require.NoError(t, e.app.Jobs.RunPending(context.Background()))
}

// key returns a fresh idempotency key.
func key() string { return uuid.NewString() }
