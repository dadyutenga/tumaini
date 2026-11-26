package app_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func creditSale(t *testing.T, c *client, customer, product, qty string) string {
	t.Helper()
	res := c.call(t, http.MethodPost, "/api/v1/sales", map[string]any{"customer_id": customer, "items": []map[string]any{{"product_id": product, "qty": qty}}, "payments": []map[string]any{}}, key())
	require.Equal(t, 201, res.code, res.raw)
	require.Equal(t, "completed", str(res.data["status"]))
	return str(res.data["id"])
}

func TestCustomersAccountsAgedDebtAndWriteOff(t *testing.T) {
	// Start 100 days before defaultNow so we can advance the clock cleanly without violating sale immutability triggers.
	clk := &stepClock{t: defaultNow.AddDate(0, 0, -100)}
	e := newEnvWithClock(t, clk)
	o := e.owner
	staff, _ := e.user(t, "staff")
	mgr, _ := e.user(t, "manager")
	product := e.product(t, "Sack of rice", "10000", "100", "6000")

	// Create and validate. Phone is unique; a plain staff user cannot set a credit limit on creation.
	require.Equal(t, 400, o.call(t, http.MethodPost, "/api/v1/customers", map[string]any{"name": " "}, "").code)
	a := o.call(t, http.MethodPost, "/api/v1/customers", map[string]any{"name": "Asha Duka", "phone": "0711000001", "credit_limit": "200000", "payment_terms_days": 30}, "")
	require.Equal(t, 201, a.code, a.raw)
	aID := str(a.data["id"])
	require.Equal(t, "200000.00", str(a.data["credit_limit"]))
	require.Equal(t, 409, o.call(t, http.MethodPost, "/api/v1/customers", map[string]any{"name": "Copy", "phone": "0711000001"}, "").code)
	s1 := staff.call(t, http.MethodPost, "/api/v1/customers", map[string]any{"name": "Walk-in friend", "phone": "0711000009", "credit_limit": "999999"}, "")
	require.Equal(t, 201, s1.code, s1.raw)
	require.Equal(t, "0.00", str(s1.data["credit_limit"]), "staff cannot grant themselves credit")
	b := o.call(t, http.MethodPost, "/api/v1/customers", map[string]any{"name": "Baraka Stores", "phone": "0711000002", "credit_limit": "100000"}, "")
	require.Equal(t, 201, b.code, b.raw)
	bID := str(b.data["id"])

	// Partial update keeps the other fields; limits need their own permission; a missing customer is 404.
	up := o.call(t, http.MethodPut, "/api/v1/customers/"+aID, map[string]any{"address": "Kariakoo"}, "")
	require.Equal(t, 200, up.code, up.raw)
	require.Equal(t, "Asha Duka", str(up.data["name"]))
	require.Equal(t, "200000.00", str(up.data["credit_limit"]), "an update without a limit does not reset it")
	require.Equal(t, 404, o.call(t, http.MethodPut, "/api/v1/customers/"+key(), map[string]any{"name": "Ghost"}, "").code)
	require.Equal(t, 403, staff.call(t, http.MethodPut, "/api/v1/customers/"+aID+"/credit-limit", map[string]any{"credit_limit": "1", "payment_terms_days": 0}, "").code)
	require.Equal(t, 422, o.call(t, http.MethodPut, "/api/v1/customers/"+aID+"/credit-limit", map[string]any{"credit_limit": "-5", "payment_terms_days": 0}, "").code)

	// Re-login after long jumps so tokens stay fresh under the test clock.
	relogin := func(phone, pass string) string {
		return login(t, e.app.Fiber, phone, pass)
	}
	// Ancient sale to Baraka (100 days ago).
	_ = creditSale(t, o, bID, product, "1") // 10,000
	clk.Advance(55 * 24 * time.Hour)        // now 45 days ago
	o.token = relogin(e.phone, "correct-horse")
	old := creditSale(t, o, aID, product, "3") // 30,000
	clk.Advance(40 * 24 * time.Hour)           // now 5 days ago
	o.token = relogin(e.phone, "correct-horse")
	_ = creditSale(t, o, aID, product, "2") // 20,000
	clk.Advance(5 * 24 * time.Hour)         // now defaultNow
	o.token = relogin(e.phone, "correct-horse")

	debts := o.call(t, http.MethodGet, "/api/v1/customers/debts", nil, "")
	require.Equal(t, 200, debts.code, debts.raw)
	rows := asList(debts.body["data"])
	require.Len(t, rows, 2)
	require.Equal(t, "Asha Duka", str(rows[0]["name"]), "largest debt first")
	require.Equal(t, "50000.00", str(rows[0]["balance"]))
	require.Equal(t, "20000.00", str(rows[0]["bucket_0_30"]))
	require.Equal(t, "30000.00", str(rows[0]["bucket_31_60"]))
	require.Equal(t, "0.00", str(rows[0]["bucket_61_90"]))
	require.Equal(t, "10000.00", str(rows[1]["bucket_over_90"]))
	require.EqualValues(t, 2, dec(rows[0]["unpaid_sales"]).IntPart())

	// Per-customer: unpaid sales oldest first with their age, and the account summary.
	unpaid := o.call(t, http.MethodGet, "/api/v1/customers/"+aID+"/unpaid", nil, "")
	require.Equal(t, 200, unpaid.code, unpaid.raw)
	list := asList(unpaid.data["sales"])
	require.Len(t, list, 2)
	require.Equal(t, old, str(list[0]["sale_id"]))
	require.EqualValues(t, 45, dec(list[0]["age_days"]).IntPart())
	profile := o.call(t, http.MethodGet, "/api/v1/customers/"+aID, nil, "")
	require.Equal(t, "50000.00", str(profile.data["balance"]))
	require.Equal(t, 404, o.call(t, http.MethodGet, "/api/v1/customers/"+key(), nil, "").code)

	// A payment settles the oldest sale first and the statement shows both sides with a running balance.
	cash := e.cashAccount(t)
	pay := o.call(t, http.MethodPost, "/api/v1/customers/"+aID+"/payments", map[string]any{"amount": "35000", "method": "cash", "money_account_id": cash}, key())
	require.Equal(t, 201, pay.code, pay.raw)
	after := asList(o.call(t, http.MethodGet, "/api/v1/customers/"+aID+"/unpaid", nil, "").data["sales"])
	require.Len(t, after, 1, "the oldest sale is cleared")
	require.Equal(t, "15000.00", str(after[0]["balance"]))
	stmt := o.call(t, http.MethodGet, "/api/v1/customers/"+aID+"/statement?from=2026-01-01&to=2026-12-31", nil, "")
	require.Equal(t, 200, stmt.code, stmt.raw)
	require.Equal(t, "15000.00", str(stmt.data["closing_balance"]))
	pdf := o.call(t, http.MethodGet, "/api/v1/customers/"+aID+"/statement?from=2026-01-01&to=2026-12-31&format=pdf", nil, "")
	require.Equal(t, 200, pdf.code)
	require.True(t, strings.HasPrefix(pdf.raw, "%PDF"), "the statement downloads as a PDF")
	require.Equal(t, 400, o.call(t, http.MethodGet, "/api/v1/customers/"+aID+"/statement?from=2026-12-31&to=2026-01-01", nil, "").code)

	// Debt write-off was removed in the approved BMS schema (returns 410).
	_ = mgr
	wo := o.call(t, http.MethodPost, "/api/v1/customers/"+aID+"/write-off", map[string]any{"amount": "5000", "reason": "Customer disappeared"}, key())
	require.Equal(t, 410, wo.code, wo.raw)

	// Search and pagination of the list, with balances (view_all sees all; staff sees only own registered).
	found := o.call(t, http.MethodGet, "/api/v1/customers?q=baraka", nil, "")
	require.Equal(t, 200, found.code, found.raw)
	require.Len(t, asList(found.body["data"]), 1)
	require.NotNil(t, found.body["meta"])
	staff2, _ := e.user(t, "staff")
	require.Empty(t, asList(staff2.call(t, http.MethodGet, "/api/v1/customers?q=baraka", nil, "").body["data"]), "staff without view_all sees only customers they registered")

	// A customer without a balance can be deactivated and drops out of sales.
	require.Equal(t, 200, o.call(t, http.MethodPost, "/api/v1/customers/"+str(s1.data["id"])+"/deactivate", nil, "").code)
	blocked := o.call(t, http.MethodPost, "/api/v1/sales", map[string]any{"customer_id": str(s1.data["id"]), "items": []map[string]any{{"product_id": product, "qty": "1"}}, "payments": []map[string]any{}}, key())
	require.GreaterOrEqual(t, blocked.code, 400)
	require.Less(t, blocked.code, 500)

	// Another business sees none of these customers or debts.
	other := newEnv(t, defaultNow)
	require.Equal(t, 404, other.owner.call(t, http.MethodGet, "/api/v1/customers/"+aID, nil, "").code)
	require.Empty(t, asList(other.owner.call(t, http.MethodGet, "/api/v1/customers/debts", nil, "").body["data"]))
	require.Equal(t, 404, other.owner.call(t, http.MethodGet, "/api/v1/customers/"+aID+"/statement", nil, "").code)
}
