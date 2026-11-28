package app_test

import (
	"context"
	"net/http"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"bms/internal/platform/db"
)

// Two people finishing the setup page at once make one business, not two.
func TestConcurrentSetupMakesOneBusiness(t *testing.T) {
	a := newApp(t, &stepClock{t: defaultNow})
	const attempts = 6
	results := make([]result, attempts)
	var start, done sync.WaitGroup
	start.Add(1)
	for i := range attempts {
		phone := uniquePhone()
		done.Add(1)
		go func() {
			defer done.Done()
			c := &client{app: a.Fiber}
			start.Wait()
			results[i] = c.call(t, http.MethodPost, "/api/v1/setup", map[string]any{
				"business_name": "Shop " + phone, "owner_name": "Owner", "phone": phone,
				"email": "owner+" + phone + "@demo.test", "password": "correct-horse",
			}, "")
		}()
	}
	start.Done()
	done.Wait()

	var created int
	for _, r := range results {
		if r.code == 201 {
			created++
			continue
		}
		require.Equal(t, 409, r.code, r.raw)
		require.Equal(t, "setup_already_done", errCode(r.body))
	}
	require.Equal(t, 1, created)
	require.NoError(t, a.DB.InJobsTx(context.Background(), db.Actor{}, func(ctx context.Context, tx pgx.Tx) error {
		var businesses, members int
		if err := tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM app.business_profile), (SELECT count(*) FROM app.memberships)`).Scan(&businesses, &members); err != nil {
			return err
		}
		require.Equal(t, []int{1, 1}, []int{businesses, members})
		return nil
	}))
}

// Salaries are private: staff read their own pay, a Manager nobody else's, an Admin everyone's.
// The routes say so, and so does the database underneath them.
func TestPayIsPrivate(t *testing.T) {
	e := newEnv(t, defaultNow)
	o := e.owner
	managerEmp := employee(t, o, "Mariam", "900000", nil)
	amani := employee(t, o, "Amani", "500000", nil)
	baraka := employee(t, o, "Baraka", "400000", nil)
	manager := e.employeeLogin(t, managerEmp, "manager")
	staff := e.employeeLogin(t, amani, "staff")
	for _, id := range []string{managerEmp, amani, baraka} {
		r := o.call(t, http.MethodPut, "/api/v1/employees/"+id+"/components", map[string]any{
			"components": []map[string]any{{"name": "Housing", "amount": "10000"}},
		}, "")
		require.Equal(t, 200, r.code, r.raw)
	}
	run := o.call(t, http.MethodPost, "/api/v1/payroll-runs", map[string]any{"period_start": "2026-11-01", "period_end": "2026-11-30"}, "")
	require.Equal(t, 201, run.code, run.raw)
	runID := str(run.data["id"])
	calc := o.call(t, http.MethodPost, "/api/v1/payroll-runs/"+runID+"/calculate", nil, "")
	require.Equal(t, 200, calc.code, calc.raw)
	approved := payrollApprover(t, e).call(t, http.MethodPost, "/api/v1/payroll-runs/"+runID+"/approve", nil, key())
	require.Equal(t, 200, approved.code, approved.raw)
	detail := o.call(t, http.MethodGet, "/api/v1/payroll-runs/"+runID, nil, "")
	require.Equal(t, 200, detail.code, detail.raw)
	require.Len(t, asList(detail.data["payslips"]), 3, "an Admin reads all")
	slip := func(employeeID string) string { return str(slipOf(t, detail, employeeID)["id"]) }
	for _, id := range []string{managerEmp, amani, baraka} {
		one := o.call(t, http.MethodGet, "/api/v1/payslips/"+slip(id), nil, "")
		require.Equal(t, 200, one.code, one.raw)
		require.Equal(t, id, str(one.data["employee_id"]))
		require.NotEmpty(t, asList(one.data["lines"]))
	}
	// The PDF the payroll and My work screens download.
	code, _, _ := raw(t, e.app.Fiber, http.MethodGet, "/api/v1/payslips/"+slip(amani)+"?format=pdf", nil, map[string]string{"Authorization": "Bearer " + staff.token})
	require.Equal(t, 200, code)

	for who, c := range map[string]struct {
		client *client
		own    string
		others []string
	}{
		"Manager": {manager, managerEmp, []string{amani, baraka}},
		"Staff":   {staff, amani, []string{managerEmp, baraka}},
	} {
		for _, path := range []string{"/payroll-runs", "/payroll-runs/" + runID, "/salary-payments", "/loans"} {
			require.Equal(t, 403, c.client.call(t, http.MethodGet, "/api/v1"+path, nil, "").code, "%s %s", who, path)
		}
		mine := c.client.call(t, http.MethodGet, "/api/v1/me/payslips", nil, "")
		require.Equal(t, 200, mine.code, mine.raw)
		require.Len(t, asList(mine.body["data"]), 1, who)
		require.Equal(t, slip(c.own), str(asList(mine.body["data"])[0]["payroll_line_id"]), who)
		require.Equal(t, 200, c.client.call(t, http.MethodGet, "/api/v1/payslips/"+slip(c.own), nil, "").code, who)
		for _, other := range c.others {
			require.Equal(t, 404, c.client.call(t, http.MethodGet, "/api/v1/payslips/"+slip(other), nil, "").code, "%s reads another payslip", who)
			require.Equal(t, 403, c.client.call(t, http.MethodGet, "/api/v1/employees/"+other+"/components", nil, "").code, who)
			require.Equal(t, 403, c.client.call(t, http.MethodGet, "/api/v1/employees/"+other+"/statement", nil, "").code, who)
		}
	}
	seen := manager.call(t, http.MethodGet, "/api/v1/employees/"+amani, nil, "")
	require.Equal(t, 200, seen.code, seen.raw)
	require.Empty(t, str(seen.data["base_pay"]), "a Manager sees who works here, not what they are paid")
	listed := manager.call(t, http.MethodGet, "/api/v1/employees", nil, "")
	require.Equal(t, 200, listed.code, listed.raw)
	for _, row := range asList(listed.body["data"]) {
		if str(row["id"]) != managerEmp {
			require.Empty(t, str(row["base_pay"]), row["name"])
		}
	}

	// The same rules for any query the API role could be made to run.
	for who, c := range map[string]struct {
		employee string
		visible  int
	}{managerEmp: {managerEmp, 1}, amani: {amani, 1}, "admin": {"", 3}} {
		actor := e.ownerActor(t)
		if c.employee != "" {
			require.NoError(t, e.app.DB.InGlobalTx(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
				return tx.QueryRow(ctx, `SELECT m.user_id, m.id FROM app.memberships m JOIN app.employees em ON em.membership_id = m.id WHERE em.id = $1`,
					uuid.MustParse(c.employee)).Scan(&actor.UserID, &actor.MembershipID)
			}), who)
		}
		require.NoError(t, e.app.DB.InActorTx(context.Background(), actor, func(ctx context.Context, tx pgx.Tx) error {
			for table, query := range map[string]string{
				"employee_compensation": `SELECT count(DISTINCT employee_id) FROM app.employee_compensation`,
				"employee_allowances":   `SELECT count(DISTINCT employee_id) FROM app.employee_allowances`,
				"payroll_lines":         `SELECT count(DISTINCT employee_id) FROM app.payroll_lines`,
				"payslips":              `SELECT count(*) FROM app.payslips`,
			} {
				var people int
				if err := tx.QueryRow(ctx, query).Scan(&people); err != nil {
					return err
				}
				require.Equal(t, c.visible, people, "%s reading %s", who, table)
			}
			return nil
		}), who)
	}
}

// The branch chosen at the top of the screen filters what is listed: sales and stock belong to a location.
func TestSwitchingBranchFiltersSalesAndStock(t *testing.T) {
	e := newEnv(t, defaultNow)
	o := e.owner
	second := o.call(t, http.MethodPost, "/api/v1/branches", map[string]any{"name": "Second shop"}, "")
	require.Equal(t, 201, second.code, second.raw)
	other := str(second.data["id"])
	rice := e.product(t, "Rice", "3000", "10", "2000")
	sold := cashSale(t, o, rice, "4", e.cashAccount(t), "12000")
	require.Equal(t, 201, sold.code, sold.raw)

	seen := func(branch string) (sales int, onHand string) {
		o.branch = branch
		list := o.call(t, http.MethodGet, "/api/v1/sales", nil, "")
		require.Equal(t, 200, list.code, list.raw)
		stock := o.call(t, http.MethodGet, "/api/v1/stock/levels", nil, "")
		require.Equal(t, 200, stock.code, stock.raw)
		row := find(asList(stock.body["data"]), "product_id", rice)
		return len(asList(list.body["data"])), dec(row["qty_on_hand"]).String()
	}
	for branch, want := range map[string]struct {
		sales  int
		onHand string
	}{e.branch: {1, "6"}, other: {0, "0"}, "all": {1, "6"}} {
		sales, onHand := seen(branch)
		require.Equal(t, want.sales, sales, branch)
		require.Equal(t, want.onHand, onHand, branch)
	}
}
