package app_test

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func createCustomRoleUser(t *testing.T, e *env, roleName string, perms []string, branch string) (*client, string, string, string) {
	t.Helper()
	permEntries := make([]map[string]any, 0, len(perms))
	for _, k := range perms {
		permEntries = append(permEntries, map[string]any{"key": k, "scope": "all"})
	}
	roleRes := e.owner.call(t, http.MethodPost, "/api/v1/roles", map[string]any{
		"name":        roleName,
		"permissions": permEntries,
	}, "")
	require.Equal(t, 201, roleRes.code, roleRes.raw)
	roleID := str(roleRes.data["id"])

	phone := uniquePhone()
	pass := "custom-pass-123"
	if branch == "" {
		branch = e.branch
	}
	uRes := e.owner.call(t, http.MethodPost, "/api/v1/users", map[string]any{
		"name":       roleName + " User",
		"phone":      phone,
		"password":   pass,
		"role_id":    roleID,
		"branch_ids": []string{branch},
	}, "")
	require.Equal(t, 201, uRes.code, uRes.raw)
	userID := str(uRes.data["id"])
	c := &client{app: e.app.Fiber, token: login(t, e.app.Fiber, phone, pass), branch: branch}
	return c, userID, roleID, pass
}

// TestRBACCashierRejectedFromAdminAndFinancialSettings directly verifies the core challenge:
// if a cashier manually sends API requests to administrator-only or system-wide financial
// settings endpoints, the backend rejects every single one with 403.
func TestRBACCashierRejectedFromAdminAndFinancialSettings(t *testing.T) {
	e := newEnv(t, defaultNow)
	cashier, _ := e.user(t, "staff")

	checks := []struct {
		name   string
		method string
		path   string
		body   any
		idem   string
	}{
		{"update sales settings", http.MethodPut, "/api/v1/settings/sales", map[string]any{"allow_credit": false}, ""},
		{"update receipt settings", http.MethodPut, "/api/v1/settings/receipt", map[string]any{"footer": "Hacked"}, ""},
		{"read system settings", http.MethodGet, "/api/v1/settings", nil, ""},
		{"update business profile", http.MethodPut, "/api/v1/business", map[string]any{"name": "Pwned"}, ""},
		{"create branch", http.MethodPost, "/api/v1/branches", map[string]any{"name": "Rogue Branch"}, ""},
		{"update branch", http.MethodPut, "/api/v1/branches/" + e.branch, map[string]any{"name": "Renamed"}, ""},
		{"deactivate branch", http.MethodPost, "/api/v1/branches/" + e.branch + "/deactivate", nil, ""},
		{"create tax rate", http.MethodPost, "/api/v1/tax-rates", map[string]any{"name": "Zero", "rate": "0"}, ""},
		{"update payment methods", http.MethodPut, "/api/v1/payment-methods", map[string]any{"methods": []string{"cash"}}, ""},
		{"list users", http.MethodGet, "/api/v1/users", nil, ""},
		{"create user", http.MethodPost, "/api/v1/users", map[string]any{"name": "Rogue", "phone": uniquePhone(), "password": "password-1234", "role_id": e.roleID(t, "staff")}, ""},
		{"list roles", http.MethodGet, "/api/v1/roles", nil, ""},
		{"create role", http.MethodPost, "/api/v1/roles", map[string]any{"name": "Super", "permissions": []map[string]any{{"key": "settings.manage", "scope": "all"}}}, ""},
		{"view audit logs", http.MethodGet, "/api/v1/audit-logs", nil, ""},
		{"view login history", http.MethodGet, "/api/v1/login-history", nil, ""},
		{"trigger data backup export", http.MethodPost, "/api/v1/backups/export", nil, ""},
		{"create money account", http.MethodPost, "/api/v1/money-accounts", map[string]any{"name": "Secret", "type": "bank"}, ""},
		{"create money transfer", http.MethodPost, "/api/v1/money-transfers", map[string]any{"from_account_id": uuid.NewString(), "to_account_id": uuid.NewString(), "amount": "100"}, key()},
		{"create day closing", http.MethodPost, "/api/v1/day-closings", map[string]any{"date": "2026-11-16"}, ""},
		{"create payroll run", http.MethodPost, "/api/v1/payroll-runs", map[string]any{"period_year": 2026, "period_month": 11}, ""},
		{"create employee", http.MethodPost, "/api/v1/employees", map[string]any{"name": "Ghost"}, ""},
		{"create expense category", http.MethodPost, "/api/v1/expense-categories", map[string]any{"name": "Slush"}, ""},
	}

	for _, tc := range checks {
		t.Run(tc.name, func(t *testing.T) {
			res := cashier.call(t, tc.method, tc.path, tc.body, tc.idem)
			require.Equal(t, 403, res.code, "%s %s: %s", tc.method, tc.path, res.raw)
		})
	}
}

// TestRBACSalesAndInventoryStaffRestrictedFromReportsAndFinancialAdjustments verifies:
//   - Sales staff cannot access unauthorized financial reports.
//   - Managers without reports.business.view cannot access business P&L/balance-sheet/tax reports.
//   - Inventory staff cannot approve expenses, approve refunds, void transactions, transfer money,
//     write off debts, or approve financial approvals.
//   - Only authorized personnel can issue/approve refunds or void/reverse transactions, and
//     cashiers cannot cancel another cashier's return or an already-approved return.
func TestRBACSalesAndInventoryStaffRestrictedFromReportsAndFinancialAdjustments(t *testing.T) {
	e := newEnv(t, defaultNow)
	cashierA, _ := e.user(t, "staff")
	cashierB, _ := e.user(t, "staff")
	manager, _ := e.user(t, "manager")
	invStaff, _, _, _ := createCustomRoleUser(t, e, "Inventory Clerk", []string{
		"shop.product.view", "shop.stock.view", "shop.stock.manage",
	}, e.branch)

	// 1. Sales staff cannot access any financial or business report.
	for _, path := range []string{
		"/api/v1/reports/profit-loss",
		"/api/v1/reports/cash-flow",
		"/api/v1/reports/balance-sheet",
		"/api/v1/reports/tax",
		"/api/v1/reports/expenses",
		"/api/v1/reports/inventory/valuation",
		"/api/v1/reports/payroll/summary",
		"/api/v1/reports/sales/summary",
		"/api/v1/dashboard",
	} {
		res := cashierA.call(t, http.MethodGet, path, nil, "")
		require.Equal(t, 403, res.code, "staff should be denied %s: %s", path, res.raw)
	}

	// 2. Manager (operations reports only) cannot access business financial reports,
	// and on /dashboard cannot see bank/cash balances without finance.account.view.
	require.Equal(t, 200, manager.call(t, http.MethodGet, "/api/v1/reports/sales/summary", nil, "").code)
	mgrDash := manager.call(t, http.MethodGet, "/api/v1/dashboard", nil, "")
	require.Equal(t, 200, mgrDash.code, mgrDash.raw)
	require.Nil(t, mgrDash.data["cash_balances"], "manager without finance.account.view must not see cash/bank balances")
	for _, path := range []string{
		"/api/v1/reports/profit-loss",
		"/api/v1/reports/cash-flow",
		"/api/v1/reports/balance-sheet",
		"/api/v1/reports/tax",
		"/api/v1/reports/expenses",
		"/api/v1/reports/inventory/valuation",
		"/api/v1/reports/payroll/summary",
	} {
		res := manager.call(t, http.MethodGet, path, nil, "")
		require.Equal(t, 403, res.code, "manager without reports.business.view should be denied %s: %s", path, res.raw)
	}

	// 3. Seed a sale, return request, expense, customer, and payment for authorization tests.
	tillID := e.cashAccount(t)
	prodID := e.product(t, "Sugar", "5000", "20", "3000")

	custRes := e.owner.call(t, http.MethodPost, "/api/v1/customers", map[string]any{"name": "Credit Buyer"}, "")
	require.Equal(t, 201, custRes.code, custRes.raw)
	custID := str(custRes.data["id"])

	saleRes := cashierA.call(t, http.MethodPost, "/api/v1/sales", map[string]any{
		"customer_id": custID,
		"items":       []map[string]any{{"product_id": prodID, "qty": "2"}},
		"payments":    []map[string]any{{"method": "cash", "money_account_id": tillID, "amount": "10000"}},
	}, key())
	require.Equal(t, 201, saleRes.code, saleRes.raw)
	saleID := str(saleRes.data["id"])
	saleDetail := e.owner.call(t, http.MethodGet, "/api/v1/sales/"+saleID, nil, "")
	require.Equal(t, 200, saleDetail.code, saleDetail.raw)
	saleItems := asList(saleDetail.data["items"])
	require.NotEmpty(t, saleItems)
	saleItemID := str(saleItems[0]["id"])
	salePayments := asList(saleDetail.data["payments"])
	require.NotEmpty(t, salePayments)
	paymentID := str(salePayments[0]["id"])

	retRes := cashierA.call(t, http.MethodPost, "/api/v1/sales/"+saleID+"/returns", map[string]any{
		"reason":           "Damaged pack",
		"refund_method":    "cash",
		"money_account_id": tillID,
		"items":            []map[string]any{{"sale_item_id": saleItemID, "qty": "1"}},
	}, key())
	require.Equal(t, 201, retRes.code, retRes.raw)
	retID := str(retRes.data["id"])

	cats := e.owner.call(t, http.MethodGet, "/api/v1/expense-categories", nil, "")
	require.Equal(t, 200, cats.code, cats.raw)
	catID := str(asList(cats.body["data"])[0]["id"])
	expRes := cashierA.call(t, http.MethodPost, "/api/v1/expenses", map[string]any{
		"category_id": catID, "amount": "2500", "description": "Stationery",
	}, key())
	require.Equal(t, 201, expRes.code, expRes.raw)
	expID := str(expRes.data["id"])

	// 4. Inventory staff cannot approve or perform unauthorized financial adjustments.
	require.Equal(t, 403, invStaff.call(t, http.MethodPost, "/api/v1/expenses/"+expID+"/approve", nil, "").code)
	require.Equal(t, 403, invStaff.call(t, http.MethodPost, "/api/v1/sale-returns/"+retID+"/approve", nil, "").code)
	require.Equal(t, 403, invStaff.call(t, http.MethodPost, "/api/v1/sales/"+saleID+"/void", map[string]any{"reason": "nope"}, "").code)
	require.Equal(t, 403, invStaff.call(t, http.MethodPost, "/api/v1/payments/"+paymentID+"/void", map[string]any{"reason": "nope"}, "").code)
	require.Equal(t, 403, invStaff.call(t, http.MethodPost, "/api/v1/money-transfers", map[string]any{
		"from_account_id": tillID, "to_account_id": uuid.NewString(), "amount": "500",
	}, key()).code)
	require.Equal(t, 403, invStaff.call(t, http.MethodPost, "/api/v1/customers/"+custID+"/write-off", map[string]any{
		"amount": "100", "reason": "Unauthorized write-off",
	}, key()).code)

	// 5. Refund & reversal authorization + CancelReturn IDOR protection.
	// Neither cashierA nor cashierB can approve the refund or void the sale/payment.
	require.Equal(t, 403, cashierA.call(t, http.MethodPost, "/api/v1/sale-returns/"+retID+"/approve", nil, "").code)
	require.Equal(t, 403, cashierB.call(t, http.MethodPost, "/api/v1/sale-returns/"+retID+"/approve", nil, "").code)
	require.Equal(t, 403, cashierA.call(t, http.MethodPost, "/api/v1/sales/"+saleID+"/void", map[string]any{"reason": "undo"}, "").code)
	require.Equal(t, 403, cashierA.call(t, http.MethodPost, "/api/v1/payments/"+paymentID+"/void", map[string]any{"reason": "undo"}, "").code)
	// Cashier B cannot cancel Cashier A's return request (IDOR).
	require.Equal(t, 404, cashierB.call(t, http.MethodPost, "/api/v1/sale-returns/"+retID+"/cancel", nil, "").code)
	// Owner approves the return; once approved, Cashier A cannot cancel an already-approved return.
	require.Equal(t, 200, e.owner.call(t, http.MethodPost, "/api/v1/sale-returns/"+retID+"/approve", nil, "").code)
	require.Equal(t, 403, cashierA.call(t, http.MethodPost, "/api/v1/sale-returns/"+retID+"/cancel", nil, "").code)
}

// TestRBACSelfRoleAndPermissionModificationBlocked verifies that even a delegated user
// holding security.user.manage cannot alter their own role, modify their own permission
// overrides, edit their own role or the Admin role, assign an outranking role, or grant
// permissions they do not hold.
func TestRBACSelfRoleAndPermissionModificationBlocked(t *testing.T) {
	e := newEnv(t, defaultNow)
	delegated, delegatedID, delegatedRoleID, delegatedPass := createCustomRoleUser(t, e, "Delegated Admin", []string{
		"security.user.manage", "shop.sale.create",
	}, e.branch)
	_, staffID := e.user(t, "staff")
	adminRoleID := e.roleID(t, "admin")
	staffRoleID := e.roleID(t, "staff")

	// Re-authenticate so RequireRecentAuth passes and every 403 below proves pure RBAC enforcement.
	reauth := delegated.call(t, http.MethodPost, "/api/v1/auth/reauthenticate", map[string]any{"password": delegatedPass}, "")
	require.Equal(t, 200, reauth.code, reauth.raw)

	// Cannot change own role via PUT /users/:self.
	res := delegated.call(t, http.MethodPut, "/api/v1/users/"+delegatedID, map[string]any{
		"role_id": adminRoleID,
	}, "")
	require.Equal(t, 403, res.code, res.raw)

	// Cannot modify own permission overrides via PUT /users/:self/permissions.
	res = delegated.call(t, http.MethodPut, "/api/v1/users/"+delegatedID+"/permissions", map[string]any{
		"overrides": []map[string]any{
			{"permission_key": "shop.sale.create", "allowed": true, "reason": "self edit"},
		},
	}, "")
	require.Equal(t, 403, res.code, res.raw)

	// Cannot deactivate self.
	res = delegated.call(t, http.MethodPost, "/api/v1/users/"+delegatedID+"/deactivate", nil, "")
	require.Equal(t, 403, res.code, res.raw)

	// Cannot edit own role.
	res = delegated.call(t, http.MethodPut, "/api/v1/roles/"+delegatedRoleID, map[string]any{
		"name":        "Escalated Role",
		"permissions": []map[string]any{{"key": "security.user.manage", "scope": "all"}},
	}, "")
	require.Equal(t, 403, res.code, res.raw)

	// Cannot edit the Admin role.
	res = delegated.call(t, http.MethodPut, "/api/v1/roles/"+adminRoleID, map[string]any{
		"name":        "Hijacked Admin",
		"permissions": []map[string]any{{"key": "shop.sale.create", "scope": "all"}},
	}, "")
	require.Equal(t, 403, res.code, res.raw)

	// Cannot assign an outranking role (Admin) to another user.
	res = delegated.call(t, http.MethodPut, "/api/v1/users/"+staffID, map[string]any{
		"role_id": adminRoleID,
	}, "")
	require.Equal(t, 403, res.code, res.raw)

	// Cannot grant a permission they do not hold (privilege escalation).
	res = delegated.call(t, http.MethodPut, "/api/v1/users/"+staffID+"/permissions", map[string]any{
		"overrides": []map[string]any{
			{"permission_key": "settings.manage", "allowed": true, "reason": "escalate"},
		},
	}, "")
	require.Equal(t, 403, res.code, res.raw)

	// Sanity check: delegated admin CAN legitimately grant a permission they hold to lower-ranked staff.
	res = delegated.call(t, http.MethodPut, "/api/v1/users/"+staffID+"/permissions", map[string]any{
		"overrides": []map[string]any{
			{"permission_key": "shop.sale.create", "allowed": false, "reason": "temporary pause"},
		},
	}, "")
	require.Equal(t, 200, res.code, res.raw)
	_ = staffRoleID
}

// TestRBACBranchIsolationAndIDORProtection verifies branch-level access restrictions
// and cross-user / cross-branch IDOR protection across sales, search, expenses, clients,
// attachments, and stored files.
func TestRBACBranchIsolationAndIDORProtection(t *testing.T) {
	e := newEnv(t, defaultNow)
	branch2Res := e.owner.call(t, http.MethodPost, "/api/v1/branches", map[string]any{"name": "Branch Two"}, "")
	require.Equal(t, 201, branch2Res.code, branch2Res.raw)
	branch2 := str(branch2Res.data["id"])

	cashierA, _ := e.user(t, "staff", e.branch)
	cashierB, _ := e.user(t, "staff", e.branch)

	// 1. Branch-level restrictions for single-branch staff.
	branchesRes := cashierA.call(t, http.MethodGet, "/api/v1/branches", nil, "")
	require.Equal(t, 200, branchesRes.code, branchesRes.raw)
	require.Len(t, asList(branchesRes.body["data"]), 1, "branch-assigned staff only see their own branch")
	require.Equal(t, e.branch, str(asList(branchesRes.body["data"])[0]["id"]))

	// Cannot inspect another branch directly.
	require.Equal(t, 403, cashierA.call(t, http.MethodGet, "/api/v1/branches/"+branch2, nil, "").code)

	// Spoofing X-Branch-Id to Branch 2 or "all" is rejected with 403.
	spoofB2 := &client{app: e.app.Fiber, token: cashierA.token, branch: branch2}
	require.Equal(t, 403, spoofB2.call(t, http.MethodGet, "/api/v1/sales", nil, "").code)
	require.Equal(t, 403, spoofB2.call(t, http.MethodGet, "/api/v1/expenses", nil, "").code)

	spoofAll := &client{app: e.app.Fiber, token: cashierA.token, branch: "all"}
	require.Equal(t, 403, spoofAll.call(t, http.MethodGet, "/api/v1/sales", nil, "").code)

	// Owner can switch branches and use X-Branch-Id: all freely.
	ownerB2 := &client{app: e.app.Fiber, token: e.owner.token, branch: branch2}
	require.Equal(t, 200, ownerB2.call(t, http.MethodGet, "/api/v1/sales", nil, "").code)
	ownerAll := &client{app: e.app.Fiber, token: e.owner.token, branch: "all"}
	require.Equal(t, 200, ownerAll.call(t, http.MethodGet, "/api/v1/sales", nil, "").code)

	// 2. Cross-user IDOR isolation (Cashier A's records vs Cashier B).
	tillID := e.cashAccount(t)
	prodID := e.product(t, "Coffee", "4000", "20", "2000")

	saleA := cashierA.call(t, http.MethodPost, "/api/v1/sales", map[string]any{
		"items":    []map[string]any{{"product_id": prodID, "qty": "1"}},
		"payments": []map[string]any{{"method": "cash", "money_account_id": tillID, "amount": "4000"}},
	}, key())
	require.Equal(t, 201, saleA.code, saleA.raw)
	saleAID := str(saleA.data["id"])
	receiptNoA := str(saleA.data["number"])

	// Upload a file as Cashier A and attach it to Cashier A's sale.
	upCode, upRaw := upload(t, e.app.Fiber, cashierA, "/api/v1/files", "sale-slip.png", "image/png", pngBytes)
	require.Equal(t, 201, upCode, upRaw)
	saleFileID := regexpID(t, upRaw)
	attA := cashierA.call(t, http.MethodPost, "/api/v1/attachments", map[string]any{
		"entity_type": "sale", "entity_id": saleAID, "file_id": saleFileID, "label": "Signed slip",
	}, "")
	require.Equal(t, 201, attA.code, attA.raw)
	attAID := str(attA.data["id"])

	// Submit an expense with a receipt file as Cashier A.
	upExpCode, upExpRaw := upload(t, e.app.Fiber, cashierA, "/api/v1/files", "exp-receipt.png", "image/png", pngBytes)
	require.Equal(t, 201, upExpCode, upExpRaw)
	expFileID := regexpID(t, upExpRaw)
	cats := e.owner.call(t, http.MethodGet, "/api/v1/expense-categories", nil, "")
	catID := str(asList(cats.body["data"])[0]["id"])
	expA := cashierA.call(t, http.MethodPost, "/api/v1/expenses", map[string]any{
		"category_id": catID, "amount": "1500", "description": "Cashier A transport", "receipt_file_id": expFileID,
	}, key())
	require.Equal(t, 201, expA.code, expA.raw)
	expAID := str(expA.data["id"])

	// Create a client as Cashier A.
	clientA := cashierA.call(t, http.MethodPost, "/api/v1/clients", map[string]any{"name": "Private Client A"}, "")
	require.Equal(t, 201, clientA.code, clientA.raw)
	clientAID := str(clientA.data["id"])

	// Create an employee with an HR document as Owner.
	empRes := e.owner.call(t, http.MethodPost, "/api/v1/employees", map[string]any{"name": "Confidential Employee", "position": "Accountant"}, "")
	require.Equal(t, 201, empRes.code, empRes.raw)
	empID := str(empRes.data["id"])

	// Cashier B attempts IDOR against every single resource owned by Cashier A or HR:
	require.Equal(t, 404, cashierB.call(t, http.MethodGet, "/api/v1/sales/"+saleAID, nil, "").code)
	require.Equal(t, 404, cashierB.call(t, http.MethodGet, "/api/v1/sales/"+saleAID+"/receipt", nil, "").code)

	// Global search must NOT leak Cashier A's sale to Cashier B.
	searchB := cashierB.call(t, http.MethodGet, "/api/v1/search?q="+url.QueryEscape(receiptNoA), nil, "")
	require.Equal(t, 200, searchB.code, searchB.raw)
	for _, hit := range asList(searchB.body["data"]) {
		require.NotEqual(t, saleAID, str(hit["id"]), "global search leaked another cashier's sale")
	}

	// Attachments & files IDOR checks.
	require.Equal(t, 404, cashierB.call(t, http.MethodGet, "/api/v1/attachments?entity_type=sale&entity_id="+saleAID, nil, "").code)
	upBCode, upBRaw := upload(t, e.app.Fiber, cashierB, "/api/v1/files", "b.png", "image/png", pngBytes)
	require.Equal(t, 201, upBCode, upBRaw)
	fileBID := regexpID(t, upBRaw)
	require.Equal(t, 404, cashierB.call(t, http.MethodPost, "/api/v1/attachments", map[string]any{
		"entity_type": "sale", "entity_id": saleAID, "file_id": fileBID,
	}, "").code)
	require.Equal(t, 404, cashierB.call(t, http.MethodDelete, "/api/v1/attachments/"+attAID, nil, "").code)
	require.Equal(t, 404, cashierB.call(t, http.MethodGet, "/api/v1/files/"+saleFileID, nil, "").code)
	require.Equal(t, 404, cashierB.call(t, http.MethodGet, "/api/v1/files/"+expFileID, nil, "").code)
	require.Equal(t, 403, cashierB.call(t, http.MethodGet, "/api/v1/attachments?entity_type=employee&entity_id="+empID, nil, "").code)

	// Expenses IDOR checks.
	require.Equal(t, 404, cashierB.call(t, http.MethodGet, "/api/v1/expenses/"+expAID, nil, "").code)
	require.Equal(t, 404, cashierB.call(t, http.MethodPut, "/api/v1/expenses/"+expAID, map[string]any{
		"category_id": catID, "amount": "1500", "description": "tampered",
	}, "").code)
	require.Equal(t, 403, cashierB.call(t, http.MethodPost, "/api/v1/expenses/"+expAID+"/cancel", nil, "").code)

	// Clients read & write IDOR checks: a rep with services.client.manage (without
	// services.client.view_all) cannot view, edit, convert, or deactivate another user's client.
	ownOnlyRep, _, _, _ := createCustomRoleUser(t, e, "Own Client Rep", []string{"services.client.manage"}, e.branch)
	require.Equal(t, 404, ownOnlyRep.call(t, http.MethodGet, "/api/v1/clients/"+clientAID, nil, "").code)
	require.Equal(t, 404, ownOnlyRep.call(t, http.MethodPut, "/api/v1/clients/"+clientAID, map[string]any{"name": "Hijacked"}, "").code)
	require.Equal(t, 404, ownOnlyRep.call(t, http.MethodPost, "/api/v1/clients/"+clientAID+"/convert", nil, "").code)
	require.Equal(t, 404, ownOnlyRep.call(t, http.MethodPost, "/api/v1/clients/"+clientAID+"/deactivate", nil, "").code)

}

// TestRBACPermissionChangesTakeEffectImmediately verifies that revoking a permission via
// either a user override or a role edit takes effect on the very next API request using the
// exact same still-valid JWT access token — with zero stale privileges.
func TestRBACPermissionChangesTakeEffectImmediately(t *testing.T) {
	e := newEnv(t, defaultNow)
	analyst, analystID, roleID, _ := createCustomRoleUser(t, e, "Ops Analyst", []string{
		"reports.operations.view",
	}, e.branch)

	// 1. Access succeeds with the active token.
	require.Equal(t, 200, analyst.call(t, http.MethodGet, "/api/v1/reports/sales/summary", nil, "").code)

	// 2. Revoke via per-user deny override -> SAME token immediately gets 403.
	denyRes := e.owner.call(t, http.MethodPut, "/api/v1/users/"+analystID+"/permissions", map[string]any{
		"overrides": []map[string]any{
			{"permission_key": "reports.operations.view", "allowed": false, "reason": "Revoked immediately"},
		},
	}, "")
	require.Equal(t, 200, denyRes.code, denyRes.raw)
	require.Equal(t, 403, analyst.call(t, http.MethodGet, "/api/v1/reports/sales/summary", nil, "").code)

	// 3. Clear override -> SAME token immediately regains 200.
	clearRes := e.owner.call(t, http.MethodPut, "/api/v1/users/"+analystID+"/permissions", map[string]any{
		"overrides": []map[string]any{},
	}, "")
	require.Equal(t, 200, clearRes.code, clearRes.raw)
	require.Equal(t, 200, analyst.call(t, http.MethodGet, "/api/v1/reports/sales/summary", nil, "").code)

	// 4. Remove permission from the role itself -> SAME token immediately gets 403.
	roleEdit := e.owner.call(t, http.MethodPut, "/api/v1/roles/"+roleID, map[string]any{
		"name":        "Ops Analyst",
		"permissions": []map[string]any{{"key": "shop.product.view", "scope": "all"}},
	}, "")
	require.Equal(t, 200, roleEdit.code, roleEdit.raw)
	require.Equal(t, 403, analyst.call(t, http.MethodGet, "/api/v1/reports/sales/summary", nil, "").code)
}
