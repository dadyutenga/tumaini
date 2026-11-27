package app_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"bms/internal/platform/clock"
)

func TestListUsersIncludesBranches(t *testing.T) {
	e := newEnvWithClock(t, clock.System{})
	c := e.owner
	users := c.call(t, http.MethodGet, "/api/v1/users?page=1&per_page=25", nil, "")
	require.Equal(t, 200, users.code, users.raw)
	rows := asList(users.body["data"])
	require.Len(t, rows, 1)
	require.Equal(t, []any{c.branch}, rows[0]["branch_ids"], "user list must include assigned branches")
}
