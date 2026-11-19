package app_test

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/require"
)

var pngBytes = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89")

func upload(t *testing.T, app *fiber.App, c *client, path, filename, claimed string, body []byte) (int, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	h := make(map[string][]string)
	h["Content-Disposition"] = []string{`form-data; name="file"; filename="` + filename + `"`}
	h["Content-Type"] = []string{claimed}
	part, err := w.CreatePart(h)
	require.NoError(t, err)
	_, _ = part.Write(body)
	require.NoError(t, w.Close())
	req := httptest.NewRequest(http.MethodPost, path, &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	defer resp.Body.Close()
	var out bytes.Buffer
	_, _ = out.ReadFrom(resp.Body)
	return resp.StatusCode, out.String()
}

func TestFileUploadsAreCheckedByContent(t *testing.T) {
	e := newEnv(t, defaultNow)
	app, o := e.app.Fiber, e.owner

	// HTML sent as an image is refused: the claimed type and the name do not decide.
	code, raw := upload(t, app, o, "/api/v1/files", "receipt.png", "image/png", []byte("<html><script>alert(1)</script></html>"))
	require.Equal(t, 415, code, raw)
	code, raw = upload(t, app, o, "/api/v1/files", "logo.svg", "image/svg+xml", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`))
	require.Equal(t, 415, code, raw)
	code, _ = upload(t, app, o, "/api/v1/files", "empty.png", "image/png", nil)
	require.Equal(t, 400, code)

	// A real PNG is stored with the type detected from its bytes, even if the client lied.
	code, raw = upload(t, app, o, "/api/v1/files", `we\"ird name.png`, "text/html", pngBytes)
	require.Equal(t, 201, code, raw)
	id := regexpID(t, raw)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/files/"+id, nil)
	req.Header.Set("Authorization", "Bearer "+o.token)
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	require.Equal(t, 200, resp.StatusCode)
	require.Equal(t, "image/png", resp.Header.Get("Content-Type"))
	require.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
	require.Contains(t, resp.Header.Get("Content-Disposition"), "attachment")
	require.NotContains(t, resp.Header.Get("Content-Disposition"), "\r")
	require.NotContains(t, resp.Header.Get("Content-Disposition"), `filename="we"ird`, "a quote in the name must be escaped")
	require.Contains(t, resp.Header.Get("Content-Security-Policy"), "sandbox")

	// The logo must be an image; a PDF is refused there but allowed as a general file.
	pdf := []byte("%PDF-1.4\n1 0 obj\n<<>>\nendobj\n%%EOF\n")
	code, raw = upload(t, app, o, "/api/v1/business/logo", "logo.pdf", "image/png", pdf)
	require.Equal(t, 415, code, raw)
	code, _ = upload(t, app, o, "/api/v1/files", "doc.pdf", "application/octet-stream", pdf)
	require.Equal(t, 201, code)
	code, raw = upload(t, app, o, "/api/v1/business/logo", "logo.png", "image/png", pngBytes)
	require.Equal(t, 200, code, raw)

	// A file in one business cannot be read from another.
	other := newEnv(t, defaultNow)
	require.Equal(t, 404, other.owner.call(t, http.MethodGet, "/api/v1/files/"+id, nil, "").code)
	// Too large.
	big := append([]byte("%PDF-1.4\n"), bytes.Repeat([]byte("a"), 11<<20)...)
	// The server body limit (10 MB) rejects it before the handler; the test transport
	// reports that as an error, a real client sees 413.
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", "big.pdf")
	require.NoError(t, err)
	_, _ = part.Write(big)
	require.NoError(t, w.Close())
	bigReq := httptest.NewRequest(http.MethodPost, "/api/v1/files", &buf)
	bigReq.Header.Set("Content-Type", w.FormDataContentType())
	bigReq.Header.Set("Authorization", "Bearer "+o.token)
	bigResp, err := app.Test(bigReq, fiber.TestConfig{Timeout: 0})
	if err == nil {
		defer bigResp.Body.Close()
		require.Equal(t, 413, bigResp.StatusCode)
	} else {
		require.ErrorContains(t, err, "body size exceeds")
	}
}

func regexpID(t *testing.T, raw string) string {
	t.Helper()
	m := regexp.MustCompile(`"id":"([0-9a-f-]{36})"`).FindStringSubmatch(raw)
	require.NotNil(t, m, raw)
	return m[1]
}

func TestAttachmentsFlow(t *testing.T) {
	e := newEnv(t, defaultNow)
	code, raw := upload(t, e.app.Fiber, e.owner, "/api/v1/files", "contract.png", "image/png", pngBytes)
	require.Equal(t, 201, code, raw)
	fileID := regexpID(t, raw)

	cust := e.owner.call(t, http.MethodPost, "/api/v1/customers", map[string]any{"name": "Asha"}, "")
	require.Equal(t, 201, cust.code, cust.raw)
	custID := str(cust.data["id"])

	att := e.owner.call(t, http.MethodPost, "/api/v1/attachments", map[string]any{
		"entity_type": "customer",
		"entity_id":   custID,
		"file_id":     fileID,
		"label":       "Signed contract",
	}, "")
	require.Equal(t, 201, att.code, att.raw)
	attID := str(att.data["id"])

	list := e.owner.call(t, http.MethodGet, "/api/v1/attachments?entity_type=customer&entity_id="+custID, nil, "")
	require.Equal(t, 200, list.code, list.raw)
	require.Len(t, asList(list.body["data"]), 1)

	other := newEnv(t, defaultNow)
	require.Equal(t, 404, other.owner.call(t, http.MethodDelete, "/api/v1/attachments/"+attID, nil, "").code)

	del := e.owner.call(t, http.MethodDelete, "/api/v1/attachments/"+attID, nil, "")
	require.Equal(t, 200, del.code, del.raw)
	list2 := e.owner.call(t, http.MethodGet, "/api/v1/attachments?entity_type=customer&entity_id="+custID, nil, "")
	require.Len(t, asList(list2.body["data"]), 0)
}
