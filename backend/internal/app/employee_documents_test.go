package app_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/require"
)

// FR-EMP-19: contracts and ID copies, readable only by employee managers and the employee.
func TestEmployeeDocuments(t *testing.T) {
	e := newEnv(t, defaultNow)
	o, app := e.owner, e.app.Fiber
	mgr, _ := e.user(t, "manager")
	staff, _ := e.user(t, "staff")
	emp := employee(t, o, "Rehema", "280000", nil)
	rehema := e.employeeLogin(t, emp, "staff")
	other := employee(t, o, "Juma", "280000", nil)
	pdf := []byte("%PDF-1.4\n1 0 obj\n<<>>\nendobj\n%%EOF\n")

	add := func(c *client, id, filename, kind string, body []byte) (int, string) {
		path := "/api/v1/employees/" + id + "/documents"
		if kind != "" {
			path += "?kind=" + kind
		}
		return upload(t, app, c, path, filename, "application/octet-stream", body)
	}

	// Upload checks the content and the kind; the manager's role may manage documents, staff may not.
	code, raw := add(o, emp, "contract.pdf", "", pdf)
	require.Equal(t, 201, code, raw)
	docID := regexpID(t, raw)
	code, raw = upload(t, app, o, "/api/v1/employees/"+emp+"/documents", "page.html", "text/html", []byte("<html></html>"))
	require.Equal(t, 415, code, raw)
	code, raw = add(o, emp, "id.pdf", "id_copy", pdf)
	require.Equal(t, 201, code, raw)
	require.Contains(t, raw, `"kind":"id_copy"`)
	code, raw = add(o, emp, "id.pdf", "passport_selfie", pdf)
	require.Equal(t, 422, code, raw)
	require.Contains(t, raw, "kind")
	code, raw = upload(t, app, o, "/api/v1/employees/"+emp+"/documents?name=Signed%20contract&kind=contract", "scan.pdf", "application/pdf", pdf)
	require.Equal(t, 201, code, raw)
	require.Contains(t, raw, "Signed contract")
	code, _ = add(staff, emp, "x.pdf", "", pdf)
	require.Equal(t, 403, code)
	code, _ = add(o, key(), "x.pdf", "", pdf)
	require.Equal(t, 404, code)

	list := o.call(t, http.MethodGet, "/api/v1/employees/"+emp+"/documents", nil, "")
	require.Equal(t, 200, list.code, list.raw)
	all := asList(list.body["data"])
	require.Len(t, all, 3)
	rows := []map[string]any{find(all, "name", "other: contract.pdf")}
	require.Contains(t, str(rows[0]["name"]), "contract.pdf")
	require.Equal(t, "application/pdf", str(rows[0]["mime_type"]))
	require.Equal(t, 403, staff.call(t, http.MethodGet, "/api/v1/employees/"+emp+"/documents", nil, "").code)
	require.Equal(t, 403, mgr.call(t, http.MethodGet, "/api/v1/employees/"+emp+"/documents", nil, "").code, "listing documents needs hr.employee.manage")

	// The download follows the same rule: managers and the employee, nobody else in the business.
	url := str(rows[0]["download_url"])
	require.NotEmpty(t, url)
	get := func(c *client) int {
		req := httptest.NewRequest(http.MethodGet, url, nil)
		req.Header.Set("Authorization", "Bearer "+c.token)
		resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
		require.NoError(t, err)
		resp.Body.Close()
		return resp.StatusCode
	}
	require.Equal(t, 200, get(o))
	require.Equal(t, 200, get(mgr))
	require.Equal(t, 200, get(rehema), "the employee can read their own documents")
	require.Equal(t, 404, get(staff), "another member of staff cannot")
	otherLogin := e.employeeLogin(t, other, "staff")
	require.Equal(t, 404, get(otherLogin), "nor can a colleague with a login")

	// Removal deletes the record and the stored file; it is audited.
	require.Equal(t, 404, o.call(t, http.MethodDelete, "/api/v1/employees/"+other+"/documents/"+docID, nil, "").code, "a document belongs to one employee")
	require.Equal(t, 403, staff.call(t, http.MethodDelete, "/api/v1/employees/"+emp+"/documents/"+docID, nil, "").code)
	require.Equal(t, 204, o.call(t, http.MethodDelete, "/api/v1/employees/"+emp+"/documents/"+docID, nil, "").code)
	require.Equal(t, 404, o.call(t, http.MethodDelete, "/api/v1/employees/"+emp+"/documents/"+docID, nil, "").code)
	require.Len(t, asList(o.call(t, http.MethodGet, "/api/v1/employees/"+emp+"/documents", nil, "").body["data"]), 2, "the other documents stay")
	require.Equal(t, 404, get(o), "the file is gone with its record")
	logs := o.call(t, http.MethodGet, "/api/v1/audit-logs?action=employee.document_added", nil, "")
	require.Equal(t, 200, logs.code, logs.raw)
	require.GreaterOrEqual(t, len(asList(logs.body["data"])), 2)
}
