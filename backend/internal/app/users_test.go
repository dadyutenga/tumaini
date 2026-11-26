package app_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"bms/internal/app"
	"bms/internal/config"
	"bms/internal/platform/clock"
)

func TestListUsersIncludesBranches(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://bms_api:bms_api_dev@127.0.0.1:5433/bms?sslmode=disable")
	t.Setenv("JOBS_DATABASE_URL", "postgres://bms_jobs:bms_jobs_dev@127.0.0.1:5433/bms?sslmode=disable")
	t.Setenv("JWT_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("ARGON_MEMORY", "8192")
	t.Setenv("ARGON_TIME", "1")
	t.Setenv("ARGON_THREADS", "1")
	t.Setenv("STORAGE_PATH", t.TempDir())
	cfg, err := config.Load()
	require.NoError(t, err)
	application, err := app.New(context.Background(), cfg, clock.System{})
	require.NoError(t, err)
	t.Cleanup(application.Close)
	c := &client{app: application.Fiber}
	reg := c.call(t, http.MethodPost, "/api/v1/auth/register", map[string]any{
		"business_name": "Team regression", "owner_name": "Team Owner", "phone": uniquePhone(), "password": "correct-horse",
	}, "")
	require.Equal(t, 201, reg.code, reg.raw)
	c.token, c.branch = str(reg.data["access_token"]), str(reg.data["branch_id"])
	users := c.call(t, http.MethodGet, "/api/v1/users?page=1&per_page=25", nil, "")
	require.Equal(t, 200, users.code, users.raw)
	rows := asList(users.body["data"])
	require.Len(t, rows, 1)
	require.Equal(t, []any{c.branch}, rows[0]["branch_ids"], "user list must include assigned branches")
}

func TestUserPermissionOverrides(t *testing.T) {
	e := newEnv(t, defaultNow)
	staff, staffID := e.user(t, "staff")

	// Initially staff cannot view audit log, can create sales.
	me0 := staff.call(t, http.MethodGet, "/api/v1/me", nil, "")
	require.Equal(t, 200, me0.code, me0.raw)
	perms0 := me0.data["permissions"].(map[string]any)
	_, hasAudit0 := perms0["security.audit.view"]
	require.False(t, hasAudit0)

	// Grant security.audit.view and revoke shop.sale.create for this specific user.
	put := e.owner.call(t, http.MethodPut, "/api/v1/users/"+staffID+"/permissions", map[string]any{
		"overrides": []map[string]any{
			{"permission_key": "security.audit.view", "allowed": true, "scope": "all"},
			{"permission_key": "shop.sale.create", "allowed": false, "scope": "all"},
		},
	}, "")
	require.Equal(t, 200, put.code, put.raw)

	me1 := staff.call(t, http.MethodGet, "/api/v1/me", nil, "")
	require.Equal(t, 200, me1.code, me1.raw)
	perms1 := me1.data["permissions"].(map[string]any)
	_, hasAudit1 := perms1["security.audit.view"]
	_, hasSales1 := perms1["shop.sale.create"]
	require.True(t, hasAudit1, "override grant should apply")
	require.False(t, hasSales1, "override revoke should apply")

	// Get endpoint returns both role_permissions and overrides.
	got := e.owner.call(t, http.MethodGet, "/api/v1/users/"+staffID+"/permissions", nil, "")
	require.Equal(t, 200, got.code, got.raw)
	require.Len(t, asList(got.data["overrides"]), 2)
}
