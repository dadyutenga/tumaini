package app_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInvoiceNumbersAndPrefixes(t *testing.T) {
	e := newEnv(t, defaultNow)
	o := e.owner
	product := e.product(t, "Cement", "20000", "50", "15000")
	cust := o.call(t, http.MethodPost, "/api/v1/customers", map[string]any{"name": "Builders Ltd", "phone": "0711777001", "credit_limit": "1000000"}, "")
	require.Equal(t, 201, cust.code, cust.raw)
	sale := func(c *client) string { return creditSale(t, c, str(cust.data["id"]), product, "1") }
	invoice := func(c *client, id string) map[string]any {
		res := c.call(t, http.MethodGet, "/api/v1/sales/"+id+"/invoice", nil, "")
		require.Equal(t, 200, res.code, res.raw)
		return res.data
	}

	// Sales receive sequential receipt numbers and render as PDF invoices.
	s1, s2 := sale(o), sale(o)
	i1 := invoice(o, s1)
	i2 := invoice(o, s2)
	require.NotEmpty(t, str(i1["invoice_number"]))
	require.Equal(t, str(i1["invoice_number"]), str(invoice(o, s1)["invoice_number"]), "asking again returns the same number")
	require.NotEqual(t, str(i1["invoice_number"]), str(i2["invoice_number"]), "the next sale gets the next number")
	pdf := o.call(t, http.MethodGet, "/api/v1/sales/"+s1+"/invoice?format=pdf", nil, "")
	require.Equal(t, 200, pdf.code)
	require.True(t, strings.HasPrefix(pdf.raw, "%PDF"))
	require.Contains(t, pdf.raw, str(i1["invoice_number"]), "the number is on the printed invoice")

	// The business invoice prefix setting is validated and updated for service invoices.
	require.Equal(t, 422, o.call(t, http.MethodPut, "/api/v1/settings/invoice", map[string]any{"prefix": "inv-2026"}, "").code)
	require.Equal(t, 200, o.call(t, http.MethodPut, "/api/v1/settings/invoice", map[string]any{"prefix": "TAX"}, "").code)
	require.Equal(t, str(i1["invoice_number"]), str(invoice(o, s1)["invoice_number"]), "an issued number never changes")

	// Same permission as viewing the sale.
	staff, _ := e.user(t, "staff")
	require.Equal(t, 404, staff.call(t, http.MethodGet, "/api/v1/sales/"+s1+"/invoice", nil, "").code, "staff see only their own sales")
}

// The business's language setting is what new users start with (FR-SET-09).
func TestNewUsersGetTheBusinessLanguage(t *testing.T) {
	e := newEnv(t, defaultNow)
	require.Equal(t, 200, e.owner.call(t, http.MethodPut, "/api/v1/settings/system", map[string]any{"language": "sw"}, "").code)
	_, id := e.user(t, "staff")
	require.Equal(t, "sw", str(e.owner.call(t, http.MethodGet, "/api/v1/users/"+id, nil, "").data["language"]))
	explicit := e.owner.call(t, http.MethodPost, "/api/v1/users", map[string]any{
		"name": "English speaker", "phone": uniquePhone(), "password": "english-pass-1", "role_id": e.roleID(t, "staff"), "branch_ids": []string{e.branch}, "language": "en",
	}, "")
	require.Equal(t, 201, explicit.code, explicit.raw)
	require.Equal(t, "en", str(explicit.data["language"]), "an explicit choice wins")
}
