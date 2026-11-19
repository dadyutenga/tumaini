package app_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// FR-INV-32: batch number and expiry date per stock receipt, and a report of what is expiring.
func TestBatchAndExpiryAreRecordedPerReceipt(t *testing.T) {
	e := newEnv(t, defaultNow) // business date 2026-11-16
	o := e.owner
	milk := e.product(t, "Milk 1L", "2000", "", "")
	yogurt := e.product(t, "Yogurt", "1500", "", "")
	rice := e.product(t, "Rice", "2500", "", "")

	// Stock in with a batch and an expiry; a bad date is a validation error.
	bad := o.call(t, http.MethodPost, "/api/v1/stock-entries", map[string]any{"direction": "in", "reason": "opening",
		"items": []map[string]any{{"product_id": milk, "qty": "10", "unit_cost": "1000", "expiry_date": "next week"}}}, key())
	require.Equal(t, 422, bad.code, bad.raw)
	in := o.call(t, http.MethodPost, "/api/v1/stock-entries", map[string]any{"direction": "in", "reason": "opening", "items": []map[string]any{
		{"product_id": milk, "qty": "10", "unit_cost": "1000", "batch_no": "M-2611", "expiry_date": "2026-11-26"},
		{"product_id": yogurt, "qty": "5", "unit_cost": "800", "batch_no": "Y-OLD", "expiry_date": "2026-11-01"},
		{"product_id": rice, "qty": "50", "unit_cost": "1800"},
	}}, key())
	require.Equal(t, 201, in.code, in.raw)
	entry := o.call(t, http.MethodGet, "/api/v1/stock-entries/"+str(in.data["id"]), nil, "")
	require.Equal(t, 200, entry.code, entry.raw)
	m := find(asList(entry.data["items"]), "product_id", milk)
	require.Equal(t, "M-2611", str(m["batch_no"]))
	require.Equal(t, "2026-11-26", str(m["expiry_date"]))
	r := find(asList(entry.data["items"]), "product_id", rice)
	require.Nil(t, r["batch_no"], "a line without a batch has none")
	require.Nil(t, r["expiry_date"])

	// A stock out is not a receipt: batch and expiry apply to stock in only.
	out := o.call(t, http.MethodPost, "/api/v1/stock-entries", map[string]any{"direction": "out", "reason": "damaged", "items": []map[string]any{
		{"product_id": rice, "qty": "1", "batch_no": "X"}}}, key())
	require.Equal(t, 400, out.code, out.raw)

	// A goods receipt records them too, with the receipt line.
	sup := o.call(t, http.MethodPost, "/api/v1/suppliers", map[string]any{"name": "Dairy Co", "payment_terms_days": 14}, "")
	require.Equal(t, 201, sup.code, sup.raw)
	grn := o.call(t, http.MethodPost, "/api/v1/goods-receipts", map[string]any{"supplier_id": sup.data["id"], "items": []map[string]any{
		{"product_id": milk, "qty": "20", "unit_cost": "1100", "batch_no": "M-2612", "expiry_date": "2027-06-01"}}}, key())
	require.Equal(t, 201, grn.code, grn.raw)
	detail := o.call(t, http.MethodGet, "/api/v1/goods-receipts/"+str(grn.data["id"]), nil, "")
	require.Equal(t, 200, detail.code, detail.raw)
	line := asList(detail.data["items"])[0]
	require.Equal(t, "M-2612", str(line["batch_no"]))
	require.Equal(t, "2027-06-01", str(line["expiry_date"]))

	// The report: already expired and expiring within the window, soonest first; far-off dates excluded.
	rep := o.call(t, http.MethodGet, "/api/v1/reports/inventory/expiring?days=30", nil, "")
	require.Equal(t, 200, rep.code, rep.raw)
	rows := asList(rep.data["rows"])
	require.Len(t, rows, 2, rep.raw)
	require.Equal(t, "Yogurt", str(rows[0]["product"]))
	require.EqualValues(t, -15, dec(rows[0]["days_left"]).IntPart(), "already expired 15 days ago")
	require.Equal(t, "Milk 1L", str(rows[1]["product"]))
	require.EqualValues(t, 10, dec(rows[1]["days_left"]).IntPart())
	require.Equal(t, "stock_entry", str(rows[1]["source"]))
	require.Equal(t, "30.000", str(rows[1]["qty_on_hand"]), "milk on hand is 10 + 20 across receipts")
	wide := o.call(t, http.MethodGet, "/api/v1/reports/inventory/expiring?days=400", nil, "")
	require.Len(t, asList(wide.data["rows"]), 3)
	require.Equal(t, "goods_receipt", str(asList(wide.data["rows"])[2]["source"]))
	csv := o.call(t, http.MethodGet, "/api/v1/reports/inventory/expiring?days=30&format=csv", nil, "")
	require.Equal(t, 200, csv.code)
	require.True(t, strings.Contains(csv.raw, "M-2611"), csv.raw)

	// Needs the inventory reports permission; another business sees nothing.
	staff, _ := e.user(t, "staff")
	require.Equal(t, 403, staff.call(t, http.MethodGet, "/api/v1/reports/inventory/expiring", nil, "").code)
	other := newEnv(t, defaultNow)
	require.Empty(t, asList(other.owner.call(t, http.MethodGet, "/api/v1/reports/inventory/expiring?days=400", nil, "").data["rows"]))
}
