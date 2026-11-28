package app_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// raw sends a request with arbitrary headers and returns status, body and the refresh cookie it set.
func raw(t *testing.T, app *fiber.App, method, path string, body any, headers map[string]string) (int, map[string]any, string) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		require.NoError(t, json.NewEncoder(&buf).Encode(body))
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var parsed map[string]any
	_ = json.Unmarshal(b, &parsed)
	cookie := ""
	for _, c := range resp.Cookies() {
		if c.Name == "refresh_token" {
			cookie = c.Value
		}
	}
	return resp.StatusCode, parsed, cookie
}

func errCode(body map[string]any) string {
	e, _ := body["error"].(map[string]any)
	return str(e["code"])
}

func TestAuthLoginLockoutAndHistory(t *testing.T) {
	e := newEnv(t, defaultNow)
	app := e.app.Fiber

	// A wrong password and an unknown account look the same, so accounts cannot be enumerated.
	code, bad, _ := raw(t, app, http.MethodPost, "/api/v1/auth/login", map[string]any{"identifier": e.phone, "password": "wrong-password"}, nil)
	require.Equal(t, 401, code)
	code, unknown, _ := raw(t, app, http.MethodPost, "/api/v1/auth/login", map[string]any{"identifier": "255000000000", "password": "wrong-password"}, nil)
	require.Equal(t, 401, code)
	require.Equal(t, errCode(bad), errCode(unknown))
	require.Equal(t, bad["error"].(map[string]any)["message"], unknown["error"].(map[string]any)["message"])

	// Five failures lock the account: even the right password is refused afterwards, and the Owner is told.
	staff, staffID := e.user(t, "staff")
	_ = staff
	var staffPhone string
	list := e.owner.call(t, http.MethodGet, "/api/v1/users/"+staffID, nil, "")
	staffPhone = str(list.data["phone"])
	for i := 0; i < 5; i++ {
		raw(t, app, http.MethodPost, "/api/v1/auth/login", map[string]any{"identifier": staffPhone, "password": "nope-nope-1"}, nil)
	}
	code, locked, _ := raw(t, app, http.MethodPost, "/api/v1/auth/login", map[string]any{"identifier": staffPhone, "password": "staff-pass-123"}, nil)
	require.Equal(t, 401, code)
	// Locked accounts return the same generic error code/message so lockouts cannot enumerate accounts.
	require.Equal(t, errCode(bad), errCode(locked))
	require.Equal(t, bad["error"].(map[string]any)["message"], locked["error"].(map[string]any)["message"])
	require.Contains(t, titles(t, e.owner), "Account locked")

	// Login history records every attempt with its result (including the internal "locked" reason); only login_history.view may read it.
	hist := e.owner.call(t, http.MethodGet, "/api/v1/login-history?user_id="+staffID, nil, "")
	require.Equal(t, 200, hist.code, hist.raw)
	rows := asList(hist.body["data"])
	require.GreaterOrEqual(t, len(rows), 5)
	require.Equal(t, false, rows[0]["success"])
	require.Equal(t, "locked", str(rows[0]["reason"]))
	mgr, _ := e.user(t, "manager")
	require.Equal(t, 403, mgr.call(t, http.MethodGet, "/api/v1/login-history", nil, "").code)
	require.Equal(t, 400, e.owner.call(t, http.MethodGet, "/api/v1/login-history?from=garbage", nil, "").code)
	require.Equal(t, 400, e.owner.call(t, http.MethodGet, "/api/v1/login-history?user_id=nope", nil, "").code)
}

func TestAuthRefreshRotationReplayAndLogout(t *testing.T) {
	e := newEnv(t, defaultNow)
	app := e.app.Fiber
	code, body, first := raw(t, app, http.MethodPost, "/api/v1/auth/login", map[string]any{"identifier": e.phone, "password": "correct-horse"}, nil)
	require.Equal(t, 200, code)
	require.NotEmpty(t, first, "the refresh token travels in a cookie")
	require.NotContains(t, body["data"], "refresh", "and never in the body")

	// Refresh rotates the token.
	code, _, second := raw(t, app, http.MethodPost, "/api/v1/auth/refresh", nil, map[string]string{"Cookie": "refresh_token=" + first})
	require.Equal(t, 200, code)
	require.NotEmpty(t, second)
	require.NotEqual(t, first, second)

	// Replaying the old token is detected as reuse/theft and revokes the entire session,
	// so even the rotated token (second) stops working immediately.
	code, replay, _ := raw(t, app, http.MethodPost, "/api/v1/auth/refresh", nil, map[string]string{"Cookie": "refresh_token=" + first})
	require.Equal(t, 401, code)
	require.Equal(t, "session_ended", errCode(replay))
	code, afterReuse, _ := raw(t, app, http.MethodPost, "/api/v1/auth/refresh", nil, map[string]string{"Cookie": "refresh_token=" + second})
	require.Equal(t, 401, code)
	require.Equal(t, "session_ended", errCode(afterReuse))

	// Garbage and missing cookies are plain 401s.
	code, _, _ = raw(t, app, http.MethodPost, "/api/v1/auth/refresh", nil, map[string]string{"Cookie": "refresh_token=not-a-token"})
	require.Equal(t, 401, code)
	code, _, _ = raw(t, app, http.MethodPost, "/api/v1/auth/refresh", nil, nil)
	require.Equal(t, 401, code)

	// Logout ends the session at once: the access token stops working before it expires.
	code, body, _ = raw(t, app, http.MethodPost, "/api/v1/auth/login", map[string]any{"identifier": e.phone, "password": "correct-horse"}, nil)
	require.Equal(t, 200, code)
	access := str(body["data"].(map[string]any)["access_token"])
	auth := map[string]string{"Authorization": "Bearer " + access}
	code, _, _ = raw(t, app, http.MethodGet, "/api/v1/me", nil, auth)
	require.Equal(t, 200, code)
	code, _, _ = raw(t, app, http.MethodPost, "/api/v1/auth/logout", nil, auth)
	require.Equal(t, 200, code)
	code, ended, _ := raw(t, app, http.MethodGet, "/api/v1/me", nil, auth)
	require.Equal(t, 401, code)
	require.Equal(t, "session_ended", errCode(ended))
}

func TestAuthTokenTamperingIsRejected(t *testing.T) {
	e := newEnv(t, defaultNow)
	app := e.app.Fiber
	good := e.owner.token
	parts := strings.Split(good, ".")
	require.Len(t, parts, 3)

	// alg=none with the same claims.
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	none := header + "." + parts[1] + "."
	// A different signature on the same claims.
	forged := parts[0] + "." + parts[1] + "." + base64.RawURLEncoding.EncodeToString([]byte("forged-signature-forged-signature"))
	// Claims edited to another business with the old signature.
	claims, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var m map[string]any
	require.NoError(t, json.Unmarshal(claims, &m))
	m["bid"] = uuid.NewString()
	edited, _ := json.Marshal(m)
	swapped := parts[0] + "." + base64.RawURLEncoding.EncodeToString(edited) + "." + parts[2]

	for name, tok := range map[string]string{"alg none": none, "forged signature": forged, "edited claims": swapped, "empty": "", "junk": "junk"} {
		code, body, _ := raw(t, app, http.MethodGet, "/api/v1/me", nil, map[string]string{"Authorization": "Bearer " + tok})
		require.Equal(t, 401, code, name)
		require.Equal(t, "invalid_token", errCode(body), name)
	}
	code, _, _ := raw(t, app, http.MethodGet, "/api/v1/me", nil, map[string]string{"Authorization": good})
	require.Equal(t, 401, code, "the Bearer scheme is required")
}

func TestSetupValidation(t *testing.T) {
	e := newEnv(t, defaultNow)
	app := e.app.Fiber
	// The same phone cannot register twice.
	code, dup, _ := raw(t, app, http.MethodPost, "/api/v1/setup", map[string]any{"business_name": "Again", "owner_name": "O", "phone": e.phone, "email": "owner@demo.test", "password": "correct-horse"}, nil)
	require.Equal(t, 409, code)
	require.Equal(t, "setup_already_done", errCode(dup))
	code, weak, _ := raw(t, app, http.MethodPost, "/api/v1/setup", map[string]any{"business_name": "Weak", "owner_name": "O", "phone": uniquePhone(), "email": "weak@demo.test", "password": "password"}, nil)
	require.Equal(t, 400, code)
	require.Equal(t, "weak_password", errCode(weak))
	code, _, _ = raw(t, app, http.MethodPost, "/api/v1/setup", map[string]any{"business_name": "", "owner_name": "", "phone": "", "email": "", "password": "correct-horse"}, nil)
	require.GreaterOrEqual(t, code, 400)
	require.Less(t, code, 500)
	code, _, _ = raw(t, app, http.MethodPost, "/api/v1/auth/login", nil, nil)
	require.Less(t, code, 500, "an empty body is a client error, not a crash")
}

func TestAuthPasswordPINAndSessions(t *testing.T) {
	e := newEnv(t, defaultNow)
	app := e.app.Fiber
	c, userID := e.user(t, "staff")

	// Change password: needs the current one and a decent new one; the new one works afterwards.
	require.Equal(t, 401, c.call(t, http.MethodPut, "/api/v1/auth/password", map[string]any{"current_password": "wrong", "new_password": "a-new-passphrase"}, "").code)
	require.Equal(t, 400, c.call(t, http.MethodPut, "/api/v1/auth/password", map[string]any{"current_password": "staff-pass-123", "new_password": "short"}, "").code)
	require.Equal(t, 200, c.call(t, http.MethodPut, "/api/v1/auth/password", map[string]any{"current_password": "staff-pass-123", "new_password": "a-new-passphrase"}, "").code)
	phone := str(e.owner.call(t, http.MethodGet, "/api/v1/users/"+userID, nil, "").data["phone"])
	code, _, _ := raw(t, app, http.MethodPost, "/api/v1/auth/login", map[string]any{"identifier": phone, "password": "staff-pass-123"}, nil)
	require.Equal(t, 401, code, "the old password no longer works")
	login(t, app, phone, "a-new-passphrase")

	// PIN endpoints were removed in the approved BMS schema (decision #2).
	require.Equal(t, 404, c.call(t, http.MethodPut, "/api/v1/auth/pin", map[string]any{"pin": "4821"}, "").code)
	require.Equal(t, 404, c.call(t, http.MethodPost, "/api/v1/auth/pin", map[string]any{"pin": "4821"}, "").code)

	// Sessions: list own, end another of own, and an unknown id is a 404.
	second := &client{app: app, token: login(t, app, phone, "a-new-passphrase"), branch: c.branch}
	sessions := second.call(t, http.MethodGet, "/api/v1/me/sessions", nil, "")
	require.Equal(t, 200, sessions.code, sessions.raw)
	list := asList(sessions.body["data"])
	require.GreaterOrEqual(t, len(list), 2)
	var closed int
	for _, s := range list {
		if s["current"] == false {
			require.Equal(t, 200, second.call(t, http.MethodDelete, "/api/v1/me/sessions/"+str(s["id"]), nil, "").code)
			closed++
		}
	}
	require.GreaterOrEqual(t, closed, 1)
	require.Equal(t, 404, second.call(t, http.MethodDelete, "/api/v1/me/sessions/"+uuid.NewString(), nil, "").code)
	require.Equal(t, 401, c.call(t, http.MethodGet, "/api/v1/me", nil, "").code, "the ended session is cut off")

	// Only security.user.manage ends someone else's sessions; then every token of theirs stops.
	require.Equal(t, 403, second.call(t, http.MethodDelete, "/api/v1/users/"+userID+"/sessions", nil, "").code)
	require.Equal(t, 200, e.owner.call(t, http.MethodDelete, "/api/v1/users/"+userID+"/sessions", nil, "").code)
	require.Equal(t, 401, second.call(t, http.MethodGet, "/api/v1/me", nil, "").code)
}

func TestUsersDeactivationAndOwnerSafety(t *testing.T) {
	e := newEnv(t, defaultNow)
	app := e.app.Fiber
	staff, staffID := e.user(t, "staff")
	phone := str(e.owner.call(t, http.MethodGet, "/api/v1/users/"+staffID, nil, "").data["phone"])
	ownerID := meID(t, e.owner)

	// Deactivating a user ends their sessions immediately and blocks login (FR-AUTH-08).
	require.Equal(t, 200, staff.call(t, http.MethodGet, "/api/v1/me", nil, "").code)
	require.Equal(t, 200, e.owner.call(t, http.MethodPost, "/api/v1/users/"+staffID+"/deactivate", nil, "").code)
	require.Equal(t, 401, staff.call(t, http.MethodGet, "/api/v1/me", nil, "").code)
	code, _, _ := raw(t, app, http.MethodPost, "/api/v1/auth/login", map[string]any{"identifier": phone, "password": "staff-pass-123"}, nil)
	require.Equal(t, 401, code)
	require.Equal(t, 200, e.owner.call(t, http.MethodPost, "/api/v1/users/"+staffID+"/activate", nil, "").code)
	login(t, app, phone, "staff-pass-123")

	// An admin cannot deactivate themselves or change their own role.
	require.NotEmpty(t, ownerID)
	selfDeact := e.owner.call(t, http.MethodPost, "/api/v1/users/"+ownerID+"/deactivate", nil, "")
	require.Equal(t, 403, selfDeact.code, selfDeact.raw)
	own := e.owner.call(t, http.MethodPut, "/api/v1/users/"+ownerID, map[string]any{"role_id": e.roleID(t, "staff")}, "")
	require.Equal(t, 403, own.code, own.raw)

	// A user from another business is invisible.
	other := newEnv(t, defaultNow)
	require.Equal(t, 404, other.owner.call(t, http.MethodGet, "/api/v1/users/"+staffID, nil, "").code)
	require.Equal(t, 404, other.owner.call(t, http.MethodPost, "/api/v1/users/"+staffID+"/deactivate", nil, "").code)
	require.Equal(t, 404, other.owner.call(t, http.MethodPost, "/api/v1/users/"+staffID+"/reset-password", map[string]any{"password": "hijacked-pass-1"}, "").code)
	// And the phone is unique across the platform, so a second business cannot register a user with it.
	dup := other.owner.call(t, http.MethodPost, "/api/v1/users", map[string]any{
		"name": "Clash", "phone": phone, "password": "clash-pass-123", "role_id": other.roleID(t, "staff"), "branch_ids": []string{other.branch},
	}, "")
	require.Equal(t, 409, dup.code, dup.raw)
}

// A delegated administrator must not be able to promote themselves or take over an Admin.
func TestPrivilegeEscalationIsBlocked(t *testing.T) {
	e := newEnv(t, defaultNow)
	app := e.app.Fiber
	ownerID := meID(t, e.owner)
	adminRole := e.owner.call(t, http.MethodPost, "/api/v1/roles", map[string]any{"name": "Delegated admin", "permissions": []map[string]any{
		{"key": "security.user.manage", "scope": "all"},
	}}, "")
	require.Equal(t, 201, adminRole.code, adminRole.raw)
	phone := uniquePhone()
	created := e.owner.call(t, http.MethodPost, "/api/v1/users", map[string]any{
		"name": "Delegate", "phone": phone, "password": "delegate-pass-1", "role_id": adminRole.data["id"], "branch_ids": []string{e.branch},
	}, "")
	require.Equal(t, 201, created.code, created.raw)
	admin := &client{app: app, token: login(t, app, phone, "delegate-pass-1"), branch: e.branch}
	ownerRole := e.roleID(t, "admin")

	// Cannot create an Admin, or promote anyone to Admin.
	mk := admin.call(t, http.MethodPost, "/api/v1/users", map[string]any{
		"name": "Sneaky", "phone": uniquePhone(), "password": "sneaky-pass-1", "role_id": ownerRole, "branch_ids": []string{e.branch},
	}, "")
	require.Equal(t, 403, mk.code, "creating an Admin: "+mk.raw)
	_, staffID := e.user(t, "staff")
	promote := admin.call(t, http.MethodPut, "/api/v1/users/"+staffID, map[string]any{"role_id": ownerRole}, "")
	require.Equal(t, 403, promote.code, "promoting to Admin: "+promote.raw)

	// The employee-login route is another way to create a user and follows the same rule.
	hrRole := e.owner.call(t, http.MethodPost, "/api/v1/roles", map[string]any{"name": "HR admin", "permissions": []map[string]any{
		{"key": "security.user.manage", "scope": "all"},
		{"key": "hr.employee.view", "scope": "all"}, {"key": "hr.employee.manage", "scope": "all"},
		{"key": "hr.compensation.manage", "scope": "all"},
	}}, "")
	require.Equal(t, 201, hrRole.code, hrRole.raw)
	hrPhone := uniquePhone()
	require.Equal(t, 201, e.owner.call(t, http.MethodPost, "/api/v1/users", map[string]any{
		"name": "HR delegate", "phone": hrPhone, "password": "hr-delegate-1", "role_id": hrRole.data["id"], "branch_ids": []string{e.branch},
	}, "").code)
	hrAdmin := &client{app: app, token: login(t, app, hrPhone, "hr-delegate-1"), branch: e.branch}
	emp := employee(t, hrAdmin, "Neema", "300000", nil)
	viaEmployee := hrAdmin.call(t, http.MethodPost, "/api/v1/employees/"+emp+"/user", map[string]any{"password": "neema-pass-123", "role_id": ownerRole}, "")
	require.Equal(t, 403, viaEmployee.code, "creating an Admin through an employee login: "+viaEmployee.raw)

	// Cannot touch an Admin's account: password, contact details, sessions or active state.
	require.Equal(t, 403, admin.call(t, http.MethodPost, "/api/v1/users/"+ownerID+"/reset-password", map[string]any{"password": "taken-over-pass-1"}, "").code, "reset the Admin's password")
	require.Equal(t, 403, admin.call(t, http.MethodPut, "/api/v1/users/"+ownerID, map[string]any{"phone": uniquePhone()}, "").code, "change the Admin's phone")
	require.Equal(t, 403, admin.call(t, http.MethodPost, "/api/v1/users/"+ownerID+"/deactivate", nil, "").code)
	require.Equal(t, 403, admin.call(t, http.MethodDelete, "/api/v1/users/"+ownerID+"/sessions", nil, "").code)
	code, _, _ := raw(t, app, http.MethodPost, "/api/v1/auth/login", map[string]any{"identifier": e.phone, "password": "correct-horse"}, nil)
	require.Equal(t, 200, code, "the Admin's password is unchanged")

	// Cannot edit the role they hold, nor grant a permission they do not have.
	own := admin.call(t, http.MethodPut, "/api/v1/roles/"+str(adminRole.data["id"]), map[string]any{"permissions": []map[string]any{
		{"key": "security.user.manage", "scope": "all"}, {"key": "hr.payroll.approve", "scope": "all"},
	}}, "")
	require.Equal(t, 403, own.code, "editing own role: "+own.raw)
	grant := admin.call(t, http.MethodPost, "/api/v1/roles", map[string]any{"name": "Payroll bosses", "permissions": []map[string]any{
		{"key": "hr.payroll.approve", "scope": "all"},
	}}, "")
	require.Equal(t, 403, grant.code, "granting a permission the caller lacks: "+grant.raw)
	// What they do hold, they may delegate.
	ok := admin.call(t, http.MethodPost, "/api/v1/roles", map[string]any{"name": "User managers", "permissions": []map[string]any{
		{"key": "security.user.manage", "scope": "all"},
	}}, "")
	require.Equal(t, 201, ok.code, ok.raw)

	// The Admin role cannot be edited or deleted by anyone, and system roles stay.
	require.Equal(t, 403, e.owner.call(t, http.MethodPut, "/api/v1/roles/"+ownerRole, map[string]any{"permissions": []map[string]any{}}, "").code)
	require.Equal(t, 409, e.owner.call(t, http.MethodDelete, "/api/v1/roles/"+ownerRole, nil, "").code)
	// A role in use cannot be deleted; an unused custom one can.
	require.Equal(t, 409, e.owner.call(t, http.MethodDelete, "/api/v1/roles/"+str(adminRole.data["id"]), nil, "").code)
	require.Equal(t, 200, e.owner.call(t, http.MethodDelete, "/api/v1/roles/"+str(ok.data["id"]), nil, "").code)
}

func TestRolesValidationAndPermissionChangesApplyImmediately(t *testing.T) {
	e := newEnv(t, defaultNow)
	app := e.app.Fiber
	require.Equal(t, 400, e.owner.call(t, http.MethodPost, "/api/v1/roles", map[string]any{"name": ""}, "").code)
	require.Equal(t, 400, e.owner.call(t, http.MethodPost, "/api/v1/roles", map[string]any{"name": "Bad key", "permissions": []map[string]any{{"key": "nope.nothing", "scope": "all"}}}, "").code)
	role := e.owner.call(t, http.MethodPost, "/api/v1/roles", map[string]any{"name": "Counter", "permissions": []map[string]any{{"key": "shop.product.view", "scope": "all"}}}, "")
	require.Equal(t, 201, role.code, role.raw)
	require.Equal(t, 409, e.owner.call(t, http.MethodPost, "/api/v1/roles", map[string]any{"name": "Counter"}, "").code, "names are unique")

	phone := uniquePhone()
	require.Equal(t, 201, e.owner.call(t, http.MethodPost, "/api/v1/users", map[string]any{
		"name": "Counter user", "phone": phone, "password": "counter-pass-1", "role_id": role.data["id"], "branch_ids": []string{e.branch},
	}, "").code)
	c := &client{app: app, token: login(t, app, phone, "counter-pass-1"), branch: e.branch}
	require.Equal(t, 200, c.call(t, http.MethodGet, "/api/v1/products", nil, "").code)
	require.Equal(t, 403, c.call(t, http.MethodGet, "/api/v1/customers", nil, "").code)

	// Granting a permission takes effect on the next request, without signing in again.
	upd := e.owner.call(t, http.MethodPut, "/api/v1/roles/"+str(role.data["id"]), map[string]any{"permissions": []map[string]any{
		{"key": "shop.product.view", "scope": "all"}, {"key": "shop.customer.view_all", "scope": "all"},
	}}, "")
	require.Equal(t, 200, upd.code, upd.raw)
	require.Equal(t, 200, c.call(t, http.MethodGet, "/api/v1/customers", nil, "").code)
	// And revoking it does too.
	e.owner.call(t, http.MethodPut, "/api/v1/roles/"+str(role.data["id"]), map[string]any{"permissions": []map[string]any{{"key": "shop.product.view", "scope": "all"}}}, "")
	require.Equal(t, 403, c.call(t, http.MethodGet, "/api/v1/customers", nil, "").code)

	cat := e.owner.call(t, http.MethodGet, "/api/v1/permissions", nil, "")
	require.Equal(t, 200, cat.code, cat.raw)
	me := c.call(t, http.MethodGet, "/api/v1/me", nil, "")
	require.Equal(t, 200, me.code, me.raw)
	require.Equal(t, 200, c.call(t, http.MethodPut, "/api/v1/me", map[string]any{"name": "Renamed", "language": "sw"}, "").code)
	bad := c.call(t, http.MethodPut, "/api/v1/me", map[string]any{"language": "xx"}, "")
	require.GreaterOrEqual(t, bad.code, 400, "an unsupported language is rejected: "+bad.raw)
	require.Less(t, bad.code, 500, "as a client error, not a crash: "+bad.raw)
}

func meID(t *testing.T, c *client) string {
	t.Helper()
	me := c.call(t, http.MethodGet, "/api/v1/me", nil, "")
	require.Equal(t, 200, me.code, me.raw)
	user, _ := me.data["user"].(map[string]any)
	return str(user["id"])
}

func TestPINGuessingIsLimitedAndPasswordChangeEndsOtherSessions(t *testing.T) {
	e := newEnv(t, defaultNow)
	app := e.app.Fiber
	c, userID := e.user(t, "staff")
	phone := str(e.owner.call(t, http.MethodGet, "/api/v1/users/"+userID, nil, "").data["phone"])

	// A password change signs out every other session of that user, but not the one making the change.
	other := &client{app: app, token: login(t, app, phone, "staff-pass-123"), branch: c.branch}
	third := &client{app: app, token: login(t, app, phone, "staff-pass-123"), branch: c.branch}
	require.Equal(t, 200, other.call(t, http.MethodGet, "/api/v1/me", nil, "").code)
	code, _, refresh := raw(t, app, http.MethodPost, "/api/v1/auth/login", map[string]any{"identifier": phone, "password": "staff-pass-123"}, nil)
	require.Equal(t, 200, code)
	require.Equal(t, 200, third.call(t, http.MethodPut, "/api/v1/auth/password", map[string]any{"current_password": "staff-pass-123", "new_password": "a-brand-new-pass"}, "").code)
	require.Equal(t, 200, third.call(t, http.MethodGet, "/api/v1/me", nil, "").code, "the session that changed the password stays")
	require.Equal(t, 401, other.call(t, http.MethodGet, "/api/v1/me", nil, "").code, "another session is signed out")
	require.Equal(t, 401, c.call(t, http.MethodGet, "/api/v1/me", nil, "").code)
	code, _, _ = raw(t, app, http.MethodPost, "/api/v1/auth/refresh", nil, map[string]string{"Cookie": "refresh_token=" + refresh})
	require.Equal(t, 401, code, "a stolen refresh token stops working too")
	login(t, app, phone, "a-brand-new-pass")
}

// FR-NTF-08: a sign-in from a device the account has not been used from before notifies the user.
func TestNewDeviceSignInNotifiesTheUser(t *testing.T) {
	e := newEnv(t, defaultNow)
	app := e.app.Fiber
	staff, userID := e.user(t, "staff") // e.user already signed in once, from the test client
	_ = staff
	phone := str(e.owner.call(t, http.MethodGet, "/api/v1/users/"+userID, nil, "").data["phone"])
	signIn := func(ua string) *client {
		code, body, _ := raw(t, app, http.MethodPost, "/api/v1/auth/login", map[string]any{"identifier": phone, "password": "staff-pass-123"}, map[string]string{"User-Agent": ua})
		require.Equal(t, 200, code)
		return &client{app: app, token: str(body["data"].(map[string]any)["access_token"]), branch: e.branch}
	}
	count := func(c *client) int {
		n := 0
		for _, title := range titles(t, c) {
			if title == "Sign-in from a new device" {
				n++
			}
		}
		return n
	}
	chrome := signIn("Chrome/130 Windows")
	before := count(chrome)
	signIn("Chrome/130 Windows")
	require.Equal(t, before, count(chrome), "the same device again is not news")
	phoneApp := signIn("TumainiApp/1.0 Android")
	require.Equal(t, before+1, count(phoneApp), "a device never seen before notifies the user")
	signIn("TumainiApp/1.0 Android")
	require.Equal(t, before+1, count(phoneApp), "and only once")
	// The owner is not told about the staff member's devices.
	require.NotContains(t, titles(t, e.owner), "Sign-in from a new device")
}

// FR-SET-09: a session idle longer than the business's timeout is over; activity keeps it alive.
func TestIdleSessionsTimeOut(t *testing.T) {
	clk := &stepClock{t: defaultNow}
	e := newEnvWithClock(t, clk)
	app := e.app.Fiber
	require.Equal(t, 200, e.owner.call(t, http.MethodPut, "/api/v1/settings/system", map[string]any{"session_timeout_minutes": 5}, "").code)
	require.Equal(t, 422, e.owner.call(t, http.MethodPut, "/api/v1/settings/system", map[string]any{"session_timeout_minutes": 1}, "").code, "at least 5 minutes")

	code, body, refresh := raw(t, app, http.MethodPost, "/api/v1/auth/login", map[string]any{"identifier": e.phone, "password": "correct-horse"}, nil)
	require.Equal(t, 200, code)
	c := &client{app: app, token: str(body["data"].(map[string]any)["access_token"]), branch: e.branch}

	// Activity inside the timeout keeps the session alive, even past 5 minutes in total.
	clk.Advance(3 * time.Minute)
	require.Equal(t, 200, c.call(t, http.MethodGet, "/api/v1/me", nil, "").code)
	clk.Advance(3 * time.Minute)
	require.Equal(t, 200, c.call(t, http.MethodGet, "/api/v1/me", nil, "").code, "6 minutes after sign-in but 3 after the last request")

	// Idle for longer than the timeout: the request is refused and the session is gone.
	clk.Advance(6 * time.Minute)
	ended := c.call(t, http.MethodGet, "/api/v1/me", nil, "")
	require.Equal(t, 401, ended.code, ended.raw)
	require.Equal(t, "session_ended", errCode(ended.body))
	require.Contains(t, ended.raw, "inactivity")
	// The refresh token cannot bring it back.
	code, _, _ = raw(t, app, http.MethodPost, "/api/v1/auth/refresh", nil, map[string]string{"Cookie": "refresh_token=" + refresh})
	require.Equal(t, 401, code)

	// A fresh sign-in works, and a refresh inside the timeout does too.
	code, body, refresh = raw(t, app, http.MethodPost, "/api/v1/auth/login", map[string]any{"identifier": e.phone, "password": "correct-horse"}, nil)
	require.Equal(t, 200, code)
	clk.Advance(4 * time.Minute)
	code, _, _ = raw(t, app, http.MethodPost, "/api/v1/auth/refresh", nil, map[string]string{"Cookie": "refresh_token=" + refresh})
	require.Equal(t, 200, code, "inside the timeout")
	_ = body
}
