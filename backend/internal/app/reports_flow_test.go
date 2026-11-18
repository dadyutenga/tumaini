package app_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

// rawGet returns status, content type and body for file downloads.
func (c *client) rawGet(t *testing.T, path string) (int, string, []byte) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+c.token)
	if c.branch != "" {
		req.Header.Set("X-Branch-Id", c.branch)
	}
	resp, err := c.app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header.Get("Content-Type"), body
}

func rd(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func TestReportsAndDashboard(t *testing.T) {
	e := newEnv(t, defaultNow)
	c := e.owner
	riceID := e.product(t, "Rice", "2500", "10", "1000")
	sugarID := e.product(t, "Sugar", "1000", "5", "400")
	till := e.cashAccount(t)
	open := c.call(t, http.MethodPost, "/api/v1/tills/open", map[string]any{"money_account_id": till, "opening_float": "0"}, "")
	require.Equal(t, 201, open.code, open.raw)
	for _, body := range []map[string]any{
		{"items": []map[string]any{{"product_id": riceID, "qty": "2"}}, "payments": []map[string]any{{"method": "cash", "money_account_id": till, "amount": "5000"}}},
		{"items": []map[string]any{{"product_id": sugarID, "qty": "1"}}, "payments": []map[string]any{{"method": "cash", "money_account_id": till, "amount": "1000"}}},
	} {
		sale := c.call(t, http.MethodPost, "/api/v1/sales", body, key())
		require.Equal(t, 201, sale.code, sale.raw)
	}
	cats := c.call(t, http.MethodGet, "/api/v1/expense-categories", nil, "")
	require.Equal(t, 200, cats.code, cats.raw)
	exp := c.call(t, http.MethodPost, "/api/v1/expenses", map[string]any{
		"category_id": str(asList(cats.body["data"])[0]["id"]), "amount": "500", "description": "Transport",
	}, key())
	require.Equal(t, 201, exp.code, exp.raw)
	paid := c.call(t, http.MethodPost, "/api/v1/expenses/"+str(exp.data["id"])+"/pay", map[string]any{"money_account_id": till}, key())
	require.Equal(t, 200, paid.code, paid.raw)

	// Sales summary: 2 sales, net 6,000, cost 2,400, gross profit 3,600.
	sum := c.call(t, http.MethodGet, "/api/v1/reports/sales/summary?group_by=day&from=2026-11-01&to=2026-11-30", nil, "")
	require.Equal(t, 200, sum.code, sum.raw)
	tot := sum.data["totals"].(map[string]any)
	require.True(t, dec(tot["sales_count"]).Equal(rd("2")), sum.raw)
	require.True(t, dec(tot["net_sales"]).Equal(rd("6000")), sum.raw)
	require.True(t, dec(tot["cost"]).Equal(rd("2400")), sum.raw)
	require.True(t, dec(tot["gross_profit"]).Equal(rd("3600")), sum.raw)

	// P&L: net profit = sales net of tax - cost - expenses.
	pl := c.call(t, http.MethodGet, "/api/v1/reports/profit-loss?from=2026-11-01&to=2026-11-30&compare=previous", nil, "")
	require.Equal(t, 200, pl.code, pl.raw)
	require.True(t, dec(pl.data["net_profit"]).Equal(rd("3100")), pl.raw)
	require.True(t, dec(pl.data["gross_profit"]).Equal(rd("3600")), pl.raw)
	require.True(t, dec(pl.data["cost_of_goods_sold"]).Equal(rd("2400")), pl.raw)
	require.NotNil(t, pl.data["previous"])
	require.NotEmpty(t, pl.data["lines"])

	byProduct := c.call(t, http.MethodGet, "/api/v1/reports/sales/by-product?sort=-net_sales", nil, "")
	require.Equal(t, 200, byProduct.code, byProduct.raw)
	rows := asList(byProduct.data["rows"])
	require.Len(t, rows, 2)
	require.Equal(t, "Rice", str(rows[0]["product"]))
	require.True(t, dec(rows[0]["gross_profit"]).Equal(rd("3000")), byProduct.raw)

	// Valuation: 8 x 1,000 + 4 x 400 = 9,600, equal to on-hand value.
	val := c.call(t, http.MethodGet, "/api/v1/reports/inventory/valuation", nil, "")
	require.Equal(t, 200, val.code, val.raw)
	require.True(t, dec(val.data["totals"].(map[string]any)["value"]).Equal(rd("9600")), val.raw)
	// Movement timestamps come from the database clock, so use dates well before and after them.
	later := c.call(t, http.MethodGet, "/api/v1/reports/inventory/valuation?as_of=2099-12-31&group_by=category", nil, "")
	require.Equal(t, 200, later.code, later.raw)
	require.True(t, dec(later.data["totals"].(map[string]any)["value"]).Equal(rd("9600")), later.raw)
	before := c.call(t, http.MethodGet, "/api/v1/reports/inventory/valuation?as_of=2000-01-01", nil, "")
	require.Equal(t, 200, before.code, before.raw)
	require.Empty(t, asList(before.data["rows"]), before.raw)

	bs := c.call(t, http.MethodGet, "/api/v1/reports/balance-sheet", nil, "")
	require.Equal(t, 200, bs.code, bs.raw)
	require.Equal(t, true, bs.data["balanced"], bs.raw)
	require.True(t, dec(bs.data["assets"]).Equal(dec(bs.data["liabilities_and_equity"])), bs.raw)

	pm := c.call(t, http.MethodGet, "/api/v1/reports/sales/by-payment-method", nil, "")
	require.Equal(t, 200, pm.code, pm.raw)
	require.True(t, dec(find(asList(pm.data["rows"]), "method", "cash")["net"]).Equal(rd("6000")), pm.raw)

	cf := c.call(t, http.MethodGet, "/api/v1/reports/cash-flow?from=2026-11-01&to=2026-11-30", nil, "")
	require.Equal(t, 200, cf.code, cf.raw)
	require.True(t, dec(cf.data["totals"].(map[string]any)["closing"]).Equal(rd("5500")), cf.raw)

	br := c.call(t, http.MethodGet, "/api/v1/reports/branches", nil, "")
	require.Equal(t, 200, br.code, br.raw)
	require.True(t, dec(asList(br.data["rows"])[0]["net_profit"]).Equal(rd("3100")), br.raw)

	// Every report route answers for the owner.
	for _, path := range []string{
		"/reports/sales/by-category", "/reports/sales/by-cashier", "/reports/sales/by-customer", "/reports/tills",
		"/reports/expenses?group_by=payee", "/reports/expenses?group_by=month", "/reports/inventory/on-hand",
		"/reports/inventory/movements?type=sale", "/reports/inventory/low-stock", "/reports/inventory/dead-stock?days=1",
		"/reports/inventory/losses", "/reports/purchases?group_by=month", "/reports/purchases?group_by=product",
		"/reports/supplier-debt", "/reports/payroll/summary", "/reports/payroll/by-department",
		"/reports/payroll/deductions", "/reports/payroll/loans", "/reports/staff-performance", "/reports/tax",
		"/reports/sales/summary?group_by=month",
	} {
		res := c.call(t, http.MethodGet, "/api/v1"+path, nil, "")
		require.Equal(t, 200, res.code, path+" "+res.raw)
	}
	bad := c.call(t, http.MethodGet, "/api/v1/reports/sales/summary?group_by=year", nil, "")
	require.Equal(t, 400, bad.code, bad.raw)
	badDate := c.call(t, http.MethodGet, "/api/v1/reports/tax?from=2026-13-01", nil, "")
	require.Equal(t, 400, badDate.code, badDate.raw)

	// Exports.
	code, ctype, body := c.rawGet(t, "/api/v1/reports/sales/summary?format=xlsx")
	require.Equal(t, 200, code)
	require.Contains(t, ctype, "spreadsheetml")
	require.True(t, len(body) > 100)
	code, ctype, body = c.rawGet(t, "/api/v1/reports/profit-loss?format=pdf")
	require.Equal(t, 200, code)
	require.Equal(t, "application/pdf", ctype)
	require.True(t, strings.HasPrefix(string(body), "%PDF"))
	code, _, body = c.rawGet(t, "/api/v1/reports/sales/by-product?format=csv")
	require.Equal(t, 200, code)
	require.Contains(t, string(body), "Rice")

	// Dashboard: owner sees profit, manager without the profit permission does not.
	dash := c.call(t, http.MethodGet, "/api/v1/dashboard?period=month", nil, "")
	require.Equal(t, 200, dash.code, dash.raw)
	require.True(t, dec(dash.data["sales"].(map[string]any)["total"]).Equal(rd("6000")), dash.raw)
	require.NotNil(t, dash.data["profit"])
	require.NotNil(t, dash.data["gross_profit"])
	require.Len(t, asList(dash.data["sales_trend"]), 30)
	require.NotEmpty(t, asList(dash.data["top_products"]))
	manager, _ := e.user(t, "manager")
	md := manager.call(t, http.MethodGet, "/api/v1/dashboard?period=today", nil, "")
	require.Equal(t, 200, md.code, md.raw)
	require.NotNil(t, md.data["sales"])
	require.Nil(t, md.data["profit"])
	require.Nil(t, md.data["gross_profit"])
	_, hasValue := md.data["stock"].(map[string]any)["value"]
	require.False(t, hasValue, md.raw)
	mSales := manager.call(t, http.MethodGet, "/api/v1/reports/sales/by-product", nil, "")
	require.Equal(t, 200, mSales.code, mSales.raw)
	_, hasCost := asList(mSales.data["rows"])[0]["cost"]
	require.False(t, hasCost, mSales.raw)
	mPL := manager.call(t, http.MethodGet, "/api/v1/reports/profit-loss", nil, "")
	require.Equal(t, 403, mPL.code, mPL.raw)

	// Staff: no report access, but sees own dashboard.
	staff, _ := e.user(t, "staff")
	denied := staff.call(t, http.MethodGet, "/api/v1/reports/sales/summary", nil, "")
	require.Equal(t, 403, denied.code, denied.raw)
	denied = staff.call(t, http.MethodGet, "/api/v1/dashboard", nil, "")
	require.Equal(t, 403, denied.code, denied.raw)
	me := staff.call(t, http.MethodGet, "/api/v1/dashboard/me", nil, "")
	require.Equal(t, 200, me.code, me.raw)
	require.True(t, dec(me.data["sales_today"].(map[string]any)["count"]).IsZero(), me.raw)
	ownerMe := c.call(t, http.MethodGet, "/api/v1/dashboard/me", nil, "")
	require.Equal(t, 200, ownerMe.code, ownerMe.raw)
	require.True(t, dec(ownerMe.data["sales_today"].(map[string]any)["count"]).Equal(rd("2")), ownerMe.raw)
	require.NotNil(t, ownerMe.data["till"])

	// A second business sees nothing from the first.
	other := newEnv(t, defaultNow)
	os := other.owner.call(t, http.MethodGet, "/api/v1/reports/sales/summary", nil, "")
	require.Equal(t, 200, os.code, os.raw)
	require.Empty(t, asList(os.data["rows"]))
	// A branch outside the caller's scope is not found.
	foreign := other.owner.call(t, http.MethodGet, "/api/v1/reports/sales/summary?branch_id="+e.branch, nil, "")
	require.Equal(t, 200, foreign.code, foreign.raw) // owner scope is the own business; RLS hides the other tenant's rows
	require.Empty(t, asList(foreign.data["rows"]))
	mForeign := manager.call(t, http.MethodGet, "/api/v1/reports/sales/summary?branch_id="+other.branch, nil, "")
	require.Equal(t, 404, mForeign.code, mForeign.raw)
}
