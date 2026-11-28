package app_test

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every read answers for every kind of user, with and without filters. A query whose parameters do
// not match its values only fails when it runs, and the filters and the "own records only" scopes
// each build a different query.
func TestEveryReadAnswersWithoutAServerError(t *testing.T) {
	e := newEnv(t, defaultNow)
	manager, _ := e.user(t, "manager")
	staff, _ := e.user(t, "staff")
	workWeek(t, e.owner)
	linked := e.employeeLogin(t, employee(t, e.owner, "Neema", "200000", nil), "staff")
	users := map[string]*client{"owner": e.owner, "manager": manager, "staff": staff, "employee": linked}

	other := uuid.NewString()
	filters := []string{
		"",
		"?page=2&per_page=5&q=a",
		"?from=2026-11-01&to=2026-11-30&date=2026-11-16&month=2026-11",
		"?overdue=true&within_days=30&client_id=" + other + "&project_id=" + other + "&branch_id=" + e.branch,
		"?status=open&assignee_id=" + other + "&designer_id=" + other + "&category_id=" + other + "&employee_id=" + other,
	}
	param := regexp.MustCompile(`:[a-z_]+`)
	read := 0
	for _, route := range e.app.Fiber.GetRoutes(true) {
		if route.Method != http.MethodGet || !strings.HasPrefix(route.Path, "/api/v1/") || strings.Contains(route.Path, "*") {
			continue
		}
		path := param.ReplaceAllString(route.Path, other)
		for who, c := range users {
			for _, filter := range filters {
				req := httptest.NewRequest(http.MethodGet, path+filter, nil)
				req.Header.Set("Authorization", "Bearer "+c.token)
				req.Header.Set("X-Branch-Id", c.branch)
				resp, err := e.app.Fiber.Test(req, fiber.TestConfig{Timeout: 0})
				require.NoError(t, err, path+filter)
				_ = resp.Body.Close()
				// 503 is how a feature without its outside service answers.
				if resp.StatusCode != http.StatusServiceUnavailable {
					assert.Less(t, resp.StatusCode, 500, "%s GET %s%s", who, route.Path, filter)
				}
				read++
			}
		}
	}
	require.Greater(t, read, 1000)
}
