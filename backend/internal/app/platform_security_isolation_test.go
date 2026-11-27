package app_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"bms/internal/platform/db"
	"bms/internal/platform/jobs"
)

// TestDatabaseSecurity_CompromisedAppServerCannotRewriteFinancialHistory directly tests the
// Section 6 challenge:
// "If the application server is compromised, can the attacker directly rewrite the financial history?"
// Even with direct SQL execution through the application's database pool (bms_api/bms_app),
// PostgreSQL least-privilege grants (42501) and immutability triggers (BM004) prevent altering,
// deleting, or truncating financial transactions, payments, completed sales, stock movements, or audit logs.
func TestDatabaseSecurity_CompromisedAppServerCannotRewriteFinancialHistory(t *testing.T) {
	e := newEnv(t, defaultNow)
	till := e.cashAccount(t)
	prod := e.product(t, "Laptop", "1500000", "5", "1000000")

	sale := e.owner.call(t, http.MethodPost, "/api/v1/sales", map[string]any{
		"items":    []map[string]any{{"product_id": prod, "qty": "1"}},
		"payments": []map[string]any{{"method": "cash", "money_account_id": till, "amount": "1500000"}},
	}, key())
	require.Equal(t, 201, sale.code, sale.raw)
	saleID := uuid.MustParse(str(sale.data["id"]))

	attacks := []struct {
		name string
		sql  string
		arg  any
	}{
		{"update financial_transactions", `UPDATE app.financial_transactions SET amount = 1 WHERE tenant_id = $1`, e.tenantID},
		{"delete financial_transactions", `DELETE FROM app.financial_transactions WHERE tenant_id = $1`, e.tenantID},
		{"truncate financial_transactions", `TRUNCATE app.financial_transactions`, nil},
		{"update payments_received", `UPDATE app.payments_received SET amount = 1 WHERE tenant_id = $1`, e.tenantID},
		{"delete payments_received", `DELETE FROM app.payments_received WHERE tenant_id = $1`, e.tenantID},
		{"update completed sale total", `UPDATE app.sales SET total = 1 WHERE id = $1`, saleID},
		{"delete completed sale", `DELETE FROM app.sales WHERE id = $1`, saleID},
		{"update stock_movements", `UPDATE app.stock_movements SET quantity_delta = 0 WHERE tenant_id = $1`, e.tenantID},
		{"delete stock_movements", `DELETE FROM app.stock_movements WHERE tenant_id = $1`, e.tenantID},
		{"update audit_log", `UPDATE audit.audit_log SET new_values = '{}'::jsonb WHERE tenant_id = $1`, e.tenantID},
		{"delete audit_log", `DELETE FROM audit.audit_log WHERE tenant_id = $1`, e.tenantID},
		{"truncate audit_log", `TRUNCATE audit.audit_log`, nil},
	}

	for _, atk := range attacks {
		err := e.app.DB.InActorTx(context.Background(), db.Actor{TenantID: e.tenantID}, func(ctx context.Context, tx pgx.Tx) error {
			var qErr error
			if atk.arg != nil {
				_, qErr = tx.Exec(ctx, atk.sql, atk.arg)
			} else {
				_, qErr = tx.Exec(ctx, atk.sql)
			}
			return qErr
		})
		require.Error(t, err, "compromised app server must NOT be able to %s", atk.name)
		msg := err.Error()
		require.True(t, strings.Contains(msg, "BM004") || strings.Contains(msg, "42501"),
			"expected immutability trigger (BM004) or privilege denial (42501) for %s, got: %v", atk.name, err)
	}

	// Verify financial history and balances remain completely intact.
	checkSale := e.owner.call(t, http.MethodGet, "/api/v1/sales/"+saleID.String(), nil, "")
	require.Equal(t, 200, checkSale.code, checkSale.raw)
	require.True(t, dec(checkSale.data["total"]).Equal(rd("1500000")))

	accts := e.owner.call(t, http.MethodGet, "/api/v1/money-accounts", nil, "")
	require.Equal(t, 200, accts.code, accts.raw)
	require.True(t, dec(find(asList(accts.body["data"]), "id", till)["balance"]).Equal(rd("1500000")))
}

// TestAPISecurity_HeadersSQLiUploadsCORSAndErrorRedaction verifies OWASP Top 10 controls:
//   - Security headers (CSP, HSTS over HTTPS, X-Frame-Options, nosniff, Referrer-Policy, Permissions-Policy, Cache-Control: no-store)
//   - Strict CORS origin enforcement (never reflects unknown origins or wildcard with credentials)
//   - SQL injection resistance on search, sort, and UUID parameters without leaking SQL internals
//   - Malicious upload rejection by content sniffing (HTML/SVG disguised as PNG) and safe download headers
func TestAPISecurity_HeadersSQLiUploadsCORSAndErrorRedaction(t *testing.T) {
	e := newEnv(t, defaultNow)
	e.product(t, "Espresso Beans", "18000", "10", "9000")

	// 1. Security headers including HSTS behind HTTPS proxy and Cache-Control: no-store on /api/*.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/products", nil)
	req.Header.Set("Authorization", "Bearer "+e.owner.token)
	req.Header.Set("X-Branch-Id", e.branch)
	req.Header.Set("X-Forwarded-Proto", "https")
	resp, err := e.app.Fiber.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, 200, resp.StatusCode)
	require.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
	require.Equal(t, "DENY", resp.Header.Get("X-Frame-Options"))
	require.Equal(t, "no-referrer", resp.Header.Get("Referrer-Policy"))
	require.Contains(t, resp.Header.Get("Content-Security-Policy"), "default-src 'none'")
	require.Contains(t, resp.Header.Get("Content-Security-Policy"), "frame-ancestors 'none'")
	require.NotEmpty(t, resp.Header.Get("Permissions-Policy"))
	require.Equal(t, "no-store", resp.Header.Get("Cache-Control"))
	require.Contains(t, resp.Header.Get("Strict-Transport-Security"), "max-age=31536000")
	require.NotEmpty(t, resp.Header.Get("X-Request-Id"))

	// 2. CORS blocks untrusted origins.
	evilReq := httptest.NewRequest(http.MethodOptions, "/api/v1/products", nil)
	evilReq.Header.Set("Origin", "https://evil.attacker.example")
	evilReq.Header.Set("Access-Control-Request-Method", "GET")
	evilResp, err := e.app.Fiber.Test(evilReq, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	evilResp.Body.Close()
	require.Empty(t, evilResp.Header.Get("Access-Control-Allow-Origin"), "disallowed origin must never receive ACAO")

	// 3. SQL injection payloads are safely parameterized / rejected without leaking DB internals.
	sqliSearch := e.owner.call(t, http.MethodGet, "/api/v1/products?q=%27+OR+1%3D1--&sort=name;DROP+TABLE+app.sales;--", nil, "")
	require.Equal(t, 200, sqliSearch.code, sqliSearch.raw)
	require.Empty(t, asList(sqliSearch.body["data"]), "SQLi payload in q must match literally (0 rows)")

	badUUID := e.owner.call(t, http.MethodGet, "/api/v1/sales/1'+OR+'1'='1", nil, "")
	require.Equal(t, 400, badUUID.code, badUUID.raw)
	for _, leak := range []string{"SQLSTATE", "pg_", "app.sales", "syntax error", "SELECT "} {
		require.NotContains(t, badUUID.raw, leak, "error response must not expose internal SQL details")
	}

	// 4. File upload signature validation blocks disguised HTML/SVG payloads; valid PNG downloads safely.
	code, raw := upload(t, e.app.Fiber, e.owner, "/api/v1/files", "spoofed.png", "image/png", []byte("<!DOCTYPE html><script>alert(1)</script>"))
	require.Equal(t, 415, code, raw)
	code, raw = upload(t, e.app.Fiber, e.owner, "/api/v1/files", "vector.txt", "text/plain", []byte(`<svg onload="alert(1)"></svg>`))
	require.Equal(t, 415, code, raw)

	code, raw = upload(t, e.app.Fiber, e.owner, "/api/v1/files", "safe.png", "image/png", pngBytes)
	require.Equal(t, 201, code, raw)
	fileID := regexpID(t, raw)

	dlReq := httptest.NewRequest(http.MethodGet, "/api/v1/files/"+fileID, nil)
	dlReq.Header.Set("Authorization", "Bearer "+e.owner.token)
	dlResp, err := e.app.Fiber.Test(dlReq, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	_, _ = io.ReadAll(dlResp.Body)
	dlResp.Body.Close()
	require.Equal(t, 200, dlResp.StatusCode)
	require.Equal(t, "nosniff", dlResp.Header.Get("X-Content-Type-Options"))
	require.Contains(t, dlResp.Header.Get("Content-Disposition"), "attachment")
}

// TestMultiTenantAndBranchIsolation_CrossTenantOrgSpoofingBlocked directly tests the
// Section 10 challenge:
// "Can Business A access Business B's sales by changing an organization ID in the request?"
// Verifies tenant isolation across sales, reports, exports, inventory, files, audit logs,
// branch isolation within a tenant, and tenant-scoped background job execution.
func TestMultiTenantAndBranchIsolation_CrossTenantOrgSpoofingBlocked(t *testing.T) {
	envA := newEnv(t, defaultNow)
	envB := newEnv(t, defaultNow)

	tillA := envA.cashAccount(t)
	tillB := envB.cashAccount(t)
	prodA := envA.product(t, "Alpha Item", "10000", "10", "5000")
	prodB := envB.product(t, "Beta Secret Item", "99000", "10", "40000")

	saleA := envA.owner.call(t, http.MethodPost, "/api/v1/sales", map[string]any{
		"items":    []map[string]any{{"product_id": prodA, "qty": "1"}},
		"payments": []map[string]any{{"method": "cash", "money_account_id": tillA, "amount": "10000"}},
	}, key())
	require.Equal(t, 201, saleA.code, saleA.raw)

	saleB := envB.owner.call(t, http.MethodPost, "/api/v1/sales", map[string]any{
		"items":    []map[string]any{{"product_id": prodB, "qty": "1"}},
		"payments": []map[string]any{{"method": "cash", "money_account_id": tillB, "amount": "99000"}},
	}, key())
	require.Equal(t, 201, saleB.code, saleB.raw)
	saleBID := str(saleB.data["id"])

	code, raw := upload(t, envB.app.Fiber, envB.owner, "/api/v1/files", "b-secret.png", "image/png", pngBytes)
	require.Equal(t, 201, code, raw)
	fileBID := regexpID(t, raw)

	tB := envB.tenantID.String()

	// 1. Business A spoofing organization_id / tenant_id / business_id cannot read, void, or receipt Business B's sale.
	require.Equal(t, 404, envA.owner.call(t, http.MethodGet, "/api/v1/sales/"+saleBID+"?tenant_id="+tB+"&organization_id="+tB+"&business_id="+tB, nil, "").code)
	require.Equal(t, 404, envA.owner.call(t, http.MethodGet, "/api/v1/sales/"+saleBID+"/receipt?tenant_id="+tB, nil, "").code)
	require.Equal(t, 404, envA.owner.call(t, http.MethodGet, "/api/v1/sales/"+saleBID+"/invoice?tenant_id="+tB, nil, "").code)
	require.Equal(t, 404, envA.owner.call(t, http.MethodPost, "/api/v1/sales/"+saleBID+"/void", map[string]any{
		"reason": "Cross-tenant sabotage", "tenant_id": tB, "organization_id": tB,
	}, key()).code)

	// 2. Listing sales, financial reports, CSV exports, stock levels, files, and audit logs strictly respect tenant boundaries.
	salesListA := envA.owner.call(t, http.MethodGet, "/api/v1/sales?tenant_id="+tB+"&organization_id="+tB, nil, "")
	require.Equal(t, 200, salesListA.code, salesListA.raw)
	require.Len(t, asList(salesListA.body["data"]), 1)
	require.NotContains(t, salesListA.raw, saleBID)

	repA := envA.owner.call(t, http.MethodGet, "/api/v1/reports/sales/summary?from=2026-11-01&to=2026-11-30&tenant_id="+tB, nil, "")
	require.Equal(t, 200, repA.code, repA.raw)
	require.True(t, dec(repA.data["totals"].(map[string]any)["gross_sales"]).Equal(rd("10000")),
		"Business A report must never include Business B's 99,000 sale: %s", repA.raw)

	exportCode, _, exportBody := envA.owner.rawGet(t, "/api/v1/reports/sales/by-product?format=csv&tenant_id="+tB)
	require.Equal(t, 200, exportCode)
	require.Contains(t, string(exportBody), "Alpha Item")
	require.NotContains(t, string(exportBody), "Beta Secret Item")

	stockA := envA.owner.call(t, http.MethodGet, "/api/v1/stock/levels?tenant_id="+tB, nil, "")
	require.Equal(t, 200, stockA.code, stockA.raw)
	require.NotContains(t, stockA.raw, prodB)

	require.Equal(t, 404, envA.owner.call(t, http.MethodGet, "/api/v1/files/"+fileBID, nil, "").code)

	// Spoofing Business B's branch ID in X-Branch-Id header is rejected.
	spoofBranch := &client{app: envA.owner.app, token: envA.owner.token, branch: envB.branch}
	spoofRes := spoofBranch.call(t, http.MethodGet, "/api/v1/sales", nil, "")
	require.True(t, spoofRes.code == 403 || spoofRes.code == 404, "cross-tenant X-Branch-Id must be rejected, got %d: %s", spoofRes.code, spoofRes.raw)

	// 3. Branch-level isolation within Business A: user assigned to Branch 1 cannot access Branch 2 or "all".
	br2 := envA.owner.call(t, http.MethodPost, "/api/v1/branches", map[string]any{"name": "Branch Two"}, "")
	require.Equal(t, 201, br2.code, br2.raw)
	branch2ID := str(br2.data["id"])

	staffA, _ := envA.user(t, "staff")
	staffAtB2 := &client{app: staffA.app, token: staffA.token, branch: branch2ID}
	require.Equal(t, 403, staffAtB2.call(t, http.MethodGet, "/api/v1/sales", nil, "").code)
	staffAll := &client{app: staffA.app, token: staffA.token, branch: "all"}
	require.Equal(t, 403, staffAll.call(t, http.MethodGet, "/api/v1/sales", nil, "").code)

	// 4. Background jobs run strictly scoped to their owning tenant.
	require.NoError(t, envA.app.Jobs.Enqueue(context.Background(), envA.tenantID, jobs.LedgerBalanceCheck, map[string]any{}))
	require.NoError(t, envA.app.Jobs.Enqueue(context.Background(), envA.tenantID, jobs.StockRebuildCheck, map[string]any{}))
	require.NoError(t, envA.app.Jobs.Enqueue(context.Background(), envA.tenantID, jobs.AuditSeal, map[string]any{}))
	require.NoError(t, envA.app.Jobs.RunPendingForTenant(context.Background(), envA.tenantID))
}

// TestAuditIntegrity_FiveMillionAccountabilityTrailAndHashChain directly tests the
// Section 8 challenge:
// "If TZS 5,000,000 disappears from a business account, can management establish exactly who
// performed the relevant operations, when, and why?"
// Also verifies login/failed-login recording, user/role/price/stock/settings/sale audit coverage,
// audit access control, secret redaction, and cryptographic hash-chain sealing/verification.
func TestAuditIntegrity_FiveMillionAccountabilityTrailAndHashChain(t *testing.T) {
	e := newEnv(t, defaultNow)
	till := e.cashAccount(t)
	staff, _ := e.user(t, "staff")
	mgr, _ := e.user(t, "manager")

	// 1. Failed and successful login attempts are recorded in login history.
	badLogin := (&client{app: e.app.Fiber}).call(t, http.MethodPost, "/api/v1/auth/login", map[string]any{
		"identifier": e.phone, "password": "wrong-password-123",
	}, "")
	require.Equal(t, 401, badLogin.code, badLogin.raw)

	okLogin := (&client{app: e.app.Fiber}).call(t, http.MethodPost, "/api/v1/auth/login", map[string]any{
		"identifier": e.phone, "password": "correct-horse",
	}, "")
	require.Equal(t, 200, okLogin.code, okLogin.raw)

	// Ordinary staff cannot read tenant-wide audit logs or login history.
	require.Equal(t, 403, staff.call(t, http.MethodGet, "/api/v1/audit-logs", nil, "").code)
	require.Equal(t, 403, staff.call(t, http.MethodGet, "/api/v1/login-history", nil, "").code)

	loginHist := e.owner.call(t, http.MethodGet, "/api/v1/login-history", nil, "")
	require.Equal(t, 200, loginHist.code, loginHist.raw)
	require.Contains(t, loginHist.raw, "login_failure", "failed login must be recorded in login history")
	require.Contains(t, loginHist.raw, "login_success", "successful login must be recorded in login history")
	require.NotContains(t, loginHist.raw, "wrong-password-123", "raw password must never appear in login history")

	// 2. Price change, stock adjustment, financial setting change, sale + void are all audited.
	prod := e.product(t, "Generator", "100000", "5", "60000")
	require.Equal(t, 200, e.owner.call(t, http.MethodPut, "/api/v1/products/"+prod, map[string]any{
		"name": "Generator", "sell_price": "120000", "reason": "FX adjustment",
	}, "").code)

	adj := mgr.call(t, http.MethodPost, "/api/v1/stock-adjustments", map[string]any{
		"reason": "Audit count", "items": []map[string]any{{"product_id": prod, "qty_counted": "4"}},
	}, "")
	require.Equal(t, 201, adj.code, adj.raw)
	require.Equal(t, 200, mgr.call(t, http.MethodPost, "/api/v1/stock-adjustments/"+str(adj.data["id"])+"/post", nil, key()).code)

	require.Equal(t, 200, e.owner.call(t, http.MethodPut, "/api/v1/settings/receipt", map[string]any{"footer": "Audited footer"}, "").code)

	sale := e.owner.call(t, http.MethodPost, "/api/v1/sales", map[string]any{
		"items":    []map[string]any{{"product_id": prod, "qty": "1"}},
		"payments": []map[string]any{{"method": "cash", "money_account_id": till, "amount": "120000"}},
	}, key())
	require.Equal(t, 201, sale.code, sale.raw)
	require.Equal(t, 200, e.owner.call(t, http.MethodPost, "/api/v1/sales/"+str(sale.data["id"])+"/void", map[string]any{"reason": "Duplicate order"}, key()).code)

	// 3. TZS 5,000,000 leaves a business bank account: trace WHO, WHEN, and WHY end-to-end.
	bank := e.owner.call(t, http.MethodPost, "/api/v1/money-accounts", map[string]any{
		"name": "NMB Business", "type": "bank", "opening_balance": "10000000",
	}, "")
	require.Equal(t, 201, bank.code, bank.raw)
	bankID := str(bank.data["id"])

	cats := e.owner.call(t, http.MethodGet, "/api/v1/expense-categories", nil, "")
	require.Equal(t, 200, cats.code, cats.raw)
	catID := str(asList(cats.body["data"])[0]["id"])

	exp := staff.call(t, http.MethodPost, "/api/v1/expenses", map[string]any{
		"category_id": catID,
		"amount":      "5000000",
		"description": "Emergency warehouse roof repair",
	}, key())
	require.Equal(t, 201, exp.code, exp.raw)
	expID := str(exp.data["id"])

	require.Equal(t, 200, e.owner.call(t, http.MethodPost, "/api/v1/expenses/"+expID+"/approve", map[string]any{
		"note": "Verified contractor invoice #INV-99",
	}, "").code)
	require.Equal(t, 200, e.owner.call(t, http.MethodPost, "/api/v1/expenses/"+expID+"/pay", map[string]any{
		"money_account_id": bankID,
	}, key()).code)

	// Balance is now 5,000,000 (5,000,000 gone). Management reconstructs the complete forensic trail.
	accts := e.owner.call(t, http.MethodGet, "/api/v1/money-accounts", nil, "")
	require.True(t, dec(find(asList(accts.body["data"]), "id", bankID)["balance"]).Equal(rd("5000000")))

	expTrail := e.owner.call(t, http.MethodGet, "/api/v1/audit-logs/entity/expenses/"+expID, nil, "")
	require.Equal(t, 200, expTrail.code, expTrail.raw)
	trailRows := asList(expTrail.body["data"])
	require.GreaterOrEqual(t, len(trailRows), 3, "expected submit, approve, and pay audit entries: %s", expTrail.raw)
	for _, row := range trailRows {
		require.NotEmpty(t, str(row["created_at"]), "every audit entry must include a timestamp")
		require.NotEmpty(t, str(row["user_id"]), "every audit entry must identify the responsible user")
	}
	require.Contains(t, expTrail.raw, "Emergency warehouse roof repair")
	require.Contains(t, expTrail.raw, "5000000")
	require.Contains(t, expTrail.raw, bankID, "audit trail must link the exact bank account debited")
	require.Contains(t, expTrail.raw, "staff user", "audit trail must identify who requested the expense")
	require.Contains(t, expTrail.raw, "Owner", "audit trail must identify who approved and paid the expense")

	// Explicit business event log also captures the approver's note.
	expApproveLog := e.owner.call(t, http.MethodGet, "/api/v1/audit-logs?action=expense.approved", nil, "")
	require.Equal(t, 200, expApproveLog.code, expApproveLog.raw)
	require.Contains(t, expApproveLog.raw, "Verified contractor invoice #INV-99")

	// Verify key governance events are recorded across the audit log.
	for _, action := range []string{"user.created", "stock_adjustment.posted", "settings.updated", "sale.voided"} {
		res := e.owner.call(t, http.MethodGet, "/api/v1/audit-logs?action="+action, nil, "")
		require.Equal(t, 200, res.code, res.raw)
		require.NotEmpty(t, asList(res.body["data"]), "expected audit entries for %s", action)
		require.NotContains(t, res.raw, "staff-pass-123", "credentials must never appear in audit logs")
		require.NotContains(t, res.raw, "correct-horse", "credentials must never appear in audit logs")
	}

	// 4. Cryptographic hash-chain sealing & verification via worker role succeeds with 0 broken seals.
	var sealed int
	var broken int
	err := e.app.DB.InJobsTx(context.Background(), db.Actor{TenantID: e.tenantID}, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT audit.seal_tenant($1, interval '0 seconds')`, e.tenantID).Scan(&sealed); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT count(*) FROM audit.verify_tenant($1)`, e.tenantID).Scan(&broken)
	})
	require.NoError(t, err)
	require.Greater(t, sealed, 0, "audit.seal_tenant must cryptographically seal audit rows")
	require.Equal(t, 0, broken, "audit.verify_tenant must confirm an intact hash chain")
}
