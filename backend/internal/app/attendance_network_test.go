package app_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// FR-ATT-12: clock-in can be limited to the branch network.
func TestClockInCanBeRestrictedToTheBranchNetwork(t *testing.T) {
	e := newEnv(t, defaultNow)
	o := e.owner
	workWeek(t, o)
	emp := employee(t, o, "Neema", "300000", nil)
	neema := e.employeeLogin(t, emp, "staff")

	// Off by default.
	require.Equal(t, 200, neema.call(t, http.MethodPost, "/api/v1/attendance/clock-in", nil, "").code)
	require.Equal(t, 200, neema.call(t, http.MethodPost, "/api/v1/attendance/clock-out", nil, "").code)

	// Settings are validated: networks must be IPs or CIDR ranges.
	bad := o.call(t, http.MethodPut, "/api/v1/settings/attendance", map[string]any{"allowed_networks": []string{"not-an-ip"}}, "")
	require.Equal(t, 422, bad.code, bad.raw)
	require.Equal(t, 422, o.call(t, http.MethodPut, "/api/v1/settings/attendance", map[string]any{"restrict_clock_in": "yes"}, "").code)

	// Restricted to another network: the employee cannot clock themselves in or out.
	on := o.call(t, http.MethodPut, "/api/v1/settings/attendance", map[string]any{"restrict_clock_in": true, "allowed_networks": []string{"10.1.2.0/24", "192.168.5.9"}}, "")
	require.Equal(t, 200, on.code, on.raw)
	require.Equal(t, []any{"10.1.2.0/24", "192.168.5.9"}, on.data["attendance"].(map[string]any)["allowed_networks"])
	blocked := neema.call(t, http.MethodPost, "/api/v1/attendance/clock-in", nil, "")
	require.Equal(t, 403, blocked.code, blocked.raw)
	require.Equal(t, "network_not_allowed", errCode(blocked.body))

	// A manager clocking someone in on a shared device is not restricted by the employee's address.
	byManager := o.call(t, http.MethodPost, "/api/v1/attendance/clock-in", map[string]any{"employee_id": emp}, "")
	require.Equal(t, 200, byManager.code, byManager.raw)
	require.Equal(t, 200, o.call(t, http.MethodPost, "/api/v1/attendance/clock-out", map[string]any{"employee_id": emp}, "").code)

	// Listing the client's own address lets the employee in again.
	require.Equal(t, 200, o.call(t, http.MethodPut, "/api/v1/settings/attendance", map[string]any{"allowed_networks": []string{"10.1.2.0/24", "0.0.0.0/32"}}, "").code)
	require.Equal(t, 200, neema.call(t, http.MethodPost, "/api/v1/attendance/clock-in", nil, "").code)

	// Restriction switched on with no networks listed cannot lock everyone out.
	require.Equal(t, 200, neema.call(t, http.MethodPost, "/api/v1/attendance/clock-out", nil, "").code)
	require.Equal(t, 200, o.call(t, http.MethodPut, "/api/v1/settings/attendance", map[string]any{"allowed_networks": []string{}}, "").code)
	require.Equal(t, 200, neema.call(t, http.MethodPost, "/api/v1/attendance/clock-in", nil, "").code)
}
