package app_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSensitiveAccountChangesRequireReauthentication(t *testing.T) {
	clk := &stepClock{t: defaultNow}
	e := newEnvWithClock(t, clk)
	_, staffID := e.user(t, "staff")

	// Keep the session active while advancing past the 10-minute re-auth window.
	clk.Advance(6 * time.Minute)
	require.Equal(t, 200, e.owner.call(t, http.MethodGet, "/api/v1/me", nil, "").code)
	clk.Advance(6 * time.Minute)
	require.Equal(t, 200, e.owner.call(t, http.MethodGet, "/api/v1/me", nil, "").code, "session is still active")

	// Sensitive account/privilege mutations now require fresh re-authentication.
	resReset := e.owner.call(t, http.MethodPost, "/api/v1/users/"+staffID+"/reset-password", map[string]any{"password": "fresh-temp-pass-1"}, "")
	require.Equal(t, 403, resReset.code, resReset.raw)
	require.Equal(t, "reauth_required", errCode(resReset.body))

	resDeact := e.owner.call(t, http.MethodPost, "/api/v1/users/"+staffID+"/deactivate", nil, "")
	require.Equal(t, 403, resDeact.code, resDeact.raw)
	require.Equal(t, "reauth_required", errCode(resDeact.body))

	resRole := e.owner.call(t, http.MethodPost, "/api/v1/roles", map[string]any{"name": "Reauth Test Role"}, "")
	require.Equal(t, 403, resRole.code, resRole.raw)
	require.Equal(t, "reauth_required", errCode(resRole.body))

	resEmail := e.owner.call(t, http.MethodPut, "/api/v1/users/"+staffID, map[string]any{"email": "changed@example.com"}, "")
	require.Equal(t, 403, resEmail.code, resEmail.raw)
	require.Equal(t, "reauth_required", errCode(resEmail.body))

	// Wrong password fails re-auth; correct password unlocks the sensitive actions again.
	require.Equal(t, 401, e.owner.call(t, http.MethodPost, "/api/v1/auth/reauthenticate", map[string]any{"password": "wrong-password"}, "").code)
	reauth := e.owner.call(t, http.MethodPost, "/api/v1/auth/reauthenticate", map[string]any{"password": "correct-horse"}, "")
	require.Equal(t, 200, reauth.code, reauth.raw)

	require.Equal(t, 200, e.owner.call(t, http.MethodPost, "/api/v1/users/"+staffID+"/reset-password", map[string]any{"password": "fresh-temp-pass-1"}, "").code)
	require.Equal(t, 200, e.owner.call(t, http.MethodPost, "/api/v1/users/"+staffID+"/deactivate", nil, "").code)
}

func TestSuspendedEmployeeImmediatelyLosesAccess(t *testing.T) {
	e := newEnv(t, defaultNow)
	app := e.app.Fiber

	empPhone := uniquePhone()
	emp := e.owner.call(t, http.MethodPost, "/api/v1/employees", map[string]any{
		"name": "Amina Hassan", "phone": empPhone, "base_pay": "400000",
	}, "")
	require.Equal(t, 201, emp.code, emp.raw)
	empID := str(emp.data["id"])

	loginCreate := e.owner.call(t, http.MethodPost, "/api/v1/employees/"+empID+"/user", map[string]any{
		"password": "amina-pass-123", "role_id": e.roleID(t, "staff"), "branch_ids": []string{e.branch},
	}, "")
	require.Equal(t, 201, loginCreate.code, loginCreate.raw)

	code, body, refresh := raw(t, app, http.MethodPost, "/api/v1/auth/login", map[string]any{
		"identifier": empPhone, "password": "amina-pass-123",
	}, nil)
	require.Equal(t, 200, code)
	empAccess := str(body["data"].(map[string]any)["access_token"])
	empAuth := map[string]string{"Authorization": "Bearer " + empAccess}

	meCode, _, _ := raw(t, app, http.MethodGet, "/api/v1/me", nil, empAuth)
	require.Equal(t, 200, meCode)

	// Administrator suspends the employee record.
	susp := e.owner.call(t, http.MethodPut, "/api/v1/employees/"+empID, map[string]any{"status": "suspended"}, "")
	require.Equal(t, 200, susp.code, susp.raw)

	// Existing access token, existing refresh cookie, and new logins are all rejected immediately.
	meAfter, _, _ := raw(t, app, http.MethodGet, "/api/v1/me", nil, empAuth)
	require.Equal(t, 401, meAfter, "suspended employee cannot keep using existing access token")

	refAfter, _, _ := raw(t, app, http.MethodPost, "/api/v1/auth/refresh", nil, map[string]string{"Cookie": "refresh_token=" + refresh})
	require.Equal(t, 401, refAfter, "suspended employee cannot refresh session")

	loginAfter, _, _ := raw(t, app, http.MethodPost, "/api/v1/auth/login", map[string]any{
		"identifier": empPhone, "password": "amina-pass-123",
	}, nil)
	require.Equal(t, 401, loginAfter, "suspended employee cannot sign in again")
}

func TestPasswordResetAuthorizationAndLockoutReset(t *testing.T) {
	clk := &stepClock{t: defaultNow}
	e := newEnvWithClock(t, clk)
	app := e.app.Fiber
	ownerID := meID(t, e.owner)

	// Nobody can use the admin reset-password route on their own account to bypass current password check.
	selfReset := e.owner.call(t, http.MethodPost, "/api/v1/users/"+ownerID+"/reset-password", map[string]any{"password": "bypassed-pass-1"}, "")
	require.Equal(t, 403, selfReset.code, selfReset.raw)

	// Two peer delegated managers (same rank) cannot reset each other's passwords.
	peerRole := e.owner.call(t, http.MethodPost, "/api/v1/roles", map[string]any{
		"name": "Peer Manager", "rank": 50,
		"permissions": []map[string]any{{"key": "security.user.manage", "scope": "all"}},
	}, "")
	require.Equal(t, 201, peerRole.code, peerRole.raw)
	roleID := str(peerRole.data["id"])

	p1, p2 := uniquePhone(), uniquePhone()
	u1 := e.owner.call(t, http.MethodPost, "/api/v1/users", map[string]any{
		"name": "Mgr One", "phone": p1, "password": "peer-one-pass-1", "role_id": roleID, "branch_ids": []string{e.branch},
	}, "")
	require.Equal(t, 201, u1.code, u1.raw)
	u2 := e.owner.call(t, http.MethodPost, "/api/v1/users", map[string]any{
		"name": "Mgr Two", "phone": p2, "password": "peer-two-pass-1", "role_id": roleID, "branch_ids": []string{e.branch},
	}, "")
	require.Equal(t, 201, u2.code, u2.raw)

	mgr1 := &client{app: app, token: login(t, app, p1, "peer-one-pass-1"), branch: e.branch}
	require.Equal(t, 403, mgr1.call(t, http.MethodPost, "/api/v1/users/"+str(u2.data["id"])+"/reset-password", map[string]any{"password": "hijacked-peer-1"}, "").code)

	// Lockout window resets cleanly after 15 minutes: one typo after expiry does not immediately re-lock.
	for i := 0; i < 5; i++ {
		raw(t, app, http.MethodPost, "/api/v1/auth/login", map[string]any{"identifier": p2, "password": "wrong-attempt-1"}, nil)
	}
	lockedCode, _, _ := raw(t, app, http.MethodPost, "/api/v1/auth/login", map[string]any{"identifier": p2, "password": "peer-two-pass-1"}, nil)
	require.Equal(t, 401, lockedCode)

	clk.Advance(16 * time.Minute)
	raw(t, app, http.MethodPost, "/api/v1/auth/login", map[string]any{"identifier": p2, "password": "one-fresh-typo"}, nil)
	okCode, _, _ := raw(t, app, http.MethodPost, "/api/v1/auth/login", map[string]any{"identifier": p2, "password": "peer-two-pass-1"}, nil)
	require.Equal(t, 200, okCode, "one failure after lockout expiry should not immediately re-lock")
}
