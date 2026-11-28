package app_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/require"
)

func TestInboxApprovalsNotificationsAuditBackups(t *testing.T) {
	e := newEnv(t, defaultNow)
	staff, _ := e.user(t, "staff")
	mgrPhone := uniquePhone()
	mgrRes := e.owner.call(t, http.MethodPost, "/api/v1/users", map[string]any{
		"name": "Approver", "phone": mgrPhone, "password": "manager-pass-1", "role_id": e.roleID(t, "manager"), "branch_ids": []string{e.branch},
	}, "")
	require.Equal(t, 201, mgrRes.code, mgrRes.raw)
	mgr := &client{app: e.app.Fiber, token: login(t, e.app.Fiber, mgrPhone, "manager-pass-1"), branch: e.branch}

	cash := e.cashAccount(t)
	rice := e.product(t, "Rice", "1000", "20", "500")
	discountSale := func(c *client) string {
		t.Helper()
		res := c.call(t, http.MethodPost, "/api/v1/sales", map[string]any{
			"items":    []map[string]any{{"product_id": rice, "qty": "1", "discount": map[string]any{"type": "percent", "value": "10"}}},
			"payments": []map[string]any{{"method": "cash", "money_account_id": cash, "amount": "900"}},
		}, key())
		require.Equal(t, 202, res.code, res.raw)
		return str(res.data["approval_request_id"])
	}

	// Staff cannot open the inbox. The owner sees the discount request, with a link to the sale.
	first := discountSale(staff)
	require.Equal(t, 403, staff.call(t, http.MethodGet, "/api/v1/approvals", nil, "").code)
	count := e.owner.call(t, http.MethodGet, "/api/v1/approvals/count", nil, "")
	require.Equal(t, 200, count.code, count.raw)
	require.GreaterOrEqual(t, int(dec(count.data["pending"]).IntPart()), 1)
	list := e.owner.call(t, http.MethodGet, "/api/v1/approvals?status=pending&kind=sale_discount", nil, "")
	require.Equal(t, 200, list.code, list.raw)
	row := find(asList(list.body["data"]), "id", first)
	require.Equal(t, true, row["can_decide"])
	require.NotNil(t, list.body["meta"])
	detail := e.owner.call(t, http.MethodGet, "/api/v1/approvals/"+first, nil, "")
	require.Equal(t, 200, detail.code, detail.raw)
	require.Contains(t, str(detail.data["subject_link"]), "/api/v1/sales/")
	require.Equal(t, 400, e.owner.call(t, http.MethodGet, "/api/v1/approvals?kind=nope", nil, "").code)

	// The requester cancels; nobody can decide a cancelled request.
	require.Equal(t, 403, mgr.call(t, http.MethodPost, "/api/v1/approvals/"+first+"/cancel", nil, "").code)
	cancel := staff.call(t, http.MethodPost, "/api/v1/approvals/"+first+"/cancel", nil, "")
	require.Equal(t, 200, cancel.code, cancel.raw)
	again := e.owner.call(t, http.MethodPost, "/api/v1/approvals/"+first+"/approve", map[string]any{}, key())
	require.Equal(t, 409, again.code, again.raw)

	// Approve is idempotent and needs a key. A manager's discount limit is 0, so the owner decides.
	second := discountSale(staff)
	require.Equal(t, 400, e.owner.call(t, http.MethodPost, "/api/v1/approvals/"+second+"/approve", map[string]any{}, "").code)
	k := key()
	ok := e.owner.call(t, http.MethodPost, "/api/v1/approvals/"+second+"/approve", map[string]any{"note": "fine"}, k)
	require.Equal(t, 200, ok.code, ok.raw)
	replay := e.owner.call(t, http.MethodPost, "/api/v1/approvals/"+second+"/approve", map[string]any{"note": "fine"}, k)
	require.Equal(t, 200, replay.code, replay.raw)
	require.JSONEq(t, ok.raw, replay.raw)

	// A manager cannot approve their own void request.
	big := e.product(t, "Generator", "2000000", "1", "100")
	sold := mgr.call(t, http.MethodPost, "/api/v1/sales", map[string]any{
		"items": []map[string]any{{"product_id": big, "qty": "1"}}, "payments": []map[string]any{{"method": "cash", "money_account_id": cash, "amount": "2000000"}},
	}, key())
	require.Equal(t, 201, sold.code, sold.raw)
	voided := mgr.call(t, http.MethodPost, "/api/v1/sales/"+str(sold.data["id"])+"/void", map[string]any{"reason": "changed mind"}, key())
	require.Equal(t, 202, voided.code, voided.raw)
	own := str(voided.data["approval_request_id"])
	self := mgr.call(t, http.MethodPost, "/api/v1/approvals/"+own+"/approve", map[string]any{}, key())
	require.Equal(t, 403, self.code, self.raw)
	require.Equal(t, 200, e.owner.call(t, http.MethodPost, "/api/v1/approvals/"+own+"/reject", map[string]any{"note": "keep the sale"}, "").code)

	// There is no PIN credential.
	require.Equal(t, 404, mgr.call(t, http.MethodPut, "/api/v1/auth/pin", map[string]any{"pin": "4321"}, "").code)
	third := discountSale(staff)
	require.Equal(t, 410, staff.call(t, http.MethodPost, "/api/v1/approvals/"+third+"/approve-with-pin", map[string]any{"approver": mgrPhone, "pin": "4321"}, key()).code)
	require.Equal(t, 200, e.owner.call(t, http.MethodPost, "/api/v1/approvals/"+third+"/approve", map[string]any{"note": "ok"}, key()).code)

	// The requester was notified of each decision; read and read-all clear the badge.
	unread := staff.call(t, http.MethodGet, "/api/v1/notifications/unread-count", nil, "")
	require.Equal(t, 200, unread.code, unread.raw)
	require.GreaterOrEqual(t, int(dec(unread.data["unread"]).IntPart()), 2)
	notes := staff.call(t, http.MethodGet, "/api/v1/notifications?unread=true&per_page=1", nil, "")
	require.Equal(t, 200, notes.code, notes.raw)
	require.Len(t, asList(notes.body["data"]), 1)
	one := str(asList(notes.body["data"])[0]["id"])
	require.Equal(t, 200, staff.call(t, http.MethodPost, "/api/v1/notifications/"+one+"/read", nil, "").code)
	require.Equal(t, 404, mgr.call(t, http.MethodPost, "/api/v1/notifications/"+one+"/read", nil, "").code)
	require.Equal(t, 200, staff.call(t, http.MethodPost, "/api/v1/notifications/read-all", nil, "").code)
	zero := staff.call(t, http.MethodGet, "/api/v1/notifications/unread-count", nil, "")
	require.EqualValues(t, 0, dec(zero.data["unread"]).IntPart())

	// Audit: the decisions are logged with the deciding user, and a record has its own history.
	logs := e.owner.call(t, http.MethodGet, "/api/v1/audit-logs?action=approval.&entity_type=approval_requests", nil, "")
	require.Equal(t, 200, logs.code, logs.raw)
	require.GreaterOrEqual(t, len(asList(logs.body["data"])), 3)
	hist := e.owner.call(t, http.MethodGet, "/api/v1/audit-logs/entity/approval_requests/"+third, nil, "")
	require.Equal(t, 200, hist.code, hist.raw)
	require.NotEmpty(t, asList(hist.body["data"])[0]["user_id"])
	require.Equal(t, 403, staff.call(t, http.MethodGet, "/api/v1/audit-logs", nil, "").code)
	require.Equal(t, 400, e.owner.call(t, http.MethodGet, "/api/v1/audit-logs?from=yesterday", nil, "").code)

	// Business export: Owner only, runs as a job, downloadable only by its requester or an Owner.
	require.Equal(t, 403, mgr.call(t, http.MethodPost, "/api/v1/backups/export", nil, "").code)
	start := e.owner.call(t, http.MethodPost, "/api/v1/backups/export", nil, "")
	require.Equal(t, 202, start.code, start.raw)
	exportID := str(start.data["id"])
	require.Equal(t, 409, e.owner.call(t, http.MethodPost, "/api/v1/backups/export", nil, "").code)
	e.runJobs(t)
	status := e.owner.call(t, http.MethodGet, "/api/v1/exports/"+exportID, nil, "")
	require.Equal(t, 200, status.code, status.raw)
	require.Equal(t, "done", status.data["status"], status.raw)
	require.Equal(t, 404, staff.call(t, http.MethodGet, "/api/v1/exports/"+exportID, nil, "").code)
	backups := e.owner.call(t, http.MethodGet, "/api/v1/backups", nil, "")
	require.Equal(t, 200, backups.code, backups.raw)
	require.Len(t, asList(backups.data["exports"]), 1)
	url := str(status.data["download_url"])
	zipBody := download(t, e.app.Fiber, e.owner, url)
	require.Equal(t, "PK", string(zipBody[:2]))
	require.NotContains(t, string(zipBody), "argon2id", "password hashes must not be exported")
	staffDownload := httptest.NewRequest(http.MethodGet, url, nil)
	staffDownload.Header.Set("Authorization", "Bearer "+staff.token)
	resp, err := e.app.Fiber.Test(staffDownload, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	require.Equal(t, 404, resp.StatusCode)

	// Global search and own login history.
	require.Equal(t, 201, e.owner.call(t, http.MethodPost, "/api/v1/clients", map[string]any{"name": "Kilimanjaro Roasters", "phone": "+255712000111"}, "").code)
	hits := e.owner.call(t, http.MethodGet, "/api/v1/search?q=Kilimanjaro", nil, "")
	require.Equal(t, 200, hits.code, hits.raw)
	require.NotEmpty(t, asList(hits.body["data"]))
	myLogins := staff.call(t, http.MethodGet, "/api/v1/me/login-history", nil, "")
	require.Equal(t, 200, myLogins.code, myLogins.raw)
	require.NotEmpty(t, asList(myLogins.body["data"]))

}

func download(t *testing.T, app *fiber.App, c *client, url string) []byte {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, 200, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return body
}
