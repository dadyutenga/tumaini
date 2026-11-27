package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"bms/internal/app"
	"bms/internal/config"
	"bms/internal/platform/clock"
)

func TestAcceptance(t *testing.T) {
	if os.Getenv("DATABASE_URL") == "" {
		t.Setenv("DATABASE_URL", "postgres://bms_api:bms_api_dev@127.0.0.1:5433/bms?sslmode=disable")
	}
	if os.Getenv("JOBS_DATABASE_URL") == "" {
		t.Setenv("JOBS_DATABASE_URL", "postgres://bms_jobs:bms_jobs_dev@127.0.0.1:5433/bms?sslmode=disable")
	}
	t.Setenv("JWT_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("RATE_LIMIT_SCOPE", uuid.NewString())
	t.Setenv("ARGON_MEMORY", "8192")
	t.Setenv("ARGON_TIME", "1")
	t.Setenv("ARGON_THREADS", "1")
	t.Setenv("COOKIE_SECURE", "false")
	t.Setenv("STORAGE_PATH", t.TempDir())
	cfg, err := config.Load()
	require.NoError(t, err)
	fixed := time.Date(2026, 11, 16, 5, 20, 0, 0, time.UTC)
	application, err := app.New(context.Background(), cfg, clock.Fixed{T: fixed})
	require.NoError(t, err)
	t.Cleanup(application.Close)
	c := &client{app: application.Fiber}

	reg := c.call(t, http.MethodPost, "/api/v1/auth/register", map[string]any{
		"business_name": "BMS Shop", "owner_name": "Amina", "phone": uniquePhone(), "password": "correct-horse",
	}, "")
	require.Equal(t, 201, reg.code, reg.raw)
	token := str(reg.data["access_token"])
	branchA := str(reg.data["branch_id"])
	c.token, c.branch = token, branchA

	roles := c.call(t, http.MethodGet, "/api/v1/roles", nil, "")
	require.Equal(t, 200, roles.code, roles.raw)
	var managerRole, staffRole string
	for _, row := range asList(roles.body["data"]) {
		if row["slug"] == "manager" {
			managerRole = str(row["id"])
		}
		if row["slug"] == "staff" {
			staffRole = str(row["id"])
		}
	}
	require.NotEmpty(t, managerRole)
	mgr := c.call(t, http.MethodPost, "/api/v1/users", map[string]any{
		"name": "Manager", "phone": uniquePhone(), "password": "manager-pass", "role_id": managerRole, "branch_ids": []string{branchA},
	}, "")
	require.Equal(t, 201, mgr.code, mgr.raw)
	cashier := c.call(t, http.MethodPost, "/api/v1/users", map[string]any{
		"name": "Cashier", "phone": uniquePhone(), "password": "cashier-pass", "role_id": staffRole, "branch_ids": []string{branchA},
	}, "")
	require.Equal(t, 201, cashier.code, cashier.raw)

	cashClient := &client{app: application.Fiber, token: login(t, application.Fiber, str(cashier.data["phone"]), "cashier-pass"), branch: branchA}
	denied := cashClient.call(t, http.MethodPost, "/api/v1/products", map[string]any{"name": "Nope", "sell_price": "10"}, "")
	require.Equal(t, 403, denied.code, denied.raw)

	branchB := c.call(t, http.MethodPost, "/api/v1/branches", map[string]any{"name": "Branch B", "code": "B"}, "")
	require.Equal(t, 201, branchB.code, branchB.raw)
	branchBID := str(branchB.data["id"])

	product := c.call(t, http.MethodPost, "/api/v1/products", map[string]any{"name": "Rice", "sell_price": "2500"}, "")
	require.Equal(t, 201, product.code, product.raw)
	productID := str(product.data["id"])

	in1 := c.call(t, http.MethodPost, "/api/v1/stock-entries", map[string]any{
		"direction": "in", "reason": "opening", "items": []map[string]any{{"product_id": productID, "qty": "10", "unit_cost": "1000"}},
	}, uuid.NewString())
	require.Equal(t, 201, in1.code, in1.raw)
	in2 := c.call(t, http.MethodPost, "/api/v1/stock-entries", map[string]any{
		"direction": "in", "reason": "opening", "items": []map[string]any{{"product_id": productID, "qty": "10", "unit_cost": "2000"}},
	}, uuid.NewString())
	require.Equal(t, 201, in2.code, in2.raw)

	levels := c.call(t, http.MethodGet, "/api/v1/stock/levels", nil, "")
	require.Equal(t, 200, levels.code, levels.raw)
	rice := find(asList(levels.body["data"]), "product_id", productID)
	require.True(t, dec(rice["qty_on_hand"]).Equal(decimal.RequireFromString("20")))
	require.True(t, dec(rice["avg_cost"]).Equal(decimal.RequireFromString("1500")))
	require.True(t, dec(rice["value"]).Equal(decimal.RequireFromString("30000")))
	card := c.call(t, http.MethodGet, "/api/v1/products/"+productID+"/stock-card", nil, "")
	require.Equal(t, 200, card.code, card.raw)
	require.Len(t, asList(card.body["data"]), 2)

	accounts := c.call(t, http.MethodGet, "/api/v1/money-accounts", nil, "")
	require.Equal(t, 200, accounts.code, accounts.raw)
	till := find(asList(accounts.body["data"]), "name", "Main till")
	tillID := str(till["id"])
	opened := c.call(t, http.MethodPost, "/api/v1/tills/open", map[string]any{"money_account_id": tillID, "opening_float": "0"}, "")
	require.Equal(t, 410, opened.code, opened.raw)

	saleBody := map[string]any{
		"items":    []map[string]any{{"product_id": productID, "qty": "3"}},
		"payments": []map[string]any{{"method": "cash", "money_account_id": tillID, "amount": "7500"}},
	}
	key := uuid.NewString()
	sale := c.call(t, http.MethodPost, "/api/v1/sales", saleBody, key)
	require.Equal(t, 201, sale.code, sale.raw)
	require.True(t, dec(sale.data["total"]).Equal(decimal.RequireFromString("7500")))
	saleID := str(sale.data["id"])
	again := c.call(t, http.MethodPost, "/api/v1/sales", saleBody, key)
	require.Equal(t, 201, again.code, again.raw)
	require.Equal(t, saleID, str(again.data["id"]))
	listed := c.call(t, http.MethodGet, "/api/v1/sales", nil, "")
	require.Equal(t, 1, int(dec(listed.body["meta"].(map[string]any)["total"]).IntPart()))

	levels = c.call(t, http.MethodGet, "/api/v1/stock/levels", nil, "")
	rice = find(asList(levels.body["data"]), "product_id", productID)
	require.True(t, dec(rice["qty_on_hand"]).Equal(decimal.RequireFromString("17")))
	accounts = c.call(t, http.MethodGet, "/api/v1/money-accounts", nil, "")
	till = find(asList(accounts.body["data"]), "id", tillID)
	require.True(t, dec(till["balance"]).Equal(decimal.RequireFromString("7500")), till)
	detail := c.call(t, http.MethodGet, "/api/v1/sales/"+saleID, nil, "")
	require.True(t, dec(detail.data["profit"]).Equal(decimal.RequireFromString("3000")), detail.raw)

	itemID := str(asList(detail.data["items"])[0]["id"])
	ret := c.call(t, http.MethodPost, "/api/v1/sales/"+saleID+"/returns", map[string]any{
		"reason": "customer changed mind", "refund_method": "cash", "money_account_id": tillID,
		"items": []map[string]any{{"sale_item_id": itemID, "qty": "1"}},
	}, uuid.NewString())
	require.Equal(t, 201, ret.code, ret.raw)
	approverRole := c.call(t, http.MethodPost, "/api/v1/roles", map[string]any{
		"name": "Return approver", "permissions": []map[string]any{{"key": "shop.return.approve", "scope": "all"}},
	}, "")
	require.Equal(t, 201, approverRole.code, approverRole.raw)
	approverPhone := uniquePhone()
	approverUser := c.call(t, http.MethodPost, "/api/v1/users", map[string]any{
		"name": "Return approver", "phone": approverPhone, "password": "approver-pass-1", "role_id": approverRole.data["id"], "branch_ids": []string{c.branch},
	}, "")
	require.Equal(t, 201, approverUser.code, approverUser.raw)
	approver := &client{app: application.Fiber, token: login(t, application.Fiber, approverPhone, "approver-pass-1"), branch: c.branch}
	require.Equal(t, 200, approver.call(t, http.MethodPost, "/api/v1/sale-returns/"+str(ret.data["id"])+"/approve", nil, "").code)
	require.Equal(t, 200, c.call(t, http.MethodPost, "/api/v1/sale-returns/"+str(ret.data["id"])+"/complete", map[string]any{"money_account_id": tillID}, uuid.NewString()).code)
	levels = c.call(t, http.MethodGet, "/api/v1/stock/levels", nil, "")
	rice = find(asList(levels.body["data"]), "product_id", productID)
	require.True(t, dec(rice["qty_on_hand"]).Equal(decimal.RequireFromString("18")), "on hand is "+dec(rice["qty_on_hand"]).String())
	accounts = c.call(t, http.MethodGet, "/api/v1/money-accounts", nil, "")
	till = find(asList(accounts.body["data"]), "id", tillID)
	require.True(t, dec(till["balance"]).Equal(decimal.RequireFromString("5000")))
	tooMany := c.call(t, http.MethodPost, "/api/v1/sales/"+saleID+"/returns", map[string]any{
		"reason": "too many", "refund_method": "cash", "money_account_id": tillID,
		"items": []map[string]any{{"sale_item_id": itemID, "qty": "3"}},
	}, uuid.NewString())
	require.Equal(t, 409, tooMany.code, tooMany.raw)

	c.as(t, branchBID)
	foreign := c.call(t, http.MethodPost, "/api/v1/sales", map[string]any{
		"items":    []map[string]any{{"product_id": productID, "qty": "1"}},
		"payments": []map[string]any{{"method": "cash", "money_account_id": tillID, "amount": "2500"}},
	}, uuid.NewString())
	_ = foreign
	c.token = login(t, application.Fiber, str(mgr.data["phone"]), "manager-pass")
	c.branch = branchA
	hidden := c.call(t, http.MethodGet, "/api/v1/sales/"+str(foreign.data["id"]), nil, "")
	if foreign.code == 201 {
		require.Equal(t, 404, hidden.code, hidden.raw)
	}
	c.token, c.branch = token, branchA

	rates := c.call(t, http.MethodGet, "/api/v1/tax-rates", nil, "")
	exempt := find(asList(rates.body["data"]), "name", "Exempt")
	updated := c.call(t, http.MethodPut, "/api/v1/tax-rates/"+str(exempt["id"]), map[string]any{"name": "Exempt", "rate": "18", "is_default": true, "is_active": true}, "")
	require.Equal(t, 200, updated.code, updated.raw)
	footer := c.call(t, http.MethodPut, "/api/v1/settings/receipt", map[string]any{"footer": "Karibu tena"}, "")
	require.Equal(t, 200, footer.code, footer.raw)
	oldReceipt := c.call(t, http.MethodGet, "/api/v1/sales/"+saleID+"/receipt", nil, "")
	require.Equal(t, "Karibu tena", str(oldReceipt.data["receipt_footer"]), "the receipt layout is read from the current settings")
	next := c.call(t, http.MethodPost, "/api/v1/sales", map[string]any{
		"items":    []map[string]any{{"product_id": productID, "qty": "1"}},
		"payments": []map[string]any{{"method": "cash", "money_account_id": tillID, "amount": "2500"}},
	}, uuid.NewString())
	require.Equal(t, 201, next.code, next.raw)
	require.True(t, dec(next.data["tax_total"]).Equal(decimal.RequireFromString("381.36")), "tax is included in the selling price")
	nextReceipt := c.call(t, http.MethodGet, "/api/v1/sales/"+str(next.data["id"])+"/receipt", nil, "")
	require.Equal(t, "Karibu tena", str(nextReceipt.data["receipt_footer"]))
	require.True(t, dec(oldReceipt.data["tax_total"]).IsZero())

	c.call(t, http.MethodPut, "/api/v1/tax-rates/"+str(exempt["id"]), map[string]any{"name": "Exempt", "rate": "0", "is_default": true, "is_active": true}, "")
	service := c.call(t, http.MethodPost, "/api/v1/products", map[string]any{"name": "Consulting", "sell_price": "50000"}, "")
	require.Equal(t, 201, service.code, service.raw)
	require.Equal(t, 201, c.call(t, http.MethodPost, "/api/v1/stock-entries", map[string]any{
		"direction": "in", "reason": "opening", "items": []map[string]any{{"product_id": service.data["id"], "qty": "2", "unit_cost": "0"}},
	}, uuid.NewString()).code)
	serviceID := str(service.data["id"])
	cust := c.call(t, http.MethodPost, "/api/v1/customers", map[string]any{"name": "Juma", "phone": uniquePhone()}, "")
	require.Equal(t, 201, cust.code, cust.raw)
	custID := str(cust.data["id"])
	c.call(t, http.MethodPut, "/api/v1/customers/"+custID+"/credit-limit", map[string]any{"credit_limit": "40000", "payment_terms_days": 30}, "")
	blocked := c.call(t, http.MethodPost, "/api/v1/sales", map[string]any{
		"customer_id": custID, "items": []map[string]any{{"product_id": serviceID, "qty": "1"}},
	}, uuid.NewString())
	require.Equal(t, 409, blocked.code, blocked.raw)
	c.call(t, http.MethodPut, "/api/v1/customers/"+custID+"/credit-limit", map[string]any{"credit_limit": "60000", "payment_terms_days": 30}, "")
	credit := c.call(t, http.MethodPost, "/api/v1/sales", map[string]any{
		"customer_id": custID, "items": []map[string]any{{"product_id": serviceID, "qty": "1"}},
	}, uuid.NewString())
	require.Equal(t, 201, credit.code, credit.raw)
	require.True(t, dec(credit.data["balance"]).Equal(decimal.RequireFromString("50000")))
	pay := c.call(t, http.MethodPost, "/api/v1/customers/"+custID+"/payments", map[string]any{
		"amount": "20000", "method": "cash", "money_account_id": tillID,
	}, uuid.NewString())
	require.Equal(t, 201, pay.code, pay.raw)
	got := c.call(t, http.MethodGet, "/api/v1/customers/"+custID, nil, "")
	require.True(t, dec(got.data["balance"]).Equal(decimal.RequireFromString("30000")), got.raw)
	stmt := c.call(t, http.MethodGet, "/api/v1/customers/"+custID+"/statement", nil, "")
	require.GreaterOrEqual(t, len(asList(stmt.data["lines"])), 2)

	sup := c.call(t, http.MethodPost, "/api/v1/suppliers", map[string]any{"name": "Mill " + uuid.NewString()[:8]}, "")
	require.Equal(t, 201, sup.code, sup.raw)
	po := c.call(t, http.MethodPost, "/api/v1/purchase-orders", map[string]any{
		"supplier_id": sup.data["id"], "items": []map[string]any{{"product_id": productID, "qty": "100", "unit_cost": "1000"}},
	}, "")
	require.Equal(t, 201, po.code, po.raw)
	sub := c.call(t, http.MethodPost, "/api/v1/purchase-orders/"+str(po.data["id"])+"/submit", map[string]any{}, "")
	require.Equal(t, 200, sub.code, sub.raw)
	r1 := c.call(t, http.MethodPost, "/api/v1/goods-receipts", map[string]any{"po_id": po.data["id"], "items": []map[string]any{{"product_id": productID, "qty": "60", "unit_cost": "1000"}}}, uuid.NewString())
	require.Equal(t, 201, r1.code, r1.raw)
	require.Equal(t, "partially_received", str(r1.data["po_status"]))
	r2 := c.call(t, http.MethodPost, "/api/v1/goods-receipts", map[string]any{"po_id": po.data["id"], "items": []map[string]any{{"product_id": productID, "qty": "40", "unit_cost": "1000"}}}, uuid.NewString())
	require.Equal(t, 201, r2.code, r2.raw)
	require.Equal(t, "received", str(r2.data["po_status"]))
	inv := c.call(t, http.MethodPost, "/api/v1/purchase-invoices", map[string]any{
		"supplier_id": sup.data["id"], "receipt_ids": []any{r1.data["id"], r2.data["id"]}, "supplier_ref": uuid.NewString()[:8],
	}, uuid.NewString())
	require.Equal(t, 410, inv.code, inv.raw)
	bank := c.call(t, http.MethodPost, "/api/v1/money-accounts", map[string]any{"name": "Bank " + uuid.NewString()[:6], "type": "bank", "opening_balance": "500000"}, "")
	require.Equal(t, 201, bank.code, bank.raw)
	paid := c.call(t, http.MethodPost, "/api/v1/suppliers/"+str(sup.data["id"])+"/payments", map[string]any{
		"amount": "100000", "method": "bank", "money_account_id": bank.data["id"],
	}, uuid.NewString())
	require.Equal(t, 201, paid.code, paid.raw)

	tr := c.call(t, http.MethodPost, "/api/v1/stock-transfers", map[string]any{
		"to_branch_id": branchBID, "items": []map[string]any{{"product_id": productID, "qty": "10"}},
	}, "")
	require.Equal(t, 201, tr.code, tr.raw)
	disp := c.call(t, http.MethodPost, "/api/v1/stock-transfers/"+str(tr.data["id"])+"/dispatch", map[string]any{}, uuid.NewString())
	require.Equal(t, 200, disp.code, disp.raw)
	c.branch = branchBID
	recv := c.call(t, http.MethodPost, "/api/v1/stock-transfers/"+str(tr.data["id"])+"/receive", map[string]any{
		"items": []map[string]any{{"product_id": productID, "qty_received": "10"}},
	}, uuid.NewString())
	require.Equal(t, 200, recv.code, recv.raw)
	require.Equal(t, "received", str(recv.data["status"]))
	c.branch = branchA
	notes := c.call(t, http.MethodGet, "/api/v1/notifications", nil, "")
	require.Equal(t, 200, notes.code, notes.raw)

	cats := c.call(t, http.MethodGet, "/api/v1/expense-categories", nil, "")
	catID := str(asList(cats.body["data"])[0]["id"])
	petty := c.call(t, http.MethodPost, "/api/v1/money-accounts", map[string]any{"name": "Petty " + uuid.NewString()[:6], "type": "petty_cash", "opening_balance": "200000"}, "")
	require.Equal(t, 201, petty.code, petty.raw)
	exp := cashClient.call(t, http.MethodPost, "/api/v1/expenses", map[string]any{"category_id": catID, "amount": "100000", "description": "Rent top-up"}, uuid.NewString())
	require.Equal(t, 201, exp.code, exp.raw)
	decided := c.call(t, http.MethodPost, "/api/v1/expenses/"+str(exp.data["id"])+"/approve", map[string]any{"note": "ok"}, "")
	require.Equal(t, 200, decided.code, decided.raw)
	paidExp := c.call(t, http.MethodPost, "/api/v1/expenses/"+str(exp.data["id"])+"/pay", map[string]any{"money_account_id": petty.data["id"]}, uuid.NewString())
	require.Equal(t, 200, paidExp.code, paidExp.raw)
	accounts = c.call(t, http.MethodGet, "/api/v1/money-accounts", nil, "")
	pettyNow := find(asList(accounts.body["data"]), "id", str(petty.data["id"]))
	require.True(t, dec(pettyNow["balance"]).Equal(decimal.RequireFromString("100000")))

	moved := c.call(t, http.MethodPost, "/api/v1/money-transfers", map[string]any{
		"from_account_id": bank.data["id"], "to_account_id": tillID, "amount": "50000",
	}, uuid.NewString())
	require.Equal(t, 201, moved.code, moved.raw)
	tb := c.call(t, http.MethodGet, "/api/v1/reports/trial-balance", nil, "")
	require.Equal(t, 410, tb.code, tb.raw)

	pl := c.call(t, http.MethodGet, "/api/v1/reports/profit-loss", nil, "")
	require.Equal(t, 200, pl.code, pl.raw)
	require.False(t, dec(pl.data["net_profit"]).IsZero() && dec(pl.data["income"]).IsZero())

	me := c.call(t, http.MethodGet, "/api/v1/me", nil, "")
	require.Equal(t, 200, me.code, me.raw)
	ownerID := str(me.data["user"].(map[string]any)["id"])
	emp := c.call(t, http.MethodPost, "/api/v1/employees", map[string]any{"name": "Amina", "base_pay": "600000", "pay_type": "monthly"}, "")
	require.Equal(t, 201, emp.code, emp.raw)
	linked := c.call(t, http.MethodPut, "/api/v1/users/"+ownerID, map[string]any{"employee_id": emp.data["id"]}, "")
	require.Equal(t, 200, linked.code, linked.raw)
	sched := c.call(t, http.MethodPut, "/api/v1/schedules", map[string]any{"weekday": 1, "start_time": "08:00", "end_time": "17:00"}, "")
	require.Equal(t, 410, sched.code, sched.raw)
	require.Equal(t, 410, c.call(t, http.MethodPost, "/api/v1/salary-components", map[string]any{"name": "Transport", "kind": "allowance", "value": "50000"}, "").code)
	loan := c.call(t, http.MethodPost, "/api/v1/loans", map[string]any{"employee_id": emp.data["id"], "principal": "50000", "monthly_deduction": "50000"}, uuid.NewString())
	require.Equal(t, 201, loan.code, loan.raw)
	payrollCash := c.call(t, http.MethodPost, "/api/v1/money-accounts", map[string]any{"name": "Payroll " + uuid.NewString()[:6], "type": "cash", "opening_balance": "1000000"}, "")
	require.Equal(t, 201, payrollCash.code, payrollCash.raw)
	disb := c.call(t, http.MethodPost, "/api/v1/loans/"+str(loan.data["id"])+"/disburse", map[string]any{"money_account_id": payrollCash.data["id"]}, uuid.NewString())
	require.Equal(t, 200, disb.code, disb.raw)
	run := c.call(t, http.MethodPost, "/api/v1/payroll-runs", map[string]any{"period_start": "2026-11-01", "period_end": "2026-11-30"}, "")
	require.Equal(t, 201, run.code, run.raw)
	calc := c.call(t, http.MethodPost, "/api/v1/payroll-runs/"+str(run.data["id"])+"/calculate", map[string]any{}, "")
	require.Equal(t, 200, calc.code, calc.raw)
	require.NotEmpty(t, asList(calc.data["payslips"]))
	payRole := c.call(t, http.MethodPost, "/api/v1/roles", map[string]any{
		"name": "Payroll approver", "permissions": []map[string]any{{"key": "hr.payroll.approve", "scope": "all"}},
	}, "")
	require.Equal(t, 201, payRole.code, payRole.raw)
	payPhone := uniquePhone()
	require.Equal(t, 201, c.call(t, http.MethodPost, "/api/v1/users", map[string]any{
		"name": "Payroll approver", "phone": payPhone, "password": "approver-pass-1", "role_id": payRole.data["id"], "branch_ids": []string{branchA},
	}, "").code)
	payer := &client{app: application.Fiber, token: login(t, application.Fiber, payPhone, "approver-pass-1"), branch: branchA}
	okApprove := payer.call(t, http.MethodPost, "/api/v1/payroll-runs/"+str(run.data["id"])+"/approve", map[string]any{}, uuid.NewString())
	require.Equal(t, 200, okApprove.code, okApprove.raw)
	paidRun := c.call(t, http.MethodPost, "/api/v1/payroll-runs/"+str(run.data["id"])+"/pay", map[string]any{"money_account_id": payrollCash.data["id"]}, uuid.NewString())
	require.Equal(t, 200, paidRun.code, paidRun.raw)
	require.Equal(t, "paid", str(paidRun.data["status"]))

	// Read APIs return persisted documents with decimal strings and proper scope.
	for _, path := range []string{"/suppliers", "/purchase-orders", "/goods-receipts", "/employees", "/loans", "/leave-types", "/leave-requests", "/commission-rules", "/pay-adjustments", "/payroll-runs", "/expenses", "/recurring-expenses"} {
		listed := c.call(t, http.MethodGet, "/api/v1"+path+"?per_page=1", nil, "")
		require.Equal(t, 200, listed.code, path+" "+listed.raw)
		require.Contains(t, listed.body, "meta", path)
		require.IsType(t, []any{}, listed.body["data"])
	}
	poDetail := c.call(t, http.MethodGet, "/api/v1/purchase-orders/"+str(po.data["id"]), nil, "")
	require.Equal(t, 200, poDetail.code, poDetail.raw)
	require.IsType(t, "", poDetail.data["total"])
	require.Len(t, asList(poDetail.data["items"]), 1)
	grnDetail := c.call(t, http.MethodGet, "/api/v1/goods-receipts/"+str(r1.data["id"]), nil, "")
	require.Equal(t, 200, grnDetail.code, grnDetail.raw)
	require.Len(t, asList(grnDetail.data["items"]), 1)
	runDetail := c.call(t, http.MethodGet, "/api/v1/payroll-runs/"+str(run.data["id"]), nil, "")
	require.Equal(t, 200, runDetail.code, runDetail.raw)
	require.Len(t, asList(runDetail.data["payslips"]), 1)
	managerClient := &client{app: application.Fiber, token: login(t, application.Fiber, str(mgr.data["phone"]), "manager-pass"), branch: branchA}
	profile := managerClient.call(t, http.MethodGet, "/api/v1/employees/"+str(emp.data["id"]), nil, "")
	require.Equal(t, 200, profile.code, profile.raw)
	require.NotContains(t, profile.data, "base_pay")
	ownerExpense := c.call(t, http.MethodPost, "/api/v1/expenses", map[string]any{"category_id": catID, "amount": "100", "description": "Owner only"}, uuid.NewString())
	require.Equal(t, 201, ownerExpense.code, ownerExpense.raw)
	ownExpenses := cashClient.call(t, http.MethodGet, "/api/v1/expenses", nil, "")
	require.Equal(t, 200, ownExpenses.code, ownExpenses.raw)
	require.Len(t, asList(ownExpenses.body["data"]), 1)
	forbiddenExpense := cashClient.call(t, http.MethodGet, "/api/v1/expenses/"+str(ownerExpense.data["id"]), nil, "")
	require.Equal(t, 404, forbiddenExpense.code, forbiddenExpense.raw)
	invalidFilter := c.call(t, http.MethodGet, "/api/v1/purchase-orders?supplier_id=broken", nil, "")
	require.Equal(t, 400, invalidFilter.code, invalidFilter.raw)
	c.branch = branchBID
	wrongBranch := c.call(t, http.MethodGet, "/api/v1/employees/"+str(emp.data["id"]), nil, "")
	require.Equal(t, 200, wrongBranch.code, wrongBranch.raw) // the Owner sees every branch
	c.branch = branchA

	audit := c.call(t, http.MethodGet, "/api/v1/audit-logs?action=sale.completed", nil, "")
	require.Equal(t, 200, audit.code, audit.raw)
	require.NotEmpty(t, asList(audit.body["data"]))

	other := &client{app: application.Fiber}
	reg2 := other.call(t, http.MethodPost, "/api/v1/auth/register", map[string]any{
		"business_name": "Other Shop", "owner_name": "Other", "phone": uniquePhone(), "password": "correct-horse",
	}, "")
	require.Equal(t, 201, reg2.code, reg2.raw)
	other.token = str(reg2.data["access_token"])
	other.branch = str(reg2.data["branch_id"])
	empty := other.call(t, http.MethodGet, "/api/v1/products", nil, "")
	require.Equal(t, 200, empty.code, empty.raw)
	require.Empty(t, asList(empty.body["data"]))
	missing := other.call(t, http.MethodGet, "/api/v1/sales/"+saleID, nil, "")
	require.Equal(t, 404, missing.code, missing.raw)
}

func login(t *testing.T, app *fiber.App, phone, password string) string {
	t.Helper()
	c := &client{app: app}
	res := c.call(t, http.MethodPost, "/api/v1/auth/login", map[string]any{"identifier": phone, "password": password}, "")
	require.Equal(t, 200, res.code, res.raw)
	return str(res.data["access_token"])
}

type client struct {
	app    *fiber.App
	token  string
	branch string
}

type result struct {
	code int
	body map[string]any
	data map[string]any
	raw  string
}

func (c *client) as(t *testing.T, branch string) { c.branch = branch }

func (c *client) call(t *testing.T, method, path string, body any, idem string) result {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		require.NoError(t, json.NewEncoder(&buf).Encode(body))
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if c.branch != "" {
		req.Header.Set("X-Branch-Id", c.branch)
	}
	if idem != "" {
		req.Header.Set("Idempotency-Key", idem)
	}
	resp, err := c.app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var parsed map[string]any
	_ = json.Unmarshal(raw, &parsed)
	data, _ := parsed["data"].(map[string]any)
	return result{code: resp.StatusCode, body: parsed, data: data, raw: string(raw)}
}

func str(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

func dec(v any) decimal.Decimal {
	d, err := decimal.NewFromString(str(v))
	if err != nil {
		return decimal.Zero
	}
	return d
}

func asList(v any) []map[string]any {
	switch t := v.(type) {
	case []any:
		out := make([]map[string]any, 0, len(t))
		for _, item := range t {
			if m, ok := item.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	default:
		return nil
	}
}

func find(rows []map[string]any, key, want string) map[string]any {
	for _, row := range rows {
		if str(row[key]) == want {
			return row
		}
	}
	return map[string]any{}
}

var phoneSeq uint64 = uint64(time.Now().UnixNano() % 100000000)

func uniquePhone() string {
	n := atomic.AddUint64(&phoneSeq, 1) % 100000000
	return fmt.Sprintf("+2557%08d", n)
}

func init() {}
