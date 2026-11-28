package app_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"

	"bms/internal/platform/middleware"
)

type twoBusinesses struct {
	e                       *env
	office                  string // a branch of the services business
	shop, services          *client
	shopUser, servicesUser  string
	roleID                  string
	saleID, saleNo, invID   string
	shopExpense, svcExpense string
}

// staffWith creates a person with the role, limited by extra fields, and signs them in.
func staffWith(t *testing.T, e *env, roleID string, extra map[string]any, branch string) (*client, string) {
	t.Helper()
	phone := uniquePhone()
	body := map[string]any{"name": "Person " + phone, "phone": phone, "password": "person-pass-123", "role_id": roleID}
	for k, v := range extra {
		body[k] = v
	}
	res := e.owner.call(t, http.MethodPost, "/api/v1/users", body, "")
	require.Equal(t, 201, res.code, res.raw)
	return &client{app: e.app.Fiber, token: login(t, e.app.Fiber, phone, "person-pass-123"), branch: branch}, str(res.data["id"])
}

// newTwoBusinesses records a shop sale and expense and a services invoice, payment and expense, and
// creates one person per business who holds permissions of both.
func newTwoBusinesses(t *testing.T) twoBusinesses {
	t.Helper()
	e := newEnv(t, defaultNow)
	o := e.owner
	out := twoBusinesses{e: e}
	office := o.call(t, http.MethodPost, "/api/v1/branches", map[string]any{"name": "Services office", "kind": "office"}, "")
	require.Equal(t, 201, office.code, office.raw)
	require.Equal(t, "B", str(office.data["business_unit"]))
	out.office = str(office.data["id"])

	cash := e.cashAccount(t)
	product := e.product(t, "Kamba Router", "12345", "10", "1000")
	sale := o.call(t, http.MethodPost, "/api/v1/sales", map[string]any{
		"items": []map[string]any{{"product_id": product, "qty": "1"}}, "payments": []map[string]any{{"method": "cash", "money_account_id": cash, "amount": "12345"}},
	}, key())
	require.Equal(t, 201, sale.code, sale.raw)
	out.saleID, out.saleNo = str(sale.data["id"]), str(sale.data["number"])

	cl := o.call(t, http.MethodPost, "/api/v1/clients", map[string]any{"name": "Kamba Consulting", "phone": uniquePhone()}, "")
	require.Equal(t, 201, cl.code, cl.raw)
	inv := o.call(t, http.MethodPost, "/api/v1/service-invoices", map[string]any{"client_id": cl.data["id"], "due_date": "2026-12-01",
		"lines": []map[string]any{{"description": "Registration", "unit_price": "77000"}}}, "")
	require.Equal(t, 201, inv.code, inv.raw)
	out.invID = str(inv.data["id"])
	require.Equal(t, 200, o.call(t, http.MethodPost, "/api/v1/service-invoices/"+out.invID+"/issue", nil, "").code)
	paid := o.call(t, http.MethodPost, "/api/v1/service-invoices/"+out.invID+"/payments", map[string]any{"amount": "33333", "method": "cash", "money_account_id": cash}, key())
	require.Equal(t, 201, paid.code, paid.raw)

	cats := o.call(t, http.MethodGet, "/api/v1/expense-categories", nil, "")
	category := str(find(asList(cats.body["data"]), "name", "Transport")["id"])
	expense := func(unit, what string) string {
		res := o.call(t, http.MethodPost, "/api/v1/expenses", map[string]any{"category_id": category, "amount": "5000", "description": what, "business_unit": unit}, key())
		require.Equal(t, 201, res.code, res.raw)
		return str(res.data["id"])
	}
	out.shopExpense, out.svcExpense = expense("A", "Shop delivery"), expense("B", "Registry trip")

	var permissions []map[string]any
	for _, code := range []string{"shop.product.view", "shop.product.manage", "shop.sale.create", "shop.sale.view_all", "shop.customer.view_all",
		"services.invoice.manage", "services.client.view_all", "services.client.manage", "services.design.manage", "services.project.view_all",
		"finance.transaction.view", "finance.expense.submit", "finance.expense.enter", "reports.business.view", "dashboard.business.view"} {
		permissions = append(permissions, map[string]any{"key": code, "scope": "all"})
	}
	role := o.call(t, http.MethodPost, "/api/v1/roles", map[string]any{"name": "Everything but one business", "permissions": permissions}, "")
	require.Equal(t, 201, role.code, role.raw)
	out.roleID = str(role.data["id"])
	out.shop, out.shopUser = staffWith(t, e, out.roleID, map[string]any{"business_units": []string{"A"}, "branch_ids": []string{e.branch}}, e.branch)
	out.services, out.servicesUser = staffWith(t, e, out.roleID, map[string]any{"business_units": []string{"B"}, "branch_ids": []string{out.office}}, out.office)
	return out
}

func ids(res result) []string {
	var out []string
	for _, row := range asList(res.body["data"]) {
		out = append(out, str(row["id"]))
	}
	return out
}

func (c *client) get(t *testing.T, path string) result {
	t.Helper()
	return c.call(t, http.MethodGet, "/api/v1"+path, nil, "")
}

func TestAShopOnlyPersonSeesNothingOfTheServicesBusiness(t *testing.T) {
	w := newTwoBusinesses(t)
	c := w.shop

	// The permissions are there; the business is not.
	for _, path := range []string{"/service-invoices", "/service-invoices/" + w.invID, "/clients", "/projects", "/design-jobs", "/service-quotations", "/renewals", "/tasks"} {
		res := c.get(t, path)
		require.Equal(t, 403, res.code, path+" "+res.raw)
		require.Equal(t, "business_forbidden", errCode(res.body), path)
	}
	require.Equal(t, 403, c.call(t, http.MethodPost, "/api/v1/clients", map[string]any{"name": "No"}, "").code)

	require.Contains(t, ids(c.get(t, "/sales")), w.saleID)
	require.Equal(t, []string{w.shopExpense}, ids(c.get(t, "/expenses")))
	require.Equal(t, 404, c.get(t, "/expenses/"+w.svcExpense).code)
	money := c.get(t, "/transactions?per_page=100")
	require.Equal(t, 200, money.code, money.raw)
	require.Contains(t, money.raw, "12345.00")
	require.NotContains(t, money.raw, "33333.00", "a services receipt")
	found := c.get(t, "/search?q=Kamba")
	require.Equal(t, 200, found.code, found.raw)
	require.NotContains(t, found.raw, "Kamba Consulting")
	require.Contains(t, found.raw, "Kamba Router")

	require.Equal(t, 403, c.get(t, "/reports/profit-loss?business=services").code)
	require.Equal(t, 403, c.get(t, "/dashboard?business=services").code)
	profit := c.get(t, "/reports/profit-loss")
	require.Equal(t, 200, profit.code, profit.raw)
	require.NotContains(t, profit.raw, "77000")
	dash := c.get(t, "/dashboard")
	require.Equal(t, 200, dash.code, dash.raw)
	require.Equal(t, "shop", str(dash.data["period"].(map[string]any)["business"]), "both is only offered to someone with both")
	require.NotContains(t, dash.raw, "77000")

	// Neither the switcher nor a branch of the other business gets round it.
	c.unit = "B"
	res := c.get(t, "/expenses")
	require.Equal(t, 403, res.code, res.raw)
	require.Equal(t, "business_forbidden", errCode(res.body))
	c.unit = "C"
	require.Equal(t, 400, c.get(t, "/expenses").code)
	c.unit = ""
	require.NotContains(t, c.get(t, "/branches").raw, w.office)
	c.branch = w.office
	require.Equal(t, 403, c.get(t, "/expenses").code)
	c.branch = w.e.branch

	me := c.get(t, "/me")
	require.Equal(t, 200, me.code, me.raw)
	require.Equal(t, []any{"A"}, me.data["business_units"])
	units := c.get(t, "/business-units")
	require.Len(t, asList(units.body["data"]), 1)
}

func TestAServicesOnlyPersonSeesNothingOfTheShop(t *testing.T) {
	w := newTwoBusinesses(t)
	c := w.services
	c.branch = "all" // the role may look at every branch of the business

	for _, path := range []string{"/sales", "/sales/" + w.saleID, "/stock/levels", "/suppliers", "/purchase-orders", "/tills"} {
		res := c.get(t, path)
		require.Equal(t, 403, res.code, path+" "+res.raw)
		require.Equal(t, "business_forbidden", errCode(res.body), path)
	}
	require.Equal(t, 403, c.call(t, http.MethodPost, "/api/v1/products", map[string]any{"name": "No", "sell_price": "1"}, "").code)
	require.Empty(t, asList(c.get(t, "/products").body["data"]), "shop products are not shown")

	require.Equal(t, 200, c.get(t, "/service-invoices/"+w.invID).code)
	require.Equal(t, []string{w.svcExpense}, ids(c.get(t, "/expenses")))
	money := c.get(t, "/transactions?per_page=100")
	require.Contains(t, money.raw, "33333.00")
	require.NotContains(t, money.raw, "12345.00", "a shop sale")
	found := c.get(t, "/search?q=Kamba")
	require.NotContains(t, found.raw, "Kamba Router")
	require.Contains(t, found.raw, "Kamba Consulting")
	require.Empty(t, asList(c.get(t, "/search?q="+w.saleNo).data["groups"]))
	require.Equal(t, 403, c.get(t, "/reports/profit-loss?business=shop").code)
	require.NotContains(t, c.get(t, "/reports/profit-loss").raw, "12345")

	// Work done in this business: money and records carry B.
	c.branch = w.office
	cl := c.call(t, http.MethodPost, "/api/v1/clients", map[string]any{"name": "Neema's Client", "phone": uniquePhone()}, "")
	require.Equal(t, 201, cl.code, cl.raw)
	job := c.call(t, http.MethodPost, "/api/v1/design-jobs", map[string]any{"client_id": cl.data["id"], "job_type": "logo", "title": "Logo", "price": "1000"}, "")
	require.Equal(t, 201, job.code, job.raw)
	cats := c.get(t, "/expense-categories")
	own := c.call(t, http.MethodPost, "/api/v1/expenses", map[string]any{"category_id": asList(cats.body["data"])[0]["id"], "amount": "100", "description": "Fare"}, key())
	require.Equal(t, 201, own.code, own.raw)
	require.Equal(t, "B", str(c.get(t, "/expenses/"+str(own.data["id"])).data["business_unit"]))
	shopExpense := c.call(t, http.MethodPost, "/api/v1/expenses", map[string]any{"category_id": asList(cats.body["data"])[0]["id"], "amount": "100", "description": "No", "business_unit": "A"}, key())
	require.Equal(t, 403, shopExpense.code, shopExpense.raw)
}

func TestTheSwitcherFiltersWhatIsSeenButNotWhatCanBeDone(t *testing.T) {
	w := newTwoBusinesses(t)
	o := w.e.owner
	o.branch = "all"
	require.ElementsMatch(t, []string{w.shopExpense, w.svcExpense}, ids(o.get(t, "/expenses")))
	o.unit = "A"
	require.Equal(t, []string{w.shopExpense}, ids(o.get(t, "/expenses")))
	require.NotContains(t, o.get(t, "/reports/profit-loss").raw, "77000")
	// A services page still shows services, and saving is not limited to the view.
	require.Equal(t, 200, o.get(t, "/service-invoices/"+w.invID).code)
	o.branch = w.e.branch
	paid := o.call(t, http.MethodPost, "/api/v1/service-invoices/"+w.invID+"/payments", map[string]any{"amount": "1000", "method": "cash", "money_account_id": w.e.cashAccount(t)}, key())
	require.Equal(t, 201, paid.code, paid.raw)
	o.unit, o.branch = "B", "all"
	require.Equal(t, []string{w.svcExpense}, ids(o.get(t, "/expenses")))
	o.unit = "all"
	both := o.get(t, "/dashboard")
	require.Equal(t, "all", str(both.data["period"].(map[string]any)["business"]))

	// A shop sale always carries the business of the branch it was made at; the services payment
	// taken at the shop's branch carries services.
	require.NoError(t, w.e.app.DB.InGlobalTx(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
		var wrong, services int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM app.sales s JOIN app.locations l ON l.id = s.location_id WHERE s.business_unit_id <> l.business_unit_id`).Scan(&wrong); err != nil {
			return err
		}
		require.Zero(t, wrong)
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM app.payments_received p JOIN app.business_units u ON u.id = p.business_unit_id WHERE u.code = 'B'`).Scan(&services); err != nil {
			return err
		}
		require.Equal(t, 2, services)
		return nil
	}))
}

func TestRowSecurityKeepsBusinessesApartForRawSQL(t *testing.T) {
	w := newTwoBusinesses(t)
	owner := w.e.ownerActor(t)
	var shop, services uuid.UUID
	require.NoError(t, w.e.app.DB.InGlobalTx(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT (SELECT id FROM app.business_units WHERE code = 'A'), (SELECT id FROM app.business_units WHERE code = 'B')`).Scan(&shop, &services)
	}))
	shopOnly := owner
	shopOnly.BusinessUnits = []uuid.UUID{shop}
	require.NoError(t, w.e.app.DB.InActorTx(context.Background(), shopOnly, func(ctx context.Context, tx pgx.Tx) error {
		for _, table := range []string{"expenses", "invoices", "payments_received", "financial_transactions", "sales", "products"} {
			var other int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM app.`+table+` WHERE business_unit_id = $1`, services).Scan(&other); err != nil {
				return err
			}
			require.Zero(t, other, table)
		}
		tag, err := tx.Exec(ctx, `UPDATE app.expenses SET description = 'changed' WHERE business_unit_id = $1`, services)
		require.Zero(t, tag.RowsAffected())
		return err
	}))
	err := w.e.app.DB.InActorTx(context.Background(), shopOnly, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE app.expenses SET business_unit_id = $1 WHERE business_unit_id = $2`, services, shop)
		return err
	})
	requireSQLState(t, err, "42501")
}

func requireSQLState(t *testing.T, err error, code string) {
	t.Helper()
	var pe *pgconn.PgError
	require.ErrorAs(t, err, &pe)
	require.Equal(t, code, pe.Code, pe.Message)
}

func TestBusinessAccessRules(t *testing.T) {
	w := newTwoBusinesses(t)
	e, o := w.e, w.e.owner

	// New people start with every business their creator has.
	_, both := e.user(t, "manager")
	require.Equal(t, []any{"A", "B"}, o.get(t, "/users/"+both).data["business_units"])
	require.Equal(t, []any{"A"}, o.get(t, "/users/"+w.shopUser).data["business_units"])

	// An Admin cannot be left without a business, through the API or around it.
	_, adminID := staffWith(t, e, e.roleID(t, "admin"), nil, e.branch)
	res := o.call(t, http.MethodPut, "/api/v1/users/"+adminID, map[string]any{"business_units": []string{"A"}}, "")
	require.Equal(t, 400, res.code, res.raw)
	actor := e.ownerActor(t)
	strip := func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM app.membership_business_units b USING app.memberships m, app.business_units u
			WHERE b.membership_id = m.id AND u.id = b.business_unit_id AND m.user_id = $1 AND u.code = 'B'`, uuid.MustParse(adminID))
		return err
	}
	requireSQLState(t, e.app.DB.InActorTx(context.Background(), actor, strip), "BM001")

	// A branch must belong to one of the person's businesses.
	res = o.call(t, http.MethodPut, "/api/v1/users/"+w.shopUser, map[string]any{"locations": []string{w.office}}, "")
	require.Equal(t, 400, res.code, res.raw)
	requireSQLState(t, e.app.DB.InActorTx(context.Background(), actor, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO app.membership_locations (membership_id, location_id) SELECT id, $2 FROM app.memberships WHERE user_id = $1`,
			uuid.MustParse(w.shopUser), uuid.MustParse(w.office))
		return err
	}), "BM001")
	res = o.call(t, http.MethodPut, "/api/v1/users/"+w.shopUser, map[string]any{"business_units": []string{}}, "")
	require.Equal(t, 400, res.code, res.raw)
	res = o.call(t, http.MethodPut, "/api/v1/users/"+w.shopUser, map[string]any{"business_units": []string{"A"}, "primary_business_unit": "B"}, "")
	require.Equal(t, 400, res.code, res.raw)

	// Widening someone's access takes effect at once and is recorded.
	res = o.call(t, http.MethodPut, "/api/v1/users/"+w.shopUser, map[string]any{"business_units": []string{"A", "B"}, "primary_business_unit": "B", "locations": []string{e.branch, w.office}}, "")
	require.Equal(t, 200, res.code, res.raw)
	require.Equal(t, "B", str(res.data["primary_business_unit"]))
	require.Equal(t, 200, w.shop.get(t, "/clients").code)
	logs := o.get(t, "/audit-logs?action=user.business_access_changed")
	require.NotEmpty(t, asList(logs.body["data"]), logs.raw)
	// Limited to two branches, they cannot look at all of them.
	w.shop.branch = "all"
	require.Equal(t, 403, w.shop.get(t, "/expenses").code)

	// Someone who manages users cannot hand out a business they do not work in.
	manage := o.call(t, http.MethodPost, "/api/v1/roles", map[string]any{"name": "Shop supervisor", "rank": 50, "permissions": []map[string]any{{"key": "security.user.manage", "scope": "all"}}}, "")
	require.Equal(t, 201, manage.code, manage.raw)
	supervisor, _ := staffWith(t, e, str(manage.data["id"]), map[string]any{"business_units": []string{"A"}}, e.branch)
	phone := uniquePhone()
	body := map[string]any{"name": "New", "phone": phone, "password": "person-pass-123", "role_id": manage.data["id"], "business_units": []string{"B"}}
	denied := supervisor.call(t, http.MethodPost, "/api/v1/users", body, "")
	require.Equal(t, 403, denied.code, denied.raw)
	delete(body, "business_units")
	made := supervisor.call(t, http.MethodPost, "/api/v1/users", body, "")
	require.Equal(t, 201, made.code, made.raw)
	require.Equal(t, []any{"A"}, made.data["business_units"])
}

func TestEachBusinessPrintsItsOwnDetails(t *testing.T) {
	w := newTwoBusinesses(t)
	o := w.e.owner
	for code, name := range map[string]string{"A": "Duka la Umeme", "B": "Huduma Bora"} {
		res := o.call(t, http.MethodPatch, "/api/v1/business-units/"+code, map[string]any{"display_name": name, "phone": "0712000999"}, "")
		require.Equal(t, 200, res.code, res.raw)
		require.Equal(t, "+255712000999", str(res.data["phone"]))
	}
	require.Equal(t, 403, w.shop.call(t, http.MethodPatch, "/api/v1/business-units/A", map[string]any{"display_name": "No"}, "").code)
	receipt := o.get(t, "/sales/"+w.saleID+"/receipt")
	require.Equal(t, 200, receipt.code, receipt.raw)
	require.Contains(t, receipt.raw, "Duka la Umeme")
	invoice := o.get(t, "/service-invoices/"+w.invID+"/pdf")
	require.Equal(t, 200, invoice.code)
	require.Contains(t, invoice.raw, "Huduma Bora")
	require.NotContains(t, invoice.raw, "Duka la Umeme")
	// An empty value goes back to the business profile's name.
	cleared := o.call(t, http.MethodPatch, "/api/v1/business-units/A", map[string]any{"display_name": ""}, "")
	require.Nil(t, cleared.data["display_name"])
	require.NotContains(t, o.get(t, "/sales/"+w.saleID+"/receipt").raw, "Duka la Umeme")
}

func TestShopStaffAreNotToldAboutServicesWork(t *testing.T) {
	e := newEnv(t, defaultNow)
	manager := e.roleID(t, "manager")
	shopManager, _ := staffWith(t, e, manager, map[string]any{"business_units": []string{"A"}}, e.branch)
	bothManager, _ := staffWith(t, e, manager, nil, e.branch)
	cl := e.owner.call(t, http.MethodPost, "/api/v1/clients", map[string]any{"name": "Renewing Client", "phone": uniquePhone()}, "")
	require.Equal(t, 201, cl.code, cl.raw)
	ren := e.owner.call(t, http.MethodPost, "/api/v1/renewals", map[string]any{"client_id": cl.data["id"], "item": "Business licence", "due_date": "2026-11-23", "amount": "1000"}, "")
	require.Equal(t, 201, ren.code, ren.raw)
	require.NoError(t, e.app.Jobs.Schedule(context.Background()))
	e.runJobs(t)
	renewals := func(c *client) int {
		n := 0
		for _, row := range asList(c.get(t, "/notifications?per_page=100").body["data"]) {
			if strings.Contains(strings.ToLower(str(row["title"])+str(row["type"])), "renewal") {
				n++
			}
		}
		return n
	}
	require.Positive(t, renewals(bothManager), "the reminder is sent to those who work in services")
	require.Zero(t, renewals(shopManager))
}

// A new part of the API must be assigned to a business, or declared shared, before it ships.
func TestEveryRouteBelongsToABusinessOrIsShared(t *testing.T) {
	e := newEnv(t, defaultNow)
	for _, route := range e.app.Fiber.GetRoutes(true) {
		if !strings.HasPrefix(route.Path, "/api/v1/") {
			continue
		}
		_, known := middleware.RouteBusiness(route.Method, route.Path)
		require.True(t, known, "add %s to routeBusiness in platform/middleware/business.go", route.Path)
	}
}
