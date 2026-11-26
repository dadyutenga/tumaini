package app_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// FR-ATT-12: clock-in can be limited to the branch network.
// One attendance row is kept per employee per day, so each person clocks in once.
func TestClockInCanBeRestrictedToTheBranchNetwork(t *testing.T) {
	e := newEnv(t, defaultNow)
	o := e.owner
	workWeek(t, o)
	neemaID := employee(t, o, "Neema", "300000", nil)
	neema := e.employeeLogin(t, neemaID, "staff")

	// Off by default.
	require.Equal(t, 200, neema.call(t, http.MethodPost, "/api/v1/attendance/clock-in", nil, "").code)
	require.Equal(t, 200, neema.call(t, http.MethodPost, "/api/v1/attendance/clock-out", nil, "").code)

	// Settings are validated: networks must be IPs or CIDR ranges.
	bad := o.call(t, http.MethodPut, "/api/v1/settings/attendance", map[string]any{"allowed_networks": []string{"not-an-ip"}}, "")
	require.Equal(t, 422, bad.code, bad.raw)
	require.Equal(t, 422, o.call(t, http.MethodPut, "/api/v1/settings/attendance", map[string]any{"restrict_clock_in": "yes"}, "").code)

	// Restricted to another network: a fresh employee cannot clock themselves in.
	on := o.call(t, http.MethodPut, "/api/v1/settings/attendance", map[string]any{"restrict_clock_in": true, "allowed_networks": []string{"10.1.2.0/24", "192.168.5.9"}}, "")
	require.Equal(t, 200, on.code, on.raw)
	require.Equal(t, []any{"10.1.2.0/24", "192.168.5.9"}, on.data["attendance"].(map[string]any)["allowed_networks"])
	barakaID := employee(t, o, "Baraka", "300000", nil)
	baraka := e.employeeLogin(t, barakaID, "staff")
	blocked := baraka.call(t, http.MethodPost, "/api/v1/attendance/clock-in", nil, "")
	require.Equal(t, 403, blocked.code, blocked.raw)
	require.Equal(t, "network_not_allowed", errCode(blocked.body))

	// A manager clocking someone in on a shared device is not restricted by the employee's address.
	byManager := o.call(t, http.MethodPost, "/api/v1/attendance/clock-in", map[string]any{"employee_id": barakaID}, "")
	require.Equal(t, 200, byManager.code, byManager.raw)
	require.Equal(t, 200, o.call(t, http.MethodPost, "/api/v1/attendance/clock-out", map[string]any{"employee_id": barakaID}, "").code)

	// Listing the client's own address lets another employee in.
	require.Equal(t, 200, o.call(t, http.MethodPut, "/api/v1/settings/attendance", map[string]any{"allowed_networks": []string{"10.1.2.0/24", "0.0.0.0/32"}}, "").code)
	daudiID := employee(t, o, "Daudi", "300000", nil)
	daudi := e.employeeLogin(t, daudiID, "staff")
	require.Equal(t, 200, daudi.call(t, http.MethodPost, "/api/v1/attendance/clock-in", nil, "").code)
	require.Equal(t, 200, daudi.call(t, http.MethodPost, "/api/v1/attendance/clock-out", nil, "").code)

	// Restriction switched on with no networks listed cannot lock everyone out.
	require.Equal(t, 200, o.call(t, http.MethodPut, "/api/v1/settings/attendance", map[string]any{"allowed_networks": []string{}}, "").code)
	chausikuID := employee(t, o, "Chausiku", "300000", nil)
	chausiku := e.employeeLogin(t, chausikuID, "staff")
	require.Equal(t, 200, chausiku.call(t, http.MethodPost, "/api/v1/attendance/clock-in", nil, "").code)
}
